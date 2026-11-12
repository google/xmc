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

	k8sclient "xmanager_cloud/cmd/actuator/k8s_client"
	"xmanager_cloud/cmd/actuator/pausescheduling"
	"xmanager_cloud/cmd/actuator/workunittaskhandler"
	"xmanager_cloud/internal/broadcast"
	"xmanager_cloud/internal/common/health"
	constants "xmanager_cloud/internal/kubernetes/common/constants"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/security/keyreloader"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/dbconfig"
	dbfactory "xmanager_cloud/internal/storage/factory"
	"xmanager_cloud/internal/task_queue_redis/redisclient"
	"xmanager_cloud/pkg/auth/jwks"
)

var osHostname = os.Hostname

// ProdServer encapsulates the production actuator dependencies and lifecycle.
type ProdServer struct {
	Handler       *workunittaskhandler.Handler
	K8sClient     *k8sclient.Client
	dbHealth      health.ReadinessChecker
	RedisClient   redisclient.Consumer
	PauseListener *pausescheduling.Listener
	PauseHandler  pausescheduling.PauseHandler
	cleanup       func() error
}

// HealthCheckers returns the health client interfaces required for active readiness checks.
func (p *ProdServer) HealthCheckers() (k8sHealthClient, health.ReadinessChecker, redisclient.Consumer) {
	if p == nil {
		return nil, nil, nil
	}
	var k8s k8sHealthClient
	if p.K8sClient != nil {
		k8s = p.K8sClient
	}
	return k8s, p.dbHealth, p.RedisClient
}

// Close closes all underlying production client connections in reverse initialization order.
func (p *ProdServer) Close() error {
	if p == nil || p.cleanup == nil {
		return nil
	}
	return p.cleanup()
}

func initActuatorRedisConsumer(ctx context.Context, cfg *Config) *redisclient.ConsumerClient {
	redisConsumer := redisclient.NewConsumer(
		redisclient.Config{Addr: cfg.RedisConfig.RedisAddr},
		constants.RedisStream,
		constants.RedisGroup,
		constants.RedisDLQ,
		cfg.RedisConfig.RedisMaxRetries,
		cfg.RedisConfig.RedisProcessTimeout,
		cfg.RedisConfig.RedisAbandonedMessageTimeout,
		cfg.RedisConfig.RedisReadBatchSize,
	)
	redisConsumer.InitializeClient(ctx)
	cleanupWorkerID := actuatorCleanupWorkerID(ctx)
	go redisConsumer.RunCleanup(ctx, cleanupWorkerID)
	logging.Info(ctx, "Initialized Redis task queue and started cleanup worker", "addr", cfg.RedisConfig.RedisAddr, "cleanup_worker_id", cleanupWorkerID)
	return redisConsumer
}

func runCleanups(cleanups []func() error) error {
	var errs []error
	for i := len(cleanups) - 1; i >= 0; i-- {
		if err := cleanups[i](); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
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
			_ = runCleanups(cleanups)
		}
	}()

	dbStorage, err := initStorage(ctx)
	if err != nil {
		return nil, err
	}
	cleanups = append(cleanups, func() error { dbStorage.Close(); return nil })

	k8sClient, err := k8sclient.New(cfg.RuntimeConfig, dbStorage)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
	}
	keyReloader, err := keyreloader.NewVerifierReloader(cfg.VerifyingKeyFile, cfg.MaxRecentKeys)
	if err != nil {
		return nil, fmt.Errorf("failed to load verifying key: %w", err)
	}

	redisConsumer := initActuatorRedisConsumer(ctx, cfg)
	cleanups = append(cleanups, func() error { return redisConsumer.Close(context.Background()) })

	pauseCache := pausescheduling.NewCache()
	pauseListener, err := initPauseListener(ctx, cfg, pauseCache, dbStorage)
	if err != nil {
		return nil, err
	}
	if pauseListener != nil {
		cleanups = append(cleanups, pauseListener.Close)
	}
	pauseHandler, err := initPauseHandler(cfg, pauseCache, dbStorage)
	if err != nil {
		return nil, err
	}

	handler, err := workunittaskhandler.New(workunittaskhandler.Params{
		K8sClient: k8sClient, ProjectID: projectID, Audience: cfg.Audience,
		XManagerServiceAccount: cfg.XManagerServiceAccount, JWTVerifier: keyReloader.Verifier,
		TokenValidator: initJWKSTokenValidator(cfg), PauseHandler: pauseHandler,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create work unit task handler: %w", err)
	}
	redisConsumer.SetTaskHandler(handler.HandleWorkUnitRedisTask)
	workerID := actuatorWorkerID(ctx)
	go redisConsumer.StartWorker(ctx, workerID)
	logging.Info(ctx, "Started Redis task queue worker", "worker_id", workerID)

	return &ProdServer{
		Handler: handler, K8sClient: k8sClient, dbHealth: dbStorage, RedisClient: redisConsumer,
		PauseListener: pauseListener, PauseHandler: pauseHandler, cleanup: func() error { return runCleanups(cleanups) },
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

func initPauseListener(ctx context.Context, cfg *Config, cache pausescheduling.Cache, storageClient storage.Storage) (*pausescheduling.Listener, error) {
	if !cfg.FeatureFlags.PauseSchedulingEnabled {
		return nil, nil
	}

	if cfg.RedisConfig.RedisAddr == "" {
		return nil, fmt.Errorf("redis address must be provided for pause scheduling listener")
	}
	if storageClient == nil {
		return nil, fmt.Errorf("cannot enable pause scheduling listener without spanner storage client")
	}

	redisClient := redisclient.NewClient(redisclient.Config{
		Addr: cfg.RedisConfig.RedisAddr,
	})
	consumer := broadcast.NewConsumer(redisClient, constants.RedisPauseStream)

	listener, err := pausescheduling.New(pausescheduling.Params{
		Cache:                cache,
		Subscriber:           consumer,
		PauseStateRepository: storageClient.PauseState(),
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

func initPauseHandler(cfg *Config, cache pausescheduling.Cache, storageClient storage.Storage) (pausescheduling.PauseHandler, error) {
	if !cfg.FeatureFlags.PauseSchedulingEnabled {
		return nil, nil
	}
	if storageClient == nil {
		return nil, fmt.Errorf("cannot enable pause scheduling without spanner storage client")
	}
	handler, err := pausescheduling.NewPauseHandler(cache, storageClient)
	if err != nil {
		return nil, fmt.Errorf("failed to create pause handler: %w", err)
	}
	return handler, nil
}

func initStorage(ctx context.Context) (*dbfactory.Storage, error) {
	dbCfg, err := dbconfig.Load()
	if err != nil {
		return nil, fmt.Errorf("failed to load database config: %w", err)
	}
	return dbfactory.NewStorage(ctx, dbCfg, dbfactory.ESS)
}
