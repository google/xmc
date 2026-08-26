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

package main

import (
	"context"
	"errors"
	"flag"
	"testing"
	"time"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/pkg/components/bridge"

	"google.golang.org/grpc"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

type mockCMS struct {
	setErr error
}

func (m *mockCMS) SetComponentIntent(ctx context.Context, in *componentspb.SetComponentIntentRequest, opts ...grpc.CallOption) (*componentspb.SetComponentIntentResponse, error) {
	if m.setErr != nil {
		return nil, m.setErr
	}
	return &componentspb.SetComponentIntentResponse{
		Component: &componentspb.Component{
			Name: in.Name,
			Spec: in.Spec,
			Status: &componentspb.ComponentAccessStatus{
				State: componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
			},
		},
	}, nil
}

func (m *mockCMS) UpdateObservedGeneration(ctx context.Context, in *componentspb.UpdateObservedGenerationRequest, opts ...grpc.CallOption) (*componentspb.UpdateObservedGenerationResponse, error) {
	return &componentspb.UpdateObservedGenerationResponse{
		Component: &componentspb.Component{
			Name: in.Name,
			Status: &componentspb.ComponentAccessStatus{
				ObservedGeneration: in.ObservedGeneration,
				State:              in.State,
			},
		},
	}, nil
}

func (m *mockCMS) ExecuteDeletion(ctx context.Context, in *componentspb.ExecuteDeletionRequest, opts ...grpc.CallOption) (*componentspb.ExecuteDeletionResponse, error) {
	return &componentspb.ExecuteDeletionResponse{}, nil
}

func (m *mockCMS) CompleteDeactivation(ctx context.Context, in *componentspb.CompleteDeactivationRequest, opts ...grpc.CallOption) (*componentspb.CompleteDeactivationResponse, error) {
	return &componentspb.CompleteDeactivationResponse{}, nil
}

func TestParseConfig(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		wantErr     bool
		wantHelpErr bool
	}{
		{
			name:    "default values",
			args:    []string{},
			wantErr: false,
		},
		{
			name:    "custom values",
			args:    []string{"--cms-address=cms:50051", "--namespace=prod", "--probe-interval=10s", "--probe-timeout=5s"},
			wantErr: false,
		},
		{
			name:    "environment variable fallback",
			args:    []string{},
			wantErr: false,
		},
		{
			name:        "help flag returns ErrHelp",
			args:        []string{"--help"},
			wantErr:     true,
			wantHelpErr: true,
		},
		{
			name:    "invalid probe interval",
			args:    []string{"--probe-interval=0s"},
			wantErr: true,
		},
		{
			name:    "invalid probe timeout",
			args:    []string{"--probe-timeout=-1s"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := ParseConfig(tt.args)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseConfig() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantHelpErr && !errors.Is(err, flag.ErrHelp) {
				t.Errorf("ParseConfig() error = %v, want errors.Is(flag.ErrHelp)", err)
			}
			if err == nil && cfg == nil {
				t.Fatal("expected non-nil Config")
			}
		})
	}
}

func TestBuildKubeConfig(t *testing.T) {
	t.Run("explicit invalid path fails immediately", func(t *testing.T) {
		_, err := BuildKubeConfig("/nonexistent/kubeconfig/path")
		if err == nil {
			t.Error("expected error for nonexistent kubeconfig path")
		}
	})

	t.Run("empty path falls back without panicking", func(t *testing.T) {
		// Outside of cluster, this will return an error or default config, but should not panic.
		_, _ = BuildKubeConfig("")
	})
}

func TestController_RunReconcilePass(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	fakeDynamic := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
		componentGVR: "ComponentList",
	})

	compObj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "xmc.dev/v1alpha1",
			"kind":       "Component",
			"metadata": map[string]any{
				"name":       "test-comp",
				"namespace":  "xmc",
				"generation": int64(1),
			},
			"spec": map[string]any{
				"enabled":            true,
				"deletionProtection": true,
			},
		},
	}

	_, err := fakeDynamic.Resource(componentGVR).Namespace("xmc").Create(ctx, compObj, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("failed to create test resource: %v", err)
	}

	r, err := bridge.NewReconciler(&mockCMS{}, nil, nil)
	if err != nil {
		t.Fatalf("failed to create reconciler: %v", err)
	}

	ctrl := NewController(fakeDynamic, r, "xmc", time.Second, time.Second)
	if err := ctrl.RunReconcilePass(ctx); err != nil {
		t.Fatalf("RunReconcilePass failed: %v", err)
	}

	updated, err := fakeDynamic.Resource(componentGVR).Namespace("xmc").Get(ctx, "test-comp", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("failed to fetch updated resource: %v", err)
	}

	hasFin := false
	for _, f := range updated.GetFinalizers() {
		if f == bridge.FinalizerDeletionProtection {
			hasFin = true
			break
		}
	}
	if !hasFin {
		t.Errorf("expected finalizer %s on resource, got %v", bridge.FinalizerDeletionProtection, updated.GetFinalizers())
	}
	status, ok := updated.Object["status"].(map[string]any)
	if !ok || status == nil {
		t.Fatal("expected status on updated resource")
	}
	if status["state"] != "COMPONENT_STATE_ENABLED" {
		t.Errorf("expected state COMPONENT_STATE_ENABLED, got %v", status["state"])
	}
}

func TestBuildStatus(t *testing.T) {
	res := &bridge.ReconcileResult{
		ObservedGeneration: 5,
		State:              componentspb.ComponentState_COMPONENT_STATE_ENABLED,
		Access:             &bridge.AccessStatus{EnvVars: map[string]string{"PORT": "8080"}},
		Conditions: []bridge.Condition{
			{
				Type:               bridge.ConditionTypeReady,
				Status:             bridge.ConditionStatusTrue,
				ObservedGeneration: 5,
				Reason:             "ComponentReady",
				Message:            "All good",
				LastTransitionTime: time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC),
			},
		},
	}

	status := BuildStatus(res)
	if status.ObservedGeneration != 5 {
		t.Errorf("expected ObservedGeneration 5, got %v", status.ObservedGeneration)
	}
	if status.State != "COMPONENT_STATE_ENABLED" {
		t.Errorf("expected State COMPONENT_STATE_ENABLED, got %v", status.State)
	}
	if status.Access == nil || status.Access.EnvVars["PORT"] != "8080" {
		t.Errorf("expected Access.EnvVars[PORT]=8080, got %+v", status.Access)
	}
	if len(status.Conditions) != 1 {
		t.Fatalf("expected 1 condition, got %d", len(status.Conditions))
	}
	cond := status.Conditions[0]
	if cond.Type != bridge.ConditionTypeReady || cond.Status != string(bridge.ConditionStatusTrue) || cond.ObservedGeneration != 5 || cond.Reason != "ComponentReady" || cond.Message != "All good" {
		t.Errorf("unexpected condition content: %+v", cond)
	}
}

func TestBuildStatusMap(t *testing.T) {
	res := &bridge.ReconcileResult{
		ObservedGeneration: 5,
		State:              componentspb.ComponentState_COMPONENT_STATE_ENABLED,
		Access:             &bridge.AccessStatus{EnvVars: map[string]string{"PORT": "8080"}},
		Conditions: []bridge.Condition{
			{
				Type:               bridge.ConditionTypeReady,
				Status:             bridge.ConditionStatusTrue,
				ObservedGeneration: 5,
				Reason:             "ComponentReady",
				Message:            "All good",
				LastTransitionTime: time.Now(),
			},
		},
	}

	statusMap := BuildStatusMap(res)
	if statusMap["observedGeneration"] != int64(5) {
		t.Errorf("expected observedGeneration 5, got %v", statusMap["observedGeneration"])
	}
	if statusMap["state"] != "COMPONENT_STATE_ENABLED" {
		t.Errorf("expected state COMPONENT_STATE_ENABLED, got %v", statusMap["state"])
	}

	accessMap, ok := statusMap["access"].(map[string]any)
	if !ok {
		t.Fatalf("expected access map in status, got %v", statusMap["access"])
	}
	envVars, ok := accessMap["envVars"].(map[string]any)
	if !ok || envVars["PORT"] != "8080" {
		t.Errorf("expected envVars[PORT]=8080, got %v", accessMap["envVars"])
	}

	conds, ok := statusMap["conditions"].([]any)
	if !ok || len(conds) != 1 {
		t.Fatalf("expected 1 condition in status, got %v", statusMap["conditions"])
	}
	condMap, ok := conds[0].(map[string]any)
	if !ok || condMap["type"] != bridge.ConditionTypeReady || condMap["status"] != string(bridge.ConditionStatusTrue) {
		t.Errorf("unexpected condition content: %+v", condMap)
	}
}

func TestParseConfig_EnvVars(t *testing.T) {
	t.Setenv("CMS_ADDRESS", "custom-cms:50051")
	t.Setenv("NAMESPACE", "custom-ns")
	t.Setenv("PROBE_INTERVAL", "15s")
	t.Setenv("PROBE_TIMEOUT", "8s")
	t.Setenv("KUBECONFIG", "/custom/kubeconfig")

	cfg, err := ParseConfig([]string{})
	if err != nil {
		t.Fatalf("ParseConfig() unexpected error: %v", err)
	}
	if cfg.CMSAddress != "custom-cms:50051" {
		t.Errorf("cfg.CMSAddress = %q, want custom-cms:50051", cfg.CMSAddress)
	}
	if cfg.Namespace != "custom-ns" {
		t.Errorf("cfg.Namespace = %q, want custom-ns", cfg.Namespace)
	}
	if cfg.ProbeInterval != 15*time.Second {
		t.Errorf("cfg.ProbeInterval = %v, want 15s", cfg.ProbeInterval)
	}
	if cfg.ProbeTimeout != 8*time.Second {
		t.Errorf("cfg.ProbeTimeout = %v, want 8s", cfg.ProbeTimeout)
	}
	if cfg.Kubeconfig != "/custom/kubeconfig" {
		t.Errorf("cfg.Kubeconfig = %q, want /custom/kubeconfig", cfg.Kubeconfig)
	}
}

func TestParseConfig_InvalidEnvDuration(t *testing.T) {
	t.Run("invalid PROBE_INTERVAL", func(t *testing.T) {
		t.Setenv("PROBE_INTERVAL", "invalid-duration")
		_, err := ParseConfig([]string{})
		if err == nil {
			t.Error("expected error for invalid PROBE_INTERVAL, got nil")
		}
	})

	t.Run("invalid PROBE_TIMEOUT", func(t *testing.T) {
		t.Setenv("PROBE_TIMEOUT", "not-a-duration")
		_, err := ParseConfig([]string{})
		if err == nil {
			t.Error("expected error for invalid PROBE_TIMEOUT, got nil")
		}
	})
}

func TestController_RunReconcilePass_ErrorSyncsStatus(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	fakeDynamic := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
		componentGVR: "ComponentList",
	})

	compObj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "xmc.dev/v1alpha1",
			"kind":       "Component",
			"metadata": map[string]any{
				"name":       "err-comp",
				"namespace":  "xmc",
				"generation": int64(3),
			},
			"spec": map[string]any{
				"enabled": true,
			},
		},
	}

	_, err := fakeDynamic.Resource(componentGVR).Namespace("xmc").Create(ctx, compObj, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("failed to create test resource: %v", err)
	}

	cmsErr := errors.New("cms connection failure")
	r, err := bridge.NewReconciler(&mockCMS{setErr: cmsErr}, nil, nil)
	if err != nil {
		t.Fatalf("failed to create reconciler: %v", err)
	}

	ctrl := NewController(fakeDynamic, r, "xmc", time.Second, time.Second)
	if err := ctrl.RunReconcilePass(ctx); err != nil {
		t.Fatalf("RunReconcilePass failed: %v", err)
	}

	updated, err := fakeDynamic.Resource(componentGVR).Namespace("xmc").Get(ctx, "err-comp", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("failed to fetch updated resource: %v", err)
	}

	status, ok := updated.Object["status"].(map[string]any)
	if !ok || status == nil {
		t.Fatal("expected status on updated resource even when reconciliation encounters an error")
	}
	if status["state"] != "COMPONENT_STATE_PENDING_RECONCILIATION" {
		t.Errorf("expected state COMPONENT_STATE_PENDING_RECONCILIATION, got %v", status["state"])
	}
	conds, ok := status["conditions"].([]any)
	if !ok || len(conds) == 0 {
		t.Fatalf("expected conditions on updated resource, got %v", status["conditions"])
	}
}
func TestController_SyncFinalizers_NilResult(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	fakeDynamic := dynamicfake.NewSimpleDynamicClient(scheme)
	ctrl := NewController(fakeDynamic, nil, "xmc", time.Second, time.Second)

	item := &unstructured.Unstructured{
		Object: map[string]any{
			"metadata": map[string]any{
				"name":      "test-item",
				"namespace": "xmc",
			},
		},
	}

	got, err := ctrl.syncFinalizers(ctx, item, nil)
	if err != nil {
		t.Fatalf("syncFinalizers returned unexpected error: %v", err)
	}
	if got != item {
		t.Errorf("expected original item returned on nil res, got %v", got)
	}
}

func TestController_ReconcileItem(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	fakeDynamic := dynamicfake.NewSimpleDynamicClient(scheme)
	r, err := bridge.NewReconciler(&mockCMS{}, nil, nil)
	if err != nil {
		t.Fatalf("failed to create reconciler: %v", err)
	}
	ctrl := NewController(fakeDynamic, r, "xmc", time.Second, time.Second)

	item := &unstructured.Unstructured{
		Object: map[string]any{
			"metadata": map[string]any{
				"name":      "test-item",
				"namespace": "xmc",
			},
			"spec": map[string]any{
				"enabled": true,
			},
		},
	}

	// Should safely reconcile without panicking
	ctrl.reconcileItem(ctx, item)
}

