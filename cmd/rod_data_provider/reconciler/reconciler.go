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

	ctrl "golang/sigs_k8s_io/controller_runtime/v/v0_11/controllerruntime"
	"golang/sigs_k8s_io/controller_runtime/v/v0_11/pkg/client/client"
	kueuev1beta1 "sigs.k8s.io/kueue/apis/kueue/v1beta1"
	"xmanager_cloud/cmd/rod_data_provider/cache"
)

// KueueCollector orchestrates ClusterQueue, LocalQueue, and ResourceFlavor reconcilers
// to collect and sync Kueue entity information into the in-memory cache.
type KueueCollector struct {
	Client          client.Client
	ClusterQueues   cache.Cache[*kueuev1beta1.ClusterQueue]
	LocalQueues     cache.Cache[*kueuev1beta1.LocalQueue]
	ResourceFlavors cache.Cache[*kueuev1beta1.ResourceFlavor]
}

func (c *KueueCollector) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	cqRec := &ClusterQueueReconciler{Client: c.Client, Cache: c.ClusterQueues}
	if err := cqRec.SetupWithManager(ctx, mgr); err != nil {
		return fmt.Errorf("failed to setup ClusterQueueReconciler: %w", err)
	}
	lqRec := &LocalQueueReconciler{Client: c.Client, Cache: c.LocalQueues}
	if err := lqRec.SetupWithManager(ctx, mgr); err != nil {
		return fmt.Errorf("failed to setup LocalQueueReconciler: %w", err)
	}
	rfRec := &ResourceFlavorReconciler{Client: c.Client, Cache: c.ResourceFlavors}
	if err := rfRec.SetupWithManager(ctx, mgr); err != nil {
		return fmt.Errorf("failed to setup ResourceFlavorReconciler: %w", err)
	}
	return nil
}
