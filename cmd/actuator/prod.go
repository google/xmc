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

	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	"golang/google_api/option/option"
	"xmanager_cloud/common/health"
	"xmanager_cloud/kubernetes/actuator/k8s_client/k8sclient"
	"xmanager_cloud/kubernetes/actuator/workunittaskhandler"
	"xmanager_cloud/logging/logging"
	ss "xmanager_cloud/storage/spanner/spanner"
	"xmanager_cloud/storage/storage"
)

// ProdServer encapsulates the production actuator dependencies and lifecycle.
type ProdServer struct {
	Handler       *workunittaskhandler.AsynqHandler
	K8sClient     *k8sclient.Client
	SpannerClient *spanner.Client
	RedisClient   redisHealthClient
	cleanup       func() error
}

// HealthCheckers returns the health client interfaces required for active readiness checks.
func (p *ProdServer) HealthCheckers() (k8sHealthClient, health.SpannerHealthClient, redisHealthClient) {
	if p == nil {
		return nil, nil, nil
	}
	var k8s k8sHealthClient
	if p.K8sClient != nil {
		k8s = p.K8sClient
	}
	var spHealth health.SpannerHealthClient
	if p.SpannerClient != nil {
		spHealth = health.NewSpannerHealthClient(p.SpannerClient)
	}
	return k8s, spHealth, p.RedisClient
}

// Close closes all underlying production client connections in reverse initialization order.
func (p *ProdServer) Close() error {
	if p == nil || p.cleanup == nil {
		return nil
	}
	return p.cleanup()
}

// NewProd initializes production dependencies for the actuator server.
func NewProd(ctx context.Context, cfg *Config, projectID string) (_ *ProdServer, retErr error) {
	if cfg == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}
	if projectID == "" {
		return nil, fmt.Errorf("project ID is required")
	}

	var cleanups []func() error
	defer func() {
		if retErr != nil {
			for i := len(cleanups) - 1; i >= 0; i-- {
				_ = cleanups[i]()
			}
		}
	}()

	var spannerClient *spanner.Client
	var storageClient storage.Storage
	if cfg.RuntimeConfig.SpannerDatabaseName != "" {
		sessionPoolConfig := spanner.SessionPoolConfig{
			TrackSessionHandles: true,
			InactiveTransactionRemovalOptions: spanner.InactiveTransactionRemovalOptions{
				ActionOnInactiveTransaction: spanner.Warn,
			},
		}
		var err error
		spannerClient, err = spanner.NewClientWithConfig(
			ctx,
			cfg.RuntimeConfig.SpannerDatabaseName,
			spanner.ClientConfig{SessionPoolConfig: sessionPoolConfig},
			option.WithQuotaProject(cfg.RuntimeConfig.QuotaProjectID),
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create spanner client: %w", err)
		}
		cleanups = append(cleanups, func() error {
			spannerClient.Close()
			return nil
		})

		storageClient, err = ss.NewStorageWithConfig(spannerClient, ss.StorageConfig{StatusMessageHistoryEnabled: cfg.StatusMessageHistoryEnabled})
		if err != nil {
			return nil, fmt.Errorf("failed to create storage: %w", err)
		}
	}

	k8sClient, err := k8sclient.New(cfg.RuntimeConfig, storageClient, spannerClient)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	asynqHandler, err := workunittaskhandler.NewAsynqHandler(workunittaskhandler.AsynqHandlerParams{
		K8sClient: k8sClient,
		ProjectID: projectID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create asynq handler: %w", err)
	}

	asynqServer, err := NewAsynqWorkerServer(AsynqServerConfig{
		RedisAddr: cfg.RedisConfig.RedisAddr,
	}, asynqHandler)
	if err != nil {
		return nil, fmt.Errorf("failed to create asynq worker server: %w", err)
	}
	cleanups = append(cleanups, func() error {
		asynqServer.Shutdown()
		return nil
	})

	if err := asynqServer.Start(); err != nil {
		return nil, fmt.Errorf("failed to start asynq worker server: %w", err)
	}
	logging.Info(ctx, "Started Asynq task queue worker server", "addr", cfg.RedisConfig.RedisAddr)

	cleanup := func() error {
		var errs []error
		for i := len(cleanups) - 1; i >= 0; i-- {
			if err := cleanups[i](); err != nil {
				errs = append(errs, err)
			}
		}
		return errors.Join(errs...)
	}

	return &ProdServer{
		Handler:       asynqHandler,
		K8sClient:     k8sClient,
		SpannerClient: spannerClient,
		RedisClient:   asynqServer,
		cleanup:       cleanup,
	}, nil
}
