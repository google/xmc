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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const controllerTestNamespace = "xmc"

// controllerTestComponent returns a Component in controllerTestNamespace with the given spec.
func controllerTestComponent(name string, spec map[string]any) *unstructured.Unstructured {
	u := newTestComponent(name, controllerTestNamespace)
	u.SetGeneration(1)
	u.Object["spec"] = spec
	return u
}

// newTestComponentController returns a ComponentController whose reader is a controller-runtime
// fake client and whose writer is a dynamic fake client, both holding objs. The writer applies
// status patches to its tracker, and fails them with patchErr if it is non-nil.
func newTestComponentController(t *testing.T, cms *mockCMS, patchErr error, objs ...*unstructured.Unstructured) (*ComponentController, *dynamicfake.FakeDynamicClient) {
	t.Helper()
	scheme := newStatusApplierTestScheme()
	builder := fake.NewClientBuilder().WithScheme(scheme)
	writerObjs := make([]runtime.Object, 0, len(objs))
	for _, obj := range objs {
		builder = builder.WithObjects(obj.DeepCopy())
		writerObjs = append(writerObjs, obj.DeepCopy())
	}
	writer := dynamicfake.NewSimpleDynamicClient(scheme, writerObjs...)
	writer.PrependReactor("patch", "components", func(action k8stesting.Action) (bool, runtime.Object, error) {
		if patchErr != nil {
			return true, nil, patchErr
		}
		return true, nil, applyStatusPatch(writer, action.(k8stesting.PatchAction))
	})
	r, err := NewReconciler(cms, nil, nil)
	if err != nil {
		t.Fatalf("NewReconciler() failed: %v", err)
	}
	opts := ComponentControllerOptions{ProbeInterval: time.Minute, ReconcileTimeout: 5 * time.Second}
	return NewComponentController(builder.Build(), writer, r, opts), writer
}

// applyStatusPatch stores the status of the server-side apply patch pa in the tracker of client.
func applyStatusPatch(client *dynamicfake.FakeDynamicClient, pa k8stesting.PatchAction) error {
	var patch map[string]any
	if err := json.Unmarshal(pa.GetPatch(), &patch); err != nil {
		return err
	}
	obj, err := client.Tracker().Get(ComponentGVR, pa.GetNamespace(), pa.GetName())
	if err != nil {
		return err
	}
	u := obj.(*unstructured.Unstructured).DeepCopy()
	u.Object["status"] = patch["status"]
	return client.Tracker().Update(ComponentGVR, u, pa.GetNamespace())
}

// persistedReadyReason returns the Ready condition reason that client holds for Component name.
func persistedReadyReason(t *testing.T, client *dynamicfake.FakeDynamicClient, name string) string {
	t.Helper()
	obj, err := client.Tracker().Get(ComponentGVR, controllerTestNamespace, name)
	if err != nil {
		t.Fatalf("tracker Get(%s) failed: %v", name, err)
	}
	ready := findCondition(ParseConditionsFromUnstructured(obj.(*unstructured.Unstructured).Object["status"]), ConditionTypeReady)
	if ready == nil {
		return ""
	}
	return ready.Reason
}

// writeActions summarizes the mutating actions of client as "verb" or "verb/subresource".
func writeActions(client *dynamicfake.FakeDynamicClient) []string {
	var got []string
	for _, a := range client.Actions() {
		if a.GetVerb() == "get" || a.GetVerb() == "list" {
			continue
		}
		verb := a.GetVerb()
		if a.GetSubresource() != "" {
			verb += "/" + a.GetSubresource()
		}
		got = append(got, verb)
	}
	return got
}

func TestComponentControllerReconcile(t *testing.T) {
	enabled := map[string]any{"enabled": true, "deletionProtection": false}
	deleting := controllerTestComponent("deleting", enabled)
	deleting.SetFinalizers([]string{FinalizerDeletionProtection})
	deleting.SetDeletionTimestamp(&metav1.Time{Time: time.Now()})

	tests := []struct {
		name        string
		objs        []*unstructured.Unstructured
		cms         *mockCMS
		patchErr    error
		target      string
		wantResult  reconcile.Result
		wantErr     bool
		wantActions []string
	}{
		{
			name:       "missing Component is ignored",
			cms:        &mockCMS{},
			target:     "gone",
			wantResult: reconcile.Result{},
		},
		{
			name:        "successful pass persists and requeues after the probe interval",
			objs:        []*unstructured.Unstructured{controllerTestComponent("app", enabled)},
			cms:         &mockCMS{},
			target:      "app",
			wantResult:  reconcile.Result{RequeueAfter: time.Minute},
			wantActions: []string{"update", "patch/status"},
		},
		{
			name:        "failed pass persists its status and returns the error for backoff",
			objs:        []*unstructured.Unstructured{controllerTestComponent("app", enabled)},
			cms:         &mockCMS{setErr: errors.New("cms unavailable")},
			target:      "app",
			wantResult:  reconcile.Result{},
			wantErr:     true,
			wantActions: []string{"update", "patch/status"},
		},
		{
			name:        "failed persistence returns the error for backoff",
			objs:        []*unstructured.Unstructured{controllerTestComponent("app", enabled)},
			cms:         &mockCMS{},
			patchErr:    errors.New("apiserver unavailable"),
			target:      "app",
			wantResult:  reconcile.Result{},
			wantErr:     true,
			wantActions: []string{"update", "patch/status"},
		},
		{
			name:        "released finalizer is not requeued",
			objs:        []*unstructured.Unstructured{deleting},
			cms:         &mockCMS{},
			target:      "deleting",
			wantResult:  reconcile.Result{},
			wantActions: []string{"update"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, writer := newTestComponentController(t, tt.cms, tt.patchErr, tt.objs...)
			req := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: controllerTestNamespace, Name: tt.target}}

			got, err := c.Reconcile(context.Background(), req)

			if (err != nil) != tt.wantErr {
				t.Fatalf("Reconcile() error = %v, wantErr %v", err, tt.wantErr)
			}
			if diff := cmp.Diff(tt.wantResult, got); diff != "" {
				t.Errorf("Reconcile() result mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantActions, writeActions(writer)); diff != "" {
				t.Errorf("writer actions mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestComponentControllerReconcileGatesOnNamespaceDependencies(t *testing.T) {
	app := func(spec map[string]any) *unstructured.Unstructured { return controllerTestComponent("app", spec) }
	tests := []struct {
		name       string
		objs       []*unstructured.Unstructured
		wantReason string
	}{
		{
			name:       "missing dependency gates activation",
			objs:       []*unstructured.Unstructured{app(map[string]any{"enabled": true, "dependencies": []any{"db"}})},
			wantReason: ReasonDependenciesNotReady,
		},
		{
			name: "disabled dependency gates activation",
			objs: []*unstructured.Unstructured{
				app(map[string]any{"enabled": true, "dependencies": []any{"db"}}),
				controllerTestComponent("db", map[string]any{"enabled": false}),
			},
			wantReason: ReasonDependenciesNotReady,
		},
		{
			name:       "no dependencies activates",
			objs:       []*unstructured.Unstructured{app(map[string]any{"enabled": true})},
			wantReason: ReasonComponentHealthy,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, writer := newTestComponentController(t, &mockCMS{}, nil, tt.objs...)
			req := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: controllerTestNamespace, Name: "app"}}

			if _, err := c.Reconcile(context.Background(), req); err != nil {
				t.Fatalf("Reconcile() unexpected error: %v", err)
			}

			if got := persistedReadyReason(t, writer, "app"); got != tt.wantReason {
				t.Errorf("persisted Ready reason = %q, want %q", got, tt.wantReason)
			}
		})
	}
}

func TestComponentControllerMapDependents(t *testing.T) {
	c, _ := newTestComponentController(t, &mockCMS{}, nil,
		controllerTestComponent("db", map[string]any{"enabled": true}),
		controllerTestComponent("app", map[string]any{"enabled": true, "dependencies": []any{"db"}}),
		controllerTestComponent("worker", map[string]any{"enabled": true, "dependencies": []any{"app", "db"}}),
		controllerTestComponent("ui", map[string]any{"enabled": true, "dependencies": []any{"app"}}),
	)

	var got []string
	for _, req := range c.mapDependents(context.Background(), controllerTestComponent("db", nil)) {
		got = append(got, req.Name)
	}
	slices.Sort(got)

	if diff := cmp.Diff([]string{"app", "worker"}, got); diff != "" {
		t.Errorf("mapDependents(db) mismatch (-want +got):\n%s", diff)
	}
}

func TestJoinPassErrors(t *testing.T) {
	passErr := errors.New("pass failed")
	persistErr := errors.New("persist failed")
	tests := []struct {
		name                string
		passErr, persistErr error
		wantIdentical       error
		wantIs              []error
	}{
		{name: "both nil"},
		{name: "pass error only is returned unwrapped", passErr: passErr, wantIdentical: passErr},
		{name: "persist error only is returned unwrapped", persistErr: persistErr, wantIdentical: persistErr},
		{name: "both errors are joined", passErr: passErr, persistErr: persistErr, wantIs: []error{passErr, persistErr}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := joinPassErrors(tt.passErr, tt.persistErr)

			if tt.wantIs == nil && got != tt.wantIdentical {
				t.Errorf("joinPassErrors() = %v, want exactly %v", got, tt.wantIdentical)
			}
			for _, want := range tt.wantIs {
				if !errors.Is(got, want) {
					t.Errorf("joinPassErrors() = %v, want it to wrap %v", got, want)
				}
			}
		})
	}
}

func TestDependencyUnreadyReasonTypedNil(t *testing.T) {
	if got := dependencyUnreadyReason((*unstructured.Unstructured)(nil)); got != "not a Component" {
		t.Errorf("dependencyUnreadyReason(typed nil) = %q, want %q", got, "not a Component")
	}
}

func TestDependencyReadinessChanged(t *testing.T) {
	ready := func(generation int64, ltt string) *unstructured.Unstructured {
		u := controllerTestComponent("db", map[string]any{"enabled": true})
		u.SetGeneration(generation)
		u.Object["status"] = map[string]any{"conditions": []any{map[string]any{
			"type": ConditionTypeReady, "status": "True", "observedGeneration": int64(1), "lastTransitionTime": ltt,
		}}}
		return u
	}
	notReady := controllerTestComponent("db", map[string]any{"enabled": true})
	tests := []struct {
		name     string
		old, new *unstructured.Unstructured
		want     bool
	}{
		{name: "condition timestamp refresh", old: ready(1, "2026-10-12T00:00:00Z"), new: ready(1, "2026-10-12T00:01:00Z"), want: false},
		{name: "becomes ready", old: notReady, new: ready(1, "2026-10-12T00:00:00Z"), want: true},
		{name: "becomes unready", old: ready(1, "2026-10-12T00:00:00Z"), new: notReady, want: true},
		{name: "generation change", old: ready(1, "2026-10-12T00:00:00Z"), new: ready(2, "2026-10-12T00:00:00Z"), want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := dependencyReadinessChanged().Update(event.UpdateEvent{ObjectOld: tt.old, ObjectNew: tt.new}); got != tt.want {
				t.Errorf("dependencyReadinessChanged().Update() = %v, want %v", got, tt.want)
			}
		})
	}
	if !dependencyReadinessChanged().Create(event.CreateEvent{Object: notReady}) || !dependencyReadinessChanged().Delete(event.DeleteEvent{Object: notReady}) {
		t.Error("dependencyReadinessChanged() must pass create and delete events")
	}
}

func TestDeletionStarted(t *testing.T) {
	live := controllerTestComponent("db", nil)
	deleting := live.DeepCopy()
	deleting.SetDeletionTimestamp(&metav1.Time{Time: time.Now()})
	tests := []struct {
		name     string
		old, new *unstructured.Unstructured
		want     bool
	}{
		{name: "deletion starts", old: live, new: deleting, want: true},
		{name: "already deleting", old: deleting, new: deleting, want: false},
		{name: "not deleting", old: live, new: live, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := deletionStarted().Update(event.UpdateEvent{ObjectOld: tt.old, ObjectNew: tt.new}); got != tt.want {
				t.Errorf("deletionStarted().Update() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestComponentRateLimiter(t *testing.T) {
	item := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: controllerTestNamespace, Name: "app"}}
	tests := []struct {
		name     string
		failures int
		rand     float64
		want     time.Duration
	}{
		{name: "first retry at the low jitter bound", failures: 1, rand: 0, want: 4500 * time.Millisecond},
		{name: "first retry without jitter", failures: 1, rand: 0.5, want: 5 * time.Second},
		{name: "third retry doubles twice", failures: 3, rand: 0.5, want: 20 * time.Second},
		{name: "jitter never exceeds the cap", failures: 20, rand: 0.999, want: 300 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			limiter := NewComponentRateLimiter().(*jitteredRateLimiter[reconcile.Request])
			limiter.randFloat = func() float64 { return tt.rand }
			var got time.Duration
			for range tt.failures {
				got = limiter.When(item)
			}
			if got != tt.want {
				t.Errorf("When() after %d failures = %v, want %v", tt.failures, got, tt.want)
			}
		})
	}

	t.Run("forget resets the backoff", func(t *testing.T) {
		limiter := NewComponentRateLimiter().(*jitteredRateLimiter[reconcile.Request])
		limiter.randFloat = func() float64 { return 0.5 }
		limiter.When(item)
		limiter.When(item)
		limiter.Forget(item)
		if got := limiter.When(item); got != 5*time.Second {
			t.Errorf("When() after Forget = %v, want 5s", got)
		}
	})
}
