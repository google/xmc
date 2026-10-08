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
	"slices"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"

	componentsv1alpha1 "xmanager_cloud/api/protos/components/v1alpha1"
)

func newStatusApplierTestScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	s.AddKnownTypeWithName(
		schema.GroupVersionKind{Group: "xmc.dev", Version: "v1alpha1", Kind: "Component"},
		&unstructured.Unstructured{},
	)
	s.AddKnownTypeWithName(
		schema.GroupVersionKind{Group: "xmc.dev", Version: "v1alpha1", Kind: "ComponentList"},
		&unstructured.UnstructuredList{},
	)
	return s
}

func newTestComponent(name, namespace string, finalizers ...string) *unstructured.Unstructured {
	item := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "xmc.dev/v1alpha1",
			"kind":       "Component",
			"metadata": map[string]any{
				"name":      name,
				"namespace": namespace,
			},
		},
	}
	if len(finalizers) > 0 {
		item.SetFinalizers(finalizers)
	}
	return item
}

func TestBuildStatusAndBuildStatusMap(t *testing.T) {
	ts := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

	t.Run("nil ReconcileResult returns zero status", func(t *testing.T) {
		got := BuildStatus(nil)
		if diff := cmp.Diff(ComponentStatus{}, got); diff != "" {
			t.Errorf("BuildStatus(nil) mismatch (-want +got):\n%s", diff)
		}
		gotMap := BuildStatusMap(nil)
		wantMap := map[string]any{
			"observedGeneration": int64(0),
			"state":              "",
		}
		if diff := cmp.Diff(wantMap, gotMap); diff != "" {
			t.Errorf("BuildStatusMap(nil) mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("populated ReconcileResult converts access and conditions", func(t *testing.T) {
		res := &ReconcileResult{
			ObservedGeneration:  5,
			ComponentGeneration: 2,
			ComponentID:         "6200370836252645701",
			State:               componentsv1alpha1.ComponentState_COMPONENT_STATE_ENABLED,
			Access: &AccessStatus{
				EnvVars: map[string]string{"DB_HOST": "clickhouse.xmc.svc"},
			},
			Conditions: []Condition{
				{
					Type:               ConditionTypeReady,
					Status:             ConditionStatusTrue,
					ObservedGeneration: 5,
					Reason:             ReasonComponentHealthy,
					Message:            "Ready",
					LastTransitionTime: ts,
				},
			},
		}
		got := BuildStatus(res)
		want := ComponentStatus{
			ObservedGeneration:  5,
			ComponentGeneration: 2,
			ComponentID:         "6200370836252645701",
			State:               "COMPONENT_STATE_ENABLED",
			Access:              &ComponentAccess{EnvVars: map[string]string{"DB_HOST": "clickhouse.xmc.svc"}},
			Conditions: []ComponentCondition{
				{
					Type:               ConditionTypeReady,
					Status:             ConditionStatusTrue,
					ObservedGeneration: 5,
					Reason:             ReasonComponentHealthy,
					Message:            "Ready",
					LastTransitionTime: "2026-10-06T12:00:00Z",
				},
			},
		}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("BuildStatus() mismatch (-want +got):\n%s", diff)
		}

		gotMap := BuildStatusMap(res)
		if gotMap["state"] != "COMPONENT_STATE_ENABLED" || gotMap["observedGeneration"] != int64(5) || gotMap["componentGeneration"] != int64(2) || gotMap["componentId"] != "6200370836252645701" {
			t.Errorf("unexpected BuildStatusMap output: %v", gotMap)
		}
	})
}

func TestApplyComponentStatusSSA(t *testing.T) {
	ctx := context.Background()
	item := newTestComponent("comp-1", "xmc-ns")
	res := &ReconcileResult{
		ObservedGeneration: 3,
		State:              componentsv1alpha1.ComponentState_COMPONENT_STATE_ENABLED,
	}

	t.Run("rejects nil client, item, or result", func(t *testing.T) {
		client := dynamicfake.NewSimpleDynamicClient(newStatusApplierTestScheme())
		if err := ApplyComponentStatusSSA(ctx, nil, "default", item, res); err == nil {
			t.Error("expected error for nil client, got nil")
		}
		if err := ApplyComponentStatusSSA(ctx, client, "default", nil, res); err == nil {
			t.Error("expected error for nil item, got nil")
		}
		if err := ApplyComponentStatusSSA(ctx, client, "default", item, nil); err == nil {
			t.Error("expected error for nil result, got nil")
		}
	})

	t.Run("applies SSA patch to status subresource with fieldManager xmc-bridge and default namespace fallback", func(t *testing.T) {
		noNsItem := newTestComponent("comp-default-ns", "")
		client := dynamicfake.NewSimpleDynamicClient(newStatusApplierTestScheme())
		var gotSubresource, gotNamespace string
		var gotPatchType types.PatchType
		var gotPayload map[string]any

		client.PrependReactor("patch", "components", func(action k8stesting.Action) (bool, runtime.Object, error) {
			pa := action.(k8stesting.PatchAction)
			gotSubresource = pa.GetSubresource()
			gotNamespace = pa.GetNamespace()
			gotPatchType = pa.GetPatchType()
			if err := json.Unmarshal(pa.GetPatch(), &gotPayload); err != nil {
				t.Fatalf("failed to unmarshal SSA patch: %v", err)
			}
			out := noNsItem.DeepCopy()
			out.Object["status"] = gotPayload["status"]
			return true, out, nil
		})

		if err := ApplyComponentStatusSSA(ctx, client, "fallback-ns", noNsItem, res); err != nil {
			t.Fatalf("ApplyComponentStatusSSA() unexpected error: %v", err)
		}
		if gotSubresource != "status" {
			t.Errorf("subresource = %q, want %q", gotSubresource, "status")
		}
		if gotNamespace != "fallback-ns" {
			t.Errorf("namespace = %q, want %q", gotNamespace, "fallback-ns")
		}
		if gotPatchType != types.ApplyPatchType {
			t.Errorf("patchType = %q, want %q", gotPatchType, types.ApplyPatchType)
		}
		statusObj, _ := noNsItem.Object["status"].(map[string]any)
		if statusObj["state"] != "COMPONENT_STATE_ENABLED" {
			t.Errorf("updated item status = %v, want state COMPONENT_STATE_ENABLED", statusObj)
		}
	})

	t.Run("propagates patch error", func(t *testing.T) {
		client := dynamicfake.NewSimpleDynamicClient(newStatusApplierTestScheme())
		client.PrependReactor("patch", "components", func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, errors.New("apiserver unavailable")
		})
		if err := ApplyComponentStatusSSA(ctx, client, "default", item.DeepCopy(), res); err == nil {
			t.Fatal("expected error from ApplyComponentStatusSSA, got nil")
		}
	})
}

func TestSyncComponentFinalizers(t *testing.T) {
	ctx := context.Background()

	t.Run("returns early without error on nil inputs or no-op result even if client is nil", func(t *testing.T) {
		item := newTestComponent("comp-1", "default")
		if got, err := SyncComponentFinalizers(ctx, nil, "default", nil, &ReconcileResult{AddFinalizer: true}); err != nil || got != nil {
			t.Errorf("expected (nil, nil) for nil item, got (%v, %v)", got, err)
		}
		if got, err := SyncComponentFinalizers(ctx, nil, "default", item, nil); err != nil || got != item {
			t.Errorf("expected (item, nil) for nil res, got (%v, %v)", got, err)
		}
		if got, err := SyncComponentFinalizers(ctx, nil, "default", item, &ReconcileResult{}); err != nil || got != item {
			t.Errorf("expected (item, nil) for no-op res, got (%v, %v)", got, err)
		}
		if _, err := SyncComponentFinalizers(ctx, nil, "default", item, &ReconcileResult{AddFinalizer: true}); err == nil {
			t.Error("expected error when client is nil and mutation is requested")
		}
	})

	t.Run("attaches and detaches finalizer without mutating input item in-place", func(t *testing.T) {
		orig := newTestComponent("comp-fin", "default", "other-fin")
		client := dynamicfake.NewSimpleDynamicClient(newStatusApplierTestScheme(), orig)

		attached, err := SyncComponentFinalizers(ctx, client, "default", orig, &ReconcileResult{AddFinalizer: true})
		if err != nil {
			t.Fatalf("SyncComponentFinalizers(AddFinalizer) failed: %v", err)
		}
		if slices.Contains(orig.GetFinalizers(), FinalizerDeletionProtection) {
			t.Errorf("expected original input item not to be mutated in-place, got finalizers %v", orig.GetFinalizers())
		}
		if !slices.Contains(attached.GetFinalizers(), FinalizerDeletionProtection) {
			t.Errorf("expected finalizer %q on returned item, got %v", FinalizerDeletionProtection, attached.GetFinalizers())
		}

		detached, err := SyncComponentFinalizers(ctx, client, "default", attached, &ReconcileResult{RemoveFinalizer: true})
		if err != nil {
			t.Fatalf("SyncComponentFinalizers(RemoveFinalizer) failed: %v", err)
		}
		if slices.Contains(detached.GetFinalizers(), FinalizerDeletionProtection) {
			t.Errorf("expected finalizer %q to be removed, got %v", FinalizerDeletionProtection, detached.GetFinalizers())
		}
	})

	t.Run("retries on conflict and succeeds after fetching latest object", func(t *testing.T) {
		orig := newTestComponent("comp-conflict", "default")
		client := dynamicfake.NewSimpleDynamicClient(newStatusApplierTestScheme(), orig)
		attempts := 0
		client.PrependReactor("update", "components", func(action k8stesting.Action) (bool, runtime.Object, error) {
			attempts++
			if attempts == 1 {
				return true, nil, apierrors.NewConflict(schema.GroupResource{Group: "xmc.dev", Resource: "components"}, "comp-conflict", errors.New("optimistic lock conflict"))
			}
			return false, nil, nil
		})

		ctx, logs := recordLogs()
		updated, err := SyncComponentFinalizers(ctx, client, "default", orig, &ReconcileResult{AddFinalizer: true})
		if err != nil {
			t.Fatalf("SyncComponentFinalizers() unexpected error after conflict retry: %v", err)
		}
		if attempts != 2 {
			t.Errorf("expected 2 update attempts, got %d", attempts)
		}
		if !slices.Contains(updated.GetFinalizers(), FinalizerDeletionProtection) {
			t.Errorf("expected finalizer %q after retry, got %v", FinalizerDeletionProtection, updated.GetFinalizers())
		}
		if got := len(logs.recordsFor(attachFinalizerMessage)); got != 1 {
			t.Errorf("%q logged %d times across the conflict retry, want 1", attachFinalizerMessage, got)
		}
	})

	t.Run("returns Get error immediately when conflict refresh fails", func(t *testing.T) {
		orig := newTestComponent("comp-get-err", "default")
		client := dynamicfake.NewSimpleDynamicClient(newStatusApplierTestScheme(), orig)
		client.PrependReactor("update", "components", func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, apierrors.NewConflict(schema.GroupResource{Group: "xmc.dev", Resource: "components"}, "comp-get-err", errors.New("conflict"))
		})
		client.PrependReactor("get", "components", func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, errors.New("get failed during conflict recovery")
		})

		if _, err := SyncComponentFinalizers(ctx, client, "default", orig, &ReconcileResult{AddFinalizer: true}); err == nil {
			t.Fatal("expected error when Get fails during conflict retry, got nil")
		}
	})
}

func TestPersistReconcileResult(t *testing.T) {
	ctx := context.Background()
	enabled := componentsv1alpha1.ComponentState_COMPONENT_STATE_ENABLED
	errUpdate := errors.New("update failed")
	errPatch := errors.New("patch failed")
	tests := []struct {
		name        string
		nilItem     bool
		finalizers  []string
		res         *ReconcileResult
		updateErr   error
		patchErr    error
		wantErr     error
		wantActions []string
	}{
		{name: "nil result is a no-op", res: nil, wantActions: nil},
		{name: "nil item is rejected", nilItem: true, res: &ReconcileResult{State: enabled}, wantErr: errNilComponentItem, wantActions: nil},
		{name: "status only", res: &ReconcileResult{State: enabled}, wantActions: []string{"patch/status"}},
		{name: "finalizer is attached before status", res: &ReconcileResult{State: enabled, AddFinalizer: true}, wantActions: []string{"update/", "patch/status"}},
		{name: "released finalizer skips status", finalizers: []string{FinalizerDeletionProtection}, res: &ReconcileResult{RemoveFinalizer: true}, wantActions: []string{"update/"}},
		{name: "finalizer error skips status", res: &ReconcileResult{State: enabled, AddFinalizer: true}, updateErr: errUpdate, wantErr: errUpdate, wantActions: []string{"update/"}},
		{name: "status error is returned", res: &ReconcileResult{State: enabled}, patchErr: errPatch, wantErr: errPatch, wantActions: []string{"patch/status"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			item := newTestComponent("comp-persist", "xmc-ns", tt.finalizers...)
			original := item.DeepCopy()
			client := dynamicfake.NewSimpleDynamicClient(newStatusApplierTestScheme(), item.DeepCopy())
			if tt.updateErr != nil {
				client.PrependReactor("update", "components", func(k8stesting.Action) (bool, runtime.Object, error) {
					return true, nil, tt.updateErr
				})
			}
			client.PrependReactor("patch", "components", func(k8stesting.Action) (bool, runtime.Object, error) {
				if tt.patchErr != nil {
					return true, nil, tt.patchErr
				}
				persisted := item.DeepCopy()
				persisted.Object["status"] = map[string]any{"state": "ENABLED"}
				return true, persisted, nil
			})

			arg := item
			if tt.nilItem {
				arg = nil
			}
			err := PersistReconcileResult(ctx, client, "default", arg, tt.res)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("PersistReconcileResult() error = %v, want %v", err, tt.wantErr)
			}
			if diff := cmp.Diff(original.Object, item.Object); diff != "" {
				t.Errorf("PersistReconcileResult() mutated item (-want +got):\n%s", diff)
			}
			var gotActions []string
			for _, a := range client.Actions() {
				if a.GetVerb() == "update" || a.GetVerb() == "patch" {
					gotActions = append(gotActions, a.GetVerb()+"/"+a.GetSubresource())
				}
			}
			if diff := cmp.Diff(tt.wantActions, gotActions); diff != "" {
				t.Errorf("API actions mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
