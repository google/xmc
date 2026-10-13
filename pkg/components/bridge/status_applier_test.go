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

	"github.com/google/go-cmp/cmp"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

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

// componentWrites records the writes made through a fake Component client.
type componentWrites struct {
	// calls lists each write as "patch", "update" or "apply/<subresource>", in order.
	calls []string
	// apply holds the options of the last subresource apply.
	apply client.SubResourceApplyOptions
}

// newFakeComponentClient returns a controller-runtime fake client holding objs, with the Component
// status subresource, so reads observe its own writes as they would through the API server. Each
// write is recorded in the returned componentWrites and fails with failures[call] if set.
func newFakeComponentClient(t *testing.T, failures map[string]error, objs ...*unstructured.Unstructured) (client.Client, *componentWrites) {
	t.Helper()
	w := &componentWrites{}
	record := func(call string) error {
		w.calls = append(w.calls, call)
		return failures[call]
	}
	b := fake.NewClientBuilder().WithScheme(newStatusApplierTestScheme()).WithStatusSubresource(newComponentObject())
	for _, obj := range objs {
		b = b.WithObjects(obj.DeepCopy())
	}
	return b.WithInterceptorFuncs(interceptor.Funcs{
		Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
			if err := record("patch"); err != nil {
				return err
			}
			return c.Patch(ctx, obj, patch, opts...)
		},
		Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
			if err := record("update"); err != nil {
				return err
			}
			return c.Update(ctx, obj, opts...)
		},
		SubResourceApply: func(ctx context.Context, c client.Client, sub string, obj runtime.ApplyConfiguration, opts ...client.SubResourceApplyOption) error {
			if err := record("apply/" + sub); err != nil {
				return err
			}
			w.apply = client.SubResourceApplyOptions{}
			w.apply.ApplyOpts(opts)
			return c.SubResource(sub).Apply(ctx, obj, opts...)
		},
	}).Build(), w
}

// getFakeComponent returns the stored Component namespace/name of c.
func getFakeComponent(t *testing.T, c client.Client, namespace, name string) *unstructured.Unstructured {
	t.Helper()
	u := newComponentObject()
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: namespace, Name: name}, u); err != nil {
		t.Fatalf("Get(%s/%s) failed: %v", namespace, name, err)
	}
	return u
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
			ObservedGeneration: 5,
			State:              componentsv1alpha1.ComponentState_COMPONENT_STATE_ENABLED,
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
			ObservedGeneration: 5,
			State:              "COMPONENT_STATE_ENABLED",
			Access:             &ComponentAccess{EnvVars: map[string]string{"DB_HOST": "clickhouse.xmc.svc"}},
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
		if gotMap["state"] != "COMPONENT_STATE_ENABLED" || gotMap["observedGeneration"] != int64(5) {
			t.Errorf("unexpected BuildStatusMap output: %v", gotMap)
		}
	})
}

func TestApplyComponentStatusSSA(t *testing.T) {
	ctx := context.Background()
	res := &ReconcileResult{
		ObservedGeneration: 3,
		State:              componentsv1alpha1.ComponentState_COMPONENT_STATE_ENABLED,
	}

	t.Run("rejects nil client, item, or result", func(t *testing.T) {
		item := newTestComponent("comp-1", "xmc-ns")
		c, _ := newFakeComponentClient(t, nil, item)
		if err := ApplyComponentStatusSSA(ctx, nil, item, res); err == nil {
			t.Error("expected error for nil client, got nil")
		}
		if err := ApplyComponentStatusSSA(ctx, c, nil, res); err == nil {
			t.Error("expected error for nil item, got nil")
		}
		if err := ApplyComponentStatusSSA(ctx, c, item, nil); err == nil {
			t.Error("expected error for nil result, got nil")
		}
	})

	t.Run("applies the status subresource as xmc-bridge with forced ownership", func(t *testing.T) {
		item := newTestComponent("comp-1", "xmc-ns")
		c, writes := newFakeComponentClient(t, nil, item)
		if err := ApplyComponentStatusSSA(ctx, c, item, res); err != nil {
			t.Fatalf("ApplyComponentStatusSSA() unexpected error: %v", err)
		}
		if diff := cmp.Diff([]string{"apply/status"}, writes.calls); diff != "" {
			t.Errorf("writes mismatch (-want +got):\n%s", diff)
		}
		if writes.apply.FieldManager != FieldManagerBridge || !ptr.Deref(writes.apply.Force, false) {
			t.Errorf("apply options = {FieldManager: %q, Force: %v}, want {%q, true}", writes.apply.FieldManager, writes.apply.Force, FieldManagerBridge)
		}
		state, _, _ := unstructured.NestedString(getFakeComponent(t, c, "xmc-ns", "comp-1").Object, "status", "state")
		if state != "COMPONENT_STATE_ENABLED" {
			t.Errorf("stored status.state = %q, want COMPONENT_STATE_ENABLED", state)
		}
	})

	t.Run("propagates apply error", func(t *testing.T) {
		item := newTestComponent("comp-1", "xmc-ns")
		c, _ := newFakeComponentClient(t, map[string]error{"apply/status": errors.New("apiserver unavailable")}, item)
		if err := ApplyComponentStatusSSA(ctx, c, item, res); err == nil {
			t.Fatal("expected error from ApplyComponentStatusSSA, got nil")
		}
	})
}

func TestCleanupFinalizer(t *testing.T) {
	ctx := context.Background()

	t.Run("attaches and releases the finalizer and keeps the others", func(t *testing.T) {
		orig := newTestComponent("comp-fin", "default", "other-fin")
		c, _ := newFakeComponentClient(t, nil, orig)
		stored := getFakeComponent(t, c, "default", "comp-fin")

		attached, err := AddCleanupFinalizer(ctx, c, stored)
		if err != nil {
			t.Fatalf("AddCleanupFinalizer() failed: %v", err)
		}
		if HasCleanupFinalizer(stored) {
			t.Errorf("AddCleanupFinalizer() mutated its input: finalizers %v", stored.GetFinalizers())
		}
		if diff := cmp.Diff([]string{"other-fin", FinalizerCMSCleanup}, attached.GetFinalizers()); diff != "" {
			t.Errorf("returned finalizers mismatch (-want +got):\n%s", diff)
		}
		if err := ReleaseCleanupFinalizer(ctx, c, attached); err != nil {
			t.Fatalf("ReleaseCleanupFinalizer() failed: %v", err)
		}
		if diff := cmp.Diff([]string{"other-fin"}, getFakeComponent(t, c, "default", "comp-fin").GetFinalizers()); diff != "" {
			t.Errorf("stored finalizers mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("attaches to a Component without finalizers", func(t *testing.T) {
		c, _ := newFakeComponentClient(t, nil, newTestComponent("comp-new", "default"))
		if _, err := AddCleanupFinalizer(ctx, c, getFakeComponent(t, c, "default", "comp-new")); err != nil {
			t.Fatalf("AddCleanupFinalizer() failed: %v", err)
		}
		if diff := cmp.Diff([]string{FinalizerCMSCleanup}, getFakeComponent(t, c, "default", "comp-new").GetFinalizers()); diff != "" {
			t.Errorf("stored finalizers mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("stale read fails instead of overwriting finalizers", func(t *testing.T) {
		c, _ := newFakeComponentClient(t, nil, newTestComponent("comp-stale", "default", "other-fin"))
		stale := newTestComponent("comp-stale", "default")
		if _, err := AddCleanupFinalizer(ctx, c, stale); err == nil {
			t.Fatal("AddCleanupFinalizer() with a stale read succeeded, want the test op to fail")
		}
		if diff := cmp.Diff([]string{"other-fin"}, getFakeComponent(t, c, "default", "comp-stale").GetFinalizers()); diff != "" {
			t.Errorf("stored finalizers mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("accepts the legacy finalizer and release strips both names", func(t *testing.T) {
		c, _ := newFakeComponentClient(t, nil, newTestComponent("comp-legacy", "default", legacyFinalizerCMSCleanup, "other-fin", FinalizerCMSCleanup))
		stored := getFakeComponent(t, c, "default", "comp-legacy")
		if !HasCleanupFinalizer(newTestComponent("legacy-only", "default", legacyFinalizerCMSCleanup)) {
			t.Errorf("HasCleanupFinalizer() = false for a Component with only %q, want true", legacyFinalizerCMSCleanup)
		}
		if err := ReleaseCleanupFinalizer(ctx, c, stored); err != nil {
			t.Fatalf("ReleaseCleanupFinalizer() failed: %v", err)
		}
		if diff := cmp.Diff([]string{"other-fin"}, getFakeComponent(t, c, "default", "comp-legacy").GetFinalizers()); diff != "" {
			t.Errorf("stored finalizers mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("release of a missing Component succeeds", func(t *testing.T) {
		c, _ := newFakeComponentClient(t, nil)
		if err := ReleaseCleanupFinalizer(ctx, c, newTestComponent("gone", "default", FinalizerCMSCleanup)); err != nil {
			t.Errorf("ReleaseCleanupFinalizer() error = %v, want nil", err)
		}
	})

	t.Run("attach to a missing Component reports NotFound", func(t *testing.T) {
		c, _ := newFakeComponentClient(t, nil)
		if _, err := AddCleanupFinalizer(ctx, c, newTestComponent("gone", "default")); !apierrors.IsNotFound(err) {
			t.Errorf("AddCleanupFinalizer() error = %v, want NotFound", err)
		}
	})

	t.Run("attach and release skip the write when nothing changes", func(t *testing.T) {
		for _, tc := range []struct {
			name       string
			finalizers []string
			call       func(client.Client, *unstructured.Unstructured) error
		}{
			{"attach with the finalizer", []string{FinalizerCMSCleanup}, func(c client.Client, u *unstructured.Unstructured) error {
				_, err := AddCleanupFinalizer(ctx, c, u)
				return err
			}},
			{"attach with the legacy finalizer", []string{legacyFinalizerCMSCleanup}, func(c client.Client, u *unstructured.Unstructured) error {
				_, err := AddCleanupFinalizer(ctx, c, u)
				return err
			}},
			{"release without the finalizer", []string{"other-fin"}, func(c client.Client, u *unstructured.Unstructured) error {
				return ReleaseCleanupFinalizer(ctx, c, u)
			}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				c, writes := newFakeComponentClient(t, nil, newTestComponent("comp-noop", "default", tc.finalizers...))
				if err := tc.call(c, getFakeComponent(t, c, "default", "comp-noop")); err != nil {
					t.Fatalf("call failed: %v", err)
				}
				if len(writes.calls) != 0 {
					t.Errorf("writes = %v, want none", writes.calls)
				}
				if diff := cmp.Diff(tc.finalizers, getFakeComponent(t, c, "default", "comp-noop").GetFinalizers()); diff != "" {
					t.Errorf("stored finalizers mismatch (-want +got):\n%s", diff)
				}
			})
		}
	})
}

func TestPersistReconcileResult(t *testing.T) {
	ctx := context.Background()
	enabled := componentsv1alpha1.ComponentState_COMPONENT_STATE_ENABLED
	errPatch := errors.New("patch failed")
	errApply := errors.New("apply failed")
	tests := []struct {
		name       string
		nilItem    bool
		res        *ReconcileResult
		failures   map[string]error
		wantErr    error
		wantWrites []string
	}{
		{name: "nil result is a no-op", res: nil},
		{name: "nil item is rejected", nilItem: true, res: &ReconcileResult{State: enabled}, wantErr: errNilComponentItem},
		{name: "status only", res: &ReconcileResult{State: enabled}, wantWrites: []string{"apply/status"}},
		{name: "released finalizer skips status", res: &ReconcileResult{RemoveFinalizer: true}, wantWrites: []string{"patch"}},
		{name: "release error is returned", res: &ReconcileResult{RemoveFinalizer: true}, failures: map[string]error{"patch": errPatch}, wantErr: errPatch, wantWrites: []string{"patch"}},
		{name: "status error is returned", res: &ReconcileResult{State: enabled}, failures: map[string]error{"apply/status": errApply}, wantErr: errApply, wantWrites: []string{"apply/status"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			item := newTestComponent("comp-persist", "xmc-ns", FinalizerCMSCleanup)
			c, writes := newFakeComponentClient(t, tt.failures, item)

			arg := getFakeComponent(t, c, "xmc-ns", "comp-persist")
			if tt.nilItem {
				arg = nil
			}
			before := arg.DeepCopy()
			err := PersistReconcileResult(ctx, c, arg, tt.res)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("PersistReconcileResult() error = %v, want %v", err, tt.wantErr)
			}
			if diff := cmp.Diff(before, arg); diff != "" {
				t.Errorf("PersistReconcileResult() mutated item (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantWrites, writes.calls); diff != "" {
				t.Errorf("writes mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
