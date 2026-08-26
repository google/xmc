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
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
)

type mockCMS struct {
	setErr, delErr, obsErr error
	state                  componentspb.ComponentState
}

func (m *mockCMS) SetComponentIntent(ctx context.Context, in *componentspb.SetComponentIntentRequest, opts ...grpc.CallOption) (*componentspb.SetComponentIntentResponse, error) {
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
	return &componentspb.UpdateObservedGenerationResponse{}, m.obsErr
}

func (m *mockCMS) ExecuteDeletion(ctx context.Context, in *componentspb.ExecuteDeletionRequest, opts ...grpc.CallOption) (*componentspb.ExecuteDeletionResponse, error) {
	return &componentspb.ExecuteDeletionResponse{State: componentspb.ComponentState_COMPONENT_STATE_DELETED}, m.delErr
}

type mockProber struct {
	healthy bool
	msg     string
}

func (m *mockProber) ProbeHealth(ctx context.Context, spec *componentspb.ComponentSpec) (bool, string) {
	if m == nil {
		return true, "Component is active"
	}
	return m.healthy, m.msg
}

type mockWM struct {
	ensureErr, deleteErr error
	active               bool
}

func (m *mockWM) Ensure1PWorkload(ctx context.Context, ns, name string, spec *componentspb.ComponentSpec) error {
	if m == nil {
		return nil
	}
	if m.ensureErr == nil {
		m.active = true
	}
	return m.ensureErr
}

func (m *mockWM) Delete1PWorkload(ctx context.Context, ns, name string) error {
	if m == nil {
		return nil
	}
	if m.deleteErr == nil {
		m.active = false
	}
	return m.deleteErr
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

	tests := []struct {
		name          string
		cms           *mockCMS
		prober        *mockProber
		wm            *mockWM
		in            ReconcileComponentInput
		wantErr       bool
		wantState     componentspb.ComponentState
		wantAddFin    bool
		wantRemoveFin bool
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
			name: "active 1P without deletion protection still attaches finalizer",
			cms:  &mockCMS{},
			wm:   &mockWM{},
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
			wm:   &mockWM{},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "ingestion", Generation: 1, Spec: spec1P,
				HasFinalizer: true, DeletionProtection: true, Enabled: true,
			},
			wantState: componentspb.ComponentState_COMPONENT_STATE_ENABLED,
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
			wm:   &mockWM{active: true},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "ingestion", Generation: 2, Spec: spec1P,
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
			name: "deletion 1P workload delete failure",
			cms:  &mockCMS{},
			wm:   &mockWM{deleteErr: errors.New("delete error")},
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
			name: "disabled component",
			cms:  &mockCMS{},
			wm:   &mockWM{active: true},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "ingestion", Generation: 2, Spec: spec1P,
				HasFinalizer: true, DeletionProtection: true, Enabled: false,
			},
			wantState: componentspb.ComponentState_COMPONENT_STATE_DISABLED,
		},
		{
			name: "disabled component 1P workload delete failure",
			cms:  &mockCMS{},
			wm:   &mockWM{deleteErr: errors.New("cannot stop container")},
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
			wm:   &mockWM{},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "ingestion", Generation: 2, Spec: spec1P,
				HasFinalizer: true, DeletionProtection: true, Enabled: false,
			},
			wantErr:   true,
			wantState: componentspb.ComponentState_COMPONENT_STATE_DISABLED,
		},
		{
			name:    "intent application CMS error",
			cms:     &mockCMS{setErr: status.Error(codes.InvalidArgument, "bad spec")},
			wm:      &mockWM{active: true},
			wantErr: true,
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "ingestion", Generation: 1, Spec: spec1P,
				HasFinalizer: true, DeletionProtection: true, Enabled: true,
			},
			wantState: componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
		},
		{
			name:    "1P workload provisioning error",
			cms:     &mockCMS{},
			wm:      &mockWM{ensureErr: errors.New("quota exceeded")},
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
				Namespace: "xmc", Name: "clickhouse", Generation: 1, Spec: spec3P,
				HasFinalizer: true, DeletionProtection: true, Enabled: true,
			},
			wantState: componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
		},
		{
			name:   "health probe healthy",
			cms:    &mockCMS{},
			prober: &mockProber{healthy: true, msg: "Service active"},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "clickhouse", Generation: 1, Spec: spec3P,
				HasFinalizer: true, DeletionProtection: true, Enabled: true,
			},
			wantState: componentspb.ComponentState_COMPONENT_STATE_ENABLED,
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
			var wm WorkloadManager
			if tt.wm != nil {
				wm = tt.wm
			}
			r, _ := NewReconciler(tt.cms, p, wm)
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
			if tt.name == "intent application CMS error" && tt.wm != nil && !tt.wm.active {
				t.Errorf("expected 1P workload to remain active on transient CMS intent failure")
			}
			if (tt.name == "deletion 1P workload delete failure" || tt.name == "deletion CMS failure") && len(res.Conditions) == 0 {
				t.Errorf("expected conditions to be populated on deletion failure, got empty conditions")
			}
		})
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
	if len(conds) != 1 || conds[0].LastTransitionTime != prev {
		t.Errorf("expected transition time preserved: %+v", conds)
	}

	// Test 2: Updates condition when status changes
	setCondition(&conds, existing, ConditionTypeReady, ConditionStatusFalse, 2, "R2", "M2")
	if len(conds) != 1 || conds[0].Status != ConditionStatusFalse || conds[0].LastTransitionTime == prev {
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

func TestExtractHelpers(t *testing.T) {
	m := map[string]any{
		"int64":        int64(42),
		"int":          int(100),
		"float64":      float64(200.5),
		"stringInt":    "300",
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
	if extractInt(m, "float64") != 200 {
		t.Errorf("extractInt float64 failed")
	}
	if extractInt(m, "stringInt") != 300 {
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
