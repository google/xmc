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
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
)

type fakeCMSClient struct {
	mu                     sync.Mutex
	setIntentCalls         []*componentspb.SetComponentIntentRequest
	updateObservedGenCalls []*componentspb.UpdateObservedGenerationRequest
	completeDeactCalls     []*componentspb.CompleteDeactivationRequest
	executeDeletionCalls   []*componentspb.ExecuteDeletionRequest
	setIntentErr           error
	updateObservedGenErr   error
	completeDeactErr       error
	executeDeletionErr     error
}

func (f *fakeCMSClient) SetComponentIntent(ctx context.Context, in *componentspb.SetComponentIntentRequest, opts ...grpc.CallOption) (*componentspb.SetComponentIntentResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setIntentCalls = append(f.setIntentCalls, in)
	if f.setIntentErr != nil {
		return nil, f.setIntentErr
	}
	return &componentspb.SetComponentIntentResponse{
		Component: &componentspb.Component{
			Name: in.GetName(),
			Spec: in.GetSpec(),
		},
	}, nil
}

func (f *fakeCMSClient) UpdateObservedGeneration(ctx context.Context, in *componentspb.UpdateObservedGenerationRequest, opts ...grpc.CallOption) (*componentspb.UpdateObservedGenerationResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updateObservedGenCalls = append(f.updateObservedGenCalls, in)
	if f.updateObservedGenErr != nil {
		return nil, f.updateObservedGenErr
	}
	return &componentspb.UpdateObservedGenerationResponse{
		Component: &componentspb.Component{
			Name: in.GetName(),
			Status: &componentspb.ComponentAccessStatus{
				ObservedGeneration: in.GetObservedGeneration(),
				State:              in.GetState(),
				HealthMessage:      in.GetStatusMessage(),
			},
		},
	}, nil
}

func (f *fakeCMSClient) CompleteDeactivation(ctx context.Context, in *componentspb.CompleteDeactivationRequest, opts ...grpc.CallOption) (*componentspb.CompleteDeactivationResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.completeDeactCalls = append(f.completeDeactCalls, in)
	if f.completeDeactErr != nil {
		return nil, f.completeDeactErr
	}
	return &componentspb.CompleteDeactivationResponse{
		State: componentspb.ComponentState_COMPONENT_STATE_DISABLED,
	}, nil
}

func (f *fakeCMSClient) ExecuteDeletion(ctx context.Context, in *componentspb.ExecuteDeletionRequest, opts ...grpc.CallOption) (*componentspb.ExecuteDeletionResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.executeDeletionCalls = append(f.executeDeletionCalls, in)
	if f.executeDeletionErr != nil {
		return nil, f.executeDeletionErr
	}
	return &componentspb.ExecuteDeletionResponse{
		State: componentspb.ComponentState_COMPONENT_STATE_DELETED,
	}, nil
}

type mockProber struct {
	mu               sync.Mutex
	statusFn         func(ctx context.Context, ref WrapperRef) (*ResourceStatus, error)
	probeFn          func(ctx context.Context, target string, timeout time.Duration) (*HealthProbeResult, error)
	detectStatusRefs []WrapperRef
	probeTargets     []string
}

func (m *mockProber) DetectStatus(ctx context.Context, ref WrapperRef) (*ResourceStatus, error) {
	m.mu.Lock()
	m.detectStatusRefs = append(m.detectStatusRefs, ref)
	fn := m.statusFn
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, ref)
	}
	return &ResourceStatus{Found: true, Ready: true}, nil
}

func (m *mockProber) Probe(ctx context.Context, target string, timeout time.Duration) (*HealthProbeResult, error) {
	m.mu.Lock()
	m.probeTargets = append(m.probeTargets, target)
	fn := m.probeFn
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, target, timeout)
	}
	return &HealthProbeResult{Healthy: true, StatusCode: 200}, nil
}

func TestNewReconciler(t *testing.T) {
	fakeCMS := &fakeCMSClient{}
	mockProb := &mockProber{}

	tests := []struct {
		name      string
		cms       CMSClient
		prober    StatusDetectorHealthProber
		expectErr bool
	}{
		{
			name:      "nil cms client",
			cms:       nil,
			prober:    mockProb,
			expectErr: true,
		},
		{
			name:      "nil prober",
			cms:       fakeCMS,
			prober:    nil,
			expectErr: true,
		},
		{
			name:      "valid initialization",
			cms:       fakeCMS,
			prober:    mockProb,
			expectErr: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r, err := NewReconciler(tc.cms, tc.prober, nil, WithReconcilerProbeTimeout(10*time.Second))
			if tc.expectErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tc.expectErr && (err != nil || r == nil) {
				t.Fatalf("expected valid reconciler, got err: %v", err)
			}
			if !tc.expectErr && r.probeTimeout != 10*time.Second {
				t.Errorf("expected probeTimeout 10s, got %v", r.probeTimeout)
			}
		})
	}
}

func TestReconcileComponentValidation(t *testing.T) {
	fakeCMS := &fakeCMSClient{}
	mockProb := &mockProber{}
	r, err := NewReconciler(fakeCMS, mockProb, nil)
	if err != nil {
		t.Fatalf("failed to create reconciler: %v", err)
	}

	ctx := context.Background()

	t.Run("empty name", func(t *testing.T) {
		_, err := r.ReconcileComponent(ctx, "xmc", "", 1, &componentspb.ComponentSpec{})
		if err == nil {
			t.Fatal("expected error for empty name, got nil")
		}
	})

	t.Run("nil spec", func(t *testing.T) {
		_, err := r.ReconcileComponent(ctx, "xmc", "my-comp", 1, nil)
		if err == nil {
			t.Fatal("expected error for nil spec, got nil")
		}
	})

	t.Run("uninitialized reconciler", func(t *testing.T) {
		var nilReconciler *Reconciler
		_, err := nilReconciler.ReconcileComponent(ctx, "xmc", "my-comp", 1, &componentspb.ComponentSpec{})
		if err == nil {
			t.Fatal("expected error on nil reconciler call, got nil")
		}
	})
}

func TestReconcile3PWrapperClickHouseSuccess(t *testing.T) {
	fakeCMS := &fakeCMSClient{}
	mockProb := &mockProber{
		statusFn: func(ctx context.Context, ref WrapperRef) (*ResourceStatus, error) {
			if ref.Kind != "ClickHouseInstallation" || ref.Name != "chi-primary" {
				t.Errorf("unexpected wrapperRef in detect: %+v", ref)
			}
			return &ResourceStatus{
				Found:              true,
				Ready:              true,
				ObservedGeneration: 3,
				Endpoint:           "http://clickhouse.xmc.svc.cluster.local:8123",
				Message:            "ClickHouse is ready",
			}, nil
		},
		probeFn: func(ctx context.Context, target string, timeout time.Duration) (*HealthProbeResult, error) {
			if target != "http://clickhouse.xmc.svc.cluster.local:8123" {
				t.Errorf("unexpected target in probe: %s", target)
			}
			return &HealthProbeResult{Healthy: true, StatusCode: 200, Message: "HTTP 200 OK"}, nil
		},
	}

	r, err := NewReconciler(fakeCMS, mockProb, nil)
	if err != nil {
		t.Fatalf("failed to create reconciler: %v", err)
	}

	spec := &componentspb.ComponentSpec{
		Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER,
		Version: "v24.3",
		WrapperRef: &componentspb.WrapperRef{
			CrdGroup:        "clickhouse.altinity.com",
			CrdVersion:      "v1",
			CrdKind:         "ClickHouseInstallation",
			TargetName:      "chi-primary",
			TargetNamespace: "xmc",
		},
	}

	res, err := r.ReconcileComponent(context.Background(), "xmc", "clickhouse-wrapper", 3, spec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if res.State != componentspb.ComponentState_COMPONENT_STATE_ENABLED {
		t.Errorf("expected state ENABLED, got %v", res.State)
	}
	if res.ObservedGeneration != 3 {
		t.Errorf("expected observedGen 3, got %d", res.ObservedGeneration)
	}
	if res.Coordinates == nil || res.Coordinates.Host != "clickhouse.xmc.svc.cluster.local" || res.Coordinates.Port != 8123 {
		t.Errorf("unexpected coordinates: %+v", res.Coordinates)
	}

	readyCond := findCondition(res.Conditions, ConditionTypeReady)
	if readyCond == nil || readyCond.Status != metav1.ConditionTrue || readyCond.Reason != ReasonComponentReady {
		t.Errorf("unexpected Ready condition: %+v", readyCond)
	}

	degradedCond := findCondition(res.Conditions, ConditionTypeDegraded)
	if degradedCond == nil || degradedCond.Status != metav1.ConditionFalse || degradedCond.Reason != ReasonHealthy {
		t.Errorf("unexpected Degraded condition: %+v", degradedCond)
	}

	if len(fakeCMS.setIntentCalls) != 1 || fakeCMS.setIntentCalls[0].Name != "components/clickhouse-wrapper" {
		t.Errorf("unexpected setIntentCalls: %+v", fakeCMS.setIntentCalls)
	}
	if len(fakeCMS.updateObservedGenCalls) != 1 || fakeCMS.updateObservedGenCalls[0].State != componentspb.ComponentState_COMPONENT_STATE_ENABLED {
		t.Errorf("unexpected updateObservedGenCalls: %+v", fakeCMS.updateObservedGenCalls)
	}
}

func TestReconcile3PWrapperJobSetSuccess(t *testing.T) {
	fakeCMS := &fakeCMSClient{}
	mockProb := &mockProber{
		statusFn: func(ctx context.Context, ref WrapperRef) (*ResourceStatus, error) {
			return &ResourceStatus{
				Found:              true,
				Ready:              true,
				ObservedGeneration: 1,
				Message:            "JobSet completed or running",
			}, nil
		},
	}

	r, err := NewReconciler(fakeCMS, mockProb, nil)
	if err != nil {
		t.Fatalf("failed to create reconciler: %v", err)
	}

	spec := &componentspb.ComponentSpec{
		Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER,
		WrapperRef: &componentspb.WrapperRef{
			CrdGroup:   "jobset.x-k8s.io",
			CrdVersion: "v1alpha2",
			CrdKind:    "JobSet",
			TargetName: "training-job",
		},
	}

	res, err := r.ReconcileComponent(context.Background(), "default", "jobset-comp", 1, spec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if res.State != componentspb.ComponentState_COMPONENT_STATE_ENABLED {
		t.Errorf("expected ENABLED, got %v", res.State)
	}
	readyCond := findCondition(res.Conditions, ConditionTypeReady)
	if readyCond == nil || readyCond.Status != metav1.ConditionTrue {
		t.Errorf("expected Ready=True, got %+v", readyCond)
	}
}

func TestReconcile3PWrapperRaySuccess(t *testing.T) {
	fakeCMS := &fakeCMSClient{}
	mockProb := &mockProber{
		statusFn: func(ctx context.Context, ref WrapperRef) (*ResourceStatus, error) {
			return &ResourceStatus{
				Found:              true,
				Ready:              true,
				ObservedGeneration: 2,
				Endpoint:           "ray-cluster-head.xmc.svc.cluster.local:6379",
			}, nil
		},
		probeFn: func(ctx context.Context, target string, timeout time.Duration) (*HealthProbeResult, error) {
			return &HealthProbeResult{Healthy: true, Message: "TCP connect succeeded"}, nil
		},
	}

	r, err := NewReconciler(fakeCMS, mockProb, nil)
	if err != nil {
		t.Fatalf("failed to create reconciler: %v", err)
	}

	spec := &componentspb.ComponentSpec{
		Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER,
		WrapperRef: &componentspb.WrapperRef{
			CrdGroup:   "ray.io",
			CrdVersion: "v1",
			CrdKind:    "RayCluster",
			TargetName: "ray-cluster",
		},
	}

	res, err := r.ReconcileComponent(context.Background(), "xmc", "ray-comp", 2, spec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if res.Coordinates == nil || res.Coordinates.Port != 6379 {
		t.Errorf("expected port 6379, got %+v", res.Coordinates)
	}
}

func TestReconcile3PWrapperCRDMissingNoMatchError(t *testing.T) {
	fakeCMS := &fakeCMSClient{}
	noMatchErr := &meta.NoResourceMatchError{
		PartialResource: schema.GroupVersionResource{
			Group:    "clickhouse.altinity.com",
			Version:  "v1",
			Resource: "clickhouseinstallations",
		},
	}

	mockProb := &mockProber{
		statusFn: func(ctx context.Context, ref WrapperRef) (*ResourceStatus, error) {
			return nil, noMatchErr
		},
	}

	r, err := NewReconciler(fakeCMS, mockProb, nil)
	if err != nil {
		t.Fatalf("failed to create reconciler: %v", err)
	}

	spec := &componentspb.ComponentSpec{
		Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER,
		WrapperRef: &componentspb.WrapperRef{
			CrdGroup:   "clickhouse.altinity.com",
			CrdVersion: "v1",
			CrdKind:    "ClickHouseInstallation",
			TargetName: "missing-crd-chi",
		},
	}

	res, err := r.ReconcileComponent(context.Background(), "xmc", "missing-crd-comp", 1, spec)
	if err != nil {
		t.Fatalf("reconcile should handle missing CRD gracefully without returning fatal error, got: %v", err)
	}

	if res.State != componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION {
		t.Errorf("expected PENDING_RECONCILIATION, got %v", res.State)
	}

	readyCond := findCondition(res.Conditions, ConditionTypeReady)
	if readyCond == nil || readyCond.Status != metav1.ConditionFalse || readyCond.Reason != ReasonDetectionFailed {
		t.Errorf("unexpected Ready condition: %+v", readyCond)
	}

	degCond := findCondition(res.Conditions, ConditionTypeDegraded)
	if degCond == nil || degCond.Status != metav1.ConditionTrue || degCond.Reason != ReasonCRDMissing {
		t.Errorf("unexpected Degraded condition: %+v", degCond)
	}

	if len(fakeCMS.updateObservedGenCalls) != 1 || fakeCMS.updateObservedGenCalls[0].State != componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION {
		t.Errorf("unexpected updateObservedGenCalls in CMS: %+v", fakeCMS.updateObservedGenCalls)
	}
}

func TestReconcile3PWrapperResourceNotFoundAndNotReady(t *testing.T) {
	fakeCMS := &fakeCMSClient{}

	t.Run("resource not found", func(t *testing.T) {
		mockProb := &mockProber{
			statusFn: func(ctx context.Context, ref WrapperRef) (*ResourceStatus, error) {
				return &ResourceStatus{Found: false, Ready: false, Message: "custom resource not found"}, nil
			},
		}
		r, _ := NewReconciler(fakeCMS, mockProb, nil)
		spec := &componentspb.ComponentSpec{
			Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER,
			WrapperRef: &componentspb.WrapperRef{
				CrdGroup: "jobset.x-k8s.io", CrdVersion: "v1alpha2", CrdKind: "JobSet", TargetName: "missing-job",
			},
		}
		res, err := r.ReconcileComponent(context.Background(), "default", "job-comp", 1, spec)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		readyCond := findCondition(res.Conditions, ConditionTypeReady)
		degCond := findCondition(res.Conditions, ConditionTypeDegraded)
		if readyCond == nil || readyCond.Status != metav1.ConditionFalse || readyCond.Reason != ReasonResourceNotFound {
			t.Errorf("unexpected ready condition: %+v", readyCond)
		}
		if degCond == nil || degCond.Status != metav1.ConditionTrue || degCond.Reason != ReasonResourceMissing {
			t.Errorf("unexpected degraded condition: %+v", degCond)
		}
	})

	t.Run("resource not ready", func(t *testing.T) {
		mockProb := &mockProber{
			statusFn: func(ctx context.Context, ref WrapperRef) (*ResourceStatus, error) {
				return &ResourceStatus{Found: true, Ready: false, Message: "Pods are provisioning"}, nil
			},
		}
		r, _ := NewReconciler(fakeCMS, mockProb, nil)
		spec := &componentspb.ComponentSpec{
			Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER,
			WrapperRef: &componentspb.WrapperRef{
				CrdGroup: "jobset.x-k8s.io", CrdVersion: "v1alpha2", CrdKind: "JobSet", TargetName: "prov-job",
			},
		}
		res, err := r.ReconcileComponent(context.Background(), "default", "job-comp", 1, spec)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		readyCond := findCondition(res.Conditions, ConditionTypeReady)
		degCond := findCondition(res.Conditions, ConditionTypeDegraded)
		if readyCond == nil || readyCond.Status != metav1.ConditionFalse || readyCond.Reason != ReasonResourceNotReady {
			t.Errorf("unexpected ready condition: %+v", readyCond)
		}
		if degCond == nil || degCond.Status != metav1.ConditionTrue || degCond.Reason != ReasonDegraded {
			t.Errorf("unexpected degraded condition: %+v", degCond)
		}
	})
}

func TestReconcile3PWrapperUnhealthyProbe(t *testing.T) {
	fakeCMS := &fakeCMSClient{}
	mockProb := &mockProber{
		statusFn: func(ctx context.Context, ref WrapperRef) (*ResourceStatus, error) {
			return &ResourceStatus{
				Found:    true,
				Ready:    true,
				Endpoint: "http://clickhouse.xmc.svc.cluster.local:8123",
			}, nil
		},
		probeFn: func(ctx context.Context, target string, timeout time.Duration) (*HealthProbeResult, error) {
			return &HealthProbeResult{Healthy: false, StatusCode: 503, Message: "HTTP 503 (unhealthy status)"}, nil
		},
	}

	r, err := NewReconciler(fakeCMS, mockProb, nil)
	if err != nil {
		t.Fatalf("failed to create reconciler: %v", err)
	}

	spec := &componentspb.ComponentSpec{
		Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER,
		WrapperRef: &componentspb.WrapperRef{
			CrdGroup:   "clickhouse.altinity.com",
			CrdVersion: "v1",
			CrdKind:    "ClickHouseInstallation",
			TargetName: "unhealthy-chi",
		},
	}

	res, err := r.ReconcileComponent(context.Background(), "xmc", "unhealthy-chi", 2, spec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if res.State != componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION {
		t.Errorf("expected PENDING_RECONCILIATION, got %v", res.State)
	}

	readyCond := findCondition(res.Conditions, ConditionTypeReady)
	if readyCond == nil || readyCond.Status != metav1.ConditionFalse || readyCond.Reason != ReasonProbeFailed {
		t.Errorf("unexpected Ready condition: %+v", readyCond)
	}

	degCond := findCondition(res.Conditions, ConditionTypeDegraded)
	if degCond == nil || degCond.Status != metav1.ConditionTrue || degCond.Reason != ReasonProbeUnhealthy {
		t.Errorf("unexpected Degraded condition: %+v", degCond)
	}
}

func TestReconcileDisabledComponent(t *testing.T) {
	fakeCMS := &fakeCMSClient{}
	mockProb := &mockProber{}

	r, err := NewReconciler(fakeCMS, mockProb, nil)
	if err != nil {
		t.Fatalf("failed to create reconciler: %v", err)
	}

	spec := &componentspb.ComponentSpec{
		Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER,
		WrapperRef: &componentspb.WrapperRef{
			CrdGroup:   "clickhouse.altinity.com",
			CrdVersion: "v1",
			CrdKind:    "ClickHouseInstallation",
			TargetName: "disabled-chi",
		},
	}

	res, err := r.ReconcileComponentEnabled(context.Background(), "xmc", "disabled-chi", 4, spec, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if res.State != componentspb.ComponentState_COMPONENT_STATE_DISABLED {
		t.Errorf("expected DISABLED state, got %v", res.State)
	}

	readyCond := findCondition(res.Conditions, ConditionTypeReady)
	if readyCond == nil || readyCond.Status != metav1.ConditionFalse || readyCond.Reason != ReasonComponentDisabled {
		t.Errorf("unexpected Ready condition: %+v", readyCond)
	}

	degCond := findCondition(res.Conditions, ConditionTypeDegraded)
	if degCond == nil || degCond.Status != metav1.ConditionFalse || degCond.Reason != ReasonDisabled {
		t.Errorf("unexpected Degraded condition: %+v", degCond)
	}

	if len(fakeCMS.completeDeactCalls) != 1 || fakeCMS.completeDeactCalls[0].Name != "components/disabled-chi" {
		t.Errorf("expected CompleteDeactivation call, got %+v", fakeCMS.completeDeactCalls)
	}
	if len(fakeCMS.updateObservedGenCalls) != 1 || fakeCMS.updateObservedGenCalls[0].State != componentspb.ComponentState_COMPONENT_STATE_DISABLED {
		t.Errorf("expected UpdateObservedGen with DISABLED state, got %+v", fakeCMS.updateObservedGenCalls)
	}
}

func TestReconcile1PComponent(t *testing.T) {
	fakeCMS := &fakeCMSClient{}
	mockProb := &mockProber{}

	r, err := NewReconciler(fakeCMS, mockProb, nil)
	if err != nil {
		t.Fatalf("failed to create reconciler: %v", err)
	}

	spec := &componentspb.ComponentSpec{
		Type:        componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version:     "v1.0.0",
		Description: "Built-in 1P Component",
	}

	res, err := r.ReconcileComponent(context.Background(), "xmc", "one-p-comp", 1, spec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if res.State != componentspb.ComponentState_COMPONENT_STATE_ENABLED {
		t.Errorf("expected ENABLED, got %v", res.State)
	}
}

func TestReconcileUnstructuredCRD(t *testing.T) {
	scheme := runtime.NewScheme()
	fakeDynamic := dynamicfake.NewSimpleDynamicClient(scheme)

	compObj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "xmc.dev/v1alpha1",
			"kind":       "Component",
			"metadata": map[string]any{
				"name":       "unstructured-comp",
				"namespace":  "xmc",
				"generation": int64(5),
			},
			"spec": map[string]any{
				"enabled":       true,
				"componentType": "COMPONENT_TYPE_CUSTOM_3P_WRAPPER",
				"wrapperRef": map[string]any{
					"group":     "clickhouse.altinity.com",
					"version":   "v1",
					"kind":      "ClickHouseInstallation",
					"name":      "chi-demo",
					"namespace": "xmc",
				},
				"lifecycleControl": map[string]any{
					"drainTimeoutSeconds": int64(120),
					"forceUpgrade":        true,
				},
			},
		},
	}

	_, err := fakeDynamic.Resource(ComponentGVR).Namespace("xmc").Create(context.Background(), compObj, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("failed to create fake object: %v", err)
	}

	fakeCMS := &fakeCMSClient{}
	mockProb := &mockProber{
		statusFn: func(ctx context.Context, ref WrapperRef) (*ResourceStatus, error) {
			return &ResourceStatus{
				Found:    true,
				Ready:    true,
				Endpoint: "http://chi-demo.xmc.svc:8123",
			}, nil
		},
	}

	r, err := NewReconciler(fakeCMS, mockProb, fakeDynamic)
	if err != nil {
		t.Fatalf("failed to create reconciler: %v", err)
	}

	res, err := r.Reconcile(context.Background(), compObj)
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	if res.State != componentspb.ComponentState_COMPONENT_STATE_ENABLED {
		t.Errorf("expected ENABLED, got %v", res.State)
	}

	updatedObj, err := fakeDynamic.Resource(ComponentGVR).Namespace("xmc").Get(context.Background(), "unstructured-comp", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("failed to fetch updated status: %v", err)
	}

	obsGen, found, _ := unstructured.NestedInt64(updatedObj.Object, "status", "observedGeneration")
	if !found || obsGen != 5 {
		t.Errorf("expected status.observedGeneration 5, got %d (found=%v)", obsGen, found)
	}
}

func TestHelperFunctions(t *testing.T) {
	t.Run("CanonicalComponentName and TrimComponentName", func(t *testing.T) {
		if CanonicalComponentName("foo") != "components/foo" {
			t.Errorf("expected components/foo, got %s", CanonicalComponentName("foo"))
		}
		if CanonicalComponentName("components/foo") != "components/foo" {
			t.Errorf("expected components/foo, got %s", CanonicalComponentName("components/foo"))
		}
		if TrimComponentName("components/bar") != "bar" {
			t.Errorf("expected bar, got %s", TrimComponentName("components/bar"))
		}
		if TrimComponentName("bar") != "bar" {
			t.Errorf("expected bar, got %s", TrimComponentName("bar"))
		}
	})

	t.Run("ParseCoordinates", func(t *testing.T) {
		coords := ParseCoordinates("https://ray-head.xmc.svc.cluster.local:443", "default")
		if coords == nil || coords.Scheme != "https" || coords.Host != "ray-head.xmc.svc.cluster.local" || coords.Port != 443 || coords.ServiceName != "ray-head" || coords.Namespace != "xmc" {
			t.Errorf("unexpected coords: %+v", coords)
		}

		coords2 := ParseCoordinates("clickhouse.analytics:9000", "default")
		if coords2 == nil || coords2.Host != "clickhouse.analytics" || coords2.Port != 9000 || coords2.Namespace != "analytics" {
			t.Errorf("unexpected coords2: %+v", coords2)
		}

		coords3 := ParseCoordinates("", "default")
		if coords3 != nil {
			t.Errorf("expected nil for empty endpoint, got %+v", coords3)
		}
	})

	t.Run("UnstructuredToSpec and Lifecycle", func(t *testing.T) {
		obj := &unstructured.Unstructured{
			Object: map[string]any{
				"metadata": map[string]any{
					"generation": int64(10),
				},
				"spec": map[string]any{
					"enabled":        false,
					"deletionPolicy": "Orphan",
					"version":        "v1.5.0",
					"description":    "sample",
					"manifests":      []any{"yaml1", "yaml2"},
					"lifecycleControl": map[string]any{
						"drainTimeoutSeconds": float64(60),
						"forceUpgrade":        true,
					},
				},
			},
		}

		spec, enabled, err := UnstructuredToSpec(obj)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if enabled {
			t.Errorf("expected enabled=false")
		}
		if spec.DeletionPolicy != componentspb.DeletionPolicy_DELETION_POLICY_ORPHAN {
			t.Errorf("expected Orphan deletion policy, got %v", spec.DeletionPolicy)
		}
		if len(spec.Manifests) != 2 {
			t.Errorf("expected 2 manifests, got %d", len(spec.Manifests))
		}
		if spec.LifecycleControl == nil || spec.LifecycleControl.DrainingTimeout.Seconds != 60 || !spec.LifecycleControl.ForceDeletion {
			t.Errorf("unexpected lifecycle control: %+v", spec.LifecycleControl)
		}
	})

	t.Run("UnstructuredToSpec nil object", func(t *testing.T) {
		_, _, err := UnstructuredToSpec(nil)
		if err == nil {
			t.Fatal("expected error for nil object")
		}
	})
}

func findCondition(conditions []metav1.Condition, condType string) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == condType {
			return &conditions[i]
		}
	}
	return nil
}
