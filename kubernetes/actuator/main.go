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

	projectID, _, _, err := getGceMetadata(ctx)
	if err != nil {
		logging.Fatal(ctx, "Failed to get GCE Metadata", "error", err)
	}

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

	var redisClient redisclient.Consumer
	if cfg.UseRedisQueue {
		redisConfig := redisclient.Config{
			Addr: cfg.RedisConfig.RedisAddr,
		}
		redisClient = redisclient.NewConsumer(redisConfig, constants.RedisStream, constants.RedisGroup, constants.RedisDLQ, cfg.RedisConfig.RedisMaxRetries, cfg.RedisConfig.RedisProcessTimeout, cfg.RedisConfig.RedisAbandonedMessageTimeout, cfg.RedisConfig.RedisReadBatchSize)
		redisClient.InitializeClient(ctx)
		actuatorCleanupWorkerID := actuatorCleanupWorkerID(ctx)
		go redisClient.RunCleanup(ctx, actuatorCleanupWorkerID)
		logging.Info(ctx, "Initialized Redis task queue and started cleanup worker", "addr", cfg.RedisConfig.RedisAddr, "cleanup_worker_id", actuatorCleanupWorkerID)
	}

	handler := workunittaskhandler.New(k8sClient, projectID, cfg.Audience, cfg.XManagerServiceAccount, keyReloader.Verifier, redisClient)

	if cfg.UseRedisQueue {
		redisClient.SetTaskHandler(handler.HandleWorkUnitRedisTask)
		workerID := actuatorWorkerID(ctx)
		go redisClient.StartWorker(ctx, workerID)
		logging.Info(ctx, "Started Redis task queue worker", "worker_id", workerID)
	} else {
		logging.Info(ctx, "Registering work unit task handler")
		mux.HandleFunc("/actuate", handler.HandleWorkUnitHTTPTask)
	}

	mux.HandleFunc("/healthz", activeHealthCheck(k8sClient, spannerClient, redisClient, cfg.UseRedisQueue))
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

func activeHealthCheck(k8s *k8sclient.Client, spClient *spanner.Client, redis redisclient.Consumer, useRedisQueue bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		eg, ctx := errgroup.WithContext(ctx)

		// 1. Kubernetes check
		eg.Go(func() error {
			return health.CheckKubernetes(k8s.Clientset())
		})

		// 2. Spanner check
		eg.Go(func() error {
			return health.CheckSpanner(ctx, spClient)
		})

		// 3. Redis check
		eg.Go(func() error {
			if useRedisQueue {
				if redis == nil {
					return fmt.Errorf("redis client is nil but useRedisQueue is true")
				}
				if err := redis.Ping(ctx); err != nil {
					return fmt.Errorf("redis reachability: %w", err)
				}
			}
			return nil
		})

		if err := eg.Wait(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}
}
