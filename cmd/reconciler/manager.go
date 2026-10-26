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
	"sync"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/selection"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/config"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	v1alpha2 "sigs.k8s.io/jobset/api/jobset/v1alpha2"
	v1beta1 "sigs.k8s.io/kueue/apis/kueue/v1beta1"
	"xmanager_cloud/cmd/actuator/k8s_client"
	"xmanager_cloud/cmd/reconciler/clusterqueue"
	"xmanager_cloud/cmd/reconciler/jobset"
	"xmanager_cloud/cmd/reconciler/shared"
	"xmanager_cloud/internal/common/health"
	constants "xmanager_cloud/internal/kubernetes/common/constants"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/storage/dbconfig"
	dbfactory "xmanager_cloud/internal/storage/factory"
)

// healthProbeBindAddress is the address on which the manager serves the
// /healthz and /readyz probes.
const healthProbeBindAddress = ":8080"

// Manager encapsulates the controller-runtime Manager and its underlying dependencies and lifecycle.
type Manager struct {
	ctrlMgr   ctrl.Manager
	batcher   *shared.UpdateBatcher
	k8sClient *k8sclient.Client
	dbHealth  health.ReadinessChecker
	cleanup   func() error
	closeOnce sync.Once
	closeErr  error
}

// CtrlManager returns the underlying controller-runtime Manager.
func (m *Manager) CtrlManager() ctrl.Manager {
	if m == nil {
		return nil
	}
	return m.ctrlMgr
}

// HealthCheckers returns the client interfaces required for active readiness checks.
func (m *Manager) HealthCheckers() (*k8sclient.Client, health.ReadinessChecker) {
	if m == nil {
		return nil, nil
	}
	return m.k8sClient, m.dbHealth
}

// Close closes all underlying client connections using LIFO cleanup.
func (m *Manager) Close() error {
	if m == nil || m.cleanup == nil {
		return nil
	}
	m.closeOnce.Do(func() {
		m.closeErr = m.cleanup()
	})
	return m.closeErr
}

// Start starts the controller-runtime manager, which in turn runs all
// registered controllers and background runnables until ctx is cancelled.
func (m *Manager) Start(ctx context.Context) error {
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

func initStorage(ctx context.Context) (*dbfactory.Storage, error) {
	dbCfg, err := dbconfig.Load()
	if err != nil {
		return nil, fmt.Errorf("failed to load database config: %w", err)
	}
	return dbfactory.NewStorage(ctx, dbCfg, dbfactory.ESS)
}

func metricsBindAddress(port int) string {
	if port <= 0 {
		return "0"
	}
	return fmt.Sprintf(":%d", port)
}

func newScheme() (*runtime.Scheme, error) {
	scheme := runtime.NewScheme()
	builder := runtime.NewSchemeBuilder(clientgoscheme.AddToScheme, v1alpha2.AddToScheme, v1beta1.AddToScheme)
	if err := builder.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("failed to build scheme: %w", err)
	}
	return scheme, nil
}

// newCacheOptions restricts the informer cache to the objects the reconcilers
// actually need: XMC-managed JobSets and Pods that belong to a JobSet.
func newCacheOptions() (cache.Options, error) {
	hasJobSetName, err := labels.NewRequirement(v1alpha2.JobSetNameKey, selection.Exists, nil)
	if err != nil {
		return cache.Options{}, fmt.Errorf("failed to build pod label selector: %w", err)
	}
	return cache.Options{
		ByObject: map[runtimeclient.Object]cache.ByObject{
			&v1alpha2.JobSet{}: {
				Label: labels.SelectorFromSet(labels.Set{constants.ManagedByLabel: constants.XmcLabelValue}),
			},
			&corev1.Pod{}: {
				Label: labels.NewSelector().Add(*hasJobSetName),
			},
		},
	}, nil
}

func initCtrlManager(ctx context.Context, cfg Config) (ctrl.Manager, error) {
	scheme, err := newScheme()
	if err != nil {
		return nil, err
	}
	cacheOpts, err := newCacheOptions()
	if err != nil {
		return nil, err
	}

	k8sConfig, err := ctrl.GetConfig()
	if err != nil {
		return nil, fmt.Errorf("unable to get kubeconfig: %w", err)
	}
	k8sConfig.QPS = float32(cfg.KubeAPIQPS)
	k8sConfig.Burst = cfg.KubeAPIBurst

	mgr, err := ctrl.NewManager(k8sConfig, ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: metricsBindAddress(cfg.MetricsPort)},
		HealthProbeBindAddress: healthProbeBindAddress,
		Cache:                  cacheOpts,
		Controller:             config.Controller{ReconciliationTimeout: cfg.ReconciliationTimeout},
	})
	if err != nil {
		return nil, fmt.Errorf("unable to create manager: %w", err)
	}

	if err := mgr.GetFieldIndexer().IndexField(ctx, &v1alpha2.JobSet{}, "metadata.name", func(rawObj runtimeclient.Object) []string {
		return []string{rawObj.GetName()}
	}); err != nil {
		return nil, fmt.Errorf("unable to index JobSet metadata.name: %w", err)
	}

	return mgr, nil
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

	storage, err := initStorage(ctx)
	if err != nil {
		return nil, err
	}
	cleanups = append(cleanups, func() error {
		storage.Close()
		return nil
	})

	k8sClient, err := k8sclient.New(cfg.RuntimeConfig, storage)
	if err != nil {
		return nil, fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	ctrlMgr, err := initCtrlManager(ctx, cfg)
	if err != nil {
		return nil, err
	}

	cqCache := clusterqueue.NewNameCache()
	mlRunProject := cfg.MLRunProject
	if mlRunProject == "" {
		mlRunProject = gceMeta.ProjectID
	}
	mlRunLocation := cfg.MLRunLocation
	if mlRunLocation == "" {
		mlRunLocation = gceMeta.Location
	}
	mlRunClient := jobset.NewInstrumentedMLRunCreator(
		newMLRunCreator(ctx, mlRunProject, cfg.MldiagnosticsEnvironment, mlRunLocation, cfg.MLRunImpersonateSA))

	batcher, err := registerControllers(ctx, controllerRegistrationParams{
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
		ctrlMgr:   ctrlMgr,
		batcher:   batcher,
		k8sClient: k8sClient,
		dbHealth:  storage,
		cleanup:   cleanup,
	}, nil
}
