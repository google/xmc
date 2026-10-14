// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package bridge

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	cmsserver "xmanager_cloud/pkg/components/cms"
	"xmanager_cloud/pkg/components/store"
)

func TestReconcileComponent_DeletionOrderingAndIdempotency(t *testing.T) {
	ctx := context.Background()
	spec1P := &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT}

	t.Run("CMS validation failure halts deletion before K8s teardown", func(t *testing.T) {
		helm := &mockHelm{active: true}
		cms := &mockCMS{delErr: status.Error(codes.FailedPrecondition, "active dependents block deletion")}
		r, err := NewReconciler(cms, nil, helm)
		if err != nil {
			t.Fatalf("NewReconciler failed: %v", err)
		}

		in := ReconcileComponentInput{
			Namespace:  "xmc",
			Name:       "ingestion",
			Generation: 2,
			Spec:       spec1P,
			IsDeleting: true,
		}
		res := &ReconcileResult{}
		_, err = r.reconcileDeletion(ctx, in, "xmc.ingestion", res)
		if err == nil {
			t.Fatal("expected error from CMS validation failure, got nil")
		}
		if helm.purgeCalled || helm.orphanCalled {
			t.Error("expected 1P Helm teardown NOT to be called when CMS ExecuteDeletion fails")
		}
		if !helm.active {
			t.Error("expected Helm component to remain active when CMS ExecuteDeletion fails")
		}
		if res.RemoveFinalizer {
			t.Error("expected RemoveFinalizer to be false when CMS ExecuteDeletion fails")
		}
	})

	t.Run("CMS NotFound on retry proceeds to K8s teardown idempotently", func(t *testing.T) {
		helm := &mockHelm{active: true}
		cms := &mockCMS{delErr: status.Error(codes.NotFound, "component already deleted in CMS")}
		r, err := NewReconciler(cms, nil, helm)
		if err != nil {
			t.Fatalf("NewReconciler failed: %v", err)
		}

		in := ReconcileComponentInput{
			Namespace:  "xmc",
			Name:       "ingestion",
			Generation: 2,
			Spec:       spec1P,
			IsDeleting: true,
		}
		res := &ReconcileResult{}
		resOut, err := r.reconcileDeletion(ctx, in, "xmc.ingestion", res)
		if err != nil {
			t.Fatalf("expected nil error on idempotent retry, got: %v", err)
		}
		if !helm.purgeCalled {
			t.Error("expected 1P Helm purge to be called on idempotent CMS retry")
		}
		if helm.active {
			t.Error("expected Helm component to be inactive after purge")
		}
		if !resOut.RemoveFinalizer {
			t.Error("expected RemoveFinalizer to be true after successful teardown")
		}
		if resOut.State != componentspb.ComponentState_COMPONENT_STATE_DELETED {
			t.Errorf("expected state DELETED, got %v", resOut.State)
		}
	})
}

func TestReconciler_DeleteDisabledHelmComponent_SkipsWhenInactive(t *testing.T) {
	ctx := context.Background()
	helm := &mockHelm{
		active:    false,
		deleteErr: errors.New("Delete1PComponent should not be called when component is inactive"),
	}
	cms := &mockCMS{}
	r, err := NewReconciler(cms, nil, helm)
	if err != nil {
		t.Fatalf("failed to create reconciler: %v", err)
	}

	spec1P := &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT}
	in := ReconcileComponentInput{
		Namespace:  "xmc",
		Name:       "metrics",
		Generation: 1,
		Spec:       spec1P,
		Enabled:    false,
	}
	res, err := r.ReconcileComponent(ctx, in)
	if err != nil {
		t.Fatalf("expected ReconcileComponent to succeed without invoking Delete1PComponent, got %v", err)
	}
	if res.State != componentspb.ComponentState_COMPONENT_STATE_DISABLED {
		t.Errorf("expected state DISABLED, got %v", res.State)
	}
	if helm.deleteCalled {
		t.Errorf("expected Delete1PComponent NOT to be called when component is inactive")
	}
}

func TestReconciler_ColdStartDeactivation_DeletesPreviouslyActiveComponent(t *testing.T) {
	ctx := context.Background()
	helm := &mockHelm{active: false}
	cms := &mockCMS{}
	r, err := NewReconciler(cms, nil, helm)
	if err != nil {
		t.Fatalf("NewReconciler failed: %v", err)
	}

	in := ReconcileComponentInput{
		Namespace:  "xmc",
		Name:       "ingestion-gateway",
		Generation: 2,
		Spec:       &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT},
		Enabled:    false,
		ExistingConditions: []Condition{
			{
				Type:               ConditionTypeReady,
				Status:             ConditionStatusTrue,
				ObservedGeneration: 1,
				Reason:             "ComponentHealthy",
			},
		},
	}

	res, err := r.ReconcileComponent(ctx, in)
	if err != nil {
		t.Fatalf("ReconcileComponent failed: %v", err)
	}
	if res.State != componentspb.ComponentState_COMPONENT_STATE_DISABLED {
		t.Errorf("expected COMPONENT_STATE_DISABLED, got %v", res.State)
	}
	if !helm.deleteCalled {
		t.Error("expected Delete1PComponent to be called when deactivating previously Ready component after controller restart")
	}
}

// TestReconcileComponent_GatedDependentBlocksDeactivationInCMS verifies against the real CMS server
// and MemoryStore that a gated dependent still registers its dependency edge, so disabling the
// dependency is blocked.
func TestReconcileComponent_GatedDependentBlocksDeactivationInCMS(t *testing.T) {
	ctx := context.Background()
	r, err := NewReconciler(NewCMSClientAdapter(cmsserver.NewServer(store.NewMemoryStore(), cmsserver.WithAuthorizer(cmsserver.NewDisabledAuthorizer()))), nil, nil)
	if err != nil {
		t.Fatalf("NewReconciler() error = %v", err)
	}
	spec := func(deps ...string) *componentspb.ComponentSpec {
		return &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT, Dependencies: deps}
	}
	unreadyRedpanda := NewDependencySnapshot([]unstructured.Unstructured{readyComponent("redpanda", func(u *unstructured.Unstructured) { setConditionStatus(u, 0, "status", ConditionStatusFalse) })})
	reconcile := func(name string, gen int64, enabled bool, s *componentspb.ComponentSpec, existing []Condition) *ReconcileResult {
		t.Helper()
		res, err := r.ReconcileComponent(ctx, ReconcileComponentInput{
			Namespace: "xmc", Name: name, Generation: gen, Spec: s, Enabled: enabled,
			ExistingConditions: existing, Dependencies: unreadyRedpanda,
		})
		if err != nil {
			t.Fatalf("ReconcileComponent(%s, gen %d) error = %v", name, gen, err)
		}
		return res
	}
	redpanda := reconcile("redpanda", 1, true, spec(), nil)
	gateway := reconcile("ingestion-gateway", 1, true, spec("redpanda"), nil)
	if c := findCondition(gateway.Conditions, ConditionTypeReady); c == nil || c.Reason != ReasonDependenciesNotReady {
		t.Fatalf("ingestion-gateway Ready = %+v, want reason %s", c, ReasonDependenciesNotReady)
	}
	disabled := reconcile("redpanda", 2, false, spec(), redpanda.Conditions)
	got := summarizeGate(disabled, &mockCMS{}, &mockHelm{})
	want := gateOutcome{
		State:       componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
		ReadyStatus: ConditionStatusFalse, ReadyReason: ReasonDeactivationBlocked, DegradedStatus: ConditionStatusFalse,
		DeactivationBlocked: ConditionStatusTrue,
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("disabling redpanda outcome mismatch (-want +got):\n%s", diff)
	}
}

func TestReconcileComponent_DeactivationGating(t *testing.T) {
	spec1P := &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT}
	spec3P := &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER}
	wasReady := []Condition{{Type: ConditionTypeReady, Status: ConditionStatusTrue, ObservedGeneration: 1, Reason: ReasonComponentHealthy}}
	tornDown := []Condition{{Type: ConditionTypeReady, Status: ConditionStatusFalse, ObservedGeneration: 2, Reason: ReasonDeactivationIncomplete}}
	blockedErr := status.Error(codes.FailedPrecondition, "component redpanda has active dependents: [ingestion-gateway]")
	blockedMsg := `Cannot disable "redpanda": component redpanda has active dependents: [ingestion-gateway]`
	notRegisteredErr := status.Error(codes.FailedPrecondition, store.DependencyNotFoundMessagePrefix+` component "redpanda" depends on "zookeeper", which is not registered`)
	blockedAt := func(gen int64, age time.Duration) []Condition {
		return append(slices.Clone(wasReady), Condition{
			Type: ConditionTypeDeactivationBlocked, Status: ConditionStatusTrue, ObservedGeneration: gen,
			LastTransitionTime: time.Now().Add(-age), Reason: ReasonDeactivationBlocked, Message: blockedMsg,
		})
	}
	disabled := func(o gateOutcome) gateOutcome {
		o.State = componentspb.ComponentState_COMPONENT_STATE_DISABLED
		o.ReadyStatus, o.ReadyReason, o.DegradedStatus = ConditionStatusFalse, reasonComponentDisabled, ConditionStatusFalse
		o.SetCalls = 1
		return o
	}
	blocked := func(prepareCalls int) gateOutcome {
		return gateOutcome{
			State:       componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
			ReadyStatus: ConditionStatusFalse, ReadyReason: ReasonDeactivationBlocked, DegradedStatus: ConditionStatusFalse,
			DeactivationBlocked: ConditionStatusTrue, SetCalls: 1, PrepareCalls: prepareCalls,
		}
	}
	failed := func(reason string, o gateOutcome) gateOutcome {
		o.State = componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION
		o.ReadyStatus, o.ReadyReason, o.DegradedStatus = ConditionStatusFalse, reason, ConditionStatusTrue
		o.SetCalls = 1
		return o
	}
	tests := []struct {
		name     string
		cms      *mockCMS
		helm     *mockHelm
		spec     *componentspb.ComponentSpec
		existing []Condition
		want     gateOutcome
		wantMsg  string
		wantErr  bool
	}{
		{
			name: "no active dependents deletes workload and completes handshake",
			cms:  &mockCMS{}, helm: &mockHelm{active: true}, spec: spec1P, existing: wasReady,
			want: disabled(gateOutcome{PrepareCalls: 1, CompleteCalls: 1, HelmDeleted: true}),
		},
		{
			name: "active dependent blocks deactivation and keeps workload",
			cms:  &mockCMS{prepareErr: blockedErr}, helm: &mockHelm{active: true}, spec: spec1P, existing: wasReady,
			want: blocked(1), wantMsg: blockedMsg,
		},
		{
			name: "active dependent blocks 3P wrapper deactivation",
			cms:  &mockCMS{prepareErr: blockedErr}, helm: &mockHelm{}, spec: spec3P, existing: wasReady,
			want: blocked(1), wantMsg: blockedMsg,
		},
		{
			name: "recent block skips the CMS recheck",
			cms:  &mockCMS{}, helm: &mockHelm{active: true}, spec: spec1P, existing: blockedAt(2, 5*time.Second),
			want: blocked(0), wantMsg: blockedMsg,
		},
		{
			name: "block older than the recheck interval is rechecked",
			cms:  &mockCMS{prepareErr: blockedErr}, helm: &mockHelm{active: true}, spec: spec1P, existing: blockedAt(2, time.Minute),
			want: blocked(1), wantMsg: blockedMsg,
		},
		{
			name: "block at an older generation is rechecked",
			cms:  &mockCMS{}, helm: &mockHelm{active: true}, spec: spec1P, existing: blockedAt(1, 5*time.Second),
			want: disabled(gateOutcome{PrepareCalls: 1, CompleteCalls: 1, HelmDeleted: true}),
		},
		{
			name: "component unknown to CMS proceeds without completion",
			cms:  &mockCMS{prepareErr: status.Error(codes.NotFound, "component not found")}, helm: &mockHelm{active: true}, spec: spec1P, existing: wasReady,
			want: disabled(gateOutcome{PrepareCalls: 1, HelmDeleted: true}),
		},
		{
			name: "already disabled at generation skips handshake",
			cms:  &mockCMS{}, helm: &mockHelm{}, spec: spec1P,
			existing: []Condition{{Type: ConditionTypeReady, Status: ConditionStatusFalse, ObservedGeneration: 2, Reason: reasonComponentDisabled}},
			want:     disabled(gateOutcome{}),
		},
		{
			name: "never-actuated gated component has no workload to delete",
			cms:  &mockCMS{}, helm: &mockHelm{}, spec: spec1P,
			existing: []Condition{{Type: ConditionTypeReady, Status: ConditionStatusFalse, ObservedGeneration: 2, Reason: ReasonDependenciesNotReady}},
			want:     disabled(gateOutcome{PrepareCalls: 1, CompleteCalls: 1}),
		},
		{
			name: "prepare failure keeps workload and downgrades Ready=True to Unknown",
			cms:  &mockCMS{prepareErr: status.Error(codes.Unavailable, "cms down")}, helm: &mockHelm{active: true}, spec: spec1P, existing: wasReady,
			want: gateOutcome{
				State:       componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
				ReadyStatus: ConditionStatusUnknown, ReadyReason: ReasonCMSSyncFailed, DegradedStatus: ConditionStatusFalse,
				SetCalls: 1, PrepareCalls: 1,
			},
			wantErr: true,
		},
		{
			name: "complete failure after teardown reports DeactivationIncomplete",
			cms:  &mockCMS{completeErr: status.Error(codes.Internal, "boom")}, helm: &mockHelm{active: true}, spec: spec1P, existing: wasReady,
			want:    failed(ReasonDeactivationIncomplete, gateOutcome{PrepareCalls: 1, CompleteCalls: 1, HelmDeleted: true}),
			wantMsg: "failed to complete deactivation in CMS: rpc error: code = Internal desc = boom",
			wantErr: true,
		},
		{
			name: "retry after incomplete deactivation re-runs the handshake without another uninstall",
			cms:  &mockCMS{}, helm: &mockHelm{}, spec: spec1P, existing: tornDown,
			want: disabled(gateOutcome{PrepareCalls: 1, CompleteCalls: 1}),
		},
		{
			name: "block after teardown keeps reporting DeactivationIncomplete",
			cms:  &mockCMS{prepareErr: blockedErr}, helm: &mockHelm{}, spec: spec1P, existing: tornDown,
			want: gateOutcome{
				State:       componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
				ReadyStatus: ConditionStatusFalse, ReadyReason: ReasonDeactivationIncomplete, DegradedStatus: ConditionStatusTrue,
				DeactivationBlocked: ConditionStatusTrue, SetCalls: 1, PrepareCalls: 1,
			},
			wantMsg: "workload already removed; " + blockedMsg,
		},
		{
			name: "prepare failure after teardown keeps reporting DeactivationIncomplete",
			cms:  &mockCMS{prepareErr: status.Error(codes.Unavailable, "cms down")}, helm: &mockHelm{}, spec: spec1P, existing: tornDown,
			want:    failed(ReasonDeactivationIncomplete, gateOutcome{PrepareCalls: 1}),
			wantErr: true,
		},
		{
			name: "unregistered dependency does not block deactivation",
			cms: &mockCMS{
				setErr:     notRegisteredErr,
				prepareErr: status.Error(codes.NotFound, "component not found"),
				obsErr:     status.Error(codes.NotFound, "component not found"),
			},
			helm: &mockHelm{active: true}, spec: spec1P, existing: wasReady,
			want: disabled(gateOutcome{PrepareCalls: 1, HelmDeleted: true}),
		},
		{
			name: "unregistered dependency on a registered component deactivates against the existing record",
			cms:  &mockCMS{setErr: notRegisteredErr}, helm: &mockHelm{active: true}, spec: spec1P, existing: wasReady,
			want: disabled(gateOutcome{PrepareCalls: 1, CompleteCalls: 1, HelmDeleted: true}),
		},
		{
			name: "other intent failure still fails deactivation and downgrades Ready=True to Unknown",
			cms:  &mockCMS{setErr: status.Error(codes.Unavailable, "cms down")}, helm: &mockHelm{active: true}, spec: spec1P, existing: wasReady,
			want: gateOutcome{
				State:       componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
				ReadyStatus: ConditionStatusUnknown, ReadyReason: ReasonCMSSyncFailed, DegradedStatus: ConditionStatusFalse,
				SetCalls: 1,
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, _ := NewReconciler(tt.cms, nil, tt.helm)
			res, err := r.ReconcileComponent(context.Background(), ReconcileComponentInput{
				Namespace: "xmc", Name: "redpanda", Generation: 2, Spec: tt.spec,
				Enabled: false, ExistingConditions: tt.existing,
			})
			if (err != nil) != tt.wantErr {
				t.Fatalf("ReconcileComponent() error = %v, wantErr %v", err, tt.wantErr)
			}
			if diff := cmp.Diff(tt.want, summarizeGate(res, tt.cms, tt.helm)); diff != "" {
				t.Errorf("ReconcileComponent() outcome mismatch (-want +got):\n%s", diff)
			}
			checkReadyMessage(t, res, tt.wantMsg)
		})
	}
}

// TestReconcileComponent_DeactivationUnblocksAcrossPasses feeds each pass's conditions into the next:
// blocked while a dependent is active, then disabled once CMS allows it after the recheck interval.
func TestReconcileComponent_DeactivationUnblocksAcrossPasses(t *testing.T) {
	cms, helm := &mockCMS{prepareErr: status.Error(codes.FailedPrecondition, "component redpanda has active dependents: [ingestion-gateway]")}, &mockHelm{active: true}
	r, _ := NewReconciler(cms, nil, helm)
	in := ReconcileComponentInput{
		Namespace: "xmc", Name: "redpanda", Generation: 2, Enabled: false,
		Spec:               &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT},
		ExistingConditions: []Condition{{Type: ConditionTypeReady, Status: ConditionStatusTrue, ObservedGeneration: 1, Reason: ReasonComponentHealthy}},
	}
	first, err := r.ReconcileComponent(context.Background(), in)
	if err != nil {
		t.Fatalf("first pass: ReconcileComponent() error = %v", err)
	}
	if got := summarizeGate(first, cms, helm); got.DeactivationBlocked != ConditionStatusTrue || got.HelmDeleted {
		t.Fatalf("first pass outcome = %+v, want DeactivationBlocked=True and workload kept", got)
	}

	in.ExistingConditions = slices.Clone(first.Conditions)
	findCondition(in.ExistingConditions, ConditionTypeDeactivationBlocked).LastTransitionTime = time.Now().Add(-time.Minute)
	cms.prepareErr = nil
	second, err := r.ReconcileComponent(context.Background(), in)
	if err != nil {
		t.Fatalf("second pass: ReconcileComponent() error = %v", err)
	}
	want := gateOutcome{
		State:       componentspb.ComponentState_COMPONENT_STATE_DISABLED,
		ReadyStatus: ConditionStatusFalse, ReadyReason: reasonComponentDisabled, DegradedStatus: ConditionStatusFalse,
		SetCalls: 2, PrepareCalls: 2, CompleteCalls: 1, HelmDeleted: true,
	}
	if diff := cmp.Diff(want, summarizeGate(second, cms, helm)); diff != "" {
		t.Errorf("second pass outcome mismatch (-want +got):\n%s", diff)
	}
}

// TestReconcileComponent_BlockAfterTeardownSyncsCMS verifies that a block after teardown syncs the DeactivationIncomplete
// outcome to CMS, throttled by the Ready condition's heartbeat like every other outcome.
func TestReconcileComponent_BlockAfterTeardownSyncsCMS(t *testing.T) {
	tests := []struct {
		name               string
		lastTransitionTime time.Time
		wantSync           bool
	}{
		{name: "never synced", wantSync: true},
		{name: "synced within the heartbeat interval", lastTransitionTime: time.Now(), wantSync: false},
		{name: "synced before the heartbeat interval", lastTransitionTime: time.Now().Add(-defaultCMSHeartbeatInterval), wantSync: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cms := &mockCMS{prepareErr: status.Error(codes.FailedPrecondition, "component redpanda has active dependents: [ingestion-gateway]")}
			r, _ := NewReconciler(cms, nil, &mockHelm{})
			_, err := r.ReconcileComponent(context.Background(), ReconcileComponentInput{
				Namespace: "xmc", Name: "redpanda", Generation: 2, Enabled: false,
				Spec: &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT},
				ExistingConditions: []Condition{{
					Type: ConditionTypeReady, Status: ConditionStatusFalse, ObservedGeneration: 2,
					Reason: ReasonDeactivationIncomplete, LastTransitionTime: tt.lastTransitionTime,
				}},
			})
			if err != nil {
				t.Fatalf("ReconcileComponent() error = %v", err)
			}
			got := cms.lastObsReq
			if !tt.wantSync {
				if got != nil {
					t.Errorf("UpdateObservedGeneration called with %v, want no call within the heartbeat interval", got)
				}
				return
			}
			if got == nil {
				t.Fatal("UpdateObservedGeneration was not called, want the DeactivationIncomplete outcome synced to CMS")
			}
			if got.GetObservedGeneration() != 2 || got.GetState() != componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION || got.GetStatusMessage() != "Ready=False" {
				t.Errorf("UpdateObservedGeneration request = %v, want generation 2, PENDING_RECONCILIATION, \"Ready=False\"", got)
			}
		})
	}
}

func TestWasComponentPreviouslyActive(t *testing.T) {
	tests := []struct {
		name   string
		status string
		reason string
		want   bool
	}{
		{name: "healthy", status: ConditionStatusTrue, reason: ReasonComponentHealthy, want: true},
		{name: "unhealthy after actuation", status: ConditionStatusFalse, reason: ReasonHealthProbeFailed, want: true},
		{name: "deactivation blocked keeps workload", status: ConditionStatusFalse, reason: ReasonDeactivationBlocked, want: true},
		{name: "deactivation incomplete was already torn down", status: ConditionStatusFalse, reason: ReasonDeactivationIncomplete, want: false},
		{name: "helm upgrade failed after actuation", status: ConditionStatusFalse, reason: ReasonHelmUpgradeFailed, want: true},
		{name: "initial sync pending never actuated", status: ConditionStatusUnknown, reason: ReasonInitialSyncPending, want: false},
		{name: "gated on dependencies", status: ConditionStatusFalse, reason: ReasonDependenciesNotReady, want: false},
		{name: "gated on a cycle", status: ConditionStatusFalse, reason: ReasonDependencyCycle, want: false},
		{name: "disabled", status: ConditionStatusFalse, reason: reasonComponentDisabled, want: false},
		{name: "no reason", status: ConditionStatusFalse, reason: "", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			existing := []Condition{{Type: ConditionTypeReady, Status: tt.status, Reason: tt.reason}}
			if got := wasComponentPreviouslyActive(existing); got != tt.want {
				t.Errorf("wasComponentPreviouslyActive(%s/%s) = %v, want %v", tt.status, tt.reason, got, tt.want)
			}
		})
	}
	if wasComponentPreviouslyActive(nil) {
		t.Error("wasComponentPreviouslyActive(nil) = true, want false")
	}
}
