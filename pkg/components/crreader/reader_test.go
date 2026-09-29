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

package crreader

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

func baselineComponent() *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "xmc.dev/v1alpha1",
			"kind":       "Component",
			"metadata": map[string]any{
				"name":      "clickhouse",
				"namespace": "xmc",
			},
			"spec": map[string]any{
				"enabled":       true,
				"componentType": "COMPONENT_TYPE_CUSTOM_3P_WRAPPER",
				"parameters": map[string]any{
					"host": "clickhouse-from-spec.xmc.svc.cluster.local",
					"port": "9000",
				},
			},
			"status": map[string]any{
				"state": "COMPONENT_STATE_ENABLED",
				"access": map[string]any{
					"envVars": map[string]any{
						"ENDPOINT": "clickhouse-live.xmc.svc.cluster.local",
						"PORT":     "9000",
					},
				},
			},
		},
	}
}

func TestComponentGVR(t *testing.T) {
	t.Parallel()
	want := schema.GroupVersionResource{
		Group:    "xmc.dev",
		Version:  "v1alpha1",
		Resource: "components",
	}
	if diff := cmp.Diff(want, ComponentGVR()); diff != "" {
		t.Errorf("ComponentGVR() mismatch (-want +got):\n%s", diff)
	}
}

func TestResolveNamespace(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty defaults to xmc", in: "", want: "xmc"},
		{name: "whitespace defaults to xmc", in: "   ", want: "xmc"},
		{name: "custom namespace trimmed", in: " custom-ns ", want: "custom-ns"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := ResolveNamespace(tt.in); got != tt.want {
				t.Errorf("ResolveNamespace(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestNewGetterFromDynamic(t *testing.T) {
	t.Parallel()
	scheme := runtime.NewScheme()
	dynClient := dynamicfake.NewSimpleDynamicClient(scheme, baselineComponent())
	getter := NewGetterFromDynamic(dynClient)

	got, err := getter.GetComponent(context.Background(), "", "clickhouse")
	if err != nil {
		t.Fatalf("GetComponent() unexpected error: %v", err)
	}
	if diff := cmp.Diff(baselineComponent().Object, got.Object); diff != "" {
		t.Errorf("GetComponent() mismatch (-want +got):\n%s", diff)
	}

	if _, err := getter.GetComponent(context.Background(), "xmc", "nonexistent"); err == nil {
		t.Errorf("GetComponent(nonexistent) succeeded, want not-found error")
	}

	nilGetter := NewGetterFromDynamic(nil)
	if _, err := nilGetter.GetComponent(context.Background(), "xmc", "clickhouse"); err == nil {
		t.Errorf("GetComponent() with nil dynamic client succeeded, want error")
	}

	var rawNil *k8sGetter
	if _, err := rawNil.GetComponent(context.Background(), "xmc", "clickhouse"); err == nil {
		t.Errorf("GetComponent() on nil *k8sGetter succeeded, want error")
	}
}

func TestNewK8sGetter(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	t.Setenv("KUBERNETES_SERVICE_PORT", "")

	tmpDir := t.TempDir()
	validKubeconfig := filepath.Join(tmpDir, "kubeconfig")
	kubeconfigContent := `apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://127.0.0.1:6443
  name: test-cluster
contexts:
- context:
    cluster: test-cluster
    user: test-user
  name: test-context
current-context: test-context
users:
- name: test-user
  user:
    token: fake-token
`
	if err := os.WriteFile(validKubeconfig, []byte(kubeconfigContent), 0600); err != nil {
		t.Fatalf("failed to write temp kubeconfig: %v", err)
	}

	t.Setenv("KUBECONFIG", validKubeconfig)
	getter, err := NewK8sGetter()
	if err != nil {
		t.Fatalf("NewK8sGetter() with valid kubeconfig failed: %v", err)
	}
	if getter == nil {
		t.Fatalf("NewK8sGetter() returned nil getter")
	}

	invalidKubeconfig := filepath.Join(tmpDir, "invalid-kubeconfig")
	if err := os.WriteFile(invalidKubeconfig, []byte(":::not-valid-yaml:::"), 0600); err != nil {
		t.Fatalf("failed to write invalid kubeconfig: %v", err)
	}
	t.Setenv("KUBECONFIG", invalidKubeconfig)
	if _, err := NewK8sGetter(); err == nil {
		t.Errorf("NewK8sGetter() with invalid kubeconfig succeeded, want error")
	}
}
