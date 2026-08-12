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
// Package main implements the Actuator Server for XManager Cloud.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"go/google"
	"golang/cloud_google_com/go/compute/metadata/v/v0/metadata"
	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	"golang/go_sync/errgroup/errgroup"
	"golang/google_api/option/option"
	"xmanager_cloud/common/health"
	"xmanager_cloud/kubernetes/actuator/k8s_client/k8sclient"
	"xmanager_cloud/kubernetes/actuator/workunittaskhandler"
	constants "xmanager_cloud/kubernetes/common/constants"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/security/keyreloader"
	ss "xmanager_cloud/storage/spanner/spanner"
	"xmanager_cloud/storage/storage"
	"xmanager_cloud/task_queue_redis/redisclient"

	"golang/prometheus/client/prometheus/promhttp/promhttp"
	ctrl "golang/sigs_k8s_io/controller_runtime/v/v0_11/controllerruntime"
)

var osHostname = os.Hostname

func main() {
	google.Init()
	ctx := ctrl.SetupSignalHandler()

	cfg := Load()

	gceMeta, err := getGceMetadata(ctx)
	if err != nil {
		logging.Fatal(ctx, "Failed to get GCE Metadata", "error", err)
	}
	projectID := gceMeta.ProjectID

	logger := logging.NewGCPLogger(slog.LevelInfo, projectID)
	ctx = logging.WithLogger(ctx, logger)

	mux := http.NewServeMux()

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

	keyReloader, err := keyreloader.NewVerifierReloader(cfg.VerifyingKeyFile, cfg.MaxRecentKeys)
	if err != nil {
		logging.Fatal(ctx, "Failed to load verifying key", "error", err)
	}

	redisConfig := redisclient.Config{
		Addr: cfg.RedisConfig.RedisAddr,
	}
	redisClient := redisclient.NewConsumer(redisConfig, constants.RedisStream, constants.RedisGroup, constants.RedisDLQ, cfg.RedisConfig.RedisMaxRetries, cfg.RedisConfig.RedisProcessTimeout, cfg.RedisConfig.RedisAbandonedMessageTimeout, cfg.RedisConfig.RedisReadBatchSize)
	redisClient.InitializeClient(ctx)
	actuatorCleanupWorkerID := actuatorCleanupWorkerID(ctx)
	go redisClient.RunCleanup(ctx, actuatorCleanupWorkerID)
	logging.Info(ctx, "Initialized Redis task queue and started cleanup worker", "addr", cfg.RedisConfig.RedisAddr, "cleanup_worker_id", actuatorCleanupWorkerID)

	handler := workunittaskhandler.New(k8sClient, projectID, cfg.Audience, cfg.XManagerServiceAccount, keyReloader.Verifier, redisClient)

	redisClient.SetTaskHandler(handler.HandleWorkUnitRedisTask)
	workerID := actuatorWorkerID(ctx)
	go redisClient.StartWorker(ctx, workerID)
	logging.Info(ctx, "Started Redis task queue worker", "worker_id", workerID)

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("/readyz", activeHealthCheck(k8sClient, health.NewSpannerHealthClient(spannerClient), redisClient))
	mux.Handle("/metrics", promhttp.Handler())

	addr := fmt.Sprintf(":%s", cfg.Port)
	logging.Info(ctx, "Actuator server listening on", "addr", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		logging.Fatal(ctx, "Server failed to start", "error", err)
	}
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

// GceMetadata holds cluster metadata fetched from GCE metadata server.
type GceMetadata struct {
	ProjectID   string
	Location    string
	ClusterName string
}

func getGceMetadata(ctx context.Context) (GceMetadata, error) {
	logging.Debug(ctx, "Fetching GCE Metadata...")
	projectID := os.Getenv("XMC_PROJECT_ID")
	if projectID == "" {
		pid, err := metadata.ProjectIDWithContext(ctx)
		if err != nil {
			return GceMetadata{}, fmt.Errorf("failed to get Project ID from metadata: %v", err)
		}
		projectID = pid
	}

	clusterName := os.Getenv("XMC_CLUSTER_NAME")
	if clusterName == "" {
		cname, err := metadata.InstanceAttributeValueWithContext(ctx, "cluster-name")
		if err != nil {
			return GceMetadata{}, fmt.Errorf("failed to get cluster-name from metadata: %v", err)
		}
		clusterName = cname
	}

	location := os.Getenv("XMC_CLUSTER_LOCATION")
	if location == "" {
		loc, err := metadata.InstanceAttributeValueWithContext(ctx, "cluster-location")
		if err != nil {
			return GceMetadata{}, fmt.Errorf("failed to get cluster-location from metadata: %v", err)
		}
		location = loc
	}
	logging.Debug(ctx, "Successfully fetched GCE Metadata", "project_id", projectID, "location", location, "cluster_name", clusterName)
	return GceMetadata{
		ProjectID:   projectID,
		Location:    location,
		ClusterName: clusterName,
	}, nil
}

func activeHealthCheck(k8s *k8sclient.Client, spClient health.SpannerHealthClient, redis redisclient.Consumer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		eg, ctx := errgroup.WithContext(ctx)

		// 1. Kubernetes check
		eg.Go(func() error {
			err := health.CheckKubernetes(k8s.Clientset())
			if err != nil {
				slog.Error("Kubernetes healthcheck failed", "error", err)
			}
			return err
		})

		// 2. Spanner check
		eg.Go(func() error {
			err := health.CheckSpannerESS(ctx, spClient)
			if err != nil {
				slog.Error("Spanner healthcheck failed", "error", err)
			}
			return err
		})

		// 3. Redis check
		eg.Go(func() error {
			if redis == nil {
				slog.Error("Redis healthcheck failed: client is nil")
				return fmt.Errorf("redis client is nil")
			}
			if err := redis.Ping(ctx); err != nil {
				slog.Error("Redis healthcheck failed", "error", err)
				return fmt.Errorf("redis reachability: %w", err)
			}
			return nil
		})

		if err := eg.Wait(); err != nil {
			slog.Error("Overall healthcheck failed", "error", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}
}
