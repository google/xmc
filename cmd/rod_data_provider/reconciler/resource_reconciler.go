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

	"k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	kueuev1beta1 "sigs.k8s.io/kueue/apis/kueue/v1beta1"
	"xmanager_cloud/cmd/rod_data_provider/cache"
)

// ResourceReconciler manages reconciliation of Kueue CRDs into a ResourceCache.
type ResourceReconciler[T client.Object] struct {
	client.Client
	Cache   *cache.ResourceCache[T]
	NewObj  func() T
	KeyFunc func(ctrl.Request) string
}

func (r *ResourceReconciler[T]) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	obj := r.NewObj()
	key := r.KeyFunc(req)
	if err := r.Get(ctx, req.NamespacedName, obj); err != nil {
		if errors.IsNotFound(err) {
			r.Cache.Delete(key)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	r.Cache.Set(key, obj.DeepCopyObject().(T))
	return ctrl.Result{}, nil
}

func (r *ResourceReconciler[T]) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(r.NewObj()).
		WithEventFilter(predicate.ResourceVersionChangedPredicate{}).
		Complete(r)
}

type ClusterQueueReconciler = ResourceReconciler[*kueuev1beta1.ClusterQueue]
type ResourceFlavorReconciler = ResourceReconciler[*kueuev1beta1.ResourceFlavor]
type LocalQueueReconciler = ResourceReconciler[*kueuev1beta1.LocalQueue]

func NewClusterQueueReconciler(c client.Client, cache *cache.ResourceCache[*kueuev1beta1.ClusterQueue]) *ClusterQueueReconciler {
	return &ClusterQueueReconciler{
		Client:  c,
		Cache:   cache,
		NewObj:  func() *kueuev1beta1.ClusterQueue { return &kueuev1beta1.ClusterQueue{} },
		KeyFunc: func(req ctrl.Request) string { return req.Name },
	}
}

func NewResourceFlavorReconciler(c client.Client, cache *cache.ResourceCache[*kueuev1beta1.ResourceFlavor]) *ResourceFlavorReconciler {
	return &ResourceFlavorReconciler{
		Client:  c,
		Cache:   cache,
		NewObj:  func() *kueuev1beta1.ResourceFlavor { return &kueuev1beta1.ResourceFlavor{} },
		KeyFunc: func(req ctrl.Request) string { return req.Name },
	}
}

func NewLocalQueueReconciler(c client.Client, cache *cache.ResourceCache[*kueuev1beta1.LocalQueue]) *LocalQueueReconciler {
	return &LocalQueueReconciler{
		Client:  c,
		Cache:   cache,
		NewObj:  func() *kueuev1beta1.LocalQueue { return &kueuev1beta1.LocalQueue{} },
		KeyFunc: func(req ctrl.Request) string { return req.NamespacedName.String() },
	}
}
