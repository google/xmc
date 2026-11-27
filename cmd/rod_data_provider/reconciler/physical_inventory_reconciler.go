// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package reconciler

import (
	"context"
	"fmt"

	"xmanager_cloud/cmd/rod_data_provider/cache"
	"xmanager_cloud/internal/kubernetes/common/constants"

	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// gpuResource is the extended resource name tracked by the physical inventory.
const gpuResource = corev1.ResourceName(constants.NvidiaGPUResourceName)

// PhysicalInventoryReconciler synchronizes GPU-allocatable nodes and GPU-consuming pods.
type PhysicalInventoryReconciler struct {
	client.Client
	Cache *cache.PhysicalInventoryCache
}

// +kubebuilder:rbac:groups="",resources=nodes;pods,verbs=get;list;watch

// SetupWithManager registers the Node and Pod controllers, skipping no-op resync events.
func (r *PhysicalInventoryReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := ctrl.NewControllerManagedBy(mgr).
		Named("physical-inventory-node").
		For(&corev1.Node{}).
		WithEventFilter(predicate.ResourceVersionChangedPredicate{}).
		Complete(reconcile.Func(r.ReconcileNode)); err != nil {
		return fmt.Errorf("failed to setup node controller: %w", err)
	}
	if err := ctrl.NewControllerManagedBy(mgr).
		Named("physical-inventory-pod").
		For(&corev1.Pod{}).
		WithEventFilter(predicate.ResourceVersionChangedPredicate{}).
		Complete(reconcile.Func(r.ReconcilePod)); err != nil {
		return fmt.Errorf("failed to setup pod controller: %w", err)
	}
	return nil
}

// ReconcileNode caches nodes with GPU capacity and evicts all others.
func (r *PhysicalInventoryReconciler) ReconcileNode(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var node corev1.Node
	if err := r.Get(ctx, req.NamespacedName, &node); err != nil {
		if client.IgnoreNotFound(err) == nil {
			r.Cache.Nodes.Delete(req.Name)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	gpuQty := node.Status.Capacity[gpuResource]
	if gpuQty.Sign() <= 0 {
		r.Cache.Nodes.Delete(req.Name)
		return ctrl.Result{}, nil
	}

	r.Cache.Nodes.Set(req.Name, &node)
	return ctrl.Result{}, nil
}

// ReconcilePod caches scheduled, non-terminal pods that request GPUs and evicts all others.
func (r *PhysicalInventoryReconciler) ReconcilePod(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var pod corev1.Pod
	if err := r.Get(ctx, req.NamespacedName, &pod); err != nil {
		if client.IgnoreNotFound(err) == nil {
			r.Cache.Pods.Delete(req.String())
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if !isActiveScheduledPod(&pod) || podRequestedGPUs(&pod) <= 0 {
		r.Cache.Pods.Delete(req.String())
		return ctrl.Result{}, nil
	}

	r.Cache.Pods.Set(req.String(), &pod)
	return ctrl.Result{}, nil
}

// isActiveScheduledPod reports whether the pod is bound to a node and not terminal.
func isActiveScheduledPod(pod *corev1.Pod) bool {
	if pod.Spec.NodeName == "" {
		return false
	}
	return pod.Status.Phase != corev1.PodSucceeded && pod.Status.Phase != corev1.PodFailed
}

// podRequestedGPUs returns the pod's effective GPU request following Kubernetes semantics:
// max(sum(containers) + sum(sidecars), max over init containers of (init + sidecars started before it)).
func podRequestedGPUs(pod *corev1.Pod) int64 {
	var regular int64
	for i := range pod.Spec.Containers {
		regular += containerGPUs(&pod.Spec.Containers[i])
	}

	var sidecars, initPeak int64
	for i := range pod.Spec.InitContainers {
		c := &pod.Spec.InitContainers[i]
		if c.RestartPolicy != nil && *c.RestartPolicy == corev1.ContainerRestartPolicyAlways {
			sidecars += containerGPUs(c)
			initPeak = max(initPeak, sidecars)
			continue
		}
		initPeak = max(initPeak, sidecars+containerGPUs(c))
	}
	return max(regular+sidecars, initPeak)
}

// containerGPUs returns a container's GPU request, falling back to its limit when unset.
func containerGPUs(c *corev1.Container) int64 {
	if qty, ok := c.Resources.Requests[gpuResource]; ok {
		return qty.Value()
	}
	if qty, ok := c.Resources.Limits[gpuResource]; ok {
		return qty.Value()
	}
	return 0
}
