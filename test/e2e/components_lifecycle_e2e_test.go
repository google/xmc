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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/pkg/components/bridge"
)

// TestComponentsLifecycle verifies Component lifecycle control-plane behavior on a KinD API server
// running the production Component CRD, reconciled by an in-process bridge controller manager per
// test namespace against an in-process CMS (MemoryStore). Components are 1P without a HelmManager,
// so no workloads are actuated: scenarios assert CMS lifecycle state, CR status conditions and
// finalizers. CRD schema and deletion ValidatingAdmissionPolicy admission are covered by the bridge
// envtest suite (pkg/components/bridge/components_admission_test.go). Each scenario uses its own
// namespace and component names because the CMS store is shared.
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
	for _, name := range []string{dep, app} {
		ready := waitForComponent(ctx, t, h, ns, name, "Ready", isExistingComponentReady)
		assertCMSComponent(ctx, t, h, ready, componentspb.ComponentState_COMPONENT_STATE_ENABLED)
		assertCleanupFinalizer(ctx, t, h, ns, name)
	}

	applyLifecycleComponent(ctx, t, h, ns, dep, lifecycleComponentSpec{enabled: false})
	assertDeactivationBlocked(ctx, t, h, ns, dep, app)

	applyLifecycleComponent(ctx, t, h, ns, app, lifecycleComponentSpec{enabled: false, dependencies: []string{dep}})
	assertComponentsDisabled(ctx, t, h, ns, app, dep)
}

// assertDeactivationBlocked waits for the bridge to report DeactivationBlocked on the disabled
// dependency dep, naming its active dependent. The workload keeps running and serving dependent, so
// Ready stays True (ComponentHealthy) while Stalled=True (DeactivationBlocked) surfaces the block;
// CMS keeps dep undeactivated and the finalizer stays.
func assertDeactivationBlocked(ctx context.Context, t *testing.T, h *kindClusterHarness, ns, dep, dependent string) {
	t.Helper()
	blocked := waitForComponent(ctx, t, h, ns, dep, "DeactivationBlocked", func(u *unstructured.Unstructured) bool {
		return isObservedComponent(u) && conditionField(u, bridge.ConditionTypeDeactivationBlocked, "status") == bridge.ConditionStatusTrue
	})
	if msg := conditionField(blocked, bridge.ConditionTypeDeactivationBlocked, "message"); !strings.Contains(msg, dependent) {
		t.Errorf("DeactivationBlocked message = %q, want it to name active dependent %q", msg, dependent)
	}
	// Ready=True with ComponentHealthy (not DeactivationIncomplete) shows the workload was not torn down.
	assertCondition(t, blocked, bridge.ConditionTypeReady, bridge.ConditionStatusTrue, bridge.ReasonComponentHealthy)
	assertCondition(t, blocked, bridge.ConditionTypeStalled, bridge.ConditionStatusTrue, bridge.ReasonDeactivationBlocked)
	// The deactivation has not completed, so the bridge keeps reporting PENDING_RECONCILIATION.
	assertCMSComponent(ctx, t, h, blocked, componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION)
	assertCleanupFinalizer(ctx, t, h, ns, dep)
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
		assertCleanupFinalizer(ctx, t, h, ns, name)
	}
}

// testFinalizerCleanup verifies the deletion path through the full bridge persistence flow: the
// bridge finalizer holds a deleted CR until CMS ExecuteDeletion runs, after which the finalizer is
// released and the CR disappears. The running bridge can release the finalizer before a GET
// observes the held deletion, so the hold is asserted from a watch opened before the DELETE.
// Admission of the DELETE itself is covered by the envtest suite.
func testFinalizerCleanup(ctx context.Context, t *testing.T, h *kindClusterHarness) {
	ns := createTestNamespace(ctx, t, h, "xmc-lc-finalizer")
	const name = "lc-finalizer"
	compRes := h.dynClient.Resource(componentGVR).Namespace(ns)
	applyLifecycleComponent(ctx, t, h, ns, name, lifecycleComponentSpec{enabled: true, deletionProtection: false})
	waitForComponent(ctx, t, h, ns, name, "Ready", isExistingComponentReady)
	assertCleanupFinalizer(ctx, t, h, ns, name)

	cmsComponentState(ctx, t, h, name) // The CMS record must exist for the NotFound check below to be meaningful.
	w, err := compRes.Watch(ctx, metav1.ListOptions{FieldSelector: fields.OneTermEqualSelector("metadata.name", name).String()})
	if err != nil {
		t.Fatalf("failed to watch %s/%s: %v", ns, name, err)
	}
	defer w.Stop()
	if err := compRes.Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
		t.Fatalf("DELETE with deletionProtection=false failed: %v", err)
	}
	assertFinalizerHeldDeletion(ctx, t, w, bridge.FinalizerCMSCleanup)
	if _, err := h.cmsServer.Store().GetComponent(ctx, name); status.Code(err) != codes.NotFound {
		t.Errorf("CMS GetComponent(%s) after CR deletion: error = %v, want NotFound (ExecuteDeletion)", name, err)
	}
}

// assertFinalizerHeldDeletion consumes w until the watched Component is DELETED, and fails unless
// an earlier MODIFIED event showed its deletion held by finalizer.
func assertFinalizerHeldDeletion(ctx context.Context, t *testing.T, w watch.Interface, finalizer string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, componentTransitionTimeout)
	defer cancel()
	held := false
	for {
		select {
		case <-ctx.Done():
			t.Fatalf("Component was not deleted within %v (deletion held by %s observed: %v)", componentTransitionTimeout, finalizer, held)
		case ev, ok := <-w.ResultChan():
			if !ok {
				t.Fatalf("watch closed before the Component was deleted (deletion held by %s observed: %v)", finalizer, held)
			}
			u, _ := ev.Object.(*unstructured.Unstructured)
			switch {
			case ev.Type == watch.Modified && u != nil && u.GetDeletionTimestamp() != nil && slices.Contains(u.GetFinalizers(), finalizer):
				held = true
			case ev.Type == watch.Deleted:
				if !held {
					t.Errorf("Component was deleted without a MODIFIED event holding its deletion with %s", finalizer)
				}
				return
			}
		}
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

func isComponentInState(want componentspb.ComponentState) func(*unstructured.Unstructured) bool {
	return func(u *unstructured.Unstructured) bool {
		if !isObservedComponent(u) {
			return false
		}
		state, _, _ := unstructured.NestedString(u.Object, "status", "state")
		return state == want.String()
	}
}

func getComponent(ctx context.Context, t *testing.T, compRes dynamic.ResourceInterface, name string) *unstructured.Unstructured {
	t.Helper()
	u, err := compRes.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("failed to get Component %s: %v", name, err)
	}
	return u
}

func assertCleanupFinalizer(ctx context.Context, t *testing.T, h *kindClusterHarness, ns, name string) {
	t.Helper()
	if u := getComponent(ctx, t, h.dynClient.Resource(componentGVR).Namespace(ns), name); !slices.Contains(u.GetFinalizers(), bridge.FinalizerCMSCleanup) {
		t.Errorf("Component %s/%s finalizers = %v, want %s", ns, name, u.GetFinalizers(), bridge.FinalizerCMSCleanup)
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
