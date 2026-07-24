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
// Package main implements the Reconciler for XManager Cloud.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"go/google"
	"golang/cloud_google_com/go/compute/metadata/v/v0/metadata"
	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	"golang/go_sync/errgroup/errgroup"
	"golang/google_api/option/option"
	v1alpha2 "sigs.k8s.io/jobset/api/jobset/v1alpha2"
	v1beta1 "sigs.k8s.io/kueue/apis/kueue/v1beta1"
	"xmanager_cloud/common/health"
	spb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/kubernetes/actuator/k8s_client/k8sclient"
	constants "xmanager_cloud/kubernetes/common/constants"
	"xmanager_cloud/kubernetes/reconciler/clusterqueue/clusterqueue"
	"xmanager_cloud/kubernetes/reconciler/jobset/jobset"
	"xmanager_cloud/kubernetes/reconciler/kueue_sync/kueuesync"
	"xmanager_cloud/kubernetes/reconciler/shared/shared"
	"xmanager_cloud/kubernetes/reconciler/workload/workload"
	"xmanager_cloud/logging/logging"
	ss "xmanager_cloud/storage/spanner/spanner"
	"xmanager_cloud/storage/storage"

	"golang/k8s_io/apimachinery/v/v0_23/pkg/labels/labels"
	"golang/k8s_io/apimachinery/v/v0_23/pkg/runtime/runtime"
	clientgoscheme "golang/k8s_io/client_go/v/v0_23/kubernetes/scheme/scheme"
	ctrl "golang/sigs_k8s_io/controller_runtime/v/v0_11/controllerruntime"
	"golang/sigs_k8s_io/controller_runtime/v/v0_11/pkg/cache/cache"
	runtimeclient "golang/sigs_k8s_io/controller_runtime/v/v0_11/pkg/client/client"
)

func main() {
	google.Init()
	ctx := ctrl.SetupSignalHandler()

	cfg := Load()

	projectID, location, clusterName, err := getGceMetadata(ctx)
	if err != nil {
		logging.Fatal(ctx, "Failed to get GCE Metadata", "error", err)
	}

	logger := logging.NewGCPLogger(slog.LevelInfo, projectID)
	ctx = logging.WithLogger(ctx, logger)

	var spannerClient *spanner.Client
	var storage storage.Storage
	if cfg.RuntimeConfig.SpannerDatabaseName != "" {
		sessionPoolConfig := spanner.SessionPoolConfig{
			TrackSessionHandles: true,
			InactiveTransactionRemovalOptions: spanner.InactiveTransactionRemovalOptions{
				ActionOnInactiveTransaction: spanner.Warn,
			},
		}
		spannerClient, err = spanner.NewClientWithConfig(
			ctx,
			cfg.RuntimeConfig.SpannerDatabaseName,
			spanner.ClientConfig{SessionPoolConfig: sessionPoolConfig},
			option.WithQuotaProject(cfg.RuntimeConfig.QuotaProjectID),
		)
		if err != nil {
			logging.Fatal(ctx, "Failed to create spanner client", "error", err)
		}

		storage, err = ss.NewStorageWithConfig(spannerClient, ss.StorageConfig{StatusMessageHistoryEnabled: cfg.StatusMessageHistoryEnabled})
		if err != nil {
			logging.Fatal(ctx, "Failed to create storage", "error", err)
		}
	}

	k8sClient, err := k8sclient.New(cfg.RuntimeConfig, storage, spannerClient)
	if err != nil {
		logging.Fatal(ctx, "Failed to create Kubernetes client", "error", err)
	}

	cqCache := clusterqueue.NewNameCache()

	mgr, batcher, err := buildManager(ctx, cfg, storage, projectID, location, clusterName, cqCache)
	if err != nil {
		logging.Fatal(ctx, "Failed to setup manager", "error", err)
	}

	kueueSyncReconciler := &kueuesync.Reconciler{
		Clientset:                        k8sClient.Clientset(),
		Client:                           mgr.GetClient(),
		CQCache:                          cqCache,
		Storage:                          storage,
		UpdateBatcher:                    batcher,
		KueueSyncTickDuration:            cfg.KueueSyncTickDuration,
		MaxConcurrentKueueSyncReconciles: cfg.MaxConcurrentKueueSyncReconciles,
	}
	go kueueSyncReconciler.RunPositionInQueueSync(ctx)

	logging.Debug(ctx, "Actuator Reconciler initialization complete, starting manager...")

	if err := mgr.AddHealthzCheck("healthz", func(req *http.Request) error { return nil }); err != nil {
		logging.Fatal(ctx, "unable to set up health check", "error", err)
	}
	if err := mgr.AddReadyzCheck("readyz", activeHealthChecker(k8sClient, health.NewSpannerHealthClient(spannerClient))); err != nil {
		logging.Fatal(ctx, "unable to set up ready check", "error", err)
	}

	startManager(ctx, mgr)
}

func startManager(ctx context.Context, mgr ctrl.Manager) {
	logging.Info(ctx, "Starting manager")
	if err := mgr.Start(ctx); err != nil {
		logging.Fatal(ctx, "Failed to start manager", "error", err)
	}
	logging.Info(ctx, "Manager started")
}

func getGceMetadata(ctx context.Context) (projectID, location, clusterName string, err error) {
	logging.Debug(ctx, "Fetching GCE Metadata...")
	projectID, err = metadata.ProjectIDWithContext(ctx)
	if err != nil {
		return "", "", "", fmt.Errorf("failed to get Project ID from metadata: %v", err)
	}
	clusterName, err = metadata.InstanceAttributeValueWithContext(ctx, "cluster-name")
	if err != nil {
		return "", "", "", fmt.Errorf("failed to get cluster-name from metadata: %v", err)
	}
	location, err = metadata.InstanceAttributeValueWithContext(ctx, "cluster-location")
	if err != nil {
		return "", "", "", fmt.Errorf("failed to get cluster-location from metadata: %v", err)
	}
	logging.Debug(ctx, "Successfully fetched GCE Metadata", "project_id", projectID, "location", location, "cluster_name", clusterName)
	return projectID, location, clusterName, nil
}

func activeHealthChecker(k8s *k8sclient.Client, spClient health.SpannerHealthClient) func(req *http.Request) error {
	return func(req *http.Request) error {
		ctx := req.Context()
		eg, ctx := errgroup.WithContext(ctx)

		// 1. Kubernetes check
		eg.Go(func() error {
			return health.CheckKubernetes(k8s.Clientset())
		})

		// 2. Spanner check
		eg.Go(func() error {
			return health.CheckSpannerESS(ctx, spClient)
		})

		return eg.Wait()
	}
}

func buildManager(ctx context.Context, cfg Config, storage storage.Storage, projectID, location, clusterName string, cqCache *clusterqueue.NameCache) (ctrl.Manager, *shared.UpdateBatcher, error) {
	scheme := runtime.NewScheme()
	clientgoscheme.AddToScheme(scheme)
	v1alpha2.AddToScheme(scheme)
	v1beta1.AddToScheme(scheme)

	k8sConfig := ctrl.GetConfigOrDie()
	k8sConfig.QPS = float32(cfg.KubeAPIQPS)
	k8sConfig.Burst = cfg.KubeAPIBurst

	mgr, err := ctrl.NewManager(k8sConfig, ctrl.Options{
		Scheme:                 scheme,
		MetricsBindAddress:     ":8081",
		HealthProbeBindAddress: ":8080",
		NewCache: cache.BuilderWithOptions(cache.Options{
			SelectorsByObject: cache.SelectorsByObject{
				&v1alpha2.JobSet{}: {
					Label: labels.SelectorFromSet(labels.Set{
						constants.ManagedByLabel: constants.XmcLabelValue,
					}),
				},
				&v1beta1.MultiKueueCluster{}: {},
				&v1beta1.ResourceFlavor{}:    {},
			},
		}),
	})
	if err != nil {
		return nil, nil, fmt.Errorf("unable to start manager: %w", err)
	}

	if err := mgr.GetFieldIndexer().IndexField(ctx, &v1alpha2.JobSet{}, "metadata.name", func(rawObj runtimeclient.Object) []string {
		jobset := rawObj.(*v1alpha2.JobSet)
		return []string{jobset.Name}
	}); err != nil {
		return nil, nil, fmt.Errorf("unable to index JobSet metadata.name: %w", err)
	}

	clusterQueueReconciler := &clusterqueue.Reconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
		Cache:  cqCache,
	}
	if err := clusterQueueReconciler.SetupWithManager(ctx, mgr); err != nil {
		return nil, nil, fmt.Errorf("unable to setup clusterqueue controller: %w", err)
	}

	batcher := shared.NewUpdateBatcher(
		ctx,
		&shared.BatcherConfig{
			TickDuration:           cfg.BatcherTickDuration,
			MaxParallelExperiments: cfg.MaxParallelExperiments,
		},
		storage.(*ss.Storage),
	)

	jobsetReconciler := &jobset.Reconciler{
		Client:                      mgr.GetClient(),
		Scheme:                      mgr.GetScheme(),
		Project:                     projectID,
		Location:                    location,
		ClusterName:                 clusterName,
		Batcher:                     batcher,
		MaxConcurrentReconciles:     cfg.MaxConcurrentReconciles,
		StartupPodErrorCheckTime:    cfg.StartupPodErrorCheckTime,
		StartupPodErrorCheckEnabled: cfg.StartupPodErrorCheckEnabled,
		Storage:                     storage,
	}

	if err := jobsetReconciler.SetupWithManager(ctx, mgr); err != nil {
		return nil, nil, fmt.Errorf("unable to setup jobset controller: %w", err)
	}

	workloadReconciler := &workload.Reconciler{
		Client:  mgr.GetClient(),
		Scheme:  mgr.GetScheme(),
		Batcher: batcher,
		ManagementClusterInfo: &spb.ClusterInfo{
			Project:     projectID,
			Location:    location,
			ClusterName: clusterName,
		},
		MaxConcurrentReconciles:     cfg.MaxConcurrentReconciles,
		DetectInadmissibleWorkUnits: cfg.DetectInadmissibleWorkUnits,
	}

	if err := workloadReconciler.SetupWithManager(ctx, mgr); err != nil {
		return nil, nil, fmt.Errorf("unable to setup workload controller: %w", err)
	}

	return mgr, batcher, nil
}
