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
	"fmt"
	"slices"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"xmanager_cloud/internal/logging"
)

// FieldManagerBridge is the Server-Side Apply fieldManager owner identifier for xmc-bridge status updates.
const FieldManagerBridge = "xmc-bridge"

// ComponentGVR identifies the xmc.dev/v1alpha1 Component custom resource in Kubernetes.
var ComponentGVR = schema.GroupVersionResource{
	Group:    "xmc.dev",
	Version:  "v1alpha1",
	Resource: "components",
}

// errNilComponentItem is returned by PersistReconcileResult for a nil Component item.
var errNilComponentItem = errors.New("component item must not be nil")

// ComponentStatus defines the structured representation of .status in the Component CRD.
type ComponentStatus struct {
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// ComponentGeneration is the CMS component generation the status describes. CMS orders intents
	// by custom resource ownership, so it is independent of ObservedGeneration. Omitted while unknown.
	ComponentGeneration int64                `json:"componentGeneration,omitempty"`
	State               string               `json:"state"`
	Access              *ComponentAccess     `json:"access,omitempty"`
	Conditions          []ComponentCondition `json:"conditions,omitempty"`
}

// ComponentAccess defines the access configuration in .status.access.
type ComponentAccess struct {
	EnvVars map[string]string `json:"envVars,omitempty"`
}

// ComponentCondition defines a condition entry matching standard RFC3339 timestamps.
type ComponentCondition struct {
	Type               string `json:"type"`
	Status             string `json:"status"`
	ObservedGeneration int64  `json:"observedGeneration"`
	Reason             string `json:"reason"`
	Message            string `json:"message"`
	LastTransitionTime string `json:"lastTransitionTime"`
}

// BuildStatus converts a ReconcileResult into a typed ComponentStatus matching the Component CRD status schema,
// including observedGeneration (the CR generation) and componentGeneration (the CMS generation, omitted while 0).
func BuildStatus(res *ReconcileResult) ComponentStatus {
	if res == nil {
		return ComponentStatus{}
	}
	status := ComponentStatus{
		ObservedGeneration:  res.ObservedGeneration,
		ComponentGeneration: res.ComponentGeneration,
		State:               res.State.String(),
	}
	if res.Access != nil && len(res.Access.EnvVars) > 0 {
		status.Access = &ComponentAccess{EnvVars: res.Access.EnvVars}
	}
	if len(res.Conditions) > 0 {
		status.Conditions = make([]ComponentCondition, 0, len(res.Conditions))
		for _, c := range res.Conditions {
			status.Conditions = append(status.Conditions, ComponentCondition{
				Type:               c.Type,
				Status:             c.Status,
				ObservedGeneration: c.ObservedGeneration,
				Reason:             c.Reason,
				Message:            c.Message,
				LastTransitionTime: c.LastTransitionTime.Format(time.RFC3339),
			})
		}
	}
	return status
}

// BuildStatusMap converts a ReconcileResult into an unstructured map matching the Component CRD status schema.
func BuildStatusMap(res *ReconcileResult) map[string]any {
	status := BuildStatus(res)
	m, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&status)
	if err != nil {
		fallback := ComponentStatus{
			ObservedGeneration:  status.ObservedGeneration,
			ComponentGeneration: status.ComponentGeneration,
			State:               status.State,
		}
		fallbackMap, _ := runtime.DefaultUnstructuredConverter.ToUnstructured(&fallback)
		return fallbackMap
	}
	return m
}

// ApplyComponentStatusSSA persists res as the .status of item through Server-Side Apply on the
// status subresource, with field manager FieldManagerBridge. item is not modified.
func ApplyComponentStatusSSA(ctx context.Context, c client.Client, item *unstructured.Unstructured, res *ReconcileResult) error {
	if c == nil || item == nil || res == nil {
		return errors.New("client, item, and reconcile result must not be nil")
	}
	applied := &unstructured.Unstructured{Object: map[string]any{"status": BuildStatusMap(res)}}
	applied.SetGroupVersionKind(componentGVK)
	applied.SetName(item.GetName())
	applied.SetNamespace(item.GetNamespace())
	if uid := item.GetUID(); uid != "" {
		applied.SetUID(uid)
	}
	if err := c.Status().Apply(ctx, client.ApplyConfigurationFromUnstructured(applied), client.FieldOwner(FieldManagerBridge), client.ForceOwnership); err != nil {
		return err
	}
	logging.Debug(ctx, "Updated Component status via Server-Side Apply", "name", item.GetName(), "state", res.State.String(), "observedGeneration", res.ObservedGeneration)
	return nil
}

// AddCleanupFinalizer persists FinalizerCMSCleanup on item and returns the stored object; item is
// not modified. The patch fails, rather than overwriting finalizers that other writers changed,
// unless the stored finalizers still equal those of item. An item that already carries the cleanup
// finalizer, or its legacy name, is returned as is without a write.
func AddCleanupFinalizer(ctx context.Context, c client.Client, item *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	if item == nil {
		return nil, errNilComponentItem
	}
	if HasCleanupFinalizer(item) {
		return item, nil
	}
	logging.Info(ctx, "Attaching CMS cleanup finalizer", "name", item.GetName())
	return patchFinalizers(ctx, c, item, append(slices.Clone(item.GetFinalizers()), FinalizerCMSCleanup))
}

// ReleaseCleanupFinalizer removes FinalizerCMSCleanup, and its legacy name, from item, guarded like
// AddCleanupFinalizer. A Component that no longer exists, or whose item carries neither name,
// counts as released.
func ReleaseCleanupFinalizer(ctx context.Context, c client.Client, item *unstructured.Unstructured) error {
	if item == nil {
		return errNilComponentItem
	}
	if !HasCleanupFinalizer(item) {
		return nil
	}
	logging.Info(ctx, "Releasing CMS cleanup finalizer", "name", item.GetName())
	kept := slices.DeleteFunc(slices.Clone(item.GetFinalizers()), isCleanupFinalizer)
	_, err := patchFinalizers(ctx, c, item, kept)
	return client.IgnoreNotFound(err)
}

// HasCleanupFinalizer reports whether item carries FinalizerCMSCleanup or its legacy name.
func HasCleanupFinalizer(item *unstructured.Unstructured) bool {
	return slices.ContainsFunc(item.GetFinalizers(), isCleanupFinalizer)
}

func isCleanupFinalizer(f string) bool {
	return f == FinalizerCMSCleanup || f == legacyFinalizerCMSCleanup
}

// jsonPatchOp is one RFC 6902 JSON patch operation.
type jsonPatchOp struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value any    `json:"value"`
}

// patchFinalizers sets the finalizers of item to want with a JSON patch whose test ops require the
// stored UID (when present on item) and finalizers to equal those of item. A test against null passes
// only while no finalizers are stored.
func patchFinalizers(ctx context.Context, c client.Client, item *unstructured.Unstructured, want []string) (*unstructured.Unstructured, error) {
	if c == nil {
		return nil, errors.New("client must not be nil")
	}
	var stored any
	// The apiserver drops an empty finalizers list from stored objects, so "none stored" must be
	// tested as null: a test against [] would never pass.
	if f := item.GetFinalizers(); len(f) > 0 {
		stored = f
	}
	ops := make([]jsonPatchOp, 0, 3)
	if uid := item.GetUID(); uid != "" {
		ops = append(ops, jsonPatchOp{Op: "test", Path: "/metadata/uid", Value: string(uid)})
	}
	ops = append(ops,
		jsonPatchOp{Op: "test", Path: "/metadata/finalizers", Value: stored},
		jsonPatchOp{Op: "add", Path: "/metadata/finalizers", Value: want},
	)
	patch, err := json.Marshal(ops)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal finalizer patch: %w", err)
	}
	updated := item.DeepCopy()
	if err := c.Patch(ctx, updated, client.RawPatch(types.JSONPatchType, patch)); err != nil {
		return nil, err
	}
	return updated, nil
}

// PersistReconcileResult persists res for item: it releases the cleanup finalizer when res asks for
// it, and otherwise applies .status with ApplyComponentStatusSSA. It is a no-op for a nil or stale-incarnation
// res and rejects a nil item. item is not modified.
func PersistReconcileResult(ctx context.Context, c client.Client, item *unstructured.Unstructured, res *ReconcileResult) error {
	if res == nil || res.StaleIncarnation {
		return nil
	}
	if item == nil {
		return errNilComponentItem
	}
	if res.RemoveFinalizer {
		if err := ReleaseCleanupFinalizer(ctx, c, item); err != nil {
			return fmt.Errorf("failed to release finalizer: %w", err)
		}
		recordDeletionConflictEvent(ctx, c, item, res)
		return nil
	}
	// A Component that no longer exists has no status left to persist. The only 409 expected with
	// ForceOwnership is the metadata.uid precondition (set when item has a UID): the Component was
	// replaced by a newer incarnation, which has no status of ours to persist either.
	err := ApplyComponentStatusSSA(ctx, c, item, res)
	if err == nil || apierrors.IsNotFound(err) || (apierrors.IsConflict(err) && item.GetUID() != "") {
		return nil
	}
	return fmt.Errorf("failed to apply status: %w", err)
}

// ReasonStaleDeletion is the Kubernetes Warning Event reason emitted when CMS deletion is skipped
// because the deleted CR is not the active CMS owner (DD §5).
const ReasonStaleDeletion = "StaleDeletion"

// deletionEventSuffix is appended to the CR name to form the Event GenerateName prefix.
const deletionEventSuffix = ".deletion-skipped-"

// maxEventGenerateNameLen bounds the GenerateName prefix to the API server's generated-name base
// length (63 minus the 5-character random suffix), well within the 253-character name limit.
const maxEventGenerateNameLen = 58

// deletionEventGenerateName returns a GenerateName prefix for name that stays within
// maxEventGenerateNameLen by truncating name while keeping deletionEventSuffix. Trailing '-' and
// '.' left by truncation are trimmed so the prefix remains a valid DNS-1123 subdomain.
func deletionEventGenerateName(name string) string {
	maxNameLen := maxEventGenerateNameLen - len(deletionEventSuffix)
	if len(name) > maxNameLen {
		name = strings.TrimRight(name[:maxNameLen], "-.")
	}
	return name + deletionEventSuffix
}

func recordDeletionConflictEvent(ctx context.Context, c client.Client, item *unstructured.Unstructured, res *ReconcileResult) {
	if c == nil || item == nil || res == nil || res.ConflictMessage == "" {
		return
	}
	now := metav1.Now()
	ev := &corev1.Event{
		ObjectMeta:     metav1.ObjectMeta{GenerateName: deletionEventGenerateName(item.GetName()), Namespace: item.GetNamespace()},
		InvolvedObject: corev1.ObjectReference{APIVersion: item.GetAPIVersion(), Kind: item.GetKind(), Namespace: item.GetNamespace(), Name: item.GetName(), UID: item.GetUID()},
		Reason:         ReasonStaleDeletion,
		Message:        res.ConflictMessage,
		Source:         corev1.EventSource{Component: FieldManagerBridge},
		FirstTimestamp: now,
		LastTimestamp:  now,
		Count:          1,
		Type:           corev1.EventTypeWarning,
	}
	if err := c.Create(ctx, ev); err != nil {
		logging.Warn(ctx, "Failed to record deletion conflict warning event", "namespace", item.GetNamespace(), "name", item.GetName(), "error", err)
	}
}
