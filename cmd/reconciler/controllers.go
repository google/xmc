// Copyright 2025 Google LLC
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
package main

import (
	"context"
	"errors"
	"fmt"

	ctrl "golang/sigs_k8s_io/controller_runtime/v/v0_11/controllerruntime"
	spb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/kubernetes/actuator/k8s_client/k8sclient"
	"xmanager_cloud/kubernetes/reconciler/clusterqueue/clusterqueue"
	"xmanager_cloud/kubernetes/reconciler/hypercomputeclient/hypercomputeclient"
	"xmanager_cloud/kubernetes/reconciler/jobset/jobset"
	"xmanager_cloud/kubernetes/reconciler/kueue_sync/kueuesync"
	"xmanager_cloud/kubernetes/reconciler/shared/shared"
	"xmanager_cloud/kubernetes/reconciler/workload/workload"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/storage/storage"
)

// newMLRunCreator builds the MLRun control-plane client. It returns a nil
// MLRunCreator interface (never a typed-nil *ControlPlaneClient) when
// initialization fails, so the reconciler's r.MLRunClient == nil guard is not
// defeated by a non-nil interface that wraps a nil pointer.
func newMLRunCreator(ctx context.Context, projectID, environment, location string) jobset.MLRunCreator {
	client, err := hypercomputeclient.NewControlPlaneClient(ctx, projectID, environment, location)
	if err != nil {
		logging.Error(ctx, "Failed to create MLRun control plane client", "error", err)
		return nil
	}
	return client
}

type controllerRegistrationParams struct {
	mgr         ctrl.Manager
	cfg         Config
	storage     storage.Storage
	k8sClient   *k8sclient.Client
	cqCache     *clusterqueue.NameCache
	mlRunClient jobset.MLRunCreator
	projectID   string
	location    string
	clusterName string
}

func registerControllers(ctx context.Context, p controllerRegistrationParams) (*shared.UpdateBatcher, *kueuesync.Reconciler, error) {
	if p.mgr == nil {
		return nil, nil, errors.New("manager cannot be nil")
	}
	if p.k8sClient == nil {
		return nil, nil, errors.New("k8sClient cannot be nil")
	}

	cqReconciler, err := clusterqueue.NewReconciler(clusterqueue.Params{
		Client: p.mgr.GetClient(),
		Scheme: p.mgr.GetScheme(),
		Cache:  p.cqCache,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create clusterqueue reconciler: %w", err)
	}
	if err := cqReconciler.SetupWithManager(ctx, p.mgr); err != nil {
		return nil, nil, fmt.Errorf("unable to setup clusterqueue controller: %w", err)
	}

	batcher := shared.NewUpdateBatcher(
		ctx,
		&shared.BatcherConfig{
			TickDuration:           p.cfg.BatcherTickDuration,
			MaxParallelExperiments: p.cfg.MaxParallelExperiments,
		},
		p.storage,
	)

	jobsetReconciler, err := jobset.NewReconciler(jobset.Params{
		Client:                   p.mgr.GetClient(),
		Scheme:                   p.mgr.GetScheme(),
		Project:                  p.projectID,
		Location:                 p.location,
		ClusterName:              p.clusterName,
		Batcher:                  batcher,
		MaxConcurrentReconciles:  p.cfg.MaxConcurrentReconciles,
		StartupPodErrorCheckTime: p.cfg.StartupPodErrorCheckTime,
		Storage:                  p.storage,
		MLRunClient:              p.mlRunClient,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create jobset reconciler: %w", err)
	}
	if err := jobsetReconciler.SetupWithManager(ctx, p.mgr); err != nil {
		return nil, nil, fmt.Errorf("unable to setup jobset controller: %w", err)
	}

	workloadReconciler, err := workload.NewReconciler(workload.Params{
		Client:  p.mgr.GetClient(),
		Scheme:  p.mgr.GetScheme(),
		Batcher: batcher,
		ManagementClusterInfo: &spb.ClusterInfo{
			Project:     p.projectID,
			Location:    p.location,
			ClusterName: p.clusterName,
		},
		MaxConcurrentReconciles: p.cfg.MaxConcurrentReconciles,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create workload reconciler: %w", err)
	}
	if err := workloadReconciler.SetupWithManager(ctx, p.mgr); err != nil {
		return nil, nil, fmt.Errorf("unable to setup workload controller: %w", err)
	}

	kueueSyncReconciler, err := kueuesync.NewReconciler(kueuesync.Params{
		Client:                           p.mgr.GetClient(),
		Clientset:                        p.k8sClient.Clientset(),
		CQCache:                          p.cqCache,
		Storage:                          p.storage,
		UpdateBatcher:                    batcher,
		KueueSyncTickDuration:            p.cfg.KueueSyncTickDuration,
		MaxConcurrentKueueSyncReconciles: p.cfg.MaxConcurrentKueueSyncReconciles,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create kueuesync reconciler: %w", err)
	}

	return batcher, kueueSyncReconciler, nil
}
