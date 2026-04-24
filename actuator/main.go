// Copyright 2025 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
// Package main implements the Actuator for XManager Cloud.
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
	"golang/google_api/option/option"
	spb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/kubernetes/api/v1alpha2/v1alpha2"
	"xmanager_cloud/kubernetes/api/v1beta1/v1beta1"
	constants "xmanager_cloud/kubernetes/common/constants"
	"xmanager_cloud/kubernetes/reconciler/clusterqueue/clusterqueue"
	"xmanager_cloud/kubernetes/reconciler/jobset/jobset"
	"xmanager_cloud/kubernetes/reconciler/kueue_sync/kueuesync"
	"xmanager_cloud/kubernetes/reconciler/shared/shared"
	"xmanager_cloud/kubernetes/reconciler/workload/workload"
	"xmanager_cloud/kubernetes/actuator/k8s_client/k8sclient"
	"xmanager_cloud/kubernetes/actuator/workunittaskhandler"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/security/keyreloader"
	ss "xmanager_cloud/storage/spanner/spanner"
	"xmanager_cloud/storage/storage"
	"xmanager_cloud/task_queue_redis/redisclient"

	"golang/k8s_io/apimachinery/v/v0_23/pkg/labels/labels"
	"golang/k8s_io/apimachinery/v/v0_23/pkg/runtime/runtime"
	clientgoscheme "golang/k8s_io/client_go/v/v0_23/kubernetes/scheme/scheme"
	"golang/prometheus/client/prometheus/promhttp/promhttp"
	ctrl "golang/sigs_k8s_io/controller_runtime/v/v0_11/controllerruntime"
	"golang/sigs_k8s_io/controller_runtime/v/v0_11/pkg/cache/cache"
	runtimeclient "golang/sigs_k8s_io/controller_runtime/v/v0_11/pkg/client/client"
)

var osHostname = os.Hostname

func main() {
	google.Init()
	ctx := ctrl.SetupSignalHandler()

	cfg := Load()

	projectID, location, clusterName, err := getGceMetadata(ctx)
	if err != nil {
		logging.Fatal(ctx, "Failed to get GCE Metadata", "error", err)
	}

	// Do not set the slog.default logger to the GCP logger. The runtime-controller eats the slog
	// handler and causes the entire program's logging to seize up. It should only ever be passed
	// contexts without a slog.Handler attached.
	logger := logging.NewGCPLogger(slog.LevelInfo, projectID)
	ctx = logging.WithLogger(ctx, logger)

	// Setup router
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

	// Add a simple health check endpoint.
	mux.HandleFunc("/healthz", activeHealthCheck(k8sClient, spannerClient, redisClient, cfg.UseRedisQueue))

	// Add prometheus metrics endpoint.
	mux.Handle("/metrics", promhttp.Handler())

	// Start HTTP server in a goroutine so it doesn't block manager startup.
	go func() {
		addr := fmt.Sprintf(":%s", cfg.Port)
		logging.Info(ctx, "Actuator server listening on", "addr", addr)
		if err := http.ListenAndServe(addr, mux); err != nil {
			logging.Fatal(ctx, "Server failed to start", "error", err)
		}
	}()

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

	logging.Debug(ctx, "Actuator initialization complete, starting manager...")

	startManager(ctx, mgr)
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

func buildManager(ctx context.Context, cfg Config, storage storage.Storage, projectID, location, clusterName string, cqCache *clusterqueue.NameCache) (ctrl.Manager, *shared.UpdateBatcher, error) {
	scheme := runtime.NewScheme()
	clientgoscheme.AddToScheme(scheme)
	v1alpha2.AddToScheme(scheme)
	v1beta1.AddToScheme(scheme)

	k8sConfig := ctrl.GetConfigOrDie()
	k8sConfig.QPS = float32(cfg.KubeAPIQPS)
	k8sConfig.Burst = cfg.KubeAPIBurst

	mgr, err := ctrl.NewManager(k8sConfig, ctrl.Options{
		Scheme:             scheme,
		MetricsBindAddress: "0",
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

	// Index JobSet name so that we can look up JobSets by name in the position in queue sync loop.
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
