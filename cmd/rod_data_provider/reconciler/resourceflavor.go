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

	"golang/k8s_io/apimachinery/v/v0_23/pkg/api/errors/errors"
	ctrl "golang/sigs_k8s_io/controller_runtime/v/v0_11/controllerruntime"
	"golang/sigs_k8s_io/controller_runtime/v/v0_11/pkg/client/client"
	kueuev1beta1 "sigs.k8s.io/kueue/apis/kueue/v1beta1"
)

// ResourceFlavorReconciler reconciles ResourceFlavor custom resources and updates the in-memory cache.
type ResourceFlavorReconciler struct {
	client.Client
	Cache Cache
}

func (r *ResourceFlavorReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var rf kueuev1beta1.ResourceFlavor
	if err := r.Get(ctx, req.NamespacedName, &rf); err != nil {
		if errors.IsNotFound(err) {
			r.Cache.DeleteResourceFlavor(req.Name)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	r.Cache.SetResourceFlavor(&rf)
	return ctrl.Result{}, nil
}

func (r *ResourceFlavorReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&kueuev1beta1.ResourceFlavor{}).
		Complete(r)
}
