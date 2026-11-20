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
	"xmanager_cloud/cmd/rod_data_provider/cache"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// PhysicalInventoryReconciler synchronizes GPU-allocatable nodes and GPU-consuming pods.
type PhysicalInventoryReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Cache  *cache.PhysicalInventoryCache
}

// +kubebuilder:rbac:groups="",resources=nodes;pods,verbs=get;list;watch

func (r *PhysicalInventoryReconciler) ReconcileNode(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var node corev1.Node
	if err := r.Get(ctx, req.NamespacedName, &node); err != nil {
		if client.IgnoreNotFound(err) == nil {
			r.Cache.Nodes.Delete(req.Name)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	gpuQty := node.Status.Capacity["nvidia.com/gpu"]
	if gpuQty.Sign() <= 0 {
		// Not a GPU node, ignore.
		r.Cache.Nodes.Delete(req.Name)
		return ctrl.Result{}, nil
	}

	r.Cache.Nodes.Set(req.Name, &node)
	return ctrl.Result{}, nil
}

func (r *PhysicalInventoryReconciler) ReconcilePod(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var pod corev1.Pod
	if err := r.Get(ctx, req.NamespacedName, &pod); err != nil {
		if client.IgnoreNotFound(err) == nil {
			r.Cache.Pods.Delete(req.String())
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if pod.Spec.NodeName == "" || pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
		// Unscheduled or terminal pods don't consume physical GPUs.
		r.Cache.Pods.Delete(req.String())
		return ctrl.Result{}, nil
	}

	// Calculate requested GPUs.
	var gpuCount int64
	for _, container := range pod.Spec.Containers {
		if qty, ok := container.Resources.Requests["nvidia.com/gpu"]; ok {
			if v, ok := qty.AsInt64(); ok {
				gpuCount += v
			}
		}
	}

	if gpuCount <= 0 {
		r.Cache.Pods.Delete(req.String())
		return ctrl.Result{}, nil
	}

	r.Cache.Pods.Set(req.String(), &pod)
	return ctrl.Result{}, nil
}
