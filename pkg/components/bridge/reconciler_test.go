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
	"google.golang.org/protobuf/types/known/durationpb"
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
	healthy   bool
	msg       string
	status    *ResourceStatus
	detectErr error
	probeRes  *HealthProbeResult
	probeErr  error
}

func (m *mockProber) DetectStatus(ctx context.Context, ref WrapperRef) (*ResourceStatus, error) {
	if m == nil {
		return &ResourceStatus{Found: true, Ready: true}, nil
	}
	if m.detectErr != nil {
		return nil, m.detectErr
	}
	if m.status != nil {
		return m.status, nil
	}
	return &ResourceStatus{Found: true, Ready: m.healthy, Message: m.msg}, nil
}

func (m *mockProber) ProbeHealth(ctx context.Context, spec *componentspb.ComponentSpec) (bool, string) {
	if m == nil {
		return true, "Component is active"
	}
	if m.status != nil {
		return m.status.Ready, m.msg
	}
	return m.healthy, m.msg
}

func (m *mockProber) Probe(ctx context.Context, target string, timeout time.Duration) (*HealthProbeResult, error) {
	if m == nil {
		return &HealthProbeResult{Healthy: true}, nil
	}
	if m.probeErr != nil {
		return nil, m.probeErr
	}
	if m.probeRes != nil {
		return m.probeRes, nil
	}
	healthy := m.healthy
	if m.status != nil && m.status.Ready {
		healthy = true
	}
	return &HealthProbeResult{Healthy: healthy, Message: m.msg}, nil
}

type mockWM struct {
	ensureErr, deleteErr error
	active               bool
}

func (m *mockWM) Ensure1PWorkload(ctx context.Context, ns, name string, opts WorkloadOptions) error {
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
	spec3PWithRef := &componentspb.ComponentSpec{
		Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER,
		WrapperRef: &componentspb.WrapperRef{
			CrdGroup: "clickhouse.altinity.com", CrdVersion: "v1", CrdKind: "ClickHouseInstallation", TargetName: "my-ch", TargetNamespace: "xmc",
		},
	}

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
			name: "active 3P with finalizer removal when deletion protection disabled",
			cms:  &mockCMS{},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "clickhouse", Generation: 1, Spec: spec3P,
				HasFinalizer: true, DeletionProtection: false, Enabled: true,
			},
			wantState:     componentspb.ComponentState_COMPONENT_STATE_ENABLED,
			wantRemoveFin: true,
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
			name: "deletion successful for 1P",
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
			name: "deletion with orphan policy preserves 1P child workload",
			cms:  &mockCMS{},
			wm:   &mockWM{active: true},
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
			name: "intent application CMS error",
			cms:  &mockCMS{setErr: status.Error(codes.InvalidArgument, "bad spec")},
			wm:   &mockWM{active: true},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "ingestion", Generation: 1, Spec: spec1P,
				HasFinalizer: true, DeletionProtection: true, Enabled: true,
			},
			wantState: componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
		},
		{
			name: "1P workload provisioning error",
			cms:  &mockCMS{},
			wm:   &mockWM{ensureErr: errors.New("quota exceeded")},
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
			name:   "3P wrapper with successful status detection and endpoint/secret access",
			cms:    &mockCMS{},
			prober: &mockProber{status: &ResourceStatus{Found: true, Ready: true, ObservedGeneration: 4, Endpoint: "http://my-ch.xmc.svc.cluster.local:8123", ConnectionSecret: "my-ch-creds"}},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "clickhouse", Generation: 1, Spec: spec3PWithRef, HasFinalizer: true, DeletionProtection: true, Enabled: true,
			},
			wantState: componentspb.ComponentState_COMPONENT_STATE_ENABLED,
		},
		{
			name:   "3P wrapper detection error",
			cms:    &mockCMS{},
			prober: &mockProber{detectErr: errors.New("k8s api error")},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "clickhouse", Generation: 1, Spec: spec3PWithRef, HasFinalizer: true, DeletionProtection: true, Enabled: true,
			},
			wantState: componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
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
			prober: &mockProber{detectErr: errors.New("detect error")},
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
			var wm WorkloadManager
			if tt.wm != nil {
				wm = tt.wm
			}
			r, _ := NewReconciler(tt.cms, p, wm)
			res, err := r.ReconcileComponent(ctx, tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ReconcileComponent() error = %v, wantErr %v", err, tt.wantErr)
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
		})
	}
}

func TestReconcile_Unstructured(t *testing.T) {
	r, _ := NewReconciler(&mockCMS{}, nil, nil)
	u := &unstructured.Unstructured{
		Object: map[string]any{
			"metadata": map[string]any{"name": "test-comp", "namespace": "xmc", "generation": int64(1)},
			"spec":     map[string]any{"enabled": true, "componentType": "COMPONENT_TYPE_CUSTOM_1P_COMPONENT"},
		},
	}
	res, err := r.Reconcile(context.Background(), u)
	if err != nil || res == nil || res.State != componentspb.ComponentState_COMPONENT_STATE_ENABLED {
		t.Fatalf("unexpected Reconcile outcome: res=%+v, err=%v", res, err)
	}
}

func TestSetCondition_PreservesTimestamp(t *testing.T) {
	prev := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	existing := []Condition{{Type: ConditionTypeReady, Status: ConditionStatusTrue, LastTransitionTime: prev}}
	var conds []Condition
	setCondition(&conds, existing, ConditionTypeReady, ConditionStatusTrue, 1, "R", "M")
	if len(conds) != 1 || conds[0].LastTransitionTime != prev {
		t.Errorf("expected transition time preserved: %+v", conds)
	}
	setCondition(&conds, existing, ConditionTypeReady, ConditionStatusFalse, 2, "R2", "M2")
	if len(conds) != 1 || conds[0].Status != ConditionStatusFalse {
		t.Errorf("expected updated condition: %+v", conds)
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
		"manifests": []any{"yaml-1", "yaml-2"},
	}
	spec, dp, en = ParseSpecFromUnstructured(raw)
	if dp || !en || spec.GetType() != componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER || spec.GetDeletionPolicy() != componentspb.DeletionPolicy_DELETION_POLICY_ORPHAN || spec.GetWrapperRef().GetTargetName() != "n" || len(spec.GetManifests()) != 2 {
		t.Errorf("unexpected parsed spec: %+v", spec)
	}

	if ParseConditionsFromUnstructured(nil) != nil || ParseConditionsFromUnstructured(map[string]any{"conditions": "inv"}) != nil {
		t.Error("expected nil for empty/invalid conditions")
	}

	conds := ParseConditionsFromUnstructured(map[string]any{
		"conditions": []any{
			map[string]any{"type": "Ready", "status": "True", "observedGeneration": float64(2), "lastTransitionTime": "2026-08-24T00:00:00Z"},
		},
	})
	if len(conds) != 1 || conds[0].ObservedGeneration != 2 || conds[0].Status != "True" {
		t.Errorf("unexpected conditions: %+v", conds)
	}

	opts := ParseWorkloadOptions("uid-1", map[string]any{
		"imageTag": "v1.2",
		"parameters": map[string]any{
			"port":     float64(8080),
			"replicas": "3",
		},
	}, &componentspb.ComponentSpec{
		Version: "v1.0",
		LifecycleControl: &componentspb.LifecycleControl{
			DrainingTimeout: &durationpb.Duration{Seconds: 60},
		},
	})
	if opts.OwnerUID != "uid-1" || opts.ImageTag != "v1.2" || opts.DrainTimeoutSeconds != 60 || opts.Parameters["port"] != "8080" || opts.Parameters["replicas"] != "3" {
		t.Errorf("unexpected parsed workload options: %+v", opts)
	}

	emptyOpts := ParseWorkloadOptions("", nil, nil)
	if emptyOpts.OwnerUID != "" || emptyOpts.ImageTag != "" {
		t.Errorf("unexpected empty workload options: %+v", emptyOpts)
	}
}

func TestParseCoordinates(t *testing.T) {
	tests := []struct {
		name       string
		endpoint   string
		defaultNS  string
		wantScheme string
		wantHost   string
		wantPort   uint32
		wantSvc    string
		wantNS     string
		wantNil    bool
	}{
		{name: "empty endpoint", endpoint: "", wantNil: true},
		{
			name:       "full k8s cluster URL",
			endpoint:   "http://clickhouse.xmc.svc.cluster.local:8123",
			defaultNS:  "default",
			wantScheme: "http",
			wantHost:   "clickhouse.xmc.svc.cluster.local",
			wantPort:   8123,
			wantSvc:    "clickhouse",
			wantNS:     "xmc",
		},
		{
			name:       "https URL with default port",
			endpoint:   "https://secure.example.com",
			defaultNS:  "xmc",
			wantScheme: "https",
			wantHost:   "secure.example.com",
			wantPort:   443,
			wantSvc:    "secure",
			wantNS:     "example",
		},
		{
			name:       "IPv4 address with port",
			endpoint:   "10.0.0.1:9092",
			defaultNS:  "xmc",
			wantScheme: "http",
			wantHost:   "10.0.0.1",
			wantPort:   9092,
			wantSvc:    "",
			wantNS:     "xmc",
		},
		{
			name:       "IPv6 address with port",
			endpoint:   "[::1]:8080",
			defaultNS:  "xmc",
			wantScheme: "http",
			wantHost:   "::1",
			wantPort:   8080,
			wantSvc:    "",
			wantNS:     "xmc",
		},
		{
			name:       "bare service name",
			endpoint:   "my-service",
			defaultNS:  "xmc",
			wantScheme: "http",
			wantHost:   "my-service",
			wantPort:   80,
			wantSvc:    "my-service",
			wantNS:     "xmc",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			coords := ParseCoordinates(tt.endpoint, tt.defaultNS)
			if tt.wantNil {
				if coords != nil {
					t.Errorf("expected nil coordinates, got %+v", coords)
				}
				return
			}
			if coords == nil {
				t.Fatal("expected non-nil coordinates")
			}
			if coords.Scheme != tt.wantScheme || coords.Host != tt.wantHost || coords.Port != tt.wantPort || coords.ServiceName != tt.wantSvc || coords.Namespace != tt.wantNS {
				t.Errorf("ParseCoordinates(%q) = %+v, want scheme=%s host=%s port=%d svc=%s ns=%s",
					tt.endpoint, coords, tt.wantScheme, tt.wantHost, tt.wantPort, tt.wantSvc, tt.wantNS)
			}
		})
	}
}

func TestWrapperRefHelpers(t *testing.T) {
	t.Run("ToBridgeWrapperRef nil", func(t *testing.T) {
		got := ToBridgeWrapperRef(nil, "xmc")
		if got.Namespace != "" || got.Name != "" {
			t.Errorf("expected empty WrapperRef: %+v", got)
		}
	})

	t.Run("ToBridgeWrapperRef with default ns", func(t *testing.T) {
		pRef := &componentspb.WrapperRef{
			CrdGroup: "g", CrdVersion: "v", CrdKind: "k", TargetName: "n",
		}
		got := ToBridgeWrapperRef(pRef, "default-ns")
		if got.Namespace != "default-ns" || got.Name != "n" || got.Group != "g" {
			t.Errorf("unexpected WrapperRef: %+v", got)
		}
	})

	t.Run("ParseWrapperRefOptions empty and full", func(t *testing.T) {
		empty := ParseWrapperRefOptions(nil, "xmc")
		if empty.Namespace != "xmc" {
			t.Errorf("expected default namespace: %+v", empty)
		}

		full := ParseWrapperRefOptions(map[string]any{
			"wrapperRef": map[string]any{
				"group":                    "ray.io",
				"version":                  "v1",
				"kind":                     "RayCluster",
				"name":                     "ray-1",
				"observedGenerationPath":   "status.obsGen",
				"readyConditionPath":       "status.state",
				"expectedReadyValue":       "Ready",
				"endpointJsonPath":         "status.headEndpoint",
				"connectionSecretJsonPath": "status.authSecret",
			},
		}, "xmc")
		if full.Group != "ray.io" || full.Name != "ray-1" || full.ReadyConditionPath != "status.state" || full.ExpectedReadyValue != "Ready" || full.EndpointJSONPath != "status.headEndpoint" || full.ConnectionSecretJSONPath != "status.authSecret" {
			t.Errorf("unexpected parsed WrapperRef: %+v", full)
		}
	})
}
