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
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/pkg/components/bridge"
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
}

// testBlockedDeactivation disables a dependency while an enabled component depends on it: CMS
// rejects the deactivation and the bridge keeps the dependency running with DeactivationBlocked.
// Disabling the dependent first lets both reach COMPONENT_STATE_DISABLED.
func testBlockedDeactivation(ctx context.Context, t *testing.T, h *kindClusterHarness) {
	ns := createTestNamespace(ctx, t, h, "xmc-lc-blocked")
	const dep, app = "lc-blocked-db", "lc-blocked-app"
	applyLifecycleComponent(ctx, t, h, ns, dep, lifecycleComponentSpec{enabled: true})
	applyLifecycleComponent(ctx, t, h, ns, app, lifecycleComponentSpec{enabled: true, dependencies: []string{dep}})
	waitForComponent(ctx, t, h, ns, app, "Ready", isExistingComponentReady)
	for _, name := range []string{dep, app} {
		assertDeletionProtectionFinalizer(ctx, t, h, ns, name)
	}

	applyLifecycleComponent(ctx, t, h, ns, dep, lifecycleComponentSpec{enabled: false})
	assertDeactivationBlocked(ctx, t, h, ns, dep, app)

	applyLifecycleComponent(ctx, t, h, ns, app, lifecycleComponentSpec{enabled: false, dependencies: []string{dep}})
	assertComponentsDisabled(ctx, t, h, ns, app, dep)
}

// assertDeactivationBlocked waits for the bridge to report DeactivationBlocked on the disabled
// dependency dep, naming its active dependent, while CMS keeps dep active and the finalizer stays.
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
	// Every bridge pass re-applies the intent, which resets an active component to
	// PENDING_RECONCILIATION in CMS, so either active state is valid here.
	activeStates := []componentspb.ComponentState{
		componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
		componentspb.ComponentState_COMPONENT_STATE_ENABLED,
	}
	if state := cmsComponentState(ctx, t, h, dep); !slices.Contains(activeStates, state) {
		t.Errorf("blocked %s CMS state = %v, want one of %v", dep, state, activeStates)
	}
	assertDeletionProtectionFinalizer(ctx, t, h, ns, dep)
}

// assertComponentsDisabled waits until every named Component reports COMPONENT_STATE_DISABLED with
// DeactivationBlocked cleared, and verifies the CMS state and the retained finalizer.
func assertComponentsDisabled(ctx context.Context, t *testing.T, h *kindClusterHarness, ns string, names ...string) {
	t.Helper()
	for _, name := range names {
		disabled := waitForComponent(ctx, t, h, ns, name, "DISABLED", isComponentInState(componentspb.ComponentState_COMPONENT_STATE_DISABLED))
		if got := conditionField(disabled, bridge.ConditionTypeDeactivationBlocked, "status"); got != "" {
			t.Errorf("disabled %s still reports DeactivationBlocked=%q, want condition cleared", name, got)
		}
		if state := cmsComponentState(ctx, t, h, name); state != componentspb.ComponentState_COMPONENT_STATE_DISABLED {
			t.Errorf("%s CMS state = %v, want COMPONENT_STATE_DISABLED", name, state)
		}
		assertDeletionProtectionFinalizer(ctx, t, h, ns, name)
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

type lifecycleComponentSpec struct {
	enabled, deletionProtection bool
	dependencies                []string
}

// applyLifecycleComponent server-side applies the full spec of a 1P Component, so fields omitted
// from spec are removed from the CR.
func applyLifecycleComponent(ctx context.Context, t *testing.T, h *kindClusterHarness, ns, name string, spec lifecycleComponentSpec) {
	t.Helper()
	specMap := map[string]any{
		"componentType":      "COMPONENT_TYPE_CUSTOM_1P_COMPONENT",
		"enabled":            spec.enabled,
		"deletionProtection": spec.deletionProtection,
		"version":            "1.0.0",
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
