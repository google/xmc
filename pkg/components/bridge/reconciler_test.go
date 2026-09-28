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
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/kubernetes/fake"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	cmsserver "xmanager_cloud/pkg/components/cms"
	"xmanager_cloud/pkg/components/store"
)

type mockCMS struct {
	setErr, delErr, obsErr  error
	prepareErr, completeErr error
	state                   componentspb.ComponentState
	lastObsReq              *componentspb.UpdateObservedGenerationRequest
	setCalls                int
	prepareCalls            int
	completeCalls           int
}

func (m *mockCMS) SetComponentIntent(ctx context.Context, in *componentspb.SetComponentIntentRequest, opts ...grpc.CallOption) (*componentspb.SetComponentIntentResponse, error) {
	m.setCalls++
	if m.setErr != nil {
		return nil, m.setErr
	}
	s := m.state
	if s == componentspb.ComponentState_COMPONENT_STATE_UNSPECIFIED {
		s = componentspb.ComponentState_COMPONENT_STATE_ENABLED
	}
	return &componentspb.SetComponentIntentResponse{
		Component: &componentspb.Component{Name: in.GetName(), Status: &componentspb.ComponentAccessStatus{State: s}},
	}, nil
}

func (m *mockCMS) UpdateObservedGeneration(ctx context.Context, in *componentspb.UpdateObservedGenerationRequest, opts ...grpc.CallOption) (*componentspb.UpdateObservedGenerationResponse, error) {
	m.lastObsReq = in
	return &componentspb.UpdateObservedGenerationResponse{}, m.obsErr
}

func (m *mockCMS) ExecuteDeletion(ctx context.Context, in *componentspb.ExecuteDeletionRequest, opts ...grpc.CallOption) (*componentspb.ExecuteDeletionResponse, error) {
	return &componentspb.ExecuteDeletionResponse{State: componentspb.ComponentState_COMPONENT_STATE_DELETED}, m.delErr
}

func (m *mockCMS) PrepareDeactivation(ctx context.Context, in *componentspb.PrepareDeactivationRequest, opts ...grpc.CallOption) (*componentspb.PrepareDeactivationResponse, error) {
	m.prepareCalls++
	return &componentspb.PrepareDeactivationResponse{State: componentspb.ComponentState_COMPONENT_STATE_DISABLING}, m.prepareErr
}

func (m *mockCMS) CompleteDeactivation(ctx context.Context, in *componentspb.CompleteDeactivationRequest, opts ...grpc.CallOption) (*componentspb.CompleteDeactivationResponse, error) {
	m.completeCalls++
	return &componentspb.CompleteDeactivationResponse{State: componentspb.ComponentState_COMPONENT_STATE_DISABLED}, m.completeErr
}

type mockProber struct {
	healthy     bool
	msg         string
	resStatus   *ResourceStatus
	status      *ResourceStatus
	hasStatus   bool
	detectErr   error
	nilStatus   bool
	probeErr    error
	probeRes    *HealthProbeResult
	hasProbeRes bool
}

func (m *mockProber) DetectStatus(ctx context.Context, ref WrapperRef) (*ResourceStatus, error) {
	if m == nil {
		return &ResourceStatus{Found: true, Ready: true}, nil
	}
	if m.detectErr != nil {
		return nil, m.detectErr
	}
	if m.nilStatus {
		return nil, nil
	}
	if m.resStatus != nil {
		return m.resStatus, nil
	}
	if m.hasStatus || m.status != nil {
		return m.status, nil
	}
	if m.resStatus != nil {
		return m.resStatus, nil
	}
	return &ResourceStatus{Found: true, Ready: m.healthy, Message: m.msg}, nil
}

func (m *mockProber) Probe(ctx context.Context, target string, timeout time.Duration) (*HealthProbeResult, error) {
	if m == nil {
		return &HealthProbeResult{Healthy: true}, nil
	}
	if m.probeErr != nil {
		return nil, m.probeErr
	}
	if m.hasProbeRes || m.probeRes != nil {
		return m.probeRes, nil
	}
	return &HealthProbeResult{Healthy: true}, nil
}

func (m *mockProber) ProbeHealth(ctx context.Context, spec *componentspb.ComponentSpec, defaultNamespace string) (bool, string) {
	if m == nil {
		return true, "Component is active"
	}
	if m.msg != "" {
		return m.healthy, m.msg
	}
	return m.healthy, "Component is active"
}

type mockHelm struct {
	ensureErr, deleteErr, orphanErr error
	active                          bool
	purgeCalled                     bool
	orphanCalled                    bool
	deleteCalled                    bool
	orphaned                        bool
	ensureCalls                     int
	lastRef                         ComponentRef
	lastOpts                        HelmComponentOptions
	actuatedGenerations             map[string]int64
	// unhealthyReason, when non-empty, makes ComponentHealth report unhealthy with this reason. Defaults to healthy.
	unhealthyReason string
}

func (m *mockHelm) Ensure1PComponent(ctx context.Context, ref ComponentRef, opts HelmComponentOptions) error {
	if m == nil {
		return nil
	}
	m.ensureCalls++
	m.lastRef = ref
	m.lastOpts = opts
	if m.ensureErr == nil {
		m.active = true
	}
	return m.ensureErr
}

func (m *mockHelm) Delete1PComponent(ctx context.Context, ref ComponentRef) error {
	if m == nil {
		return nil
	}
	m.deleteCalled = true
	m.lastRef = ref
	if m.deleteErr == nil {
		m.active = false
	}
	return m.deleteErr
}

func (m *mockHelm) Purge1PComponent(ctx context.Context, ref ComponentRef) error {
	if m == nil {
		return nil
	}
	m.purgeCalled = true
	m.lastRef = ref
	if m.deleteErr == nil {
		m.active = false
	}
	return m.deleteErr
}

func (m *mockHelm) Orphan1PComponent(ctx context.Context, ref ComponentRef) error {
	if m == nil {
		return nil
	}
	m.orphanCalled = true
	m.lastRef = ref
	if m.orphanErr == nil {
		m.orphaned = true
		m.active = false
	}
	return m.orphanErr
}

func (m *mockHelm) IsComponentActive(ref ComponentRef) bool {
	if m == nil {
		return false
	}
	return m.active
}

func (m *mockHelm) IsComponentActuated(ref ComponentRef, generation int64) bool {
	if m == nil || m.actuatedGenerations == nil {
		return false
	}
	return m.active && m.actuatedGenerations[ref.String()] == generation
}

func (m *mockHelm) RecordActuation(ref ComponentRef, generation int64) {
	if m == nil {
		return
	}
	if m.actuatedGenerations == nil {
		m.actuatedGenerations = make(map[string]int64)
	}
	m.actuatedGenerations[ref.String()] = generation
}

func (m *mockHelm) HydrateComponentState(ctx context.Context, ref ComponentRef, observedGeneration int64, crReady bool) error {
	if m == nil || !crReady {
		return nil
	}
	m.active = true
	if observedGeneration <= 0 {
		return nil
	}
	if m.actuatedGenerations == nil {
		m.actuatedGenerations = make(map[string]int64)
	}
	if m.actuatedGenerations[ref.String()] == 0 {
		m.actuatedGenerations[ref.String()] = observedGeneration
	}
	return nil
}

func (m *mockHelm) ComponentHealth(ctx context.Context, ref ComponentRef) (bool, string, string) {
	if m.unhealthyReason != "" {
		return false, m.unhealthyReason, "mock Helm release unhealthy"
	}
	return true, ReasonComponentHealthy, "mock Helm release deployed"
}

func TestNewReconciler(t *testing.T) {
	if _, err := NewReconciler(nil, nil, nil); err == nil {
		t.Fatal("expected error on nil CMS client")
	}
	if r, err := NewReconciler(&mockCMS{}, nil, nil); err != nil || r == nil {
		t.Fatalf("unexpected constructor failure: %v", err)
	}
}

func TestReconcileComponent_Validation(t *testing.T) {
	r, _ := NewReconciler(&mockCMS{}, nil, nil)
	ctx := context.Background()
	if _, err := r.ReconcileComponent(ctx, ReconcileComponentInput{}); err == nil {
		t.Error("expected error for empty name/spec")
	}
	if _, err := r.Reconcile(ctx, nil); err == nil {
		t.Error("expected error for nil unstructured")
	}
}

func TestReconcileComponent_Scenarios(t *testing.T) {
	ctx := context.Background()
	spec1P := &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT}
	spec3P := &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER}

	spec3PWithRef := &componentspb.ComponentSpec{
		Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER,
		WrapperRef: &componentspb.WrapperRef{
			CrdGroup: "clickhouse.altinity.com", CrdVersion: "v1", CrdKind: "ClickHouseInstallation", TargetName: "my-ch",
		},
	}

	tests := []struct {
		name          string
		cms           *mockCMS
		prober        *mockProber
		helm          *mockHelm
		in            ReconcileComponentInput
		wantErr       bool
		wantState     componentspb.ComponentState
		wantAddFin    bool
		wantRemoveFin bool
		// wantHealth, when set, asserts the Ready/Degraded condition statuses and shared reason.
		wantHealth *healthConditions
	}{
		{
			name: "active 3P with finalizer addition",
			cms:  &mockCMS{},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "clickhouse", Generation: 1, Spec: spec3P,
				DeletionProtection: true, Enabled: true,
			},
			wantState:  componentspb.ComponentState_COMPONENT_STATE_ENABLED,
			wantAddFin: true,
		},
		{
			name:   "active operatorless 3P with endpoint and secret in parameters",
			cms:    &mockCMS{},
			prober: &mockProber{healthy: true},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "clickhouse", Generation: 1,
				Spec: &componentspb.ComponentSpec{
					Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER,
					Parameters: map[string]string{
						"host":                "clickhouse.xmc-metrics.svc.cluster.local",
						"healthCheckEndpoint": "http://clickhouse.xmc-metrics.svc.cluster.local:8123/ping",
						"secretName":          "clickhouse-secret",
					},
				},
				DeletionProtection: true, Enabled: true,
			},
			wantState:  componentspb.ComponentState_COMPONENT_STATE_ENABLED,
			wantAddFin: true,
		},
		{
			name: "active 3P with coordinate injection",
			cms:  &mockCMS{},
			prober: &mockProber{status: &ResourceStatus{
				Found:            true,
				Ready:            true,
				Endpoint:         "http://chi-prod.analytics.svc.cluster.local:8123",
				ConnectionSecret: "clickhouse-credentials",
				Message:          "Operational",
			}},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "clickhouse", Generation: 1, Spec: spec3PWithRef,
				DeletionProtection: true, Enabled: true,
			},
			wantState:  componentspb.ComponentState_COMPONENT_STATE_ENABLED,
			wantAddFin: true,
		},
		{
			name:   "active 3P with status detector not ready",
			cms:    &mockCMS{},
			prober: &mockProber{status: &ResourceStatus{Found: true, Ready: false, Message: "Waiting for pods"}},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "clickhouse", Generation: 1, Spec: spec3PWithRef,
				DeletionProtection: true, Enabled: true,
			},
			wantState:  componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
			wantAddFin: true,
		},
		{
			name:   "active 3P with status detector resource not found",
			cms:    &mockCMS{},
			prober: &mockProber{status: &ResourceStatus{Found: false, Message: "CRD not installed: clickhouses.clickhouse.altinity.com"}},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "clickhouse", Generation: 1, Spec: spec3PWithRef,
				DeletionProtection: true, Enabled: true,
			},
			wantState:  componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
			wantAddFin: true,
		},
		{
			name:   "active 3P with status detector error",
			cms:    &mockCMS{},
			prober: &mockProber{detectErr: errors.New("k8s api timeout")},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "clickhouse", Generation: 1, Spec: spec3PWithRef,
				DeletionProtection: true, Enabled: true,
			},
			wantErr:    true,
			wantState:  componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
			wantAddFin: true,
		},
		{
			name:   "active 3P with status detector nil status",
			cms:    &mockCMS{},
			prober: &mockProber{nilStatus: true},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "clickhouse", Generation: 1, Spec: spec3PWithRef,
				DeletionProtection: true, Enabled: true,
			},
			wantErr:    true,
			wantState:  componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
			wantAddFin: true,
		},
		{
			name:   "active 3P with status detector error and CMS failure",
			cms:    &mockCMS{obsErr: errors.New("spanner write failed")},
			prober: &mockProber{detectErr: errors.New("k8s api timeout")},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "clickhouse", Generation: 1, Spec: spec3PWithRef,
				DeletionProtection: true, Enabled: true,
			},
			wantErr:    true,
			wantState:  componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
			wantAddFin: true,
		},
		{
			name:   "active 3P with empty target name",
			cms:    &mockCMS{},
			prober: &mockProber{status: &ResourceStatus{Found: true, Ready: true}},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "clickhouse", Generation: 1,
				Spec: &componentspb.ComponentSpec{
					Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER,
				},
				DeletionProtection: true, Enabled: true,
			},
			wantState:  componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
			wantAddFin: true,
		},
		{
			name: "active 1P without deletion protection still attaches finalizer",
			cms:  &mockCMS{},
			helm: &mockHelm{},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "ingestion", Generation: 1, Spec: spec1P,
				HasFinalizer: false, DeletionProtection: false, Enabled: true,
			},
			wantState:  componentspb.ComponentState_COMPONENT_STATE_ENABLED,
			wantAddFin: true,
		},
		{
			name: "active 1P with nil prober defaults to healthy",
			cms:  &mockCMS{},
			helm: &mockHelm{},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "ingestion", Generation: 1, Spec: spec1P,
				HasFinalizer: true, DeletionProtection: true, Enabled: true,
			},
			wantState: componentspb.ComponentState_COMPONENT_STATE_ENABLED,
		},
		{
			name:   "active 1P reports healthy from deployed Helm release regardless of prober",
			cms:    &mockCMS{},
			prober: &mockProber{healthy: false, msg: "prober must not be consulted for 1P"},
			helm:   &mockHelm{},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "ingestion", Generation: 1, Spec: spec1P,
				HasFinalizer: true, DeletionProtection: true, Enabled: true,
			},
			wantState:  componentspb.ComponentState_COMPONENT_STATE_ENABLED,
			wantHealth: &healthConditions{ReadyStatus: ConditionStatusTrue, DegradedStatus: ConditionStatusFalse, Reason: ReasonComponentHealthy},
		},
		{
			name:   "active 1P with missing Helm release is unhealthy even if prober is healthy",
			cms:    &mockCMS{},
			prober: &mockProber{healthy: true},
			helm:   &mockHelm{unhealthyReason: ReasonHelmReleaseNotFound},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "ingestion", Generation: 1, Spec: spec1P,
				HasFinalizer: true, DeletionProtection: true, Enabled: true,
			},
			wantState:  componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
			wantHealth: &healthConditions{ReadyStatus: ConditionStatusFalse, DegradedStatus: ConditionStatusTrue, Reason: ReasonHelmReleaseNotFound},
		},
		{
			name: "active 1P with failed async Helm upgrade is unhealthy",
			cms:  &mockCMS{},
			helm: &mockHelm{unhealthyReason: ReasonHelmUpgradeFailed},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "ingestion", Generation: 1, Spec: spec1P,
				HasFinalizer: true, DeletionProtection: true, Enabled: true,
			},
			wantState:  componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
			wantHealth: &healthConditions{ReadyStatus: ConditionStatusFalse, DegradedStatus: ConditionStatusTrue, Reason: ReasonHelmUpgradeFailed},
		},
		{
			name: "active 1P with in-flight Helm upgrade is not ready but not degraded",
			cms:  &mockCMS{},
			helm: &mockHelm{unhealthyReason: ReasonHelmUpgradePending},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "ingestion", Generation: 1, Spec: spec1P,
				HasFinalizer: true, DeletionProtection: true, Enabled: true,
			},
			wantState:  componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
			wantHealth: &healthConditions{ReadyStatus: ConditionStatusFalse, DegradedStatus: ConditionStatusFalse, Reason: ReasonHelmUpgradePending},
		},
		{
			name: "active 1P with stuck Helm release is degraded",
			cms:  &mockCMS{},
			helm: &mockHelm{unhealthyReason: ReasonHelmReleaseStuck},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "ingestion", Generation: 1, Spec: spec1P,
				HasFinalizer: true, DeletionProtection: true, Enabled: true,
			},
			wantState:  componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
			wantHealth: &healthConditions{ReadyStatus: ConditionStatusFalse, DegradedStatus: ConditionStatusTrue, Reason: ReasonHelmReleaseStuck},
		},
		{
			name: "active 1P transient Helm status error keeps previous healthy conditions",
			cms:  &mockCMS{},
			helm: &mockHelm{unhealthyReason: ReasonHelmStatusUnavailable},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "ingestion", Generation: 1, Spec: spec1P,
				HasFinalizer: true, DeletionProtection: true, Enabled: true,
				ExistingConditions: []Condition{
					{Type: ConditionTypeReady, Status: ConditionStatusTrue, ObservedGeneration: 1, Reason: ReasonComponentHealthy},
					{Type: ConditionTypeDegraded, Status: ConditionStatusFalse, ObservedGeneration: 1, Reason: ReasonComponentHealthy},
				},
			},
			wantState:  componentspb.ComponentState_COMPONENT_STATE_ENABLED,
			wantHealth: &healthConditions{ReadyStatus: ConditionStatusTrue, DegradedStatus: ConditionStatusFalse, Reason: ReasonComponentHealthy},
		},
		{
			name: "active 1P transient Helm status error keeps previous degraded conditions",
			cms:  &mockCMS{},
			helm: &mockHelm{unhealthyReason: ReasonHelmStatusUnavailable},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "ingestion", Generation: 1, Spec: spec1P,
				HasFinalizer: true, DeletionProtection: true, Enabled: true,
				ExistingConditions: []Condition{
					{Type: ConditionTypeReady, Status: ConditionStatusFalse, ObservedGeneration: 1, Reason: ReasonHelmReleaseNotFound},
					{Type: ConditionTypeDegraded, Status: ConditionStatusTrue, ObservedGeneration: 1, Reason: ReasonHelmReleaseNotFound},
				},
			},
			wantState:  componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
			wantHealth: &healthConditions{ReadyStatus: ConditionStatusFalse, DegradedStatus: ConditionStatusTrue, Reason: ReasonHelmReleaseNotFound},
		},
		{
			name: "active 1P transient Helm status error without current-generation conditions is unknown, not degraded",
			cms:  &mockCMS{},
			helm: &mockHelm{unhealthyReason: ReasonHelmStatusUnavailable},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "ingestion", Generation: 2, Spec: spec1P,
				HasFinalizer: true, DeletionProtection: true, Enabled: true,
				ExistingConditions: []Condition{
					{Type: ConditionTypeReady, Status: ConditionStatusTrue, ObservedGeneration: 1, Reason: ReasonComponentHealthy},
				},
			},
			wantState:  componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
			wantHealth: &healthConditions{ReadyStatus: ConditionStatusFalse, DegradedStatus: ConditionStatusFalse, Reason: ReasonHelmStatusUnavailable},
		},
		{
			name:   "active 1P without HelmManager falls back to prober",
			cms:    &mockCMS{},
			prober: &mockProber{healthy: false, msg: "probe failed"},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "ingestion", Generation: 1, Spec: spec1P,
				HasFinalizer: true, DeletionProtection: true, Enabled: true,
			},
			wantState:  componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
			wantHealth: &healthConditions{ReadyStatus: ConditionStatusFalse, DegradedStatus: ConditionStatusTrue, Reason: ReasonHealthProbeFailed},
		},
		{
			name: "deletion blocked by protection",
			cms:  &mockCMS{},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "clickhouse", Generation: 1, Spec: spec3P,
				IsDeleting: true, HasFinalizer: true, DeletionProtection: true, Enabled: true,
			},
			wantState: componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
		},
		{
			name: "deletion successful 1P",
			cms:  &mockCMS{},
			helm: &mockHelm{active: true},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "ingestion", Generation: 2, Spec: spec1P,
				IsDeleting: true, HasFinalizer: true, DeletionProtection: false,
			},
			wantState:     componentspb.ComponentState_COMPONENT_STATE_DELETED,
			wantRemoveFin: true,
		},
		{
			name: "deletion successful 1P orphan policy",
			cms:  &mockCMS{},
			helm: &mockHelm{active: true},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "ingestion", Generation: 2,
				Spec: &componentspb.ComponentSpec{
					Type:           componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
					DeletionPolicy: componentspb.DeletionPolicy_DELETION_POLICY_ORPHAN,
				},
				IsDeleting: true, HasFinalizer: true, DeletionProtection: false,
			},
			wantState:     componentspb.ComponentState_COMPONENT_STATE_DELETED,
			wantRemoveFin: true,
		},
		{
			name: "deletion successful 3P",
			cms:  &mockCMS{},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "clickhouse", Generation: 2, Spec: spec3P,
				IsDeleting: true, HasFinalizer: true, DeletionProtection: false,
			},
			wantState:     componentspb.ComponentState_COMPONENT_STATE_DELETED,
			wantRemoveFin: true,
		},
		{
			name: "deletion 1P Helm delete failure",
			cms:  &mockCMS{},
			helm: &mockHelm{deleteErr: errors.New("delete error")},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "ingestion", Generation: 2, Spec: spec1P,
				IsDeleting: true, HasFinalizer: true, DeletionProtection: false,
			},
			wantErr: true,
		},
		{
			name: "deletion CMS failure",
			cms:  &mockCMS{delErr: errors.New("cms spanner error")},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "ingestion", Generation: 2, Spec: spec1P,
				IsDeleting: true, HasFinalizer: true,
			},
			wantErr: true,
		},
		{
			name: "deletion CMS validation failure blocks 1P teardown",
			cms:  &mockCMS{delErr: status.Error(codes.FailedPrecondition, "active dependents exist")},
			helm: &mockHelm{active: true},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "ingestion", Generation: 2, Spec: spec1P,
				IsDeleting: true, HasFinalizer: true,
			},
			wantErr: true,
		},
		{
			name: "deletion CMS NotFound idempotent retry proceeds to 1P teardown",
			cms:  &mockCMS{delErr: status.Error(codes.NotFound, "already deleted")},
			helm: &mockHelm{active: true},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "ingestion", Generation: 2, Spec: spec1P,
				IsDeleting: true, HasFinalizer: true,
			},
			wantState:     componentspb.ComponentState_COMPONENT_STATE_DELETED,
			wantRemoveFin: true,
		},
		{
			name: "disabled component",
			cms:  &mockCMS{},
			helm: &mockHelm{active: true},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "ingestion", Generation: 2, Spec: spec1P,
				HasFinalizer: true, DeletionProtection: true, Enabled: false,
			},
			wantState: componentspb.ComponentState_COMPONENT_STATE_DISABLED,
		},
		{
			name: "disabled component 1P Helm delete failure",
			cms:  &mockCMS{},
			helm: &mockHelm{active: true, deleteErr: errors.New("cannot stop release")},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "ingestion", Generation: 2, Spec: spec1P,
				HasFinalizer: true, DeletionProtection: true, Enabled: false,
			},
			wantErr:   true,
			wantState: componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
		},
		{
			name: "disabled component update observed CMS failure",
			cms:  &mockCMS{obsErr: errors.New("cms unreachable")},
			helm: &mockHelm{},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "ingestion", Generation: 2, Spec: spec1P,
				HasFinalizer: true, DeletionProtection: true, Enabled: false,
			},
			wantErr: true,
		},
		{
			name: "disabled component with orphan deletion policy still deletes workload",
			cms:  &mockCMS{},
			helm: &mockHelm{active: true},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "ingestion", Generation: 2,
				Spec: &componentspb.ComponentSpec{
					Type:           componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
					DeletionPolicy: componentspb.DeletionPolicy_DELETION_POLICY_ORPHAN,
				},
				HasFinalizer: true, DeletionProtection: true, Enabled: false,
			},
			wantState: componentspb.ComponentState_COMPONENT_STATE_DISABLED,
		},
		{
			name:    "disabled component workload deletion failure",
			cms:     &mockCMS{},
			helm:    &mockHelm{active: true, deleteErr: errors.New("cannot delete child pod")},
			wantErr: true,
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "ingestion", Generation: 2, Spec: spec1P,
				HasFinalizer: true, DeletionProtection: true, Enabled: false,
			},
			wantState: componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
		},
		{
			name:    "disabled component update observed CMS error",
			cms:     &mockCMS{obsErr: errors.New("cms update error")},
			helm:    &mockHelm{active: true},
			wantErr: true,
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "ingestion", Generation: 2, Spec: spec1P,
				HasFinalizer: true, DeletionProtection: true, Enabled: false,
			},
			wantState: componentspb.ComponentState_COMPONENT_STATE_DISABLED,
		},
		{
			name:    "intent application CMS error",
			cms:     &mockCMS{setErr: status.Error(codes.InvalidArgument, "bad spec")},
			helm:    &mockHelm{active: true},
			wantErr: true,
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "ingestion", Generation: 1, Spec: spec1P,
				HasFinalizer: true, DeletionProtection: true, Enabled: true,
			},
			wantState: componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
		},
		{
			name:    "1P Helm provisioning error",
			cms:     &mockCMS{},
			helm:    &mockHelm{ensureErr: errors.New("upgrade failed")},
			wantErr: true,
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "ingestion", Generation: 1, Spec: spec1P,
				HasFinalizer: true, DeletionProtection: true, Enabled: true,
			},
			wantState: componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
		},
		{
			name:   "health probe unhealthy",
			cms:    &mockCMS{},
			prober: &mockProber{healthy: false, msg: "Pod CrashLoop"},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "clickhouse", Generation: 1, Spec: spec3PWithRef,
				HasFinalizer: true, DeletionProtection: true, Enabled: true,
			},
			wantState: componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
		},
		{
			name:   "health probe healthy",
			cms:    &mockCMS{},
			prober: &mockProber{healthy: true, msg: "Service active"},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "clickhouse", Generation: 1, Spec: spec3PWithRef,
				HasFinalizer: true, DeletionProtection: true, Enabled: true,
			},
			wantState: componentspb.ComponentState_COMPONENT_STATE_ENABLED,
		},
		{
			name:   "3P wrapper status detection error",
			cms:    &mockCMS{},
			prober: &mockProber{detectErr: errors.New("k8s api error")},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "clickhouse", Generation: 1, Spec: spec3PWithRef, HasFinalizer: true, DeletionProtection: true, Enabled: true,
			},
			wantErr: true,
		},
		{
			name:   "3P wrapper resource not found",
			cms:    &mockCMS{},
			prober: &mockProber{status: &ResourceStatus{Found: false, Ready: false, Message: "resource not found"}},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "clickhouse", Generation: 1, Spec: spec3PWithRef, HasFinalizer: true, DeletionProtection: true, Enabled: true,
			},
			wantState: componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
		},
		{
			name:   "3P wrapper unready with empty message fallback",
			cms:    &mockCMS{},
			prober: &mockProber{status: &ResourceStatus{Found: false, Ready: false, Message: ""}},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "clickhouse", Generation: 1, Spec: spec3PWithRef, HasFinalizer: true, DeletionProtection: true, Enabled: true,
			},
			wantState: componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
		},
		{
			name:   "3P wrapper resource not ready",
			cms:    &mockCMS{},
			prober: &mockProber{status: &ResourceStatus{Found: true, Ready: false, Message: "reconciling pods"}},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "clickhouse", Generation: 1, Spec: spec3PWithRef, HasFinalizer: true, DeletionProtection: true, Enabled: true,
			},
			wantState: componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
		},
		{
			name:   "3P wrapper active probe error",
			cms:    &mockCMS{},
			prober: &mockProber{status: &ResourceStatus{Found: true, Ready: true, Endpoint: "http://clickhouse.xmc:8123"}, probeErr: errors.New("connection refused")},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "clickhouse", Generation: 1, Spec: spec3PWithRef, HasFinalizer: true, DeletionProtection: true, Enabled: true,
			},
			wantState: componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
		},
		{
			name:   "3P wrapper active probe unhealthy",
			cms:    &mockCMS{},
			prober: &mockProber{status: &ResourceStatus{Found: true, Ready: true, Endpoint: "http://clickhouse.xmc:8123"}, probeRes: &HealthProbeResult{Healthy: false, Message: "503 Service Unavailable"}},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "clickhouse", Generation: 1, Spec: spec3PWithRef, HasFinalizer: true, DeletionProtection: true, Enabled: true,
			},
			wantState: componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
		},
		{
			name:   "3P wrapper status detector returns nil status without error",
			cms:    &mockCMS{},
			prober: &mockProber{hasStatus: true, status: nil},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "clickhouse", Generation: 1, Spec: spec3PWithRef, HasFinalizer: true, DeletionProtection: true, Enabled: true,
			},
			wantErr: true,
		},
		{
			name:   "3P wrapper prober returns nil probe result without error",
			cms:    &mockCMS{},
			prober: &mockProber{status: &ResourceStatus{Found: true, Ready: true, Endpoint: "http://clickhouse.xmc:8123"}, hasProbeRes: true, probeRes: nil},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "clickhouse", Generation: 1, Spec: spec3PWithRef, HasFinalizer: true, DeletionProtection: true, Enabled: true,
			},
			wantState: componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
		},
		{
			name:   "3P wrapper with nil prober returns enabled",
			cms:    &mockCMS{},
			prober: nil,
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "clickhouse", Generation: 1, Spec: spec3PWithRef, HasFinalizer: true, DeletionProtection: true, Enabled: true,
			},
			wantState: componentspb.ComponentState_COMPONENT_STATE_ENABLED,
		},
		{
			name:   "3P wrapper fallback nil wrapperRef with unhealthy prober",
			cms:    &mockCMS{},
			prober: &mockProber{healthy: false, msg: "fallback health probe failed"},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "clickhouse", Generation: 1, Spec: spec3P, HasFinalizer: true, DeletionProtection: true, Enabled: true,
			},
			wantState: componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
		},
		{
			name:   "3P wrapper handle3PUnreadyStatus CMS error",
			cms:    &mockCMS{obsErr: errors.New("cms update failed")},
			prober: &mockProber{status: &ResourceStatus{Found: false, Ready: false}},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "clickhouse", Generation: 1, Spec: spec3PWithRef, HasFinalizer: true, DeletionProtection: true, Enabled: true,
			},
			wantErr: true,
		},
		{
			name:   "3P wrapper handle3PReadyStatus CMS error",
			cms:    &mockCMS{obsErr: errors.New("cms update failed")},
			prober: &mockProber{status: &ResourceStatus{Found: true, Ready: true}},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "clickhouse", Generation: 1, Spec: spec3PWithRef, HasFinalizer: true, DeletionProtection: true, Enabled: true,
			},
			wantErr: true,
		},
		{
			name: "update observed CMS error",
			cms:  &mockCMS{obsErr: errors.New("timeout")},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "clickhouse", Generation: 1, Spec: spec3P,
				HasFinalizer: true, DeletionProtection: true, Enabled: true,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var p StatusDetectorHealthProber
			if tt.prober != nil {
				p = tt.prober
			}
			var helmMgr HelmManager
			if tt.helm != nil {
				helmMgr = tt.helm
			}
			r, _ := NewReconciler(tt.cms, p, helmMgr)
			res, err := r.ReconcileComponent(ctx, tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ReconcileComponent() error = %v, wantErr %v", err, tt.wantErr)
			}
			if res == nil {
				t.Fatal("expected non-nil ReconcileResult")
			}
			if err == nil {
				if res.State != tt.wantState {
					t.Errorf("got state %v, want %v", res.State, tt.wantState)
				}
				if res.AddFinalizer != tt.wantAddFin {
					t.Errorf("got AddFinalizer %v, want %v", res.AddFinalizer, tt.wantAddFin)
				}
				if res.RemoveFinalizer != tt.wantRemoveFin {
					t.Errorf("got RemoveFinalizer %v, want %v", res.RemoveFinalizer, tt.wantRemoveFin)
				}
			}
			if tt.name == "intent application CMS error" && tt.helm != nil && !tt.helm.active {
				t.Errorf("expected 1P component to remain active on transient CMS intent failure")
			}
			if (tt.name == "deletion 1P Helm delete failure" || tt.name == "deletion CMS failure") && len(res.Conditions) == 0 {
				t.Errorf("expected conditions to be populated on deletion failure, got empty conditions")
			}
			if tt.name == "active 3P with coordinate injection" {
				assertCoordinateEnvVars(t, res.Access)
			}
			if tt.name == "deletion blocked by protection" {
				assertConditionStatus(t, res.Conditions, ConditionTypeReady, ConditionStatusFalse)
				assertConditionStatus(t, res.Conditions, ConditionTypeDegraded, ConditionStatusFalse)
				assertConditionStatus(t, res.Conditions, ConditionTypeDeletionBlocked, ConditionStatusTrue)
			}
			verify3PStatusDetectorAssertions(t, tt.name, tt.cms, tt.in.Generation, res, err)
			if tt.wantHealth != nil {
				if diff := cmp.Diff(*tt.wantHealth, extractHealthConditions(res.Conditions)); diff != "" {
					t.Errorf("health conditions mismatch (-want +got):\n%s", diff)
				}
			}
		})
	}
}

// TestReconcileComponent_AsyncUpgradeReportsPendingUntilComplete guards against reporting Ready=True from the
// previously deployed revision while an async upgrade for a new generation is still in flight.
func TestReconcileComponent_AsyncUpgradeReportsPendingUntilComplete(t *testing.T) {
	ctx := context.Background()
	gate := make(chan struct{})
	runner := &mockHelmActionRunner{
		upgradeGate: gate,
		installed:   map[string]bool{componentKey("xmc", "xmc-cmp-ingestion"): true}, // Generation 1 is deployed.
	}
	mgr := NewK8sHelmManager(fake.NewClientset(), "xmc", "xmanager-cloud", WithActionRunner(runner))
	r, err := NewReconciler(&mockCMS{}, nil, mgr)
	if err != nil {
		t.Fatalf("NewReconciler() failed: %v", err)
	}
	in := ReconcileComponentInput{
		Namespace: "xmc", Name: "ingestion", Generation: 2,
		Spec:         &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT},
		HasFinalizer: true, DeletionProtection: true, Enabled: true,
		ExistingConditions: []Condition{
			{Type: ConditionTypeReady, Status: ConditionStatusTrue, ObservedGeneration: 1, Reason: ReasonComponentHealthy},
			{Type: ConditionTypeDegraded, Status: ConditionStatusFalse, ObservedGeneration: 1, Reason: ReasonComponentHealthy},
		},
	}

	res, err := r.ReconcileComponent(ctx, in)
	if err != nil {
		t.Fatalf("ReconcileComponent() while upgrade in flight failed: %v", err)
	}
	wantPending := healthConditions{ReadyStatus: ConditionStatusFalse, DegradedStatus: ConditionStatusFalse, Reason: ReasonHelmUpgradePending}
	if diff := cmp.Diff(wantPending, extractHealthConditions(res.Conditions)); diff != "" {
		t.Errorf("health conditions while upgrade in flight mismatch (-want +got):\n%s", diff)
	}
	if res.State != componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION {
		t.Errorf("state while upgrade in flight = %v, want PENDING_RECONCILIATION", res.State)
	}

	close(gate)
	_ = mgr.Close() // Waits for the worker to finish the upgrade.
	res, err = r.ReconcileComponent(ctx, in)
	if err != nil {
		t.Fatalf("ReconcileComponent() after upgrade failed: %v", err)
	}
	wantHealthy := healthConditions{ReadyStatus: ConditionStatusTrue, DegradedStatus: ConditionStatusFalse, Reason: ReasonComponentHealthy}
	if diff := cmp.Diff(wantHealthy, extractHealthConditions(res.Conditions)); diff != "" {
		t.Errorf("health conditions after upgrade mismatch (-want +got):\n%s", diff)
	}
	if got := runner.upgradeCalls; got != 1 {
		t.Errorf("upgradeCalls = %d, want 1 (second pass must not re-upgrade)", got)
	}
}

func TestHealthConditionStatuses(t *testing.T) {
	tests := []struct {
		healthy      bool
		reason       string
		wantReady    string
		wantDegraded string
	}{
		{healthy: true, reason: ReasonComponentHealthy, wantReady: ConditionStatusTrue, wantDegraded: ConditionStatusFalse},
		{reason: ReasonHelmUpgradePending, wantReady: ConditionStatusFalse, wantDegraded: ConditionStatusFalse},
		{reason: ReasonHelmStatusUnavailable, wantReady: ConditionStatusFalse, wantDegraded: ConditionStatusFalse},
		{reason: ReasonHelmReleaseStuck, wantReady: ConditionStatusFalse, wantDegraded: ConditionStatusTrue},
		{reason: ReasonHelmReleaseNotReady, wantReady: ConditionStatusFalse, wantDegraded: ConditionStatusTrue},
	}
	for _, tt := range tests {
		ready, degraded := healthConditionStatuses(tt.healthy, tt.reason)
		if ready != tt.wantReady || degraded != tt.wantDegraded {
			t.Errorf("healthConditionStatuses(%v, %q) = (%s, %s), want (%s, %s)", tt.healthy, tt.reason, ready, degraded, tt.wantReady, tt.wantDegraded)
		}
	}
}

// healthConditions captures the Ready/Degraded condition statuses and the Ready reason set by evaluateHealth.
type healthConditions struct {
	ReadyStatus    string
	DegradedStatus string
	Reason         string
}

func extractHealthConditions(conds []Condition) healthConditions {
	var got healthConditions
	for _, c := range conds {
		switch c.Type {
		case ConditionTypeReady:
			got.ReadyStatus, got.Reason = c.Status, c.Reason
		case ConditionTypeDegraded:
			got.DegradedStatus = c.Status
		}
	}
	return got
}

func assertCoordinateEnvVars(t *testing.T, access *AccessStatus) {
	t.Helper()
	if access == nil || access.EnvVars["ENDPOINT"] != "http://chi-prod.analytics.svc.cluster.local:8123" {
		t.Fatalf("expected access.EnvVars[ENDPOINT] populated, got %+v", access)
	}
	if access.EnvVars["HOST"] != "chi-prod.analytics.svc.cluster.local" {
		t.Errorf("expected HOST populated, got %v", access.EnvVars["HOST"])
	}
	if access.EnvVars["PORT"] != "8123" {
		t.Errorf("expected PORT populated, got %v", access.EnvVars["PORT"])
	}
	if access.EnvVars["SCHEME"] != "http" {
		t.Errorf("expected SCHEME populated, got %v", access.EnvVars["SCHEME"])
	}
	if access.EnvVars["SECRET_NAME"] != "clickhouse-credentials" {
		t.Errorf("expected SECRET_NAME populated, got %v", access.EnvVars["SECRET_NAME"])
	}
}

func assertConditionStatus(t *testing.T, conds []Condition, cType string, wantStatus string) {
	t.Helper()
	for _, c := range conds {
		if c.Type == cType {
			if c.Status != wantStatus {
				t.Errorf("condition %v: got status %v, want %v", cType, c.Status, wantStatus)
			}
			return
		}
	}
	t.Errorf("condition %v not found", cType)
}

func verify3PStatusDetectorAssertions(t *testing.T, testName string, cms *mockCMS, gen int64, res *ReconcileResult, err error) {
	t.Helper()
	if !strings.HasPrefix(testName, "active 3P with status detector") {
		return
	}
	if testName == "active 3P with status detector ready" {
		assertConditionStatus(t, res.Conditions, ConditionTypeReady, ConditionStatusTrue)
		assertConditionStatus(t, res.Conditions, ConditionTypeDegraded, ConditionStatusFalse)
		return
	}

	assertConditionStatus(t, res.Conditions, ConditionTypeReady, ConditionStatusFalse)
	assertConditionStatus(t, res.Conditions, ConditionTypeDegraded, ConditionStatusTrue)
	if res.State != componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION {
		t.Errorf("%s: expected state %v, got %v", testName, componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION, res.State)
	}
	if testName == "active 3P with status detector error and CMS failure" {
		if err == nil || !strings.Contains(err.Error(), "CMS update failed") {
			t.Errorf("expected error to mention CMS update failure, got: %v", err)
		}
		return
	}
	if cms == nil || cms.obsErr != nil {
		return
	}
	if cms.lastObsReq == nil {
		t.Errorf("%s: expected UpdateObservedGeneration to be called on CMS", testName)
		return
	}
	if cms.lastObsReq.ObservedGeneration != gen {
		t.Errorf("%s: expected CMS observed generation %d, got %d", testName, gen, cms.lastObsReq.ObservedGeneration)
	}
	if cms.lastObsReq.State != componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION {
		t.Errorf("%s: expected CMS state %v, got %v", testName, componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION, cms.lastObsReq.State)
	}
}

func TestReconcile_Unstructured(t *testing.T) {
	r, _ := NewReconciler(&mockCMS{}, nil, nil)
	u := &unstructured.Unstructured{
		Object: map[string]any{
			"metadata": map[string]any{
				"name":       "test-comp",
				"namespace":  "xmc",
				"generation": int64(1),
			},
			"spec": map[string]any{
				"enabled":       true,
				"componentType": "COMPONENT_TYPE_CUSTOM_1P_COMPONENT",
			},
		},
	}
	res, err := r.Reconcile(context.Background(), u)
	if err != nil || res == nil || res.State != componentspb.ComponentState_COMPONENT_STATE_ENABLED {
		t.Fatalf("unexpected Reconcile outcome: res=%+v, err=%v", res, err)
	}
}

func TestSetCondition(t *testing.T) {
	prev := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	existing := []Condition{
		{Type: ConditionTypeReady, Status: ConditionStatusTrue, LastTransitionTime: prev},
	}
	var conds []Condition

	// Test 1: Preserves existing timestamp when status matches
	setCondition(&conds, existing, ConditionTypeReady, ConditionStatusTrue, 1, "R", "M")
	if len(conds) != 1 || !conds[0].LastTransitionTime.Equal(prev) {
		t.Errorf("expected transition time preserved: %+v", conds)
	}

	// Test 2: Updates condition when status changes
	setCondition(&conds, existing, ConditionTypeReady, ConditionStatusFalse, 2, "R2", "M2")
	if len(conds) != 1 || conds[0].Status != ConditionStatusFalse || conds[0].LastTransitionTime.Equal(prev) {
		t.Errorf("expected updated condition with new transition time: %+v", conds)
	}

	// Test 3: Appends a new condition of a different type
	setCondition(&conds, existing, ConditionTypeDegraded, ConditionStatusTrue, 2, "D", "DegradedMsg")
	if len(conds) != 2 {
		t.Errorf("expected 2 conditions, got: %+v", conds)
	}
}

type dummyServer struct {
	componentspb.UnimplementedComponentManagerServiceServer
}

func (d *dummyServer) SetComponentIntent(context.Context, *componentspb.SetComponentIntentRequest) (*componentspb.SetComponentIntentResponse, error) {
	return &componentspb.SetComponentIntentResponse{}, nil
}

func (d *dummyServer) UpdateObservedGeneration(context.Context, *componentspb.UpdateObservedGenerationRequest) (*componentspb.UpdateObservedGenerationResponse, error) {
	return &componentspb.UpdateObservedGenerationResponse{}, nil
}

func (d *dummyServer) ExecuteDeletion(context.Context, *componentspb.ExecuteDeletionRequest) (*componentspb.ExecuteDeletionResponse, error) {
	return &componentspb.ExecuteDeletionResponse{}, nil
}

func TestCMSClientAdapter(t *testing.T) {
	a := NewCMSClientAdapter(&dummyServer{})
	ctx := context.Background()
	if _, err := a.SetComponentIntent(ctx, nil); err != nil {
		t.Error(err)
	}
	if _, err := a.UpdateObservedGeneration(ctx, nil); err != nil {
		t.Error(err)
	}
	if _, err := a.ExecuteDeletion(ctx, nil); err != nil {
		t.Error(err)
	}
}

func TestParseHelpers(t *testing.T) {
	spec, dp, en := ParseSpecFromUnstructured(nil)
	if !dp || !en || spec.GetType() != componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT {
		t.Errorf("unexpected defaults: spec=%+v, dp=%v, en=%v", spec, dp, en)
	}

	raw := map[string]any{
		"enabled":            true,
		"deletionProtection": false,
		"deletionPolicy":     "Orphan",
		"componentType":      "COMPONENT_TYPE_CUSTOM_3P_WRAPPER",
		"version":            "1.2.3",
		"description":        "desc",
		"wrapperRef": map[string]any{
			"group": "g", "version": "v", "kind": "k", "name": "n", "namespace": "ns",
		},
		"manifests": []any{"yaml-1", 123, "yaml-2"},
		"parameters": map[string]any{
			"topicName":   "xmc-metrics",
			"port":        8080,
			"bufferBytes": float64(1048576),
			"rate":        float64(3.14),
			"enabled":     true,
			"replica":     int64(3),
			"ignored":     nil,
		},
	}
	spec, dp, en = ParseSpecFromUnstructured(raw)
	wantSpec := &componentspb.ComponentSpec{
		Type:           componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER,
		Version:        "1.2.3",
		Description:    "desc",
		DeletionPolicy: componentspb.DeletionPolicy_DELETION_POLICY_ORPHAN,
		WrapperRef: &componentspb.WrapperRef{
			CrdGroup:        "g",
			CrdVersion:      "v",
			CrdKind:         "k",
			TargetName:      "n",
			TargetNamespace: "ns",
		},
		Manifests: []string{"yaml-1", "yaml-2"},
		Parameters: map[string]string{
			"topicName":   "xmc-metrics",
			"port":        "8080",
			"bufferBytes": "1048576",
			"rate":        "3.14",
			"enabled":     "true",
			"replica":     "3",
		},
	}
	if dp || !en || !proto.Equal(spec, wantSpec) {
		t.Errorf("ParseSpecFromUnstructured() = %+v (dp=%v, en=%v), want %+v (dp=false, en=true)", spec, dp, en, wantSpec)
	}

	rawCamel := map[string]any{
		"enabled":       true,
		"componentType": "COMPONENT_TYPE_CUSTOM_3P_WRAPPER",
		"wrapperRef": map[string]any{
			"crdGroup": "clickhouse.altinity.com", "crdVersion": "v1", "crdKind": "ClickHouseInstallation",
			"targetName": "chi-cluster", "targetNamespace": "clickhouse-ns",
		},
	}
	specCamel, _, _ := ParseSpecFromUnstructured(rawCamel)
	wr := specCamel.GetWrapperRef()
	wantWrapperRef := &componentspb.WrapperRef{
		CrdGroup:        "clickhouse.altinity.com",
		CrdVersion:      "v1",
		CrdKind:         "ClickHouseInstallation",
		TargetName:      "chi-cluster",
		TargetNamespace: "clickhouse-ns",
	}
	if !proto.Equal(wr, wantWrapperRef) {
		t.Errorf("ParseSpecFromUnstructured() wrapperRef = %+v, want %+v", wr, wantWrapperRef)
	}

	rawDelete := map[string]any{
		"deletionPolicy": "DELETION_POLICY_ORPHAN",
	}
	specDel, _, _ := ParseSpecFromUnstructured(rawDelete)
	if specDel.GetDeletionPolicy() != componentspb.DeletionPolicy_DELETION_POLICY_ORPHAN {
		t.Errorf("expected DELETION_POLICY_ORPHAN, got %v", specDel.GetDeletionPolicy())
	}

	if ParseConditionsFromUnstructured(nil) != nil || ParseConditionsFromUnstructured(map[string]any{"conditions": "inv"}) != nil {
		t.Error("expected nil for empty/invalid conditions")
	}

	conds := ParseConditionsFromUnstructured(map[string]any{
		"conditions": []any{
			map[string]any{"type": "Ready", "status": "True", "observedGeneration": float64(2), "lastTransitionTime": "2026-08-24T00:00:00Z"},
			map[string]any{"type": "Degraded", "status": "False", "observedGeneration": "invalid", "lastTransitionTime": "invalid-date"},
			"not-a-map",
		},
	})
	if len(conds) != 2 || conds[0].ObservedGeneration != 2 || conds[0].Status != "True" || conds[1].ObservedGeneration != 0 {
		t.Errorf("unexpected conditions: %+v", conds)
	}
}

func TestParseSpecFromUnstructured_Dependencies(t *testing.T) {
	tests := []struct {
		name string
		spec any
		want []string
	}{
		{name: "nil spec", spec: nil, want: nil},
		{name: "dependencies absent", spec: map[string]any{"version": "v1.0.0"}, want: nil},
		{name: "dependencies not a list", spec: map[string]any{"dependencies": "redpanda"}, want: nil},
		{name: "empty list", spec: map[string]any{"dependencies": []any{}}, want: nil},
		{
			name: "single dependency",
			spec: map[string]any{"dependencies": []any{"redpanda"}},
			want: []string{"redpanda"},
		},
		{
			name: "canonicalizes, dedupes, and preserves declaration order",
			spec: map[string]any{"dependencies": []any{" redpanda ", "components/auth", "redpanda", "components/redpanda"}},
			want: []string{"redpanda", "auth"},
		},
		{
			name: "keeps malformed strings verbatim for CMS validation and drops non-string entries",
			spec: map[string]any{"dependencies": []any{"", "  ", "/", float64(1), nil, map[string]any{"name": "x"}, "metrics"}},
			want: []string{"", "  ", "/", "metrics"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			spec, _, _ := ParseSpecFromUnstructured(tc.spec)
			if diff := cmp.Diff(tc.want, spec.GetDependencies()); diff != "" {
				t.Errorf("ParseSpecFromUnstructured().GetDependencies() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestDropSelfDependency(t *testing.T) {
	tests := []struct {
		name string
		deps []string
		want []string
	}{
		{name: "nil", deps: nil, want: nil},
		{name: "no self reference", deps: []string{"redpanda", "auth"}, want: []string{"redpanda", "auth"}},
		{name: "self reference dropped", deps: []string{"redpanda", "ingestion-gateway", "auth"}, want: []string{"redpanda", "auth"}},
		{name: "only self reference", deps: []string{"ingestion-gateway"}, want: nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := dropSelfDependency(context.Background(), tc.deps, "ingestion-gateway")
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("dropSelfDependency() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestParseParameters(t *testing.T) {
	tests := []struct {
		name string
		in   map[string]any
		want map[string]string
	}{
		{
			name: "nil input map",
			in:   nil,
			want: nil,
		},
		{
			name: "missing parameters key",
			in:   map[string]any{"other": "value"},
			want: nil,
		},
		{
			name: "empty parameters map",
			in:   map[string]any{"parameters": map[string]any{}},
			want: nil,
		},
		{
			name: "only nil values in parameters",
			in:   map[string]any{"parameters": map[string]any{"key": nil}},
			want: nil,
		},
		{
			name: "mixed types with large float64 integers",
			in: map[string]any{
				"parameters": map[string]any{
					"str":       "hello",
					"largeInt":  float64(1048576),
					"zeroFloat": float64(0),
					"decimal":   float64(12.34),
					"intVal":    int(42),
					"int64Val":  int64(9999),
					"boolTrue":  true,
					"boolFalse": false,
					"skipped":   nil,
				},
			},
			want: map[string]string{
				"str":       "hello",
				"largeInt":  "1048576",
				"zeroFloat": "0",
				"decimal":   "12.34",
				"intVal":    "42",
				"int64Val":  "9999",
				"boolTrue":  "true",
				"boolFalse": "false",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseParameters(tc.in)
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if len(got) != len(tc.want) {
				t.Fatalf("parseParameters() len = %d, want %d (got: %+v, want: %+v)", len(got), len(tc.want), got, tc.want)
			}
			for k, wantV := range tc.want {
				if gotV, ok := got[k]; !ok || gotV != wantV {
					t.Errorf("parseParameters()[%q] = %q, want %q", k, gotV, wantV)
				}
			}
		})
	}
}

func TestExtractHelpers(t *testing.T) {
	m := map[string]any{
		"int64":        int64(42),
		"int":          int(100),
		"int32":        int32(150),
		"float64":      float64(200.5),
		"jsonNumInt":   json.Number("300"),
		"jsonNumBad":   json.Number("not_a_number"),
		"stringInt":    "400",
		"stringBad":    "bad",
		"boolVal":      true,
		"strVal":       "hello",
		"emptyWrapper": "not-a-map",
		"emptyList":    "not-a-list",
	}

	if extractInt(m, "int64") != 42 {
		t.Errorf("extractInt int64 failed")
	}
	if extractInt(m, "int") != 100 {
		t.Errorf("extractInt int failed")
	}
	if extractInt(m, "int32") != 150 {
		t.Errorf("extractInt int32 failed")
	}
	if extractInt(m, "float64") != 200 {
		t.Errorf("extractInt float64 failed")
	}
	if extractInt(m, "jsonNumInt") != 300 {
		t.Errorf("extractInt jsonNumInt failed")
	}
	if extractInt(m, "jsonNumBad") != 0 {
		t.Errorf("extractInt jsonNumBad failed")
	}
	if extractInt(m, "stringInt") != 400 {
		t.Errorf("extractInt stringInt failed")
	}
	if extractInt(m, "stringBad") != 0 {
		t.Errorf("extractInt stringBad failed")
	}
	if extractInt(m, "boolVal") != 0 {
		t.Errorf("extractInt boolVal failed")
	}
	if extractInt(m, "nonexistent") != 0 {
		t.Errorf("extractInt nonexistent failed")
	}

	if extractStr(m, "strVal") != "hello" {
		t.Errorf("extractStr strVal failed")
	}
	if extractStr(m, "int") != "" {
		t.Errorf("extractStr int failed")
	}
	if extractStr(m, "nonexistent") != "" {
		t.Errorf("extractStr nonexistent failed")
	}

	if !extractBool(m, "boolVal", false) {
		t.Errorf("extractBool boolVal failed")
	}
	if !extractBool(m, "nonexistent", true) {
		t.Errorf("extractBool nonexistent default true failed")
	}
	if extractBool(m, "strVal", false) {
		t.Errorf("extractBool strVal default false failed")
	}

	if parseManifests(m) != nil {
		t.Errorf("parseManifests not-a-list failed")
	}
	if parseWrapperRef(m) != nil {
		t.Errorf("parseWrapperRef not-a-map failed")
	}
}

func TestReconcileComponent_1P_PopulatesEmptyHelmOptsRef(t *testing.T) {
	ctx := context.Background()
	helm := &mockHelm{}
	r, err := NewReconciler(&mockCMS{}, nil, helm)
	if err != nil {
		t.Fatalf("NewReconciler failed: %v", err)
	}

	in := ReconcileComponentInput{
		Namespace: "custom-ns",
		Name:      "custom-comp",
		Enabled:   true,
		Spec: &componentspb.ComponentSpec{
			Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		},
		HelmOpts: HelmComponentOptions{
			// Ref intentionally empty
		},
	}

	res := &ReconcileResult{}
	if _, err := r.ensure1PComponent(ctx, in, res, false); err != nil {
		t.Fatalf("ensure1PComponent failed: %v", err)
	}

	wantRef := ComponentRef{Namespace: "custom-ns", Name: "custom-comp"}
	if helm.lastRef != wantRef {
		t.Errorf("expected lastRef %v, got %v", wantRef, helm.lastRef)
	}
	if helm.lastOpts.Ref != wantRef {
		t.Errorf("expected lastOpts.Ref %v, got %v", wantRef, helm.lastOpts.Ref)
	}
}

func TestResolveComponentRef(t *testing.T) {
	tests := []struct {
		name    string
		in      ReconcileComponentInput
		wantRef ComponentRef
	}{
		{
			name: "both name and namespace empty in HelmOpts",
			in: ReconcileComponentInput{
				Namespace: "default-ns",
				Name:      "default-name",
				HelmOpts:  HelmComponentOptions{},
			},
			wantRef: ComponentRef{Namespace: "default-ns", Name: "default-name"},
		},
		{
			name: "ref name set but namespace empty in HelmOpts",
			in: ReconcileComponentInput{
				Namespace: "fallback-ns",
				Name:      "original-name",
				HelmOpts: HelmComponentOptions{
					Ref: ComponentRef{Name: "custom-name"},
				},
			},
			wantRef: ComponentRef{Namespace: "fallback-ns", Name: "custom-name"},
		},
		{
			name: "ref namespace set but name empty in HelmOpts",
			in: ReconcileComponentInput{
				Namespace: "original-ns",
				Name:      "fallback-name",
				HelmOpts: HelmComponentOptions{
					Ref: ComponentRef{Namespace: "custom-ns"},
				},
			},
			wantRef: ComponentRef{Namespace: "custom-ns", Name: "fallback-name"},
		},
		{
			name: "both ref name and namespace set in HelmOpts",
			in: ReconcileComponentInput{
				Namespace: "ignored-ns",
				Name:      "ignored-name",
				HelmOpts: HelmComponentOptions{
					Ref: ComponentRef{Namespace: "custom-ns", Name: "custom-name"},
				},
			},
			wantRef: ComponentRef{Namespace: "custom-ns", Name: "custom-name"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveComponentRef(tc.in)
			if got != tc.wantRef {
				t.Errorf("resolveComponentRef() = %+v, want %+v", got, tc.wantRef)
			}
		})
	}
}

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

func TestReconcileComponent_1P_GenerationGating(t *testing.T) {
	ctx := context.Background()
	cms := &mockCMS{}
	prober := &mockProber{healthy: true}
	helm := &mockHelm{}
	r, err := NewReconciler(cms, prober, helm)
	if err != nil {
		t.Fatalf("failed to create reconciler: %v", err)
	}

	spec1P := &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT}
	in := ReconcileComponentInput{
		Namespace:          "xmc",
		Name:               "ingestion-gateway",
		Generation:         1,
		Spec:               spec1P,
		Enabled:            true,
		DeletionProtection: true,
	}

	// First pass (generation 1): should actuate Helm upgrade
	res1, err := r.ReconcileComponent(ctx, in)
	if err != nil {
		t.Fatalf("first ReconcileComponent failed: %v", err)
	}
	if res1.State != componentspb.ComponentState_COMPONENT_STATE_ENABLED {
		t.Errorf("expected state ENABLED, got %v", res1.State)
	}
	if helm.ensureCalls != 1 {
		t.Errorf("expected 1 ensureCall on initial deploy, got %d", helm.ensureCalls)
	}

	// Periodic pass (generation 1 unchanged): should skip Helm upgrade completely
	res2, err := r.ReconcileComponent(ctx, in)
	if err != nil {
		t.Fatalf("periodic ReconcileComponent failed: %v", err)
	}
	if res2.State != componentspb.ComponentState_COMPONENT_STATE_ENABLED {
		t.Errorf("expected state ENABLED, got %v", res2.State)
	}
	if helm.ensureCalls != 1 {
		t.Errorf("expected ensureCalls to remain 1 (skipped on unchanged generation), got %d", helm.ensureCalls)
	}

	// Spec update pass (generation 2): should actuate Helm upgrade again
	in.Generation = 2
	res3, err := r.ReconcileComponent(ctx, in)
	if err != nil {
		t.Fatalf("spec update ReconcileComponent failed: %v", err)
	}
	if res3.State != componentspb.ComponentState_COMPONENT_STATE_ENABLED {
		t.Errorf("expected state ENABLED, got %v", res3.State)
	}
	if helm.ensureCalls != 2 {
		t.Errorf("expected 2 ensureCalls after generation update, got %d", helm.ensureCalls)
	}

	// Periodic pass (generation 2 unchanged): should skip Helm upgrade again
	res4, err := r.ReconcileComponent(ctx, in)
	if err != nil {
		t.Fatalf("periodic ReconcileComponent at gen 2 failed: %v", err)
	}
	if res4.State != componentspb.ComponentState_COMPONENT_STATE_ENABLED {
		t.Errorf("expected state ENABLED, got %v", res4.State)
	}
	if helm.ensureCalls != 2 {
		t.Errorf("expected ensureCalls to remain 2 (skipped on unchanged generation), got %d", helm.ensureCalls)
	}
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
		Namespace:    "xmc",
		Name:         "metrics",
		Generation:   1,
		Spec:         spec1P,
		Enabled:      false,
		HasFinalizer: true,
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

func TestParseWrapperRef(t *testing.T) {
	if ref := ParseWrapperRef(nil, "default-ns"); ref.Namespace != "default-ns" {
		t.Errorf("expected default namespace for nil spec: %+v", ref)
	}

	spec := map[string]any{
		"wrapperRef": map[string]any{
			"group":                    "ray.io",
			"version":                  "v1",
			"kind":                     "RayCluster",
			"name":                     "my-ray",
			"namespace":                "",
			"observedGenerationPath":   "status.obsGen",
			"readyConditionPath":       "status.conditions[0].status",
			"expectedReadyValue":       "True",
			"endpointJsonPath":         "status.url",
			"connectionSecretJsonPath": "status.secret",
		},
	}
	ref := ParseWrapperRef(spec, "fallback-ns")
	wantRef := WrapperRef{
		Group:                    "ray.io",
		Version:                  "v1",
		Kind:                     "RayCluster",
		Name:                     "my-ray",
		Namespace:                "fallback-ns",
		ObservedGenerationPath:   "status.obsGen",
		ReadyConditionPath:       "status.conditions[0].status",
		ExpectedReadyValue:       "True",
		EndpointJSONPath:         "status.url",
		ConnectionSecretJSONPath: "status.secret",
	}
	if ref != wantRef {
		t.Errorf("ParseWrapperRef() = %+v, want %+v", ref, wantRef)
	}

	specCamel := map[string]any{
		"wrapperRef": map[string]any{
			"crdGroup":                 "clickhouse.altinity.com",
			"crdVersion":               "v1",
			"crdKind":                  "ClickHouseInstallation",
			"targetName":               "chi-cluster",
			"targetNamespace":          "clickhouse-ns",
			"observedGenerationPath":   "status.observedGeneration",
			"readyConditionPath":       "status.ready",
			"expectedReadyValue":       "Ready",
			"endpointJsonPath":         "status.endpoint",
			"connectionSecretJsonPath": "status.secretName",
		},
	}
	refCamel := ParseWrapperRef(specCamel, "default-ns")
	wantCamel := WrapperRef{
		Group:                    "clickhouse.altinity.com",
		Version:                  "v1",
		Kind:                     "ClickHouseInstallation",
		Name:                     "chi-cluster",
		Namespace:                "clickhouse-ns",
		ObservedGenerationPath:   "status.observedGeneration",
		ReadyConditionPath:       "status.ready",
		ExpectedReadyValue:       "Ready",
		EndpointJSONPath:         "status.endpoint",
		ConnectionSecretJSONPath: "status.secretName",
	}
	if refCamel != wantCamel {
		t.Errorf("ParseWrapperRef(camelCase) = %+v, want %+v", refCamel, wantCamel)
	}
}

func TestReconcileComponent_3PWrapperAdvanced(t *testing.T) {
	ctx := context.Background()
	spec3P := &componentspb.ComponentSpec{
		Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER,
		WrapperRef: &componentspb.WrapperRef{
			CrdGroup: "ray.io", CrdVersion: "v1", CrdKind: "RayCluster", TargetName: "ray-1", TargetNamespace: "xmc",
		},
	}

	t.Run("preserves component observed generation and does not overwrite with 3p resource gen", func(t *testing.T) {
		detector := NewFakeStatusDetector()
		detector.SetStatus("xmc/ray-1", &ResourceStatus{
			Found:              true,
			Ready:              true,
			ObservedGeneration: 999,
			Endpoint:           "http://ray-head.xmc.svc.cluster.local:8265",
			ConnectionSecret:   "ray-secret",
		})
		prober := NewFakeHealthProber()
		prober.SetResult("http://ray-head.xmc.svc.cluster.local:8265", &HealthProbeResult{Healthy: true, Message: "HTTP 200"})
		bp := NewBridgeProber(detector, prober, time.Second)

		cms := &mockCMS{}
		r, _ := NewReconciler(cms, bp, nil)

		res, err := r.ReconcileComponent(ctx, ReconcileComponentInput{
			Namespace: "xmc", Name: "ray-1", Generation: 5, Spec: spec3P, Enabled: true,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.ObservedGeneration != 5 {
			t.Errorf("expected ObservedGeneration 5 (Component gen), got %d", res.ObservedGeneration)
		}
		wantAccess := &AccessStatus{
			EnvVars: map[string]string{
				"ENDPOINT":    "http://ray-head.xmc.svc.cluster.local:8265",
				"HOST":        "ray-head.xmc.svc.cluster.local",
				"PORT":        "8265",
				"SCHEME":      "http",
				"SECRET_NAME": "ray-secret",
			},
		}
		if !reflect.DeepEqual(res.Access, wantAccess) {
			t.Errorf("ReconcileComponent() access = %+v, want %+v", res.Access, wantAccess)
		}
		for _, c := range res.Conditions {
			if c.ObservedGeneration != 5 {
				t.Errorf("condition %s has observedGeneration %d, want 5", c.Type, c.ObservedGeneration)
			}
		}
	})

	t.Run("handles resource not found vs crd not found", func(t *testing.T) {
		detector := NewFakeStatusDetector()
		detector.SetStatus("xmc/missing-res", &ResourceStatus{
			Found:   false,
			Ready:   false,
			Message: "resource xmc/missing-res not found",
		})
		detector.SetStatus("xmc/missing-crd", &ResourceStatus{
			Found:   false,
			Ready:   false,
			Message: "CRD not installed: no matches for kind \"Foo\"",
		})
		bp := NewBridgeProber(detector, nil, time.Second)
		r, _ := NewReconciler(&mockCMS{}, bp, nil)

		// Case 1: ResourceNotFound
		res1, err := r.ReconcileComponent(ctx, ReconcileComponentInput{
			Namespace: "xmc", Name: "missing-res", Generation: 1,
			Spec: &componentspb.ComponentSpec{
				Type:       componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER,
				WrapperRef: &componentspb.WrapperRef{TargetName: "missing-res", TargetNamespace: "xmc"},
			},
			Enabled: true,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		foundReason := false
		for _, c := range res1.Conditions {
			if c.Type == ConditionTypeReady && c.Reason == "ResourceNotFound" {
				foundReason = true
			}
		}
		if !foundReason {
			t.Errorf("expected Condition Ready reason ResourceNotFound, got: %+v", res1.Conditions)
		}

		// Case 2: CRDNotFound
		res2, err := r.ReconcileComponent(ctx, ReconcileComponentInput{
			Namespace: "xmc", Name: "missing-crd", Generation: 1,
			Spec: &componentspb.ComponentSpec{
				Type:       componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER,
				WrapperRef: &componentspb.WrapperRef{TargetName: "missing-crd", TargetNamespace: "xmc"},
			},
			Enabled: true,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		foundCRDReason := false
		for _, c := range res2.Conditions {
			if c.Type == ConditionTypeReady && c.Reason == "CRDNotFound" {
				foundCRDReason = true
			}
		}
		if !foundCRDReason {
			t.Errorf("expected Condition Ready reason CRDNotFound, got: %+v", res2.Conditions)
		}
	})
}

func TestBridgeProber(t *testing.T) {
	fakeDetector := NewFakeStatusDetector()
	fakeDetector.SetStatus("xmc/my-crd", &ResourceStatus{Found: true, Ready: true, Endpoint: "http://my-crd.xmc:8080"})
	fakeProber := NewFakeHealthProber()
	fakeProber.SetResult("http://my-crd.xmc:8080", &HealthProbeResult{Healthy: true, Message: "HTTP 200"})

	bp := NewBridgeProber(fakeDetector, fakeProber, time.Second)
	ctx := context.Background()

	// DetectStatus
	st, err := bp.DetectStatus(ctx, WrapperRef{Name: "my-crd", Namespace: "xmc"})
	if err != nil || !st.Found || !st.Ready {
		t.Errorf("DetectStatus() = %+v, %v", st, err)
	}

	// Probe
	pr, err := bp.Probe(ctx, "http://my-crd.xmc:8080", time.Second)
	if err != nil || !pr.Healthy {
		t.Errorf("Probe() = %+v, %v", pr, err)
	}
	prZeroTimeout, err := bp.Probe(ctx, "http://my-crd.xmc:8080", 0)
	if err != nil || !prZeroTimeout.Healthy {
		t.Errorf("Probe() with zero timeout = %+v, %v", prZeroTimeout, err)
	}

	// ProbeHealth for 1P
	if ok, _ := bp.ProbeHealth(ctx, &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT}, "xmc"); !ok {
		t.Error("expected 1P component healthy")
	}

	// ProbeHealth for 3P
	spec3P := &componentspb.ComponentSpec{
		Type:       componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER,
		WrapperRef: &componentspb.WrapperRef{CrdGroup: "custom.io", CrdVersion: "v1", CrdKind: "Custom", TargetName: "my-crd", TargetNamespace: "xmc"},
	}
	if ok, msg := bp.ProbeHealth(ctx, spec3P, "xmc"); !ok || msg != "HTTP 200" {
		t.Errorf("expected 3P healthy: ok=%v, msg=%s", ok, msg)
	}

	// ProbeHealth with uninitialized detector
	emptyBP := &BridgeProber{}
	if ok, _ := emptyBP.ProbeHealth(ctx, spec3P, "xmc"); ok {
		t.Error("expected false for uninitialized BridgeProber")
	}
	if _, err := emptyBP.DetectStatus(ctx, WrapperRef{}); err == nil {
		t.Error("expected error for uninitialized detector")
	}
	if _, err := emptyBP.Probe(ctx, "http://foo", time.Second); err == nil {
		t.Error("expected error for uninitialized health prober")
	}

	// ProbeHealth with nil status returned from detector
	nilStatusDetector := &mockDetector{status: nil}
	bpWithNilStatus := NewBridgeProber(nilStatusDetector, fakeProber, time.Second)
	if ok, msg := bpWithNilStatus.ProbeHealth(ctx, spec3P, "xmc"); ok || msg != "status detector returned nil status" {
		t.Errorf("expected false for nil status from detector: ok=%v, msg=%s", ok, msg)
	}

	// ProbeHealth with nil result returned from prober
	nilResultProber := &mockProberDirect{probeRes: nil}
	bpWithNilResult := NewBridgeProber(fakeDetector, nilResultProber, time.Second)
	if ok, msg := bpWithNilResult.ProbeHealth(ctx, spec3P, "xmc"); ok || msg != "health prober returned nil result" {
		t.Errorf("expected false for nil result from prober: ok=%v, msg=%s", ok, msg)
	}

	// ProbeHealth for 3P without wrapperRef and without endpoint parameters must fail
	spec3PEmpty := &componentspb.ComponentSpec{
		Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER,
	}
	if ok, msg := bp.ProbeHealth(ctx, spec3PEmpty, "xmc"); ok || msg != "no wrapperRef or connection endpoint parameters configured" {
		t.Errorf("expected false for 3P without wrapperRef or endpoint parameters: ok=%v, msg=%s", ok, msg)
	}

	// ProbeHealth for 3P without wrapperRef with comma-prefixed / fallback endpoint parameters
	spec3PParams := &componentspb.ComponentSpec{
		Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER,
		Parameters: map[string]string{
			"healthCheckEndpoint": " , ",
			"clusterEndpoints":    " , http://my-crd.xmc:8080 , ",
		},
	}
	if ok, msg := bp.ProbeHealth(ctx, spec3PParams, "xmc"); !ok || msg != "HTTP 200" {
		t.Errorf("expected true for 3P with endpoint parameters: ok=%v, msg=%s", ok, msg)
	}
}

func TestReconcile3PWrapper_CrossNamespaceForbidden(t *testing.T) {
	ctx := context.Background()
	rec := &Reconciler{
		cmsClient: &mockCMS{},
		prober:    &mockProber{},
	}
	in := ReconcileComponentInput{
		Name:      "my-3p-comp",
		Namespace: "tenant-a",
		Enabled:   true,
		Spec: &componentspb.ComponentSpec{
			Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER,
			WrapperRef: &componentspb.WrapperRef{
				CrdGroup:        "clickhouse.altinity.com",
				CrdVersion:      "v1",
				CrdKind:         "ClickHouseInstallation",
				TargetName:      "chi-cluster",
				TargetNamespace: "kube-system",
			},
		},
	}
	res, err := rec.ReconcileComponent(ctx, in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.State != componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION {
		t.Errorf("expected PENDING_RECONCILIATION, got %v", res.State)
	}
	var readyCond *Condition
	for i := range res.Conditions {
		if res.Conditions[i].Type == ConditionTypeReady {
			readyCond = &res.Conditions[i]
			break
		}
	}
	if readyCond == nil || readyCond.Status != ConditionStatusFalse || readyCond.Reason != "CrossNamespaceForbidden" {
		t.Errorf("expected Ready condition False with reason CrossNamespaceForbidden, got %+v", readyCond)
	}
}

type mockDetector struct {
	status *ResourceStatus
	err    error
}

func (m *mockDetector) DetectStatus(ctx context.Context, ref WrapperRef) (*ResourceStatus, error) {
	return m.status, m.err
}

type mockProberDirect struct {
	probeRes *HealthProbeResult
	err      error
}

func (m *mockProberDirect) Probe(ctx context.Context, target string, timeout time.Duration) (*HealthProbeResult, error) {
	return m.probeRes, m.err
}
func (m *mockProberDirect) ProbeHTTP(ctx context.Context, targetURL string, timeout time.Duration) (*HealthProbeResult, error) {
	return m.probeRes, m.err
}
func (m *mockProberDirect) ProbeTCP(ctx context.Context, address string, timeout time.Duration) (*HealthProbeResult, error) {
	return m.probeRes, m.err
}

func TestPopulateAccessEnvVars(t *testing.T) {
	t.Run("nil result or nil status does not panic", func(t *testing.T) {
		populateAccessEnvVars(nil, &ResourceStatus{})
		res := &ReconcileResult{}
		populateAccessEnvVars(res, nil)
		if res.Access != nil {
			t.Errorf("expected Access to remain nil on nil status")
		}
	})

	t.Run("populates access with uninitialized EnvVars map", func(t *testing.T) {
		res := &ReconcileResult{}
		status := &ResourceStatus{
			Endpoint:         "http://service.prod:8080",
			ConnectionSecret: "db-secret",
		}
		populateAccessEnvVars(res, status)
		wantAccess := &AccessStatus{
			EnvVars: map[string]string{
				"ENDPOINT":    "http://service.prod:8080",
				"HOST":        "service.prod",
				"PORT":        "8080",
				"SCHEME":      "http",
				"SECRET_NAME": "db-secret",
			},
		}
		if !reflect.DeepEqual(res.Access, wantAccess) {
			t.Errorf("populateAccessEnvVars() access = %+v, want %+v", res.Access, wantAccess)
		}
	})
}

func TestShouldSyncToCMS(t *testing.T) {
	now := time.Now().UTC()

	tests := []struct {
		name         string
		conditions   []Condition
		targetStatus string
		currentGen   int64
		wantSync     bool
	}{
		{
			name:         "initial reconciliation with empty conditions",
			conditions:   nil,
			targetStatus: ConditionStatusTrue,
			currentGen:   1,
			wantSync:     true,
		},
		{
			name: "steady state unchanged health within heartbeat interval",
			conditions: []Condition{
				{
					Type:               ConditionTypeReady,
					Status:             ConditionStatusTrue,
					ObservedGeneration: 1,
					LastTransitionTime: now.Add(-1 * time.Minute),
				},
			},
			targetStatus: ConditionStatusTrue,
			currentGen:   1,
			wantSync:     false,
		},
		{
			name: "readiness state transition from False to True",
			conditions: []Condition{
				{
					Type:               ConditionTypeReady,
					Status:             ConditionStatusFalse,
					ObservedGeneration: 1,
					LastTransitionTime: now.Add(-1 * time.Minute),
				},
			},
			targetStatus: ConditionStatusTrue,
			currentGen:   1,
			wantSync:     true,
		},
		{
			name: "readiness state transition from True to False",
			conditions: []Condition{
				{
					Type:               ConditionTypeReady,
					Status:             ConditionStatusTrue,
					ObservedGeneration: 1,
					LastTransitionTime: now.Add(-1 * time.Minute),
				},
			},
			targetStatus: ConditionStatusFalse,
			currentGen:   1,
			wantSync:     true,
		},
		{
			name: "metadata generation increment",
			conditions: []Condition{
				{
					Type:               ConditionTypeReady,
					Status:             ConditionStatusTrue,
					ObservedGeneration: 1,
					LastTransitionTime: now.Add(-1 * time.Minute),
				},
			},
			targetStatus: ConditionStatusTrue,
			currentGen:   2,
			wantSync:     true,
		},
		{
			name: "coarse heartbeat expired after 5 minutes",
			conditions: []Condition{
				{
					Type:               ConditionTypeReady,
					Status:             ConditionStatusTrue,
					ObservedGeneration: 1,
					LastTransitionTime: now.Add(-6 * time.Minute),
				},
			},
			targetStatus: ConditionStatusTrue,
			currentGen:   1,
			wantSync:     true,
		},
		{
			name: "zero LastTransitionTime triggers sync",
			conditions: []Condition{
				{
					Type:               ConditionTypeReady,
					Status:             ConditionStatusTrue,
					ObservedGeneration: 1,
					LastTransitionTime: time.Time{},
				},
			},
			targetStatus: ConditionStatusTrue,
			currentGen:   1,
			wantSync:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldSyncToCMS(tt.conditions, tt.targetStatus, tt.currentGen)
			if got != tt.wantSync {
				t.Errorf("shouldSyncToCMS() = %v, want %v", got, tt.wantSync)
			}
		})
	}
}

func TestResetReadyTransitionTime(t *testing.T) {
	past := time.Now().UTC().Add(-10 * time.Minute)
	conditions := []Condition{
		{Type: ConditionTypeDegraded, Status: ConditionStatusFalse, LastTransitionTime: past},
		{Type: ConditionTypeReady, Status: ConditionStatusTrue, LastTransitionTime: past},
	}
	resetReadyTransitionTime(conditions)
	if time.Since(conditions[1].LastTransitionTime) > 5*time.Second {
		t.Errorf("expected Ready LastTransitionTime to be reset to now, got %v", conditions[1].LastTransitionTime)
	}
	if !conditions[0].LastTransitionTime.Equal(past) {
		t.Errorf("expected Degraded LastTransitionTime to remain unchanged, got %v", conditions[0].LastTransitionTime)
	}
}

func TestReconcile_OperatorlessProfile3(t *testing.T) {
	ctx := context.Background()
	cms := &mockCMS{}
	prober := &mockProber{
		probeRes: &HealthProbeResult{Healthy: true, Message: "Service operational"},
	}
	r, err := NewReconciler(cms, prober, nil)
	if err != nil {
		t.Fatalf("NewReconciler failed: %v", err)
	}

	specProfile3 := &componentspb.ComponentSpec{
		Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER,
	}

	// Case 1: Operator-less component with static endpoint probes healthy
	in := ReconcileComponentInput{
		Namespace:          "xmc",
		Name:               "external-saas-db",
		Generation:         1,
		Spec:               specProfile3,
		HasFinalizer:       true,
		DeletionProtection: true,
		Enabled:            true,
		WrapperOpts: WrapperRef{
			EndpointJSONPath: "http://external-db.example.com:5432",
		},
	}

	res, err := r.ReconcileComponent(ctx, in)
	if err != nil {
		t.Fatalf("ReconcileComponent failed: %v", err)
	}
	if res.State != componentspb.ComponentState_COMPONENT_STATE_ENABLED {
		t.Errorf("expected COMPONENT_STATE_ENABLED, got %v", res.State)
	}
	if res.Access == nil || res.Access.EnvVars["ENDPOINT"] != "http://external-db.example.com:5432" {
		t.Errorf("expected endpoint published in access env vars: %+v", res.Access)
	}

	// Case 2: Operator-less component with failing endpoint probes unready
	unhealthyProber := &mockProber{
		probeRes: &HealthProbeResult{Healthy: false, Message: "Connection timed out"},
	}
	rUnhealthy, _ := NewReconciler(cms, unhealthyProber, nil)
	resUnhealthy, err := rUnhealthy.ReconcileComponent(ctx, in)
	if err != nil {
		t.Fatalf("unexpected error on unready probe: %v", err)
	}
	if resUnhealthy.State != componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION {
		t.Errorf("expected COMPONENT_STATE_PENDING_RECONCILIATION, got %v", resUnhealthy.State)
	}
}

func TestReconciler_ColdStartHydration_SkipsRedundantUpgrade(t *testing.T) {
	ctx := context.Background()
	helm := &mockHelm{active: false}
	cms := &mockCMS{}
	r, err := NewReconciler(cms, nil, helm)
	if err != nil {
		t.Fatalf("NewReconciler failed: %v", err)
	}

	in := ReconcileComponentInput{
		Namespace:    "xmc",
		Name:         "ingestion-gateway",
		Generation:   4,
		Spec:         &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT},
		Enabled:      true,
		HasFinalizer: true,
		ExistingConditions: []Condition{
			{
				Type:               ConditionTypeReady,
				Status:             ConditionStatusTrue,
				ObservedGeneration: 4,
				Reason:             "ComponentHealthy",
				LastTransitionTime: time.Now().UTC(),
			},
			{
				Type:               ConditionTypeDegraded,
				Status:             ConditionStatusFalse,
				ObservedGeneration: 4,
				Reason:             "ComponentHealthy",
				LastTransitionTime: time.Now().UTC(),
			},
		},
	}

	res, err := r.ReconcileComponent(ctx, in)
	if err != nil {
		t.Fatalf("ReconcileComponent failed: %v", err)
	}
	if res.State != componentspb.ComponentState_COMPONENT_STATE_ENABLED {
		t.Errorf("expected COMPONENT_STATE_ENABLED, got %v", res.State)
	}
	if helm.ensureCalls != 0 {
		t.Errorf("expected 0 Ensure1PComponent calls after cold-start hydration of Ready@gen4, got %d", helm.ensureCalls)
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
		Namespace:    "xmc",
		Name:         "ingestion-gateway",
		Generation:   2,
		Spec:         &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT},
		Enabled:      false,
		HasFinalizer: true,
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

func TestResolveOperatorlessEndpointAndIPv6(t *testing.T) {
	tests := []struct {
		name string
		in   ReconcileComponentInput
		want string
	}{
		{
			name: "JSONPath EndpointJSONPath is ignored when wrapperRef is nil",
			in: ReconcileComponentInput{
				WrapperOpts: WrapperRef{EndpointJSONPath: "$.status.endpoint"},
				Spec: &componentspb.ComponentSpec{
					Type:       componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER,
					Parameters: map[string]string{"endpoint": "http://clickhouse:8123"},
				},
			},
			want: "http://clickhouse:8123",
		},
		{
			name: "Static EndpointJSONPath override is returned",
			in: ReconcileComponentInput{
				WrapperOpts: WrapperRef{EndpointJSONPath: "http://static:8123"},
			},
			want: "http://static:8123",
		},
		{
			name: "Non-nil WrapperRef does not fall through to parameters",
			in: ReconcileComponentInput{
				Spec: &componentspb.ComponentSpec{
					Type:       componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER,
					WrapperRef: &componentspb.WrapperRef{CrdKind: "ClickHouseInstallation"},
					Parameters: map[string]string{"host": "clickhouse", "httpPort": "8123"},
				},
			},
			want: "",
		},
		{
			name: "IPv6 host is bracketed when combined with port",
			in: ReconcileComponentInput{
				Spec: &componentspb.ComponentSpec{
					Type:       componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER,
					Parameters: map[string]string{"host": " , 2001:db8::1 , ::1 ", "httpPort": "8123"},
				},
			},
			want: "[2001:db8::1]:8123",
		},
		{
			name: "host already containing port is returned directly",
			in: ReconcileComponentInput{
				Spec: &componentspb.ComponentSpec{
					Type:       componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER,
					Parameters: map[string]string{"host": "my-service:8080", "httpPort": "8123"},
				},
			},
			want: "my-service:8080",
		},
		{
			name: "bracketed IPv6 host with port is returned directly",
			in: ReconcileComponentInput{
				Spec: &componentspb.ComponentSpec{
					Type:       componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER,
					Parameters: map[string]string{"host": "[2001:db8::1]:8080", "httpPort": "8123"},
				},
			},
			want: "[2001:db8::1]:8080",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveOperatorlessEndpoint(tc.in); got != tc.want {
				t.Errorf("resolveOperatorlessEndpoint() = %q, want %q", got, tc.want)
			}
		})
	}
	params := map[string]string{
		"host":                "clickhouse.xmc-metrics.svc.cluster.local",
		"nativePort":          "9000",
		"httpPort":            "8123",
		"healthCheckEndpoint": "http://clickhouse.xmc-metrics.svc.cluster.local:8123/ping",
	}
	if got := resolveProbeEndpoint(params); got != "http://clickhouse.xmc-metrics.svc.cluster.local:8123/ping" {
		t.Errorf("resolveProbeEndpoint() = %q, want healthCheckEndpoint", got)
	}
	if got := resolveConnectionEndpoint(params); got != "clickhouse.xmc-metrics.svc.cluster.local:9000" {
		t.Errorf("resolveConnectionEndpoint() = %q, want clickhouse.xmc-metrics.svc.cluster.local:9000", got)
	}
}

func TestPreserveHealthConditions_ReplacesExistingEntries(t *testing.T) {
	ready := Condition{Type: ConditionTypeReady, Status: ConditionStatusTrue, ObservedGeneration: 3, Reason: "ComponentHealthy"}
	degraded := Condition{Type: ConditionTypeDegraded, Status: ConditionStatusFalse, ObservedGeneration: 3, Reason: "ComponentHealthy"}
	in := ReconcileComponentInput{Generation: 3, ExistingConditions: []Condition{ready, degraded}}
	res := &ReconcileResult{Conditions: []Condition{{Type: ConditionTypeReady, Status: ConditionStatusFalse, Reason: "Stale"}}}

	if !preserveHealthConditions(in, res) {
		t.Fatal("preserveHealthConditions() = false, want true")
	}
	if diff := cmp.Diff([]Condition{ready, degraded}, res.Conditions); diff != "" {
		t.Errorf("preserveHealthConditions() conditions mismatch (-want +got):\n%s", diff)
	}
}

// gateOutcome summarizes the observable effects of a reconciliation pass for gating tests.
type gateOutcome struct {
	State               componentspb.ComponentState
	ReadyStatus         string
	ReadyReason         string
	DegradedStatus      string
	DependenciesReady   string
	DeactivationBlocked string
	SetCalls            int
	EnsureCalls         int
	PrepareCalls        int
	CompleteCalls       int
	HelmDeleted         bool
}

func summarizeGate(res *ReconcileResult, cms *mockCMS, helm *mockHelm) gateOutcome {
	got := gateOutcome{
		State: res.State, SetCalls: cms.setCalls, PrepareCalls: cms.prepareCalls, CompleteCalls: cms.completeCalls,
		EnsureCalls: helm.ensureCalls, HelmDeleted: helm.deleteCalled,
	}
	if c := findCondition(res.Conditions, ConditionTypeReady); c != nil {
		got.ReadyStatus, got.ReadyReason = c.Status, c.Reason
	}
	if c := findCondition(res.Conditions, ConditionTypeDegraded); c != nil {
		got.DegradedStatus = c.Status
	}
	if c := findCondition(res.Conditions, ConditionTypeDependenciesReady); c != nil {
		got.DependenciesReady = c.Status
	}
	if c := findCondition(res.Conditions, ConditionTypeDeactivationBlocked); c != nil {
		got.DeactivationBlocked = c.Status
	}
	return got
}

// checkReadyMessage fails the test if want is non-empty and differs from the Ready message.
func checkReadyMessage(t *testing.T, res *ReconcileResult, want string) {
	t.Helper()
	if want == "" {
		return
	}
	if c := findCondition(res.Conditions, ConditionTypeReady); c == nil || c.Message != want {
		t.Errorf("Ready condition = %+v, want message %q", c, want)
	}
}

func TestReconcileComponent_DependencyActivationGating(t *testing.T) {
	spec1P := func(deps ...string) *componentspb.ComponentSpec {
		return &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT, Dependencies: deps}
	}
	spec3P := &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER, Dependencies: []string{"redpanda"}}
	snapshot := func(items ...unstructured.Unstructured) DependencyReadiness { return NewDependencySnapshot(items) }
	notRegisteredErr := status.Error(codes.FailedPrecondition, store.DependencyNotFoundMessagePrefix+` component "ingestion" depends on "redpanda", which is not registered`)
	// Intent is registered with CMS even while activation is gated.
	gated := gateOutcome{
		State:       componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
		ReadyStatus: ConditionStatusFalse, ReadyReason: ReasonDependenciesNotReady, DegradedStatus: ConditionStatusFalse,
		SetCalls: 1,
	}
	actuated := gateOutcome{
		State:       componentspb.ComponentState_COMPONENT_STATE_ENABLED,
		ReadyStatus: ConditionStatusTrue, ReadyReason: ReasonComponentHealthy, DegradedStatus: ConditionStatusFalse,
		DependenciesReady: ConditionStatusTrue, SetCalls: 1, EnsureCalls: 1,
	}
	withoutDepsCondition := func(o gateOutcome) gateOutcome { o.DependenciesReady = ""; return o }
	// running is a release actuated at gen that passed the gate; the CR is at generation 2.
	running := func(gen int64) *mockHelm {
		return &mockHelm{active: true, actuatedGenerations: map[string]int64{"xmc/ingestion": gen}}
	}
	runningConds := func(gen int64) []Condition {
		return []Condition{
			{Type: ConditionTypeReady, Status: ConditionStatusTrue, ObservedGeneration: gen, Reason: ReasonComponentHealthy},
			{Type: ConditionTypeDependenciesReady, Status: ConditionStatusTrue, ObservedGeneration: gen, Reason: "AllDependenciesReady"},
		}
	}
	// upgradeHeld keeps the running workload but holds the Helm upgrade to the new generation.
	upgradeHeld := gateOutcome{
		State:       componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
		ReadyStatus: ConditionStatusFalse, ReadyReason: ReasonHelmUpgradePending, DegradedStatus: ConditionStatusFalse,
		DependenciesReady: ConditionStatusFalse, SetCalls: 1,
	}
	// keptReady has nothing to upgrade, so Ready stays health-derived while dependencies are unready.
	keptReady := gateOutcome{
		State:       componentspb.ComponentState_COMPONENT_STATE_ENABLED,
		ReadyStatus: ConditionStatusTrue, ReadyReason: ReasonComponentHealthy, DegradedStatus: ConditionStatusFalse,
		DependenciesReady: ConditionStatusFalse, SetCalls: 1,
	}
	tests := []struct {
		name     string
		cms      *mockCMS
		helm     *mockHelm
		spec     *componentspb.ComponentSpec
		deps     DependencyReadiness
		existing []Condition
		want     gateOutcome
		wantMsg  string
		wantErr  bool
	}{
		{name: "all dependencies ready actuates", spec: spec1P("redpanda"), deps: snapshot(readyComponent("redpanda", nil)), want: actuated},
		{name: "nil resolver disables gating", spec: spec1P("redpanda"), deps: nil, want: withoutDepsCondition(actuated)},
		{name: "no dependencies declared", spec: spec1P(), deps: snapshot(), want: withoutDepsCondition(actuated)},
		{name: "dependency missing", spec: spec1P("redpanda"), deps: snapshot(), want: gated, wantMsg: "waiting on dependencies: redpanda (not found)"},
		{
			name:    "dependency disabled",
			spec:    spec1P("redpanda"),
			deps:    snapshot(readyComponent("redpanda", func(u *unstructured.Unstructured) { u.Object["spec"] = map[string]any{"enabled": false} })),
			want:    gated,
			wantMsg: "waiting on dependencies: redpanda (disabled)",
		},
		{
			name:    "dependency Ready=False",
			spec:    spec1P("redpanda"),
			deps:    snapshot(readyComponent("redpanda", func(u *unstructured.Unstructured) { setConditionStatus(u, 0, "status", ConditionStatusFalse) })),
			want:    gated,
			wantMsg: "waiting on dependencies: redpanda (not ready)",
		},
		{
			name:    "dependency Ready at stale generation",
			spec:    spec1P("redpanda"),
			deps:    snapshot(readyComponent("redpanda", func(u *unstructured.Unstructured) { u.SetGeneration(3) })),
			want:    gated,
			wantMsg: "waiting on dependencies: redpanda (not ready at current generation)",
		},
		{
			name:    "dependency Degraded",
			spec:    spec1P("redpanda"),
			deps:    snapshot(readyComponent("redpanda", func(u *unstructured.Unstructured) { setConditionStatus(u, 1, "status", ConditionStatusTrue) })),
			want:    gated,
			wantMsg: "waiting on dependencies: redpanda (degraded)",
		},
		{
			name: "lists every unready dependency in declaration order",
			spec: spec1P("redpanda", "clickhouse", "foo"),
			deps: snapshot(
				readyComponent("redpanda", func(u *unstructured.Unstructured) { u.Object["spec"] = map[string]any{"enabled": false} }),
				readyComponent("clickhouse", nil),
			),
			want:    gated,
			wantMsg: "waiting on dependencies: redpanda (disabled), foo (not found)",
		},
		{name: "3P wrapper is gated after registering intent", spec: spec3P, deps: snapshot(), want: gated},
		{
			name: "CMS NotFound while gated is tolerated",
			cms:  &mockCMS{obsErr: status.Error(codes.NotFound, "component not found")},
			spec: spec1P("redpanda"), deps: snapshot(), want: gated,
		},
		{
			name: "CMS failure while gated is surfaced",
			cms:  &mockCMS{obsErr: status.Error(codes.Unavailable, "cms down")},
			spec: spec1P("redpanda"), deps: snapshot(), want: gated, wantErr: true,
		},
		{
			name: "CMS unregistered dependency gates without error",
			cms:  &mockCMS{setErr: notRegisteredErr},
			spec: spec1P("redpanda"), deps: snapshot(readyComponent("redpanda", nil)), want: gated,
			wantMsg: "waiting on dependencies: " + status.Convert(notRegisteredErr).Message(),
		},
		{
			name: "CMS unregistered dependency gates without a resolver",
			cms:  &mockCMS{setErr: notRegisteredErr},
			spec: spec1P("redpanda"), deps: nil, want: gated,
		},
		{
			name: "other intent rejection is surfaced",
			cms:  &mockCMS{setErr: status.Error(codes.FailedPrecondition, "invalid state transition")},
			spec: spec1P("redpanda"), deps: snapshot(readyComponent("redpanda", nil)),
			want: gateOutcome{
				State:       componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
				ReadyStatus: ConditionStatusFalse, ReadyReason: "IntentApplicationFailed", DegradedStatus: ConditionStatusTrue, SetCalls: 1,
			},
			wantErr: true,
		},
		{
			name: "running release holds upgrade to a new generation while a dependency is unready",
			helm: running(1), spec: spec1P("redpanda"), deps: snapshot(), existing: runningConds(1), want: upgradeHeld,
			wantMsg: "Helm upgrade to generation 2 is held until dependencies are ready",
		},
		{
			name: "running release at the current generation keeps Ready while a dependency is unready",
			helm: running(2), spec: spec1P("redpanda"), deps: snapshot(), existing: runningConds(2), want: keptReady,
		},
		{
			name: "running release is upgraded once dependencies are ready",
			helm: running(1), spec: spec1P("redpanda"), deps: snapshot(readyComponent("redpanda", nil)), existing: runningConds(1), want: actuated,
		},
		{
			name: "running release holds upgrade while CMS has not registered a dependency",
			cms:  &mockCMS{setErr: notRegisteredErr},
			helm: running(1), spec: spec1P("redpanda"), deps: nil, existing: runningConds(1), want: upgradeHeld,
		},
		{
			name: "running 3P wrapper keeps Ready while a dependency is unready",
			spec: spec3P, deps: snapshot(),
			existing: []Condition{{Type: ConditionTypeReady, Status: ConditionStatusTrue, ObservedGeneration: 1, Reason: ReasonComponentHealthy}},
			want:     keptReady,
		},
		{
			name: "previously gated component stays gated",
			spec: spec1P("redpanda"), deps: snapshot(),
			existing: []Condition{{Type: ConditionTypeReady, Status: ConditionStatusFalse, ObservedGeneration: 2, Reason: ReasonDependenciesNotReady}},
			want:     gated,
		},
		{
			name: "dependency cycle is reported without registering intent",
			spec: spec1P("redpanda"),
			deps: snapshot(readyComponent("ingestion", withDependencies("redpanda")), readyComponent("redpanda", withDependencies("ingestion"))),
			want: gateOutcome{
				State:       componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
				ReadyStatus: ConditionStatusFalse, ReadyReason: ReasonDependencyCycle, DegradedStatus: ConditionStatusTrue,
			},
			wantMsg: "dependency cycle detected: ingestion -> redpanda -> ingestion",
		},
		{
			name: "self-dependency is dropped and does not gate",
			spec: spec1P("ingestion"),
			deps: snapshot(readyComponent("ingestion", withDependencies("ingestion"))),
			want: withoutDepsCondition(actuated),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cms, helm := tt.cms, tt.helm
			if cms == nil {
				cms = &mockCMS{}
			}
			if helm == nil {
				helm = &mockHelm{}
			}
			wasActive := helm.active
			r, _ := NewReconciler(cms, nil, helm)
			res, err := r.ReconcileComponent(context.Background(), ReconcileComponentInput{
				Namespace: "xmc", Name: "ingestion", Generation: 2, Spec: tt.spec, HasFinalizer: true,
				Enabled: true, ExistingConditions: tt.existing, Dependencies: tt.deps,
			})
			if (err != nil) != tt.wantErr {
				t.Fatalf("ReconcileComponent() error = %v, wantErr %v", err, tt.wantErr)
			}
			if diff := cmp.Diff(tt.want, summarizeGate(res, cms, helm)); diff != "" {
				t.Errorf("ReconcileComponent() outcome mismatch (-want +got):\n%s", diff)
			}
			checkReadyMessage(t, res, tt.wantMsg)
			if wasActive && !helm.active {
				t.Error("ReconcileComponent() tore down an already-running release")
			}
		})
	}
}

// TestReconcileComponent_DependencyBlipHoldsUpgrade drives one component across passes: actuated,
// a dependency blip at the same generation (Ready kept), a spec change while the dependency is
// unready (upgrade held with HelmUpgradePending), then recovery.
func TestReconcileComponent_DependencyBlipHoldsUpgrade(t *testing.T) {
	cms, helm := &mockCMS{}, &mockHelm{}
	r, _ := NewReconciler(cms, nil, helm)
	ready := NewDependencySnapshot([]unstructured.Unstructured{readyComponent("redpanda", nil)})
	unready := NewDependencySnapshot([]unstructured.Unstructured{readyComponent("redpanda", func(u *unstructured.Unstructured) { setConditionStatus(u, 0, "status", ConditionStatusFalse) })})
	healthy := gateOutcome{State: componentspb.ComponentState_COMPONENT_STATE_ENABLED, ReadyStatus: ConditionStatusTrue, ReadyReason: ReasonComponentHealthy, DegradedStatus: ConditionStatusFalse}
	pending := gateOutcome{State: componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION, ReadyStatus: ConditionStatusFalse, ReadyReason: ReasonHelmUpgradePending, DegradedStatus: ConditionStatusFalse}
	with := func(o gateOutcome, depsReady string, setCalls, ensureCalls int) gateOutcome {
		o.DependenciesReady, o.SetCalls, o.EnsureCalls = depsReady, setCalls, ensureCalls
		return o
	}
	passes := []struct {
		generation int64
		deps       DependencyReadiness
		want       gateOutcome
	}{
		{generation: 2, deps: ready, want: with(healthy, ConditionStatusTrue, 1, 1)},
		{generation: 2, deps: unready, want: with(healthy, ConditionStatusFalse, 2, 1)},
		{generation: 3, deps: unready, want: with(pending, ConditionStatusFalse, 3, 1)},
		{generation: 3, deps: ready, want: with(healthy, ConditionStatusTrue, 4, 2)},
	}
	var existing []Condition
	for i, p := range passes {
		res, err := r.ReconcileComponent(context.Background(), ReconcileComponentInput{
			Namespace: "xmc", Name: "ingestion", Generation: p.generation, HasFinalizer: true, Enabled: true,
			Spec:               &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT, Dependencies: []string{"redpanda"}},
			ExistingConditions: existing, Dependencies: p.deps,
		})
		if err != nil {
			t.Fatalf("pass %d: ReconcileComponent() error = %v", i, err)
		}
		if diff := cmp.Diff(p.want, summarizeGate(res, cms, helm)); diff != "" {
			t.Errorf("pass %d: ReconcileComponent() outcome mismatch (-want +got):\n%s", i, diff)
		}
		if !helm.active {
			t.Fatalf("pass %d: release is not active", i)
		}
		existing = res.Conditions
	}
}

// TestReconcileComponent_GatedDependentBlocksDeactivationInCMS verifies against the real CMS server
// and MemoryStore that a gated dependent still registers its dependency edge, so disabling the
// dependency is blocked.
func TestReconcileComponent_GatedDependentBlocksDeactivationInCMS(t *testing.T) {
	ctx := context.Background()
	r, err := NewReconciler(NewCMSClientAdapter(cmsserver.NewServer(store.NewMemoryStore())), nil, nil)
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
			Namespace: "xmc", Name: name, Generation: gen, Spec: s, HasFinalizer: true, Enabled: enabled,
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

func TestDependencyNotRegisteredMessage(t *testing.T) {
	if dependencyNotFoundMessagePrefix != store.DependencyNotFoundMessagePrefix {
		t.Fatalf("dependencyNotFoundMessagePrefix = %q, want store.DependencyNotFoundMessagePrefix %q", dependencyNotFoundMessagePrefix, store.DependencyNotFoundMessagePrefix)
	}
	const msg = store.DependencyNotFoundMessagePrefix + ` component "a" depends on "b", which is not registered`
	notRegistered := status.Error(codes.FailedPrecondition, msg)
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "nil", err: nil, want: ""},
		{name: "unregistered dependency", err: notRegistered, want: msg},
		{name: "wrapped unregistered dependency", err: fmt.Errorf("failed to apply component intent to CMS: %w", notRegistered), want: msg},
		{name: "other FailedPrecondition", err: status.Error(codes.FailedPrecondition, "dependency cycle detected: a -> b -> a"), want: ""},
		{name: "other code with the prefix", err: status.Error(codes.Unavailable, msg), want: ""},
		{name: "non-status error", err: errors.New(msg), want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := dependencyNotRegisteredMessage(tt.err); got != tt.want {
				t.Errorf("dependencyNotRegisteredMessage() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSetDependenciesReadyConditionMessage(t *testing.T) {
	const waitMsg = "waiting on dependencies: redpanda (not ready)"
	tests := []struct {
		name     string
		compType componentspb.ComponentType
		want     string
	}{
		{name: "1P mentions held Helm upgrades", compType: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT, want: waitMsg + "; Helm upgrades are held, running workload is left in place"},
		{name: "3P wrapper omits Helm", compType: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER, want: waitMsg + "; running workload is left in place"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := &ReconcileResult{}
			setDependenciesReadyCondition(res, ReconcileComponentInput{Generation: 1, Spec: &componentspb.ComponentSpec{Type: tt.compType}}, waitMsg)
			c := findCondition(res.Conditions, ConditionTypeDependenciesReady)
			if c == nil || c.Status != ConditionStatusFalse || c.Message != tt.want {
				t.Errorf("setDependenciesReadyCondition() condition = %+v, want status %q message %q", c, ConditionStatusFalse, tt.want)
			}
		})
	}
}

func TestReconcileComponent_DeactivationGating(t *testing.T) {
	spec1P := &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT}
	spec3P := &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER}
	wasReady := []Condition{{Type: ConditionTypeReady, Status: ConditionStatusTrue, ObservedGeneration: 1, Reason: ReasonComponentHealthy}}
	tornDown := []Condition{{Type: ConditionTypeReady, Status: ConditionStatusFalse, ObservedGeneration: 2, Reason: ReasonDeactivationIncomplete}}
	blockedErr := status.Error(codes.FailedPrecondition, "component redpanda has active dependents: [ingestion-gateway]")
	blockedMsg := `Cannot disable "redpanda": component redpanda has active dependents: [ingestion-gateway]`
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
			name: "prepare failure keeps workload",
			cms:  &mockCMS{prepareErr: status.Error(codes.Unavailable, "cms down")}, helm: &mockHelm{active: true}, spec: spec1P, existing: wasReady,
			want:    failed("CMSDeactivationFailed", gateOutcome{PrepareCalls: 1}),
			wantMsg: "failed to prepare deactivation in CMS: rpc error: code = Unavailable desc = cms down",
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
			name: "retry after incomplete deactivation re-runs the handshake",
			cms:  &mockCMS{}, helm: &mockHelm{}, spec: spec1P, existing: tornDown,
			want: disabled(gateOutcome{PrepareCalls: 1, CompleteCalls: 1, HelmDeleted: true}),
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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, _ := NewReconciler(tt.cms, nil, tt.helm)
			res, err := r.ReconcileComponent(context.Background(), ReconcileComponentInput{
				Namespace: "xmc", Name: "redpanda", Generation: 2, Spec: tt.spec, HasFinalizer: true,
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

// TestReconcileComponent_ErrorPassPreservesDependenciesReady guards isActuated: an error pass must not
// drop DependenciesReady, or an actuated but unhealthy 3P wrapper would be re-gated on the next pass.
func TestReconcileComponent_ErrorPassPreservesDependenciesReady(t *testing.T) {
	r, _ := NewReconciler(&mockCMS{setErr: status.Error(codes.Unavailable, "cms down")}, nil, &mockHelm{})
	prev := Condition{Type: ConditionTypeDependenciesReady, Status: ConditionStatusFalse, ObservedGeneration: 1, Reason: ReasonDependenciesNotReady}
	res, err := r.ReconcileComponent(context.Background(), ReconcileComponentInput{
		Namespace: "xmc", Name: "ingestion", Generation: 1, HasFinalizer: true, Enabled: true,
		Spec:               &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER, Dependencies: []string{"redpanda"}},
		ExistingConditions: []Condition{{Type: ConditionTypeReady, Status: ConditionStatusFalse, ObservedGeneration: 1}, prev},
		Dependencies:       NewDependencySnapshot(nil),
	})
	if err == nil {
		t.Fatal("ReconcileComponent() error = nil, want CMS error")
	}
	if diff := cmp.Diff(&prev, findCondition(res.Conditions, ConditionTypeDependenciesReady)); diff != "" {
		t.Errorf("ReconcileComponent() DependenciesReady mismatch (-want +got):\n%s", diff)
	}
}

// TestReconcileComponent_DeactivationUnblocksAcrossPasses feeds each pass's conditions into the next:
// blocked while a dependent is active, then disabled once CMS allows it after the recheck interval.
func TestReconcileComponent_DeactivationUnblocksAcrossPasses(t *testing.T) {
	cms, helm := &mockCMS{prepareErr: status.Error(codes.FailedPrecondition, "component redpanda has active dependents: [ingestion-gateway]")}, &mockHelm{active: true}
	r, _ := NewReconciler(cms, nil, helm)
	in := ReconcileComponentInput{
		Namespace: "xmc", Name: "redpanda", Generation: 2, HasFinalizer: true, Enabled: false,
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
		{name: "deactivation incomplete", status: ConditionStatusFalse, reason: ReasonDeactivationIncomplete, want: true},
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
