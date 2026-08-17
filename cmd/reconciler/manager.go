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
	"net/http"

	"golang/cloud_google_com/go/compute/metadata/v/v0/metadata"
	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	"golang/go_sync/errgroup/errgroup"
	"golang/google_api/option/option"
	"golang/k8s_io/apimachinery/v/v0_23/pkg/labels/labels"
	"golang/k8s_io/apimachinery/v/v0_23/pkg/runtime/runtime"
	clientgoscheme "golang/k8s_io/client_go/v/v0_23/kubernetes/scheme/scheme"
	ctrl "golang/sigs_k8s_io/controller_runtime/v/v0_11/controllerruntime"
	"golang/sigs_k8s_io/controller_runtime/v/v0_11/pkg/cache/cache"
	runtimeclient "golang/sigs_k8s_io/controller_runtime/v/v0_11/pkg/client/client"
	v1alpha2 "sigs.k8s.io/jobset/api/jobset/v1alpha2"
	v1beta1 "sigs.k8s.io/kueue/apis/kueue/v1beta1"
	"xmanager_cloud/common/health"
	spb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/kubernetes/actuator/k8s_client/k8sclient"
	constants "xmanager_cloud/kubernetes/common/constants"
	"xmanager_cloud/kubernetes/reconciler/clusterqueue/clusterqueue"
	"xmanager_cloud/kubernetes/reconciler/hypercomputeclient/hypercomputeclient"
	"xmanager_cloud/kubernetes/reconciler/jobset/jobset"
	"xmanager_cloud/kubernetes/reconciler/kueue_sync/kueuesync"
	"xmanager_cloud/kubernetes/reconciler/shared/shared"
	"xmanager_cloud/kubernetes/reconciler/workload/workload"
	"xmanager_cloud/logging/logging"
	ss "xmanager_cloud/storage/spanner/spanner"
	"xmanager_cloud/storage/storage"
)

// GceMetadata holds cluster metadata fetched from GCE metadata server.
type GceMetadata struct {
	ProjectID   string
	Location    string
	ClusterName string
}

func getGceMetadata(ctx context.Context) (GceMetadata, error) {
	logging.Debug(ctx, "Fetching GCE Metadata...")
	projectID, err := metadata.ProjectIDWithContext(ctx)
	if err != nil {
		return GceMetadata{}, fmt.Errorf("failed to get Project ID from metadata: %w", err)
	}
	clusterName, err := metadata.InstanceAttributeValueWithContext(ctx, "cluster-name")
	if err != nil {
		return GceMetadata{}, fmt.Errorf("failed to get cluster-name from metadata: %w", err)
	}
	location, err := metadata.InstanceAttributeValueWithContext(ctx, "cluster-location")
	if err != nil {
		return GceMetadata{}, fmt.Errorf("failed to get cluster-location from metadata: %w", err)
	}
	logging.Debug(ctx, "Successfully fetched GCE Metadata", "project_id", projectID, "location", location, "cluster_name", clusterName)
	return GceMetadata{
		ProjectID:   projectID,
		Location:    location,
		ClusterName: clusterName,
	}, nil
}

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

// Manager encapsulates the controller-runtime Manager and its underlying dependencies and lifecycle.
type Manager struct {
	ctrlMgr       ctrl.Manager
	batcher       *shared.UpdateBatcher
	k8sClient     *k8sclient.Client
	spannerClient *spanner.Client
	kueueSync     *kueuesync.Reconciler
	cleanup       func() error
}

// HealthCheckers returns the client interfaces required for active readiness checks.
func (m *Manager) HealthCheckers() (*k8sclient.Client, health.SpannerHealthClient) {
	if m == nil {
		return nil, nil
	}
	var spClient health.SpannerHealthClient
	if m.spannerClient != nil {
		spClient = health.NewSpannerHealthClient(m.spannerClient)
	}
	return m.k8sClient, spClient
}

// Close closes all underlying client connections using LIFO cleanup.
func (m *Manager) Close() error {
	if m == nil || m.cleanup == nil {
		return nil
	}
	cleanup := m.cleanup
	m.cleanup = nil
	return cleanup()
}

// Start starts background workers and the controller-runtime manager.
func (m *Manager) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	if m.kueueSync != nil {
		go m.kueueSync.RunPositionInQueueSync(ctx)
	}
	return startManager(ctx, m.ctrlMgr)
}

func startManager(ctx context.Context, mgr ctrl.Manager) error {
	logging.Info(ctx, "Starting manager")
	if err := mgr.Start(ctx); err != nil {
		return fmt.Errorf("failed to start manager: %w", err)
	}
	logging.Info(ctx, "Manager stopped")
	return nil
}

func activeHealthChecker(k8s *k8sclient.Client, spClient health.SpannerHealthClient) func(req *http.Request) error {
	return func(req *http.Request) error {
		ctx := req.Context()
		eg, ctx := errgroup.WithContext(ctx)

		// 1. Kubernetes check
		if k8s != nil && k8s.Clientset() != nil {
			eg.Go(func() error {
				return health.CheckKubernetes(k8s.Clientset())
			})
		}

		// 2. Spanner check
		if spClient != nil {
			eg.Go(func() error {
				return health.CheckSpannerESS(ctx, spClient)
			})
		}

		return eg.Wait()
	}
}

func initSpannerAndStorage(ctx context.Context, cfg Config) (*spanner.Client, storage.Storage, error) {
	if cfg.RuntimeConfig.SpannerDatabaseName == "" {
		return nil, nil, nil
	}
	sessionPoolConfig := spanner.SessionPoolConfig{
		TrackSessionHandles: true,
		InactiveTransactionRemovalOptions: spanner.InactiveTransactionRemovalOptions{
			ActionOnInactiveTransaction: spanner.Warn,
		},
	}
	spannerClient, err := spanner.NewClientWithConfig(
		ctx,
		cfg.RuntimeConfig.SpannerDatabaseName,
		spanner.ClientConfig{SessionPoolConfig: sessionPoolConfig},
		option.WithQuotaProject(cfg.RuntimeConfig.QuotaProjectID),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create spanner client: %w", err)
	}

	storage, err := ss.NewStorageWithConfig(spannerClient, ss.StorageConfig{
		StatusMessageHistoryEnabled: cfg.StatusMessageHistoryEnabled,
	})
	if err != nil {
		spannerClient.Close()
		return nil, nil, fmt.Errorf("failed to create storage: %w", err)
	}

	return spannerClient, storage, nil
}

func initCtrlManager(ctx context.Context, cfg Config) (ctrl.Manager, error) {
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
		return nil, fmt.Errorf("unable to start manager: %w", err)
	}

	if err := mgr.GetFieldIndexer().IndexField(ctx, &v1alpha2.JobSet{}, "metadata.name", func(rawObj runtimeclient.Object) []string {
		jobset := rawObj.(*v1alpha2.JobSet)
		return []string{jobset.Name}
	}); err != nil {
		return nil, fmt.Errorf("unable to index JobSet metadata.name: %w", err)
	}

	return mgr, nil
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

	var spannerStorage *ss.Storage
	if p.storage != nil {
		var ok bool
		spannerStorage, ok = p.storage.(*ss.Storage)
		if !ok {
			return nil, nil, fmt.Errorf("storage is not *spanner.Storage: %T", p.storage)
		}
	}

	batcher := shared.NewUpdateBatcher(
		ctx,
		&shared.BatcherConfig{
			TickDuration:           p.cfg.BatcherTickDuration,
			MaxParallelExperiments: p.cfg.MaxParallelExperiments,
		},
		spannerStorage,
	)

	jobsetReconciler, err := jobset.NewReconciler(jobset.Params{
		Client:                      p.mgr.GetClient(),
		Scheme:                      p.mgr.GetScheme(),
		Project:                     p.projectID,
		Location:                    p.location,
		ClusterName:                 p.clusterName,
		Batcher:                     batcher,
		MaxConcurrentReconciles:     p.cfg.MaxConcurrentReconciles,
		StartupPodErrorCheckTime:    p.cfg.StartupPodErrorCheckTime,
		StartupPodErrorCheckEnabled: p.cfg.StartupPodErrorCheckEnabled,
		Storage:                     p.storage,
		MLRunClient:                 p.mlRunClient,
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
		MaxConcurrentReconciles:     p.cfg.MaxConcurrentReconciles,
		DetectInadmissibleWorkUnits: p.cfg.DetectInadmissibleWorkUnits,
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

func registerHealthChecks(mgr ctrl.Manager, k8sClient *k8sclient.Client, spClient *spanner.Client) error {
	var spHealth health.SpannerHealthClient
	if spClient != nil {
		spHealth = health.NewSpannerHealthClient(spClient)
	}

	if err := mgr.AddHealthzCheck("healthz", func(req *http.Request) error { return nil }); err != nil {
		return fmt.Errorf("unable to set up health check: %w", err)
	}
	if err := mgr.AddReadyzCheck("readyz", activeHealthChecker(k8sClient, spHealth)); err != nil {
		return fmt.Errorf("unable to set up ready check: %w", err)
	}
	return nil
}

// NewManager constructs and initializes the production Manager along with all controllers,
// batchers, storage, and infrastructure dependencies with automatic LIFO rollback on error.
func NewManager(ctx context.Context, cfg Config) (mgr *Manager, retErr error) {
	var cleanups []func() error
	defer func() {
		if retErr != nil {
			for i := len(cleanups) - 1; i >= 0; i-- {
				_ = cleanups[i]()
			}
		}
	}()

	gceMeta, err := getGceMetadata(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get GCE metadata: %w", err)
	}

	spannerClient, storage, err := initSpannerAndStorage(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if spannerClient != nil {
		cleanups = append(cleanups, func() error {
			spannerClient.Close()
			return nil
		})
	}

	k8sClient, err := k8sclient.New(cfg.RuntimeConfig, storage, spannerClient)
	if err != nil {
		return nil, fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	ctrlMgr, err := initCtrlManager(ctx, cfg)
	if err != nil {
		return nil, err
	}

	cqCache := clusterqueue.NewNameCache()
	mlRunClient := jobset.NewInstrumentedMLRunCreator(
		newMLRunCreator(ctx, gceMeta.ProjectID, cfg.MldiagnosticsEnvironment, gceMeta.Location))

	batcher, kueueSyncReconciler, err := registerControllers(ctx, controllerRegistrationParams{
		mgr:         ctrlMgr,
		cfg:         cfg,
		storage:     storage,
		k8sClient:   k8sClient,
		cqCache:     cqCache,
		mlRunClient: mlRunClient,
		projectID:   gceMeta.ProjectID,
		location:    gceMeta.Location,
		clusterName: gceMeta.ClusterName,
	})
	if err != nil {
		return nil, err
	}

	if err := registerHealthChecks(ctrlMgr, k8sClient, spannerClient); err != nil {
		return nil, err
	}

	cleanup := func() error {
		var errs []error
		for i := len(cleanups) - 1; i >= 0; i-- {
			if err := cleanups[i](); err != nil {
				errs = append(errs, err)
			}
		}
		return errors.Join(errs...)
	}

	return &Manager{
		ctrlMgr:       ctrlMgr,
		batcher:       batcher,
		k8sClient:     k8sClient,
		spannerClient: spannerClient,
		kueueSync:     kueueSyncReconciler,
		cleanup:       cleanup,
	}, nil
}
