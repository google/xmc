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

	"golang/cloud_google_com/go/spanner/v/v1/spanner"
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
	"xmanager_cloud/kubernetes/actuator/k8s_client/k8sclient"
	constants "xmanager_cloud/kubernetes/common/constants"
	"xmanager_cloud/kubernetes/reconciler/jobset/jobset"
	"xmanager_cloud/kubernetes/reconciler/shared/shared"
	"xmanager_cloud/logging/logging"
	ss "xmanager_cloud/storage/spanner/spanner"
	"xmanager_cloud/storage/storage"
)

// Manager encapsulates the controller-runtime Manager and its underlying dependencies and lifecycle.
type Manager struct {
	ctrlMgr       ctrl.Manager
	batcher       *shared.UpdateBatcher
	k8sClient     *k8sclient.Client
	spannerClient *spanner.Client
	cleanup       func() error
	closeOnce     sync.Once
	closeErr      error
}

// CtrlManager returns the underlying controller-runtime Manager.
func (m *Manager) CtrlManager() ctrl.Manager {
	if m == nil {
		return nil
	}
	return m.ctrlMgr
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
	m.closeOnce.Do(func() {
		m.closeErr = m.cleanup()
	})
	return m.closeErr
}

// Start starts background workers and the controller-runtime manager.
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

	k8sConfig, err := ctrl.GetConfig()
	if err != nil {
		return nil, fmt.Errorf("unable to get kubeconfig: %w", err)
	}
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
				&v1beta1.ClusterQueue{}:      {},
				&v1beta1.MultiKueueCluster{}: {},
				&v1beta1.ResourceFlavor{}:    {},
			},
		}),
	})
	if err != nil {
		return nil, fmt.Errorf("unable to start manager: %w", err)
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

	mlRunClient := jobset.NewInstrumentedMLRunCreator(
		newMLRunCreator(ctx, gceMeta.ProjectID, cfg.MldiagnosticsEnvironment, gceMeta.Location))

	batcher, _, err := registerControllers(ctx, controllerRegistrationParams{
		mgr:         ctrlMgr,
		cfg:         cfg,
		storage:     storage,
		k8sClient:   k8sClient,
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
		ctrlMgr:       ctrlMgr,
		batcher:       batcher,
		k8sClient:     k8sClient,
		spannerClient: spannerClient,
		cleanup:       cleanup,
	}, nil
}
