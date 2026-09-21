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

package statussync

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"xmanager_cloud/pkg/components/bridge"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	clientgotesting "k8s.io/client-go/testing"
)

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
		obj, err := fakeDynamic.Tracker().Get(ComponentGVR, patchAction.GetNamespace(), patchAction.GetName())
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
		if err := fakeDynamic.Tracker().Update(ComponentGVR, u, patchAction.GetNamespace()); err != nil {
			return true, nil, err
		}
		return true, u, nil
	})
}

func TestWriter_SyncFinalizers_NilResult(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	fakeDynamic := dynamicfake.NewSimpleDynamicClient(scheme)
	writer := NewWriter(fakeDynamic, "xmc")

	item := &unstructured.Unstructured{
		Object: map[string]any{
			"metadata": map[string]any{
				"name":      "test-item",
				"namespace": "xmc",
			},
		},
	}

	got, err := writer.SyncFinalizers(ctx, item, nil)
	if err != nil {
		t.Fatalf("SyncFinalizers returned unexpected error: %v", err)
	}
	if got != item {
		t.Errorf("expected original item returned on nil res, got %v", got)
	}
}

func TestWriter_SyncFinalizers(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()

	tests := []struct {
		name       string
		item       *unstructured.Unstructured
		res        *bridge.ReconcileResult
		wantFinals []string
	}{
		{
			name: "add finalizer when none exists",
			item: &unstructured.Unstructured{
				Object: map[string]any{
					"apiVersion": ComponentGVR.Group + "/" + ComponentGVR.Version,
					"kind":       "Component",
					"metadata": map[string]any{
						"name":      "comp-add",
						"namespace": "xmc",
					},
				},
			},
			res:        &bridge.ReconcileResult{AddFinalizer: true},
			wantFinals: []string{bridge.FinalizerDeletionProtection},
		},
		{
			name: "remove finalizer when exists",
			item: &unstructured.Unstructured{
				Object: map[string]any{
					"apiVersion": ComponentGVR.Group + "/" + ComponentGVR.Version,
					"kind":       "Component",
					"metadata": map[string]any{
						"name":       "comp-remove",
						"namespace":  "xmc",
						"finalizers": []any{bridge.FinalizerDeletionProtection},
					},
				},
			},
			res:        &bridge.ReconcileResult{RemoveFinalizer: true},
			wantFinals: nil,
		},
		{
			name: "no-op when finalizer already exists and add is requested",
			item: &unstructured.Unstructured{
				Object: map[string]any{
					"apiVersion": ComponentGVR.Group + "/" + ComponentGVR.Version,
					"kind":       "Component",
					"metadata": map[string]any{
						"name":       "comp-noop-add",
						"namespace":  "xmc",
						"finalizers": []any{bridge.FinalizerDeletionProtection},
					},
				},
			},
			res:        &bridge.ReconcileResult{AddFinalizer: true},
			wantFinals: []string{bridge.FinalizerDeletionProtection},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeDynamic := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
				ComponentGVR: "ComponentList",
			})
			if _, err := fakeDynamic.Resource(ComponentGVR).Namespace("xmc").Create(ctx, tt.item, metav1.CreateOptions{}); err != nil {
				t.Fatalf("failed to create object: %v", err)
			}

			writer := NewWriter(fakeDynamic, "xmc")
			updated, err := writer.SyncFinalizers(ctx, tt.item, tt.res)
			if err != nil {
				t.Fatalf("SyncFinalizers failed: %v", err)
			}
			// EquateEmpty: the API server reports "no finalizers" as either nil or an empty
			// slice depending on whether the key was ever written, and both are equivalent here.
			if diff := cmp.Diff(tt.wantFinals, updated.GetFinalizers(), cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("SyncFinalizers() finalizers mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestWriter_ApplyStatus(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	fakeDynamic := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
		ComponentGVR: "ComponentList",
	})
	attachSSAPatchReactor(fakeDynamic)

	item := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": ComponentGVR.Group + "/" + ComponentGVR.Version,
			"kind":       "Component",
			"metadata": map[string]any{
				"name":      "test-status",
				"namespace": "xmc",
			},
		},
	}
	if _, err := fakeDynamic.Resource(ComponentGVR).Namespace("xmc").Create(ctx, item, metav1.CreateOptions{}); err != nil {
		t.Fatalf("failed to create object: %v", err)
	}

	writer := NewWriter(fakeDynamic, "xmc")
	res := &bridge.ReconcileResult{
		ObservedGeneration: 10,
	}

	err := writer.ApplyStatus(ctx, item, res)
	if err != nil {
		t.Fatalf("ApplyStatus failed: %v", err)
	}

	updated, err := fakeDynamic.Resource(ComponentGVR).Namespace("xmc").Get(ctx, "test-status", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("failed to get updated item: %v", err)
	}

	status, ok := updated.Object["status"].(map[string]any)
	if !ok {
		t.Fatal("expected status map on updated object")
	}
	switch v := status["observedGeneration"].(type) {
	case int64:
		if v != int64(10) {
			t.Errorf("got observedGeneration %v, want 10", v)
		}
	case float64:
		if v != float64(10) {
			t.Errorf("got observedGeneration %v, want 10", v)
		}
	case int:
		if v != int(10) {
			t.Errorf("got observedGeneration %v, want 10", v)
		}
	default:
		t.Errorf("got observedGeneration of unexpected type %T (%v), want 10", v, v)
	}
}
