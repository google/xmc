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
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"

	"xmanager_cloud/logging/logging"
	"xmanager_cloud/pkg/components/bridge"
)

// FieldManager is the Server-Side Apply field manager the bridge owns Component status with.
// Anything it applies under this name is reported in .metadata.managedFields, which is how
// ownership of individual status fields is arbitrated against third-party controllers.
const FieldManager = "xmc-bridge"

// Writer performs Kubernetes updates and Server-Side Apply status updates.
type Writer struct {
	client           dynamic.Interface
	defaultNamespace string
}

// NewWriter constructs a new Writer instance.
func NewWriter(client dynamic.Interface, defaultNamespace string) *Writer {
	return &Writer{
		client:           client,
		defaultNamespace: defaultNamespace,
	}
}

// ApplyStatus serializes the ReconcileResult into the .status map of item and persists it
// via Kubernetes Server-Side Apply (SSA) on the /status subresource with fieldManager "xmc-bridge".
func (w *Writer) ApplyStatus(ctx context.Context, item *unstructured.Unstructured, res *bridge.ReconcileResult) error {
	ns := item.GetNamespace()
	if ns == "" {
		ns = w.defaultNamespace
	}
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
	updated, err := w.client.Resource(ComponentGVR).Namespace(ns).Patch(
		ctx,
		item.GetName(),
		types.ApplyPatchType,
		patchBytes,
		metav1.PatchOptions{
			FieldManager: FieldManager,

			Force: &force,
		},
		"status",
	)
	if err != nil {
		return fmt.Errorf("server-side apply failed: %w", err)
	}
	item.Object = updated.Object
	logging.Debug(ctx, "Updated Component status via Server-Side Apply", "name", item.GetName(), "state", res.State.String(), "observedGeneration", res.ObservedGeneration)
	return nil
}

// SyncFinalizers attaches the deletion protection finalizer if res.AddFinalizer is true,
// or removes it if res.RemoveFinalizer is true, updating the custom resource in Kubernetes.
// If the Kubernetes API update fails, the error is returned so that the controller can retry
// on the next pass while ensuring the finalizer remains in place to prevent premature deletion.
func (w *Writer) SyncFinalizers(ctx context.Context, item *unstructured.Unstructured, res *bridge.ReconcileResult) (*unstructured.Unstructured, error) {
	if res == nil {
		return item, nil
	}
	hasFin := false
	for _, f := range item.GetFinalizers() {
		if f == bridge.FinalizerDeletionProtection {
			hasFin = true
			break
		}
	}
	ns := item.GetNamespace()
	if ns == "" {
		ns = w.defaultNamespace
	}
	if res.AddFinalizer && !hasFin {
		item.SetFinalizers(append(item.GetFinalizers(), bridge.FinalizerDeletionProtection))
		logging.Info(ctx, "Attaching deletion protection finalizer", "name", item.GetName())
		return w.client.Resource(ComponentGVR).Namespace(ns).Update(ctx, item, metav1.UpdateOptions{})
	}
	if res.RemoveFinalizer && hasFin {
		var updated []string
		for _, f := range item.GetFinalizers() {
			if f != bridge.FinalizerDeletionProtection {
				updated = append(updated, f)
			}
		}
		item.SetFinalizers(updated)
		logging.Info(ctx, "Removing deletion protection finalizer", "name", item.GetName())
		return w.client.Resource(ComponentGVR).Namespace(ns).Update(ctx, item, metav1.UpdateOptions{})
	}
	return item, nil
}
