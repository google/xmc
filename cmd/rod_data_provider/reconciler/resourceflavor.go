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

	ctrl "golang/sigs_k8s_io/controller_runtime/v/v0_11/controllerruntime"
	"golang/sigs_k8s_io/controller_runtime/v/v0_11/pkg/client/client"
	"golang/sigs_k8s_io/controller_runtime/v/v0_11/pkg/predicate/predicate"
	kueuev1beta1 "sigs.k8s.io/kueue/apis/kueue/v1beta1"
	"xmanager_cloud/cmd/rod_data_provider/cache"
)

// ResourceFlavorReconciler reconciles ResourceFlavor custom resources and updates the in-memory cache.
type ResourceFlavorReconciler struct {
	client.Client
	Cache *cache.ResourceCache[*kueuev1beta1.ResourceFlavor]
}

func (r *ResourceFlavorReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var rf kueuev1beta1.ResourceFlavor
	return reconcileCache(ctx, r.Client, req, &rf, func(o *kueuev1beta1.ResourceFlavor) {
		r.Cache.Set(req.Name, o.DeepCopy())
	}, func() {
		r.Cache.Delete(req.Name)
	})
}

func (r *ResourceFlavorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&kueuev1beta1.ResourceFlavor{}).
		WithEventFilter(predicate.ResourceVersionChangedPredicate{}).
		Complete(r)
}
