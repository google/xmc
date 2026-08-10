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
)

func reconcileCache[T client.Object](ctx context.Context, c client.Client, req ctrl.Request, obj T, onFound func(T), onDelete func()) (ctrl.Result, error) {
	if err := c.Get(ctx, req.NamespacedName, obj); err != nil {
		if errors.IsNotFound(err) {
			onDelete()
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	onFound(obj)
	return ctrl.Result{}, nil
}
