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
	"xmanager_cloud/cmd/rod_data_provider/cache"
)

// LocalQueueReconciler reconciles LocalQueue custom resources and updates the in-memory cache.
type LocalQueueReconciler struct {
	client.Client
	Cache *cache.ResourceCache[*kueuev1beta1.LocalQueue]
}

func (r *LocalQueueReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var lq kueuev1beta1.LocalQueue
	if err := r.Get(ctx, req.NamespacedName, &lq); err != nil {
		if errors.IsNotFound(err) {
			r.Cache.Delete(req.NamespacedName.String())
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	r.Cache.Set(req.NamespacedName.String(), lq.DeepCopy())
	return ctrl.Result{}, nil
}

func (r *LocalQueueReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&kueuev1beta1.LocalQueue{}).
		Complete(r)
}
