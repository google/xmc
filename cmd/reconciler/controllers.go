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

	spb "github.com/google/xmc/api/protos/experiment"
	"github.com/google/xmc/cmd/actuator/k8s_client"
	"github.com/google/xmc/cmd/reconciler/clusterqueue"
	"github.com/google/xmc/cmd/reconciler/hypercomputeclient"
	"github.com/google/xmc/cmd/reconciler/jobset"
	"github.com/google/xmc/cmd/reconciler/kueue_sync"
	"github.com/google/xmc/cmd/reconciler/shared"
	"github.com/google/xmc/cmd/reconciler/workload"
	"github.com/google/xmc/internal/logging"
	"github.com/google/xmc/internal/storage"
	"github.com/google/xmc/internal/workunit"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/manager"
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

func registerControllers(ctx context.Context, p controllerRegistrationParams) (*shared.UpdateBatcher, error) {
	if p.mgr == nil {
		return nil, errors.New("manager cannot be nil")
	}
	if p.k8sClient == nil {
		return nil, errors.New("k8sClient cannot be nil")
	}

	cqReconciler, err := clusterqueue.NewReconciler(clusterqueue.Params{
		Client: p.mgr.GetClient(),
		Scheme: p.mgr.GetScheme(),
		Cache:  p.cqCache,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create clusterqueue reconciler: %w", err)
	}
	if err := cqReconciler.SetupWithManager(ctx, p.mgr); err != nil {
		return nil, fmt.Errorf("unable to setup clusterqueue controller: %w", err)
	}

	batcher := shared.NewUpdateBatcher(
		ctx,
		&shared.BatcherConfig{
			TickDuration:           p.cfg.BatcherTickDuration,
			MaxParallelExperiments: p.cfg.MaxParallelExperiments,
		},
		workunit.NewUpdater(p.storage),
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
		return nil, fmt.Errorf("failed to create jobset reconciler: %w", err)
	}
	if err := jobsetReconciler.SetupWithManager(ctx, p.mgr); err != nil {
		return nil, fmt.Errorf("unable to setup jobset controller: %w", err)
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
		return nil, fmt.Errorf("failed to create workload reconciler: %w", err)
	}
	if err := workloadReconciler.SetupWithManager(ctx, p.mgr); err != nil {
		return nil, fmt.Errorf("unable to setup workload controller: %w", err)
	}

	if err := addBackgroundRunnables(p, batcher); err != nil {
		return nil, err
	}
	return batcher, nil
}

// addBackgroundRunnables registers the non-controller background loops with
// the manager so they start and stop together with the controllers.
func addBackgroundRunnables(p controllerRegistrationParams, batcher *shared.UpdateBatcher) error {
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
		return fmt.Errorf("failed to create kueuesync reconciler: %w", err)
	}

	if err := p.mgr.Add(manager.RunnableFunc(batcher.BatchProcessor)); err != nil {
		return fmt.Errorf("unable to add update batcher runnable: %w", err)
	}
	kueueSync := func(ctx context.Context) error {
		kueueSyncReconciler.RunPositionInQueueSync(ctx)
		return nil
	}
	if err := p.mgr.Add(manager.RunnableFunc(kueueSync)); err != nil {
		return fmt.Errorf("unable to add kueuesync runnable: %w", err)
	}
	return nil
}
