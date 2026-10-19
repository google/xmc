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
		ReadyStatus: ConditionStatusTrue, ReadyReason: ReasonComponentHealthy, StalledStatus: ConditionStatusTrue,
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
	notRegisteredErr := dependencyNotRegisteredErr(t, "redpanda", "zookeeper")
	initialBlockLTT := time.Date(2026, 10, 12, 11, 0, 0, 0, time.UTC)
	blockedAt := func(gen int64) []Condition {
		return append(slices.Clone(wasReady), Condition{
			Type: ConditionTypeDeactivationBlocked, Status: ConditionStatusTrue, ObservedGeneration: gen,
			LastTransitionTime: initialBlockLTT, Reason: ReasonDeactivationBlocked, Message: blockedMsg,
		})
	}
	disabled := func(o gateOutcome) gateOutcome {
		o.State = componentspb.ComponentState_COMPONENT_STATE_DISABLED
		o.ReadyStatus, o.ReadyReason = ConditionStatusFalse, reasonComponentDisabled
		o.SetCalls = 1
		return o
	}
	// blocked keeps the running workload Ready (its health is unchanged) and surfaces the block
	// through Stalled=True (DeactivationBlocked) and DeactivationBlocked=True.
	blocked := func(prepareCalls int) gateOutcome {
		return gateOutcome{
			State:       componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
			ReadyStatus: ConditionStatusTrue, ReadyReason: ReasonComponentHealthy, StalledStatus: ConditionStatusTrue,
			DeactivationBlocked: ConditionStatusTrue, SetCalls: 1, PrepareCalls: prepareCalls,
		}
	}
	failed := func(reason string, o gateOutcome) gateOutcome {
		o.State = componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION
		o.ReadyStatus, o.ReadyReason, o.ReconcilingStatus = ConditionStatusFalse, reason, ConditionStatusTrue
		o.SetCalls = 1
		return o
	}
	// noWorkloadBlocked is a blocked component without a running workload (torn down earlier or
	// never actuated): no health probe runs and Ready reports DeactivationIncomplete.
	noWorkloadBlocked := gateOutcome{
		State:       componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
		ReadyStatus: ConditionStatusFalse, ReadyReason: ReasonDeactivationIncomplete, ReconcilingStatus: ConditionStatusTrue,
		DeactivationBlocked: ConditionStatusTrue, SetCalls: 1, PrepareCalls: 1,
	}
	tests := []struct {
		name           string
		cms            *mockCMS
		helm           *mockHelm
		spec           *componentspb.ComponentSpec
		existing       []Condition
		blockedGen     int64
		blockedAge     time.Duration
		want           gateOutcome
		wantMsg        string
		wantStalledMsg string
		wantErr        bool
	}{
		{
			name: "no active dependents deletes workload and completes handshake",
			cms:  &mockCMS{}, helm: &mockHelm{active: true}, spec: spec1P, existing: wasReady,
			want: disabled(gateOutcome{PrepareCalls: 1, CompleteCalls: 1, HelmDeleted: true}),
		},
		{
			name: "active dependent blocks deactivation and keeps workload",
			cms:  &mockCMS{prepareErr: blockedErr}, helm: &mockHelm{active: true}, spec: spec1P, existing: wasReady,
			want: blocked(1), wantStalledMsg: blockedMsg,
		},
		{
			name: "active dependent blocks 3P wrapper deactivation",
			cms:  &mockCMS{prepareErr: blockedErr}, helm: &mockHelm{}, spec: spec3P, existing: wasReady,
			want: blocked(1), wantStalledMsg: blockedMsg,
		},
		{
			name: "recent block skips the CMS recheck",
			cms:  &mockCMS{}, helm: &mockHelm{active: true}, spec: spec1P, existing: blockedAt(2),
			blockedGen: 2, blockedAge: 5 * time.Second,
			want: blocked(0), wantStalledMsg: blockedMsg,
		},
		{
			name: "block older than the recheck interval is rechecked without overwriting LastTransitionTime",
			cms:  &mockCMS{prepareErr: blockedErr}, helm: &mockHelm{active: true}, spec: spec1P, existing: blockedAt(2),
			blockedGen: 2, blockedAge: time.Minute,
			want: blocked(1), wantStalledMsg: blockedMsg,
		},
		{
			name: "block at an older generation is rechecked",
			cms:  &mockCMS{}, helm: &mockHelm{active: true}, spec: spec1P, existing: blockedAt(1),
			blockedGen: 1, blockedAge: 5 * time.Second,
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
				ReadyStatus: ConditionStatusUnknown, ReadyReason: ReasonCMSSyncFailed, ReconcilingStatus: ConditionStatusTrue,
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
			want:    noWorkloadBlocked,
			wantMsg: "no workload is running; " + blockedMsg,
		},
		{
			name: "block of a never-actuated 1P component skips the health probe",
			cms:  &mockCMS{prepareErr: blockedErr}, helm: &mockHelm{unhealthyReason: ReasonHelmReleaseNotFound}, spec: spec1P,
			existing: []Condition{{Type: ConditionTypeReady, Status: ConditionStatusFalse, ObservedGeneration: 1, Reason: ReasonDependenciesNotReady}},
			want:     noWorkloadBlocked,
			wantMsg:  "no workload is running; " + blockedMsg,
		},
		{
			name: "block of an already-disabled 3P wrapper at a new generation skips the health probe",
			cms:  &mockCMS{prepareErr: blockedErr}, helm: &mockHelm{}, spec: spec3P,
			existing: []Condition{{Type: ConditionTypeReady, Status: ConditionStatusFalse, ObservedGeneration: 1, Reason: reasonComponentDisabled}},
			want:     noWorkloadBlocked,
			wantMsg:  "no workload is running; " + blockedMsg,
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
				ReadyStatus: ConditionStatusUnknown, ReadyReason: ReasonCMSSyncFailed, ReconcilingStatus: ConditionStatusTrue,
				SetCalls: 1,
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, _ := NewReconciler(tt.cms, nil, tt.helm)
			now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
			r.syncTracker.now = func() time.Time { return now }
			if tt.blockedGen > 0 {
				now = now.Add(-tt.blockedAge)
				r.syncTracker.recordDeactivationBlock("xmc/redpanda", tt.blockedGen, blockedMsg)
				now = now.Add(tt.blockedAge)
			}
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
			wantRequeue := time.Duration(0)
			switch {
			case tt.want.DeactivationBlocked == ConditionStatusTrue:
				wantRequeue = deactivationRecheckInterval
			case tt.want.State == componentspb.ComponentState_COMPONENT_STATE_DISABLED:
				wantRequeue = defaultCMSHeartbeatInterval
			}
			if res.RequeueAfter != wantRequeue {
				t.Errorf("ReconcileResult.RequeueAfter = %v, want %v", res.RequeueAfter, wantRequeue)
			}
			if tt.blockedGen == 2 && tt.want.DeactivationBlocked == ConditionStatusTrue {
				if c := findCondition(res.Conditions, ConditionTypeDeactivationBlocked); c == nil || !c.LastTransitionTime.Equal(initialBlockLTT) {
					t.Errorf("DeactivationBlocked.LastTransitionTime = %v, want %v preserved", c, initialBlockLTT)
				}
			}
			checkReadyMessage(t, res, tt.wantMsg)
			if c := findCondition(res.Conditions, ConditionTypeStalled); tt.wantStalledMsg != "" && (c == nil || c.Reason != ReasonDeactivationBlocked || c.Message != tt.wantStalledMsg) {
				t.Errorf("Stalled condition = %+v, want reason %s and message %q", c, ReasonDeactivationBlocked, tt.wantStalledMsg)
			}
			if wantObsGen := int64(2); err == nil && res.ObservedGeneration != wantObsGen {
				t.Errorf("ObservedGeneration = %d, want %d (stamped for every evaluated outcome)", res.ObservedGeneration, wantObsGen)
			}
		})
	}
}

// TestReconcileComponent_DeactivationUnblocksAcrossPasses feeds each pass's conditions into the next:
// blocked while a dependent is active, then disabled once CMS allows it after the recheck interval.
func TestReconcileComponent_DeactivationUnblocksAcrossPasses(t *testing.T) {
	cms, helm := &mockCMS{prepareErr: status.Error(codes.FailedPrecondition, "component redpanda has active dependents: [ingestion-gateway]")}, &mockHelm{active: true}
	r, _ := NewReconciler(cms, nil, helm)
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	r.syncTracker.now = func() time.Time { return now }
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
	now = now.Add(time.Minute)
	cms.prepareErr = nil
	second, err := r.ReconcileComponent(context.Background(), in)
	if err != nil {
		t.Fatalf("second pass: ReconcileComponent() error = %v", err)
	}
	want := gateOutcome{
		State:       componentspb.ComponentState_COMPONENT_STATE_DISABLED,
		ReadyStatus: ConditionStatusFalse, ReadyReason: reasonComponentDisabled,
		SetCalls: 2, PrepareCalls: 2, CompleteCalls: 1, HelmDeleted: true,
	}
	if diff := cmp.Diff(want, summarizeGate(second, cms, helm)); diff != "" {
		t.Errorf("second pass outcome mismatch (-want +got):\n%s", diff)
	}
}

// TestReconcileComponent_BlockAfterTeardownSyncsCMS verifies that a block after teardown syncs the DeactivationIncomplete
// outcome to CMS, throttled by the in-memory cmsSyncTracker heartbeat like every other outcome.
func TestReconcileComponent_BlockAfterTeardownSyncsCMS(t *testing.T) {
	base := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name     string
		recorded bool
		age      time.Duration
		wantSync bool
	}{
		{name: "never synced", wantSync: true},
		{name: "synced within the heartbeat interval", recorded: true, age: time.Minute, wantSync: false},
		{name: "synced before the heartbeat interval", recorded: true, age: defaultCMSHeartbeatInterval, wantSync: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cms := &mockCMS{prepareErr: status.Error(codes.FailedPrecondition, "component redpanda has active dependents: [ingestion-gateway]")}
			r, _ := NewReconciler(cms, nil, &mockHelm{})
			now := base
			r.syncTracker.now = func() time.Time { return now }
			if tt.recorded {
				r.syncTracker.recordSync("xmc/redpanda", componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION, ConditionStatusFalse, 2)
				r.syncTracker.recordSource("xmc/redpanda", "", 2)
				now = base.Add(tt.age)
			}
			_, err := r.ReconcileComponent(context.Background(), ReconcileComponentInput{
				Namespace: "xmc", Name: "redpanda", Generation: 2, Enabled: false,
				Spec: &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT},
				ExistingConditions: []Condition{{
					Type: ConditionTypeReady, Status: ConditionStatusFalse, ObservedGeneration: 2,
					Reason: ReasonDeactivationIncomplete,
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
		{name: "source conflict may follow actuation", status: ConditionStatusFalse, reason: ReasonSourceConflict, want: true},
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

func TestReconcileComponent_DisabledSteadyStateThrottlesCMSUpdate(t *testing.T) {
	cms := &mockCMS{}
	r, _ := NewReconciler(cms, nil, &mockHelm{})
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	r.syncTracker.now = func() time.Time { return now }
	in := ReconcileComponentInput{
		Namespace: "xmc", Name: "redpanda", Generation: 2, Enabled: false,
		Spec: &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT},
	}

	first, err := r.ReconcileComponent(context.Background(), in)
	if err != nil {
		t.Fatalf("first pass error = %v", err)
	}
	if cms.lastObsReq == nil {
		t.Fatal("first pass: expected UpdateObservedGeneration to be called")
	}

	in.ExistingConditions = first.Conditions
	cms.lastObsReq = nil
	now = now.Add(time.Minute)
	if _, err := r.ReconcileComponent(context.Background(), in); err != nil {
		t.Fatalf("second pass error = %v", err)
	}
	if cms.lastObsReq != nil {
		t.Errorf("second pass within heartbeat interval called UpdateObservedGeneration(%v), want throttled", cms.lastObsReq)
	}
}

// TestReconcileComponent_LifecycleOwnershipRejections checks that a sourced UpdateObservedGeneration
// or CompleteDeactivation rejected by CMS ownership is projected as SourceConflict, not returned as
// an error, and that a rejected CompleteDeactivation stops the pass before the component is
// reported as disabled.
func TestReconcileComponent_LifecycleOwnershipRejections(t *testing.T) {
	ctx := context.Background()
	conflict := errorInfoStatus(t, codes.FailedPrecondition, store.ReasonSourceNamespaceConflict, store.ErrorInfoDomain, "owned by namespace team-b")
	healthy := Condition{Type: ConditionTypeReady, Status: ConditionStatusTrue, ObservedGeneration: 1, Reason: ReasonComponentHealthy}
	tests := []struct {
		name    string
		cms     *mockCMS
		enabled bool
	}{
		{name: "UpdateObservedGeneration rejected", cms: &mockCMS{obsErr: conflict}, enabled: true},
		{name: "CompleteDeactivation rejected", cms: &mockCMS{completeErr: conflict}, enabled: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := NewReconciler(tt.cms, &mockProber{healthy: true}, &mockHelm{active: true})
			if err != nil {
				t.Fatalf("NewReconciler() error = %v", err)
			}
			res, err := r.ReconcileComponent(ctx, ReconcileComponentInput{
				Namespace: "xmc", Name: "redpanda", Generation: 2, UID: "uid-a", Enabled: tt.enabled,
				Cluster:            &componentspb.ClusterIdentity{Uid: testPrimaryClusterUID, Name: "primary", Epoch: 1},
				ExistingConditions: []Condition{healthy},
				Spec:               &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT, Version: "1.0.0"},
			})
			if err != nil {
				t.Fatalf("ReconcileComponent() error = %v, want nil (ownership rejections are reported as conditions)", err)
			}
			assertSourceConflictConditions(t, res.Conditions, ReasonSourceNamespaceConflict)
			if res.State == componentspb.ComponentState_COMPONENT_STATE_DISABLED {
				t.Errorf("State = %v, want the pass to stop before reporting DISABLED", res.State)
			}
		})
	}
}
