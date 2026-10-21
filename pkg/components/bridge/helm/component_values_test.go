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

package helm

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

// TestK8sHelmManager_ComponentValuesMutations walks one component's values through persist, disable, and remove,
// checking the reported modification flag and the resulting ConfigMap entry after every step.
func TestK8sHelmManager_ComponentValuesMutations(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()
	mgr := NewK8sHelmManager(client, "xmc", "xmanager-cloud", WithSyncExecution(true))
	defer mgr.Close()

	const name = "metrics"
	helmKey := ComponentNameToHelmKey(name)
	enabledValues := map[string]any{helmKey: map[string]any{"enabled": true, "replicas": 2}}
	persist := func() (bool, error) { return mgr.persistComponentValues(ctx, "xmc", name, enabledValues) }
	disable := func() (bool, error) { return mgr.disableComponentValues(ctx, "xmc", name) }
	remove := func() (bool, error) { return mgr.removeComponentValues(ctx, "xmc", name) }

	steps := []struct {
		name         string
		op           func() (bool, error)
		wantModified bool
		wantJSON     string // empty means the key must be absent
	}{
		{name: "disable creates missing ConfigMap", op: disable, wantModified: true, wantJSON: `{"` + helmKey + `":{"enabled":false}}`},
		{name: "persist overwrites values", op: persist, wantModified: true, wantJSON: `{"` + helmKey + `":{"enabled":true,"replicas":2}}`},
		{name: "persist identical values is no-op", op: persist, wantModified: false, wantJSON: `{"` + helmKey + `":{"enabled":true,"replicas":2}}`},
		{name: "disable preserves parameters", op: disable, wantModified: true, wantJSON: `{"` + helmKey + `":{"enabled":false,"replicas":2}}`},
		{name: "disable again is no-op", op: disable, wantModified: false, wantJSON: `{"` + helmKey + `":{"enabled":false,"replicas":2}}`},
		{name: "remove deletes key", op: remove, wantModified: true},
		{name: "remove absent key is no-op", op: remove, wantModified: false},
	}
	for _, step := range steps {
		modified, err := step.op()
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", step.name, err)
		}
		if modified != step.wantModified {
			t.Errorf("%s: modified = %v, want %v", step.name, modified, step.wantModified)
		}
		cm, err := client.CoreV1().ConfigMaps("xmc").Get(ctx, ComponentValuesConfigMapName, metav1.GetOptions{})
		if err != nil {
			t.Fatalf("%s: failed to get ConfigMap: %v", step.name, err)
		}
		got, present := cm.Data[componentValuesKey(name)]
		if present != (step.wantJSON != "") || got != step.wantJSON {
			t.Errorf("%s: ConfigMap entry = %q (present %v), want %q", step.name, got, present, step.wantJSON)
		}
	}
}

func TestK8sHelmManager_RemoveComponentValues_MissingConfigMap(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()
	mgr := NewK8sHelmManager(client, "xmc", "xmanager-cloud", WithSyncExecution(true))
	defer mgr.Close()

	modified, err := mgr.removeComponentValues(ctx, "xmc", "metrics")
	if err != nil || modified {
		t.Fatalf("removeComponentValues() = (%v, %v), want (false, nil)", modified, err)
	}
	if _, err := client.CoreV1().ConfigMaps("xmc").Get(ctx, ComponentValuesConfigMapName, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Errorf("expected removeComponentValues not to create the ConfigMap, got err %v", err)
	}
}

func TestK8sHelmManager_ComponentValues_NilClient(t *testing.T) {
	mgr := NewK8sHelmManager(nil, "xmc", "xmanager-cloud", WithSyncExecution(true))
	defer mgr.Close()

	if _, err := mgr.disableComponentValues(context.Background(), "xmc", "metrics"); err == nil {
		t.Error("expected an error for an uninitialized Kubernetes client")
	}
}

func TestK8sHelmManager_ConfigMapDeclarativeSourceOfTruth(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()
	runner := &mockHelmActionRunner{}
	mgr := NewK8sHelmManager(client, "xmc", "xmanager-cloud",
		WithActionRunner(runner),
		WithSyncExecution(true),
	)
	defer mgr.Close()

	refA := ComponentRef{Namespace: "xmc", Name: "rod-data-provider"}
	optsA := HelmComponentOptions{Ref: refA, Enabled: true}
	if err := mgr.Ensure1PComponent(ctx, refA, optsA); err != nil {
		t.Fatalf("Ensure1PComponent A failed: %v", err)
	}

	refB := ComponentRef{Namespace: "xmc", Name: "ingestion-gateway"}
	optsB := HelmComponentOptions{Ref: refB, Enabled: true}
	if err := mgr.Ensure1PComponent(ctx, refB, optsB); err != nil {
		t.Fatalf("Ensure1PComponent B failed: %v", err)
	}

	// Verify ConfigMap persists both components declaratively
	cm, err := client.CoreV1().ConfigMaps("xmc").Get(ctx, ComponentValuesConfigMapName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("failed to get ConfigMap: %v", err)
	}
	if _, ok := cm.Data["rod-data-provider.json"]; !ok {
		t.Error("expected rod-data-provider.json in ConfigMap")
	}
	if _, ok := cm.Data["ingestion-gateway.json"]; !ok {
		t.Error("expected ingestion-gateway.json in ConfigMap")
	}

	// Verify that component B was actuated under its own isolated release
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if runner.upgradeCalls != 2 {
		t.Fatalf("expected 2 upgrade calls, got %d", runner.upgradeCalls)
	}
	expectedReleaseB := ComponentReleaseName("ingestion-gateway")
	if runner.lastRelease != expectedReleaseB {
		t.Errorf("expected last release %q, got %q", expectedReleaseB, runner.lastRelease)
	}
	_, hasIngestion := runner.lastValues["ingestionGatewayDeployment"]
	if !hasIngestion {
		t.Errorf("expected ingestionGatewayDeployment in upgrade values, got keys: %+v", runner.lastValues)
	}
}

// TestK8sHelmManager_ComponentValues_CreateAlreadyExistsRetriesAsUpdate verifies that when ConfigMap creation loses a
// race with another writer, AlreadyExists is translated to Conflict so RetryOnConflict re-reads the existing ConfigMap,
// preserves other components' keys, and re-applies the mutation on top of the concurrently written entry.
func TestK8sHelmManager_ComponentValues_CreateAlreadyExistsRetriesAsUpdate(t *testing.T) {
	const (
		ns    = "xmc"
		comp  = "metrics"
		other = "rod-data-provider"
	)
	helmKey := ComponentNameToHelmKey(comp)
	otherJSON := `{"rodDeployment":{"enabled":true}}`
	tests := []struct {
		name           string
		concurrentData map[string]string
		mutate         func(context.Context, *K8sHelmManager) (bool, error)
		wantData       map[string]string
	}{
		{
			name:           "persist merges with concurrently created ConfigMap",
			concurrentData: map[string]string{componentValuesKey(other): otherJSON},
			mutate: func(ctx context.Context, m *K8sHelmManager) (bool, error) {
				return m.persistComponentValues(ctx, ns, comp, map[string]any{helmKey: map[string]any{"enabled": true}})
			},
			wantData: map[string]string{
				componentValuesKey(other): otherJSON,
				componentValuesKey(comp):  `{"` + helmKey + `":{"enabled":true}}`,
			},
		},
		{
			name: "disable re-applies mutation onto concurrently written component data",
			concurrentData: map[string]string{
				componentValuesKey(other): otherJSON,
				componentValuesKey(comp):  `{"` + helmKey + `":{"enabled":true,"replicas":3}}`,
			},
			mutate: func(ctx context.Context, m *K8sHelmManager) (bool, error) {
				return m.disableComponentValues(ctx, ns, comp)
			},
			wantData: map[string]string{
				componentValuesKey(other): otherJSON,
				componentValuesKey(comp):  `{"` + helmKey + `":{"enabled":false,"replicas":3}}`,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			client := fake.NewClientset()
			raced := false
			client.PrependReactor("create", "configmaps", func(k8stesting.Action) (bool, runtime.Object, error) {
				if raced {
					return false, nil, nil
				}
				raced = true
				_ = client.Tracker().Add(&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{Name: ComponentValuesConfigMapName, Namespace: ns},
					Data:       tt.concurrentData,
				})
				return true, nil, apierrors.NewAlreadyExists(corev1.Resource("configmaps"), ComponentValuesConfigMapName)
			})
			mgr := NewK8sHelmManager(client, ns, "xmanager-cloud", WithSyncExecution(true))
			defer mgr.Close()

			modified, err := tt.mutate(ctx, mgr)
			if err != nil || !modified {
				t.Fatalf("mutate() = (%v, %v), want (true, nil)", modified, err)
			}
			cm, err := client.CoreV1().ConfigMaps(ns).Get(ctx, ComponentValuesConfigMapName, metav1.GetOptions{})
			if err != nil {
				t.Fatalf("Get ConfigMap failed: %v", err)
			}
			if diff := cmp.Diff(tt.wantData, cm.Data); diff != "" {
				t.Errorf("ConfigMap.Data mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
