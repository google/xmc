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

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	spb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/cmd/actuator/k8s_client"
	"xmanager_cloud/cmd/reconciler/clusterqueue"
	"xmanager_cloud/cmd/reconciler/jobset"
	"xmanager_cloud/cmd/reconciler/kueue_sync"
	"xmanager_cloud/cmd/reconciler/shared"
	"xmanager_cloud/cmd/reconciler/workload"
	"xmanager_cloud/internal/hypercomputeclient"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/workunit"
)

// newMLRunCreator builds the MLRun control-plane client. It returns a nil
// MLRunCreator interface (never a typed-nil *ControlPlaneClient) when
// initialization fails, so the reconciler's r.MLRunClient == nil guard is not
// defeated by a non-nil interface that wraps a nil pointer.
func newMLRunCreator(ctx context.Context, projectID, environment, location, impersonateSA string) jobset.MLRunCreator {
	client, err := hypercomputeclient.NewControlPlaneClientFromConfig(ctx, hypercomputeclient.Config{
		Project:                   projectID,
		Environment:               environment,
		Location:                  location,
		ImpersonateServiceAccount: impersonateSA,
	})
	if err != nil {
		logging.Error(ctx, "Failed to create MLRun control plane client", "error", err)
		return nil
	}
	return client
}

// jobSetStorage combines storage.Storage (read primitives) with *workunit.Updater
// (UpdateWorkUnitExecutions) to satisfy jobset.ReconcilerStorage.
type jobSetStorage struct {
	storage.Storage
	*workunit.Updater
}

// newJobSetStorage returns a jobset.ReconcilerStorage backed by s and u, or a
// nil interface when s is nil so the reconciler's r.Storage == nil guard holds.
func newJobSetStorage(s storage.Storage, u *workunit.Updater) jobset.ReconcilerStorage {
	if s == nil {
		return nil
	}
	return jobSetStorage{Storage: s, Updater: u}
}

type controllerRegistrationParams struct {
	mgr         ctrl.Manager
	cfg         Config
	storage     storage.Storage
	k8sClient   *k8sclient.Client
	cqCache     *clusterqueue.NameCache
	mlRunClient jobset.MLRunCreator
	// lifecycleNotifier publishes artifact lifecycle events; nil disables them.
	lifecycleNotifier shared.LifecycleNotifier
	projectID         string
	location          string
	clusterName       string
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

	updater := workunit.NewUpdater(p.storage)
	batcher := shared.NewUpdateBatcher(
		ctx,
		&shared.BatcherConfig{
			TickDuration:           p.cfg.BatcherTickDuration,
			MaxParallelExperiments: p.cfg.MaxParallelExperiments,
		},
		updater,
	)
	batcher.LifecycleNotifier = p.lifecycleNotifier

	jobsetReconciler, err := jobset.NewReconciler(jobset.Params{
		Client:                   p.mgr.GetClient(),
		Scheme:                   p.mgr.GetScheme(),
		Project:                  p.projectID,
		Location:                 p.location,
		ClusterName:              p.clusterName,
		MLRunProject:             p.cfg.MLRunProject,
		MLRunLocation:            p.cfg.MLRunLocation,
		Batcher:                  batcher,
		MaxConcurrentReconciles:  p.cfg.MaxConcurrentReconciles,
		StartupPodErrorCheckTime: p.cfg.StartupPodErrorCheckTime,
		Storage:                  newJobSetStorage(p.storage, updater),
		MLRunClient:              p.mlRunClient,
		LifecycleNotifier:        p.lifecycleNotifier,
		ArtifactLifecycleEnabled: p.cfg.FeatureFlags.ArtifactLifecycleEnabled,
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
