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
	"os"
	"slices"

	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	"golang/google_api/option/option"
	"xmanager_cloud/cmd/actuator/pausescheduling"
	"xmanager_cloud/common/health"
	"xmanager_cloud/internal/broadcast"
	"xmanager_cloud/internal/storage/spanner/pausestate"
	"xmanager_cloud/kubernetes/actuator/k8s_client/k8sclient"
	"xmanager_cloud/kubernetes/actuator/workunittaskhandler"
	constants "xmanager_cloud/kubernetes/common/constants"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/pkg/auth/jwks"
	"xmanager_cloud/security/keyreloader"
	ss "xmanager_cloud/storage/spanner/spanner"
	"xmanager_cloud/storage/storage"
	"xmanager_cloud/task_queue_redis/redisclient"
)

var osHostname = os.Hostname

// ProdServer encapsulates the production actuator dependencies and lifecycle.
type ProdServer struct {
	Handler       *workunittaskhandler.Handler
	K8sClient     *k8sclient.Client
	SpannerClient *spanner.Client
	RedisClient   redisclient.Consumer
	PauseCache    pausescheduling.Cache
	PauseListener *pausescheduling.Listener
	cleanup       func() error
}

// HealthCheckers returns the health client interfaces required for active readiness checks.
func (p *ProdServer) HealthCheckers() (k8sHealthClient, health.SpannerHealthClient, redisclient.Consumer) {
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

		storageClient, err = ss.NewStorage(spannerClient)
		if err != nil {
			return nil, fmt.Errorf("failed to create storage: %w", err)
		}
	}

	k8sClient, err := k8sclient.New(cfg.RuntimeConfig, storageClient, spannerClient)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	keyReloader, err := keyreloader.NewVerifierReloader(cfg.VerifyingKeyFile, cfg.MaxRecentKeys)
	if err != nil {
		return nil, fmt.Errorf("failed to load verifying key: %w", err)
	}

	redisConfig := redisclient.Config{
		Addr: cfg.RedisConfig.RedisAddr,
	}
	redisConsumer := redisclient.NewConsumer(
		redisConfig,
		constants.RedisStream,
		constants.RedisGroup,
		constants.RedisDLQ,
		cfg.RedisConfig.RedisMaxRetries,
		cfg.RedisConfig.RedisProcessTimeout,
		cfg.RedisConfig.RedisAbandonedMessageTimeout,
		cfg.RedisConfig.RedisReadBatchSize,
	)
	redisConsumer.InitializeClient(ctx)
	cleanups = append(cleanups, func() error {
		return redisConsumer.Close(context.Background())
	})

	cleanupWorkerID := actuatorCleanupWorkerID(ctx)
	go redisConsumer.RunCleanup(ctx, cleanupWorkerID)
	logging.Info(ctx, "Initialized Redis task queue and started cleanup worker", "addr", cfg.RedisConfig.RedisAddr, "cleanup_worker_id", cleanupWorkerID)

	tokenValidator := initJWKSTokenValidator(cfg)

	handler, err := workunittaskhandler.New(workunittaskhandler.Params{
		K8sClient:              k8sClient,
		ProjectID:              projectID,
		Audience:               cfg.Audience,
		XManagerServiceAccount: cfg.XManagerServiceAccount,
		JWTVerifier:            keyReloader.Verifier,
		TokenValidator:         tokenValidator,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create work unit task handler: %w", err)
	}

	pauseCache := pausescheduling.NewCache()
	pauseListener, err := initPauseScheduling(ctx, cfg, pauseCache)
	if err != nil {
		return nil, err
	}
	if pauseListener != nil {
		cleanups = append(cleanups, pauseListener.Close)
	}

	redisConsumer.SetTaskHandler(handler.HandleWorkUnitRedisTask)
	workerID := actuatorWorkerID(ctx)
	go redisConsumer.StartWorker(ctx, workerID)
	logging.Info(ctx, "Started Redis task queue worker", "worker_id", workerID)

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
		Handler:       handler,
		K8sClient:     k8sClient,
		SpannerClient: spannerClient,
		RedisClient:   redisConsumer,
		PauseCache:    pauseCache,
		PauseListener: pauseListener,
		cleanup:       cleanup,
	}, nil
}

func actuatorWorkerID(ctx context.Context) string {
	hostname, err := osHostname()
	if err != nil {
		logging.Warn(ctx, "Failed to get hostname for worker ID, using 'unknown'", "error", err)
		hostname = "unknown"
	}
	return fmt.Sprintf("%s-%s", constants.ActuatorWorkerID, hostname)
}

func actuatorCleanupWorkerID(ctx context.Context) string {
	hostname, err := osHostname()
	if err != nil {
		logging.Warn(ctx, "Failed to get hostname for cleanup worker ID, using 'unknown'", "error", err)
		hostname = "unknown"
	}
	return fmt.Sprintf("%s-%s", constants.ActuatorCleanupWorkerID, hostname)
}

func appendIfNonEmpty(list []string, item string) []string {
	if item != "" && !slices.Contains(list, item) {
		return append(list, item)
	}
	return list
}

func initJWKSTokenValidator(cfg *Config) workunittaskhandler.TokenValidator {
	if cfg.JWKSURL == "" {
		return nil
	}

	allowedAudiences := appendIfNonEmpty(nil, cfg.Audience)
	allowedAudiences = appendIfNonEmpty(allowedAudiences, os.Getenv("CLIENT_ID"))
	allowedAudiences = appendIfNonEmpty(allowedAudiences, os.Getenv("OIDC_CLIENT_ID"))

	allowedSAs := appendIfNonEmpty(nil, cfg.XManagerServiceAccount)
	allowedSAs = appendIfNonEmpty(allowedSAs, os.Getenv("OIDC_USERNAME"))
	allowedSAs = appendIfNonEmpty(allowedSAs, os.Getenv("AUTH_USERNAME"))

	return jwks.NewValidator(jwks.ValidatorConfig{
		JWKSURL:                cfg.JWKSURL,
		ExpectedIssuer:         cfg.OIDCIssuerURL,
		AllowedAudiences:       allowedAudiences,
		AllowedServiceAccounts: allowedSAs,
	})
}

func initPauseScheduling(ctx context.Context, cfg *Config, cache pausescheduling.Cache) (*pausescheduling.Listener, error) {
	if !cfg.FeatureFlags.PauseSchedulingEnabled {
		return nil, nil
	}

	if cfg.RedisConfig.RedisAddr == "" {
		return nil, fmt.Errorf("redis address must be provided for pause scheduling listener")
	}

	redisClient := redisclient.NewClient(redisclient.Config{
		Addr: cfg.RedisConfig.RedisAddr,
	})
	consumer := broadcast.NewConsumer(redisClient, constants.RedisPauseStream)

	// TODO(pausescheduling): Replace with concrete Spanner repository once available.
	listener, err := pausescheduling.New(pausescheduling.Params{
		Cache:                cache,
		Subscriber:           consumer,
		PauseStateRepository: pausestate.NewFake(),
	})
	if err != nil {
		_ = consumer.Close()
		return nil, fmt.Errorf("failed to create pause listener: %w", err)
	}
	if err := listener.Init(ctx); err != nil {
		_ = listener.Close()
		return nil, fmt.Errorf("failed to initialize pause scheduling listener: %w", err)
	}
	go listener.StartListening(ctx)
	logging.Info(ctx, "Started Actuator pause scheduling listener worker")
	return listener, nil
}
