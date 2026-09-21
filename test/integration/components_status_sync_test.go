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

// This file asserts how the bridge persists Component status against a real API server: that it
// claims ownership of the status subresource under its own field manager, that repeated applies
// merge rather than accumulate, that a third-party controller's conditions survive, and that a
// malformed status payload is rejected by the schema instead of corrupting the resource.
package integration

import (
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/pkg/components/bridge"
	"xmanager_cloud/pkg/components/bridge/statussync"
	"xmanager_cloud/test/integration/componentenv"
)

// thirdPartyManager is a stand-in for a 3P controller that co-owns Component status.
const thirdPartyManager = "external-controller"

func TestApplyStatusClaimsStatusSubresource(t *testing.T) {
	t.Parallel()
	clients := newNamespace(t)
	ctx := t.Context()
	writer := statussync.NewWriter(clients.Dynamic, clients.Namespace)
	created := createComponent(t, clients, "claims-status-subresource")

	if err := writer.ApplyStatus(ctx, created, readyResult(bridge.ConditionStatusTrue, "Ready", "All ready")); err != nil {
		t.Fatalf("ApplyStatus() returned unexpected error: %v", err)
	}

	stored := getComponent(t, clients, created.GetName())
	if !ownsStatus(stored, statussync.FieldManager) {
		t.Errorf("managedFields = %v, want an entry for manager %q on the status subresource",
			stored.GetManagedFields(), statussync.FieldManager)
	}
	assertCondition(t, stored, bridge.ConditionTypeReady, bridge.ConditionStatusTrue)
}

func TestApplyStatusMergesRepeatedApplies(t *testing.T) {
	t.Parallel()
	clients := newNamespace(t)
	ctx := t.Context()
	writer := statussync.NewWriter(clients.Dynamic, clients.Namespace)
	created := createComponent(t, clients, "merges-repeated-applies")

	if err := writer.ApplyStatus(ctx, created, readyResult(bridge.ConditionStatusTrue, "Ready", "All ready")); err != nil {
		t.Fatalf("first ApplyStatus() returned unexpected error: %v", err)
	}

	// conditions is x-kubernetes-list-type: map keyed on type, so re-applying Ready must update
	// it in place while the newly declared Configured condition is appended. A non-merging apply
	// would instead duplicate Ready.
	second := readyResult(bridge.ConditionStatusFalse, "NotReady", "Degraded")
	second.Conditions = append(second.Conditions, bridge.Condition{
		Type:               "Configured",
		Status:             bridge.ConditionStatusTrue,
		Reason:             "Applied",
		Message:            "Configuration applied",
		ObservedGeneration: 1,
		LastTransitionTime: time.Now(),
	})
	if err := writer.ApplyStatus(ctx, getComponent(t, clients, created.GetName()), second); err != nil {
		t.Fatalf("second ApplyStatus() returned unexpected error: %v", err)
	}

	stored := getComponent(t, clients, created.GetName())
	if got, want := len(conditionsOf(t, stored)), 2; got != want {
		t.Errorf("len(status.conditions) = %d, want %d: %v", got, want, conditionsOf(t, stored))
	}
	assertCondition(t, stored, bridge.ConditionTypeReady, bridge.ConditionStatusFalse)
	assertCondition(t, stored, "Configured", bridge.ConditionStatusTrue)
}

func TestApplyStatusPreservesThirdPartyConditions(t *testing.T) {
	t.Parallel()
	clients := newNamespace(t)
	ctx := t.Context()
	created := createComponent(t, clients, "preserves-third-party-conditions")

	// A third-party controller claims a condition type the bridge never declares.
	thirdPartyPatch := []byte(`{"apiVersion":"xmc.dev/v1alpha1","kind":"Component","status":{"conditions":[` +
		`{"type":"ThirdPartyReady","status":"True","reason":"Test","message":"Test",` +
		`"observedGeneration":1,"lastTransitionTime":"2026-01-01T00:00:00Z"}]}}`)
	if _, err := clients.Admin.Patch(ctx, created.GetName(), types.ApplyPatchType, thirdPartyPatch,
		metav1.PatchOptions{FieldManager: thirdPartyManager}, "status"); err != nil {
		t.Fatalf("third-party Patch() returned unexpected error: %v", err)
	}

	writer := statussync.NewWriter(clients.Dynamic, clients.Namespace)
	if err := writer.ApplyStatus(ctx, created, readyResult(bridge.ConditionStatusTrue, "Ready", "All ready")); err != nil {
		t.Fatalf("ApplyStatus() returned unexpected error: %v", err)
	}

	// Force only overrides fields this manager actually declares. Because conditions is a keyed
	// map-list and the bridge never declares ThirdPartyReady, that entry must survive.
	stored := getComponent(t, clients, created.GetName())
	assertCondition(t, stored, "ThirdPartyReady", bridge.ConditionStatusTrue)
	assertCondition(t, stored, bridge.ConditionTypeReady, bridge.ConditionStatusTrue)
}

func TestApplyStatusRejectsMalformedStatus(t *testing.T) {
	t.Parallel()
	clients := newNamespace(t)
	writer := statussync.NewWriter(clients.Dynamic, clients.Namespace)
	created := createComponent(t, clients, "rejects-malformed-status")
	result := readyResult("Maybe", "Unknown", "Outside the True/False/Unknown enum")

	err := writer.ApplyStatus(t.Context(), created, result)

	if err == nil {
		t.Fatalf("ApplyStatus() succeeded, want rejection of the out-of-enum condition status")
	}
	// The caller must receive a wrapped error, never a panic, so that a bad status payload
	// degrades into a retryable reconcile failure rather than taking the controller down.
	if !strings.Contains(err.Error(), "server-side apply failed") {
		t.Errorf("ApplyStatus() error = %q, want it wrapped with %q", err.Error(), "server-side apply failed")
	}
	if !strings.Contains(err.Error(), "Unsupported value") {
		t.Errorf("ApplyStatus() error = %q, want a schema rejection", err.Error())
	}
	if conditions := conditionsOf(t, getComponent(t, clients, created.GetName())); len(conditions) != 0 {
		t.Errorf("status.conditions = %v, want the rejected apply to leave status untouched", conditions)
	}
}

func TestThirdPartyMalformedStatusIsRejected(t *testing.T) {
	// A third-party controller co-owning Component status must not be able to write a payload
	// that violates the CRD schema, because the bridge and the CLI both read these fields back
	// and would otherwise have to defend against arbitrary shapes.
	tests := []struct {
		name string
		// condition is the raw JSON object the third party attempts to apply.
		condition string
		// wantMessage is a substring of the expected schema rejection.
		wantMessage string
	}{
		{
			name:        "status outside the enum",
			condition:   `{"type":"Ready","status":"Maybe","reason":"R","message":"M","lastTransitionTime":"2026-01-01T00:00:00Z"}`,
			wantMessage: "Unsupported value",
		},
		{
			name:        "required reason is absent",
			condition:   `{"type":"Ready","status":"True","message":"M","lastTransitionTime":"2026-01-01T00:00:00Z"}`,
			wantMessage: "reason",
		},
		{
			name:        "message is not a string",
			condition:   `{"type":"Ready","status":"True","reason":"R","message":42,"lastTransitionTime":"2026-01-01T00:00:00Z"}`,
			wantMessage: "message",
		},
		{
			name:        "lastTransitionTime is not a timestamp",
			condition:   `{"type":"Ready","status":"True","reason":"R","message":"M","lastTransitionTime":"not-a-time"}`,
			wantMessage: "lastTransitionTime",
		},
		{
			name:        "observedGeneration is not an integer",
			condition:   `{"type":"Ready","status":"True","reason":"R","message":"M","observedGeneration":"one","lastTransitionTime":"2026-01-01T00:00:00Z"}`,
			wantMessage: "observedGeneration",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			clients := newNamespace(t)
			created := createComponent(t, clients, componentName(test.name))
			patch := []byte(`{"apiVersion":"xmc.dev/v1alpha1","kind":"Component","status":{"conditions":[` + test.condition + `]}}`)

			_, err := clients.Admin.Patch(t.Context(), created.GetName(), types.ApplyPatchType, patch,
				metav1.PatchOptions{FieldManager: thirdPartyManager}, "status")

			if err == nil {
				t.Fatalf("third-party Patch() succeeded, want rejection of the malformed payload")
			}
			if !strings.Contains(err.Error(), test.wantMessage) {
				t.Errorf("third-party Patch() error = %q, want it to mention %q", err.Error(), test.wantMessage)
			}
			if conditions := conditionsOf(t, getComponent(t, clients, created.GetName())); len(conditions) != 0 {
				t.Errorf("status.conditions = %v, want the rejected payload to leave status untouched", conditions)
			}
		})
	}
}

func TestSyncFinalizersAddsAndRemovesDeletionProtection(t *testing.T) {
	t.Parallel()
	clients := newNamespace(t)
	ctx := t.Context()
	writer := statussync.NewWriter(clients.Dynamic, clients.Namespace)
	created := createComponent(t, clients, "syncs-deletion-protection-finalizer")

	if _, err := writer.SyncFinalizers(ctx, created, &bridge.ReconcileResult{AddFinalizer: true}); err != nil {
		t.Fatalf("SyncFinalizers(add) returned unexpected error: %v", err)
	}
	if got := getComponent(t, clients, created.GetName()).GetFinalizers(); !containsFinalizer(got) {
		t.Fatalf("finalizers = %v, want them to contain %q", got, bridge.FinalizerDeletionProtection)
	}

	// With the finalizer attached the object must survive DELETE and only be marked for deletion.
	if err := clients.Admin.Delete(ctx, created.GetName(), metav1.DeleteOptions{}); err != nil {
		t.Fatalf("Delete() returned unexpected error: %v", err)
	}
	deleting := getComponent(t, clients, created.GetName())
	if deleting.GetDeletionTimestamp() == nil {
		t.Fatalf("deletionTimestamp is nil, want the finalizer to have blocked removal")
	}

	if _, err := writer.SyncFinalizers(ctx, deleting, &bridge.ReconcileResult{RemoveFinalizer: true}); err != nil {
		t.Fatalf("SyncFinalizers(remove) returned unexpected error: %v", err)
	}
	if _, err := clients.Admin.Get(ctx, created.GetName(), metav1.GetOptions{}); !isNotFound(err) {
		t.Errorf("Get() error = %v, want a not-found error once the finalizer is released", err)
	}
}

// readyResult builds a ReconcileResult carrying a single Ready condition.
func readyResult(status, reason, message string) *bridge.ReconcileResult {
	return &bridge.ReconcileResult{
		ObservedGeneration: 1,
		State:              componentspb.ComponentState_COMPONENT_STATE_ENABLED,
		Conditions: []bridge.Condition{{
			Type:               bridge.ConditionTypeReady,
			Status:             status,
			Reason:             reason,
			Message:            message,
			ObservedGeneration: 1,
			LastTransitionTime: time.Now(),
		}},
	}
}

// createComponent creates an unprotected Component so tests can delete it during cleanup.
func createComponent(t *testing.T, clients *componentenv.Clients, name string) *unstructured.Unstructured {
	t.Helper()
	created, err := clients.Admin.Create(t.Context(), componentenv.NewComponent(name, false), metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("Create(%q) returned unexpected error: %v", name, err)
	}
	return created
}

// getComponent reads a Component back from the API server.
func getComponent(t *testing.T, clients *componentenv.Clients, name string) *unstructured.Unstructured {
	t.Helper()
	stored, err := clients.Admin.Get(t.Context(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get(%q) returned unexpected error: %v", name, err)
	}
	return stored
}

// ownsStatus reports whether manager holds a managedFields entry on the status subresource.
func ownsStatus(component *unstructured.Unstructured, manager string) bool {
	for _, entry := range component.GetManagedFields() {
		if entry.Manager == manager && entry.Subresource == "status" {
			return true
		}
	}
	return false
}

// conditionsOf returns the Component's status conditions, or nil when status is unset.
func conditionsOf(t *testing.T, component *unstructured.Unstructured) []any {
	t.Helper()
	conditions, found, err := unstructured.NestedSlice(component.Object, "status", "conditions")
	if err != nil {
		t.Fatalf("NestedSlice(status.conditions) returned unexpected error: %v", err)
	}
	if !found {
		return nil
	}
	return conditions
}

// assertCondition fails the test unless the Component carries conditionType with wantStatus.
func assertCondition(t *testing.T, component *unstructured.Unstructured, conditionType, wantStatus string) {
	t.Helper()
	for _, raw := range conditionsOf(t, component) {
		condition, ok := raw.(map[string]any)
		if !ok || condition["type"] != conditionType {
			continue
		}
		if got := condition["status"]; got != wantStatus {
			t.Errorf("condition %q status = %v, want %v", conditionType, got, wantStatus)
		}
		return
	}
	t.Errorf("condition %q is absent from %v", conditionType, conditionsOf(t, component))
}

// containsFinalizer reports whether the deletion-protection finalizer is present.
func containsFinalizer(finalizers []string) bool {
	for _, finalizer := range finalizers {
		if finalizer == bridge.FinalizerDeletionProtection {
			return true
		}
	}
	return false
}

// isNotFound reports whether err is a Kubernetes not-found error.
func isNotFound(err error) bool {
	return err != nil && strings.Contains(err.Error(), "not found")
}
