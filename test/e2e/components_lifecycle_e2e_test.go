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

package e2e

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/pkg/components/bridge"
	"xmanager_cloud/pkg/components/store"
)

// lifecycleWaitTimeout bounds each lifecycle transition. It exceeds the bridge's 30s
// DeactivationBlocked recheck interval, which gates how soon a blocked deactivation is retried.
const lifecycleWaitTimeout = 90 * time.Second

// TestComponentsLifecycle verifies Component lifecycle control-plane behavior on a KinD API server
// running the production Component CRD, reconciled by the in-process bridge against an in-process
// CMS (MemoryStore). Components are 1P without a HelmManager, so no workloads are actuated:
// scenarios assert CMS lifecycle state, CR status conditions and finalizers. CRD schema and
// deletion ValidatingAdmissionPolicy admission are covered by the bridge envtest suite
// (pkg/components/bridge/components_admission_test.go). Each scenario uses its own namespace and
// component names because the CMS store is shared.
func TestComponentsLifecycle(t *testing.T) {
	h := setupKindClusterHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	t.Run("BlockedDeactivation", func(t *testing.T) { testBlockedDeactivation(ctx, t, h) })
	t.Run("FinalizerCleanup", func(t *testing.T) { testFinalizerCleanup(ctx, t, h) })
	t.Run("RecreatedComponent", func(t *testing.T) { testRecreatedComponent(ctx, t, h) })
	t.Run("StaleDeletion", func(t *testing.T) { testStaleDeletion(ctx, t, h) })
}

// testBlockedDeactivation disables a dependency while an enabled component depends on it: CMS
// rejects the deactivation and the bridge keeps the dependency running with DeactivationBlocked.
// Disabling the dependent first lets both reach COMPONENT_STATE_DISABLED.
func testBlockedDeactivation(ctx context.Context, t *testing.T, h *kindClusterHarness) {
	ns := createTestNamespace(ctx, t, h, "xmc-lc-blocked")
	const dep, app = "lc-blocked-db", "lc-blocked-app"
	applyLifecycleComponent(ctx, t, h, ns, dep, lifecycleComponentSpec{enabled: true})
	applyLifecycleComponent(ctx, t, h, ns, app, lifecycleComponentSpec{enabled: true, dependencies: []string{dep}})
	for _, name := range []string{dep, app} {
		ready := waitForComponent(ctx, t, h, ns, name, "Ready", isExistingComponentReady)
		assertCMSComponent(ctx, t, h, ready, componentspb.ComponentState_COMPONENT_STATE_ENABLED)
		assertDeletionProtectionFinalizer(ctx, t, h, ns, name)
	}

	applyLifecycleComponent(ctx, t, h, ns, dep, lifecycleComponentSpec{enabled: false})
	assertDeactivationBlocked(ctx, t, h, ns, dep, app)

	applyLifecycleComponent(ctx, t, h, ns, app, lifecycleComponentSpec{enabled: false, dependencies: []string{dep}})
	assertComponentsDisabled(ctx, t, h, ns, app, dep)
}

// assertDeactivationBlocked waits for the bridge to report DeactivationBlocked on the disabled
// dependency dep, naming its active dependent, while CMS keeps dep undeactivated and the finalizer
// stays.
func assertDeactivationBlocked(ctx context.Context, t *testing.T, h *kindClusterHarness, ns, dep, dependent string) {
	t.Helper()
	blocked := waitForComponent(ctx, t, h, ns, dep, "DeactivationBlocked", func(u *unstructured.Unstructured) bool {
		return isObservedComponent(u) && conditionField(u, bridge.ConditionTypeDeactivationBlocked, "status") == bridge.ConditionStatusTrue
	})
	if msg := conditionField(blocked, bridge.ConditionTypeDeactivationBlocked, "message"); !strings.Contains(msg, dependent) {
		t.Errorf("DeactivationBlocked message = %q, want it to name active dependent %q", msg, dependent)
	}
	if reason := conditionField(blocked, bridge.ConditionTypeReady, "reason"); reason != bridge.ReasonDeactivationBlocked {
		t.Errorf("blocked %s Ready reason = %q, want %q", dep, reason, bridge.ReasonDeactivationBlocked)
	}
	// The bridge reports a blocked deactivation as gated, i.e. PENDING_RECONCILIATION.
	assertCMSComponent(ctx, t, h, blocked, componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION)
	assertDeletionProtectionFinalizer(ctx, t, h, ns, dep)
}

// assertComponentsDisabled waits until every named Component reports COMPONENT_STATE_DISABLED with
// DeactivationBlocked cleared, and verifies the CMS record and the retained finalizer.
func assertComponentsDisabled(ctx context.Context, t *testing.T, h *kindClusterHarness, ns string, names ...string) {
	t.Helper()
	for _, name := range names {
		disabled := waitForComponent(ctx, t, h, ns, name, "DISABLED", isComponentInState(componentspb.ComponentState_COMPONENT_STATE_DISABLED))
		if got := conditionField(disabled, bridge.ConditionTypeDeactivationBlocked, "status"); got != "" {
			t.Errorf("disabled %s still reports DeactivationBlocked=%q, want condition cleared", name, got)
		}
		assertCMSComponent(ctx, t, h, disabled, componentspb.ComponentState_COMPONENT_STATE_DISABLED)
		assertDeletionProtectionFinalizer(ctx, t, h, ns, name)
	}
}

// assertCMSComponent verifies that the CMS record of the Component u is in wantState, records u at
// its current generation as the intent source, and has the generation and ComponentId reported in
// status.componentGeneration and status.componentId. It returns the record.
func assertCMSComponent(ctx context.Context, t *testing.T, h *kindClusterHarness, u *unstructured.Unstructured, wantState componentspb.ComponentState) *store.ComponentRecord {
	t.Helper()
	rec, err := h.cmsServer.Store().GetComponent(ctx, u.GetName())
	if err != nil {
		t.Fatalf("CMS GetComponent(%s) failed: %v", u.GetName(), err)
	}
	componentGen, _, _ := unstructured.NestedInt64(u.Object, "status", "componentGeneration")
	if rec.State != wantState || rec.Generation != componentGen {
		t.Errorf("%s CMS state = %v, generation = %d; want %v, status.componentGeneration %d", u.GetName(), rec.State, rec.Generation, wantState, componentGen)
	}
	if componentID, _, _ := unstructured.NestedString(u.Object, "status", "componentId"); componentID != strconv.FormatInt(rec.ComponentID, 10) {
		t.Errorf("%s status.componentId = %q, want CMS ComponentId %d", u.GetName(), componentID, rec.ComponentID)
	}
	if src := rec.Source; src == nil || src.UID != string(u.GetUID()) || src.Generation != u.GetGeneration() {
		t.Errorf("%s CMS source = %+v, want UID %q at generation %d", u.GetName(), rec.Source, u.GetUID(), u.GetGeneration())
	}
	return rec
}

// testRecreatedComponent removes a Component CR at generation 3 without a CMS teardown (finalizer
// stripped) and recreates it with the same spec: the new CR at generation 1 adopts the CMS record
// at generation 3 under the same ComponentId without StaleIntent, and its next edit is applied.
func testRecreatedComponent(ctx context.Context, t *testing.T, h *kindClusterHarness) {
	ns := createTestNamespace(ctx, t, h, "xmc-lc-recreate")
	const name = "lc-recreate"
	var original *unstructured.Unstructured
	for _, version := range []string{"1.0.0", "1.0.1", "1.0.2"} {
		original = applyReadyLifecycleComponent(ctx, t, h, ns, name, version)
	}
	assertComponentGenerations(t, original, 3, 3)
	before := assertCMSComponent(ctx, t, h, original, componentspb.ComponentState_COMPONENT_STATE_ENABLED)
	removeComponentWithoutTeardown(ctx, t, h, ns, name)
	// Creation timestamps have one-second granularity: a later second orders the new CR after the
	// old one regardless of their UIDs.
	time.Sleep(time.Until(original.GetCreationTimestamp().Add(1100 * time.Millisecond)))
	recreated := applyReadyLifecycleComponent(ctx, t, h, ns, name, "1.0.2")
	if recreated.GetUID() == original.GetUID() {
		t.Fatalf("recreated %s/%s has the original UID %q", ns, name, recreated.GetUID())
	}
	assertComponentGenerations(t, recreated, 1, 3)
	if adopted := assertCMSComponent(ctx, t, h, recreated, componentspb.ComponentState_COMPONENT_STATE_ENABLED); adopted.ComponentID != before.ComponentID {
		t.Errorf("recreated CMS ComponentID = %d, want %d", adopted.ComponentID, before.ComponentID)
	}
	edited := applyReadyLifecycleComponent(ctx, t, h, ns, name, "1.1.0")
	assertComponentGenerations(t, edited, 2, 4)
	assertCMSComponent(ctx, t, h, edited, componentspb.ComponentState_COMPONENT_STATE_ENABLED)
}

// applyReadyLifecycleComponent applies an enabled Component at version and waits until it is Ready.
func applyReadyLifecycleComponent(ctx context.Context, t *testing.T, h *kindClusterHarness, ns, name, version string) *unstructured.Unstructured {
	t.Helper()
	applyLifecycleComponent(ctx, t, h, ns, name, lifecycleComponentSpec{enabled: true, version: version})
	return waitForComponent(ctx, t, h, ns, name, "Ready at version "+version, isExistingComponentReady)
}

// assertComponentGenerations verifies the CR generation (also status.observedGeneration) and
// status.componentGeneration of u.
func assertComponentGenerations(t *testing.T, u *unstructured.Unstructured, wantCRGen, wantComponentGen int64) {
	t.Helper()
	observedGen, _, _ := unstructured.NestedInt64(u.Object, "status", "observedGeneration")
	componentGen, _, _ := unstructured.NestedInt64(u.Object, "status", "componentGeneration")
	if u.GetGeneration() != wantCRGen || observedGen != wantCRGen || componentGen != wantComponentGen {
		t.Errorf("%s generation = %d, observedGeneration = %d, componentGeneration = %d; want %d, %d, %d", u.GetName(), u.GetGeneration(), observedGen, componentGen, wantCRGen, wantCRGen, wantComponentGen)
	}
}

// removeComponentWithoutTeardown strips the bridge finalizer and deletes the Component, so it
// disappears without a reconcile pass and its CMS record stays.
func removeComponentWithoutTeardown(ctx context.Context, t *testing.T, h *kindClusterHarness, ns, name string) {
	t.Helper()
	compRes := h.dynClient.Resource(componentGVR).Namespace(ns)
	u := getComponent(ctx, t, compRes, name)
	u.SetFinalizers(nil)
	if _, err := compRes.Update(ctx, u, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("failed to strip finalizers from %s/%s: %v", ns, name, err)
	}
	if err := compRes.Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
		t.Fatalf("failed to delete %s/%s: %v", ns, name, err)
	}
	if _, err := compRes.Get(ctx, name, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("Get(%s/%s) after delete: error = %v, want NotFound", ns, name, err)
	}
}

// testFinalizerCleanup verifies the deletion path through the full bridge persistence flow: the
// bridge finalizer holds a deleted CR until CMS ExecuteDeletion runs, after which the finalizer is
// released and the CR disappears. Admission of the DELETE itself is covered by the envtest suite.
func testFinalizerCleanup(ctx context.Context, t *testing.T, h *kindClusterHarness) {
	ns := createTestNamespace(ctx, t, h, "xmc-lc-finalizer")
	const name = "lc-finalizer"
	compRes := h.dynClient.Resource(componentGVR).Namespace(ns)
	applyLifecycleComponent(ctx, t, h, ns, name, lifecycleComponentSpec{enabled: true, deletionProtection: false})
	waitForComponent(ctx, t, h, ns, name, "Ready", isExistingComponentReady)
	assertDeletionProtectionFinalizer(ctx, t, h, ns, name)

	cmsComponentState(ctx, t, h, name) // The CMS record must exist for the NotFound check below to be meaningful.
	if err := compRes.Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
		t.Fatalf("DELETE with deletionProtection=false failed: %v", err)
	}
	if u := getComponent(ctx, t, compRes, name); u.GetDeletionTimestamp() == nil || !slices.Contains(u.GetFinalizers(), bridge.FinalizerDeletionProtection) {
		t.Fatalf("deleted %s/%s: deletionTimestamp=%v finalizers=%v, want deletion held by %s", ns, name, u.GetDeletionTimestamp(), u.GetFinalizers(), bridge.FinalizerDeletionProtection)
	}
	waitForComponent(ctx, t, h, ns, name, "gone", func(u *unstructured.Unstructured) bool { return u == nil })
	if _, err := h.cmsServer.Store().GetComponent(ctx, name); status.Code(err) != codes.NotFound {
		t.Errorf("CMS GetComponent(%s) after CR deletion: error = %v, want NotFound (ExecuteDeletion)", name, err)
	}
}

// testStaleDeletion deletes a Component CR whose CMS record a newer source object has adopted:
// the bridge releases the finalizer so the CR disappears, while the CMS record stays active and
// owned by the newer source.
func testStaleDeletion(ctx context.Context, t *testing.T, h *kindClusterHarness) {
	ns := createTestNamespace(ctx, t, h, "xmc-lc-stale-delete")
	const name = "lc-stale-delete"
	compRes := h.dynClient.Resource(componentGVR).Namespace(ns)
	applyLifecycleComponent(ctx, t, h, ns, name, lifecycleComponentSpec{enabled: true})
	ready := waitForComponent(ctx, t, h, ns, name, "Ready", isExistingComponentReady)
	rec := assertCMSComponent(ctx, t, h, ready, componentspb.ComponentState_COMPONENT_STATE_ENABLED)
	newer := store.IntentSource{UID: "lc-stale-delete-newer-owner", CreateTime: ready.GetCreationTimestamp().Add(time.Hour), Generation: 1}
	if _, err := h.cmsServer.Store().SetSourcedComponentIntent(ctx, name, rec.Spec, rec.Labels, newer); err != nil {
		t.Fatalf("CMS SetSourcedComponentIntent(%s, newer source) failed: %v", name, err)
	}

	if err := compRes.Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
		t.Fatalf("DELETE %s/%s failed: %v", ns, name, err)
	}
	waitForComponent(ctx, t, h, ns, name, "gone", func(u *unstructured.Unstructured) bool { return u == nil })
	kept, err := h.cmsServer.Store().GetComponent(ctx, name)
	if err != nil {
		t.Fatalf("CMS GetComponent(%s) after stale CR deletion failed: %v, want the record kept", name, err)
	}
	if kept.Source == nil || kept.Source.UID != newer.UID || kept.ComponentID != rec.ComponentID {
		t.Errorf("CMS record after stale CR deletion: ComponentID = %d, source = %+v; want ComponentID %d owned by %q", kept.ComponentID, kept.Source, rec.ComponentID, newer.UID)
	}
	assertSingleStaleDeletionEvent(ctx, t, h, ns, ready.GetUID())
}

// assertSingleStaleDeletionEvent waits for the Warning Event that the bridge emits on a stale
// deletion and checks that exactly one core/v1 Event with reason StaleDeletion names the custom
// resource uid (the recorder aggregates repeats into that one Event).
func assertSingleStaleDeletionEvent(ctx context.Context, t *testing.T, h *kindClusterHarness, ns string, uid types.UID) {
	t.Helper()
	selector := fmt.Sprintf("involvedObject.uid=%s,reason=%s", uid, bridge.ReasonStaleDeletion)
	var events []corev1.Event
	waitUntil(ctx, 30*time.Second, 500*time.Millisecond, func() bool {
		list, err := h.clientset.CoreV1().Events(ns).List(ctx, metav1.ListOptions{FieldSelector: selector})
		if err == nil {
			events = list.Items
		}
		return len(events) > 0
	})
	if len(events) != 1 || events[0].Type != corev1.EventTypeWarning {
		t.Errorf("Events with %s = %+v, want exactly one Warning Event", selector, events)
	}
}

type lifecycleComponentSpec struct {
	enabled, deletionProtection bool
	dependencies                []string
	// version is spec.version, "1.0.0" if empty.
	version string
}

// applyLifecycleComponent server-side applies the full spec of a 1P Component, so fields omitted
// from spec are removed from the CR.
func applyLifecycleComponent(ctx context.Context, t *testing.T, h *kindClusterHarness, ns, name string, spec lifecycleComponentSpec) {
	t.Helper()
	specMap := map[string]any{
		"componentType":      "COMPONENT_TYPE_CUSTOM_1P_COMPONENT",
		"enabled":            spec.enabled,
		"deletionProtection": spec.deletionProtection,
		"version":            cmp.Or(spec.version, "1.0.0"),
	}
	if len(spec.dependencies) > 0 {
		specMap["dependencies"] = spec.dependencies
	}
	applyUnstructuredObject(ctx, t, h, map[string]any{
		"apiVersion": "xmc.dev/v1alpha1", "kind": "Component", "metadata": map[string]any{"name": name, "namespace": ns}, "spec": specMap,
	})
}

// waitForComponent runs bridge reconcile passes over ns until cond holds for the named Component,
// described by desc in failures. cond receives nil once the Component no longer exists. Reconcile
// pass errors do not stop the wait, because cond judges the persisted CR state; they are logged
// whenever they change and reported on timeout.
func waitForComponent(ctx context.Context, t *testing.T, h *kindClusterHarness, ns, name, desc string, cond func(*unstructured.Unstructured) bool) *unstructured.Unstructured {
	t.Helper()
	var last *unstructured.Unstructured
	var lastErr error
	lastReconcileMsg := ""
	ok := waitUntil(ctx, lifecycleWaitTimeout, 500*time.Millisecond, func() bool {
		reconcileMsg := ""
		if err := h.reconcileNamespaceComponents(ctx, ns); err != nil {
			lastErr = err
			reconcileMsg = err.Error()
		}
		if reconcileMsg != lastReconcileMsg {
			t.Logf("reconcile pass over %s while waiting for %s/%s to be %s: error = %q", ns, ns, name, desc, reconcileMsg)
			lastReconcileMsg = reconcileMsg
		}
		u, err := h.dynClient.Resource(componentGVR).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
		switch {
		case apierrors.IsNotFound(err):
			last = nil
		case err != nil:
			lastErr = err
			return false
		default:
			last = u
		}
		return cond(last)
	})
	if !ok {
		t.Fatalf("timed out waiting for Component %s/%s to be %s (last err: %v); last object: %v", ns, name, desc, lastErr, last)
	}
	return last
}

func isObservedComponent(u *unstructured.Unstructured) bool {
	if u == nil {
		return false
	}
	obsGen, _, _ := unstructured.NestedInt64(u.Object, "status", "observedGeneration")
	return obsGen == u.GetGeneration()
}

func isExistingComponentReady(u *unstructured.Unstructured) bool {
	return u != nil && isComponentReady(u)
}

func isComponentInState(want componentspb.ComponentState) func(*unstructured.Unstructured) bool {
	return func(u *unstructured.Unstructured) bool {
		if !isObservedComponent(u) {
			return false
		}
		state, _, _ := unstructured.NestedString(u.Object, "status", "state")
		return state == want.String()
	}
}

// conditionField returns field of the status condition condType, or "" if u is nil or the condition
// is absent.
func conditionField(u *unstructured.Unstructured, condType, field string) string {
	if u == nil {
		return ""
	}
	conds, _, _ := unstructured.NestedSlice(u.Object, "status", "conditions")
	for _, raw := range conds {
		if m, ok := raw.(map[string]any); ok && m["type"] == condType {
			v, _ := m[field].(string)
			return v
		}
	}
	return ""
}

func getComponent(ctx context.Context, t *testing.T, compRes dynamic.ResourceInterface, name string) *unstructured.Unstructured {
	t.Helper()
	u, err := compRes.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("failed to get Component %s: %v", name, err)
	}
	return u
}

func assertDeletionProtectionFinalizer(ctx context.Context, t *testing.T, h *kindClusterHarness, ns, name string) {
	t.Helper()
	if u := getComponent(ctx, t, h.dynClient.Resource(componentGVR).Namespace(ns), name); !slices.Contains(u.GetFinalizers(), bridge.FinalizerDeletionProtection) {
		t.Errorf("Component %s/%s finalizers = %v, want %s", ns, name, u.GetFinalizers(), bridge.FinalizerDeletionProtection)
	}
}

func cmsComponentState(ctx context.Context, t *testing.T, h *kindClusterHarness, name string) componentspb.ComponentState {
	t.Helper()
	rec, err := h.cmsServer.Store().GetComponent(ctx, name)
	if err != nil {
		t.Fatalf("CMS GetComponent(%s) failed: %v", name, err)
	}
	return rec.State
}
