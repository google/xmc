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
	"log/slog"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
)

// FieldManagerBridge is the Server-Side Apply fieldManager owner identifier for xmc-bridge status updates.
const FieldManagerBridge = "xmc-bridge"

// ComponentGVR identifies the xmc.dev/v1alpha1 Component custom resource in Kubernetes.
var ComponentGVR = schema.GroupVersionResource{
	Group:    "xmc.dev",
	Version:  "v1alpha1",
	Resource: "components",
}

// ComponentStatus defines the structured representation of .status in the Component CRD.
type ComponentStatus struct {
	ObservedGeneration int64                `json:"observedGeneration"`
	State              string               `json:"state"`
	Access             *ComponentAccess     `json:"access,omitempty"`
	Conditions         []ComponentCondition `json:"conditions,omitempty"`
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

// BuildStatus converts a ReconcileResult into a typed ComponentStatus matching the Component CRD status schema.
func BuildStatus(res *ReconcileResult) ComponentStatus {
	if res == nil {
		return ComponentStatus{}
	}
	status := ComponentStatus{
		ObservedGeneration: res.ObservedGeneration,
		State:              res.State.String(),
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
			ObservedGeneration: status.ObservedGeneration,
			State:              status.State,
		}
		fallbackMap, _ := runtime.DefaultUnstructuredConverter.ToUnstructured(&fallback)
		return fallbackMap
	}
	return m
}

// ApplyComponentStatusSSA serializes the ReconcileResult into the .status map of item and persists it
// via Kubernetes Server-Side Apply (SSA) on the /status subresource with fieldManager "xmc-bridge".
func ApplyComponentStatusSSA(ctx context.Context, client dynamic.Interface, defaultNamespace string, item *unstructured.Unstructured, res *ReconcileResult) error {
	if client == nil || item == nil || res == nil {
		return errors.New("dynamic client, item, and reconcile result must not be nil")
	}
	ns := resolveTargetNamespace(item, defaultNamespace)
	statusPatch := map[string]any{
		"apiVersion": ComponentGVR.Group + "/" + ComponentGVR.Version,
		"kind":       "Component",
		"metadata": map[string]any{
			"name":      item.GetName(),
			"namespace": ns,
		},
		"status": BuildStatusMap(res),
	}
	patchBytes, err := json.Marshal(statusPatch)
	if err != nil {
		return fmt.Errorf("failed to marshal status SSA patch: %w", err)
	}
	force := true
	updated, err := client.Resource(ComponentGVR).Namespace(ns).Patch(
		ctx,
		item.GetName(),
		types.ApplyPatchType,
		patchBytes,
		metav1.PatchOptions{
			FieldManager: FieldManagerBridge,
			Force:        &force,
		},
		"status",
	)
	if err != nil {
		return err
	}
	item.Object = updated.Object
	slog.DebugContext(ctx, "Updated Component status via Server-Side Apply", "name", item.GetName(), "state", res.State.String(), "observedGeneration", res.ObservedGeneration)
	return nil
}

// SyncComponentFinalizers attaches or detaches the deletion protection finalizer on item in Kubernetes.
func SyncComponentFinalizers(ctx context.Context, client dynamic.Interface, defaultNamespace string, item *unstructured.Unstructured, res *ReconcileResult) (*unstructured.Unstructured, error) {
	if res == nil || item == nil {
		return item, nil
	}
	hasFin := hasDeletionProtectionFinalizer(item.GetFinalizers())
	ns := resolveTargetNamespace(item, defaultNamespace)
	if res.AddFinalizer && !hasFin {
		item.SetFinalizers(append(item.GetFinalizers(), FinalizerDeletionProtection))
		slog.InfoContext(ctx, "Attaching deletion protection finalizer", "name", item.GetName())
		return client.Resource(ComponentGVR).Namespace(ns).Update(ctx, item, metav1.UpdateOptions{})
	}
	if res.RemoveFinalizer && hasFin {
		item.SetFinalizers(removeDeletionProtectionFinalizer(item.GetFinalizers()))
		slog.InfoContext(ctx, "Removing deletion protection finalizer", "name", item.GetName())
		return client.Resource(ComponentGVR).Namespace(ns).Update(ctx, item, metav1.UpdateOptions{})
	}
	return item, nil
}

func resolveTargetNamespace(item *unstructured.Unstructured, defaultNamespace string) string {
	if ns := item.GetNamespace(); ns != "" {
		return ns
	}
	return defaultNamespace
}

func hasDeletionProtectionFinalizer(finalizers []string) bool {
	for _, f := range finalizers {
		if f == FinalizerDeletionProtection {
			return true
		}
	}
	return false
}

func removeDeletionProtectionFinalizer(finalizers []string) []string {
	var updated []string
	for _, f := range finalizers {
		if f != FinalizerDeletionProtection {
			updated = append(updated, f)
		}
	}
	return updated
}
