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
	"encoding/json"
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
	clientgotesting "k8s.io/client-go/testing"
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

func (m *mockCMS) PrepareDeactivation(ctx context.Context, in *componentspb.PrepareDeactivationRequest, opts ...grpc.CallOption) (*componentspb.PrepareDeactivationResponse, error) {
	return &componentspb.PrepareDeactivationResponse{}, nil
}

func TestParseConfig(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		wantErr     bool
		wantHelpErr bool
	}{
		{
			name:    "valid flags",
			args:    []string{"--cms-address=cms:50051", "--namespace=prod", "--release-name=xmanager-cloud", "--probe-interval=10s", "--probe-timeout=5s"},
			wantErr: false,
		},
		{
			name:    "missing cms-address",
			args:    []string{"--namespace=prod", "--release-name=xmanager-cloud"},
			wantErr: true,
		},
		{
			name:    "missing namespace",
			args:    []string{"--cms-address=cms:50051", "--release-name=xmanager-cloud"},
			wantErr: true,
		},
		{
			name:    "missing release-name",
			args:    []string{"--cms-address=cms:50051", "--namespace=prod"},
			wantErr: true,
		},
		{
			name:        "help flag returns ErrHelp",
			args:        []string{"--help"},
			wantErr:     true,
			wantHelpErr: true,
		},
		{
			name:    "invalid probe interval",
			args:    []string{"--cms-address=cms:50051", "--namespace=prod", "--release-name=xmanager-cloud", "--probe-interval=0s"},
			wantErr: true,
		},
		{
			name:    "invalid probe timeout",
			args:    []string{"--cms-address=cms:50051", "--namespace=prod", "--release-name=xmanager-cloud", "--probe-timeout=-1s"},
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

func attachSSAPatchReactor(fakeDynamic *dynamicfake.FakeDynamicClient) {
	fakeDynamic.PrependReactor("patch", "components", func(action clientgotesting.Action) (handled bool, ret runtime.Object, err error) {
		patchAction, ok := action.(clientgotesting.PatchAction)
		if !ok {
			return false, nil, nil
		}
		var patchMap map[string]any
		if err := json.Unmarshal(patchAction.GetPatch(), &patchMap); err != nil {
			return true, nil, err
		}
		obj, err := fakeDynamic.Tracker().Get(componentGVR, patchAction.GetNamespace(), patchAction.GetName())
		if err != nil {
			return true, nil, err
		}
		unstruct, ok := obj.(*unstructured.Unstructured)
		if !ok {
			return true, nil, errors.New("expected *unstructured.Unstructured from tracker")
		}
		u := unstruct.DeepCopy()
		if status, ok := patchMap["status"].(map[string]any); ok {
			u.Object["status"] = status
		}
		if err := fakeDynamic.Tracker().Update(componentGVR, u, patchAction.GetNamespace()); err != nil {
			return true, nil, err
		}
		return true, u, nil
	})
}

func TestController_RunReconcilePass(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	fakeDynamic := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
		componentGVR: "ComponentList",
	})
	attachSSAPatchReactor(fakeDynamic)

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

	ctrl := NewController(fakeDynamic, r, nil, "xmc", time.Second, time.Second)
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

// passOutcome is the observable status of a Component after a reconcile pass.
type passOutcome struct {
	State       any
	ReadyReason string
}

func TestController_RunReconcilePass_GatesOnDependencySnapshot(t *testing.T) {
	component := func(name string, spec map[string]any) *unstructured.Unstructured {
		return &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "xmc.dev/v1alpha1",
			"kind":       "Component",
			"metadata":   map[string]any{"name": name, "namespace": "xmc", "generation": int64(1)},
			"spec":       spec,
		}}
	}
	tests := []struct {
		name       string
		items      []*unstructured.Unstructured
		wantState  string
		wantReason string
	}{
		{
			name:       "missing dependency gates activation",
			items:      []*unstructured.Unstructured{component("ingestion-gateway", map[string]any{"enabled": true, "dependencies": []any{"redpanda"}})},
			wantState:  "COMPONENT_STATE_PENDING_RECONCILIATION",
			wantReason: bridge.ReasonDependenciesNotReady,
		},
		{
			name: "disabled dependency gates activation",
			items: []*unstructured.Unstructured{
				component("ingestion-gateway", map[string]any{"enabled": true, "dependencies": []any{"redpanda"}}),
				component("redpanda", map[string]any{"enabled": false}),
			},
			wantState:  "COMPONENT_STATE_PENDING_RECONCILIATION",
			wantReason: bridge.ReasonDependenciesNotReady,
		},
		{
			name:       "no dependencies activates",
			items:      []*unstructured.Unstructured{component("ingestion-gateway", map[string]any{"enabled": true})},
			wantState:  "COMPONENT_STATE_ENABLED",
			wantReason: bridge.ReasonComponentHealthy,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			fakeDynamic := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{
				componentGVR: "ComponentList",
			})
			attachSSAPatchReactor(fakeDynamic)
			for _, item := range tt.items {
				if _, err := fakeDynamic.Resource(componentGVR).Namespace("xmc").Create(ctx, item, metav1.CreateOptions{}); err != nil {
					t.Fatalf("failed to create %s: %v", item.GetName(), err)
				}
			}
			r, err := bridge.NewReconciler(&mockCMS{}, nil, nil)
			if err != nil {
				t.Fatalf("failed to create reconciler: %v", err)
			}
			if err := NewController(fakeDynamic, r, nil, "xmc", time.Second, time.Second).RunReconcilePass(ctx); err != nil {
				t.Fatalf("RunReconcilePass failed: %v", err)
			}
			updated, err := fakeDynamic.Resource(componentGVR).Namespace("xmc").Get(ctx, "ingestion-gateway", metav1.GetOptions{})
			if err != nil {
				t.Fatalf("failed to fetch ingestion-gateway: %v", err)
			}
			conds := bridge.ParseConditionsFromUnstructured(updated.Object["status"])
			var gotReason string
			for _, c := range conds {
				if c.Type == bridge.ConditionTypeReady {
					gotReason = c.Reason
				}
			}
			status, _ := updated.Object["status"].(map[string]any)
			got := passOutcome{State: status["state"], ReadyReason: gotReason}
			if want := (passOutcome{State: tt.wantState, ReadyReason: tt.wantReason}); got != want {
				t.Errorf("ingestion-gateway outcome = %+v, want %+v", got, want)
			}
		})
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
	t.Setenv("HELM_RELEASE_NAME", "custom-release")
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
	if cfg.ReleaseName != "custom-release" {
		t.Errorf("cfg.ReleaseName = %q, want custom-release", cfg.ReleaseName)
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
	t.Setenv("CMS_ADDRESS", "cms:50051")
	t.Setenv("NAMESPACE", "xmc")
	t.Setenv("HELM_RELEASE_NAME", "xmanager-cloud")

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
	attachSSAPatchReactor(fakeDynamic)

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

	ctrl := NewController(fakeDynamic, r, nil, "xmc", time.Second, time.Second)
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
func TestController_PersistResult_NilResult(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	fakeDynamic := dynamicfake.NewSimpleDynamicClient(scheme)
	ctrl := NewController(fakeDynamic, nil, nil, "xmc", time.Second, time.Second)

	item := &unstructured.Unstructured{
		Object: map[string]any{
			"metadata": map[string]any{
				"name":      "test-item",
				"namespace": "xmc",
			},
		},
	}

	if err := ctrl.persistResult(ctx, item, nil); err != nil {
		t.Fatalf("persistResult returned unexpected error: %v", err)
	}
	if got := len(fakeDynamic.Actions()); got != 0 {
		t.Errorf("persistResult(nil) issued %d API actions, want 0", got)
	}
}

func TestController_ReconcileItem(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	fakeDynamic := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
		componentGVR: "ComponentList",
	})
	attachSSAPatchReactor(fakeDynamic)

	item := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "xmc.dev/v1alpha1",
			"kind":       "Component",
			"metadata": map[string]any{
				"name":      "test-item",
				"namespace": "xmc",
			},
			"spec": map[string]any{
				"enabled": true,
			},
		},
	}
	if _, err := fakeDynamic.Resource(componentGVR).Namespace("xmc").Create(ctx, item, metav1.CreateOptions{}); err != nil {
		t.Fatalf("failed to create test-item: %v", err)
	}

	r, err := bridge.NewReconciler(&mockCMS{}, nil, nil)
	if err != nil {
		t.Fatalf("failed to create reconciler: %v", err)
	}
	ctrl := NewController(fakeDynamic, r, nil, "xmc", time.Second, time.Second)

	// Should safely reconcile without panicking
	ctrl.reconcileItem(ctx, item, nil)
}
