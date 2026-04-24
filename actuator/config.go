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
package main

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"go/flag"
	"xmanager_cloud/kubernetes/actuator/k8s_client/k8sclient"
	"xmanager_cloud/kubernetes/common/constants"
	"xmanager_cloud/logging/logging"
)

// RedisConfig holds the configuration for the Redis queue.
type RedisConfig struct {
	// RedisAddr is the address of the Redis instance.
	RedisAddr string
	// RedisMaxRetries is the maximum number of retries for a Redis task.
	RedisMaxRetries int
	// RedisProcessTimeout is the timeout for processing a Redis task.
	RedisProcessTimeout time.Duration
	// RedisAbandonedMessageTimeout is the timeout for abandoned Redis messages.
	RedisAbandonedMessageTimeout time.Duration
	// RedisReadBatchSize is the batch size for reading Redis messages.
	RedisReadBatchSize int64
}

// Config holds the configuration for the Actuator service.
type Config struct {
	// Port is the HTTP server port to listen on.
	Port string
	// RuntimeConfig holds runtime configuration for the Actuator.
	RuntimeConfig k8sclient.ActuatorRuntimeConfig
	// BatcherTickDuration is the tick duration for the batch processor.
	BatcherTickDuration time.Duration
	// MaxConcurrentReconciles is the maximum number of concurrent reconciles.
	MaxConcurrentReconciles int
	// MaxParallelExperiments is the maximum number of goroutines concurrently processing experiment updates.
	MaxParallelExperiments int
	// Audience is the audience to use for OIDC authentication.
	Audience string
	// XManagerServiceAccount is the service account to use for OIDC authentication.
	XManagerServiceAccount string
	// VerifyingKeyFile is the path to the key for verifying message signatures.
	VerifyingKeyFile string
	// KueueSyncTickDuration is the tick duration for kueue sync.
	KueueSyncTickDuration time.Duration
	// MaxConcurrentKueueSyncReconciles is the maximum number of concurrent work unit updates in kueue sync.
	MaxConcurrentKueueSyncReconciles int
	// MaxRecentKeys is the maximum number of recent keys to keep for key rotation.
	MaxRecentKeys int
	// DetectInadmissibleWorkUnits is whether to detect inadmissible work units.
	DetectInadmissibleWorkUnits bool
	// StartupPodErrorCheckTime is the time to wait before checking for pod errors if jobset is stuck on startup.
	StartupPodErrorCheckTime time.Duration
	// StartupPodErrorCheckEnabled is feature flag: Enabling check for pod errors when jobset startup takes longer than StartupPodErrorCheckTime.
	StartupPodErrorCheckEnabled bool
	// StatusMessageHistoryEnabled is feature flag: Whether to enable status message history.
	StatusMessageHistoryEnabled bool
	// KubeAPIQPS is the maximum QPS to the API server.
	KubeAPIQPS float64
	// KubeAPIBurst is the maximum burst for throttle.
	KubeAPIBurst int
	// UseRedisQueue is feature flag: Whether to use Redis queue.
	UseRedisQueue bool
	// RedisConfig is the configuration for the Redis queue.
	RedisConfig RedisConfig
}

var (
	port                             string
	createNewNamespaces              bool
	detectInadmissibleWorkUnits      bool
	spannerDatabaseName              string
	quotaProjectID                   string
	batcherTickDuration              time.Duration
	maxConcurrentReconciles          int
	maxParallelExperiments           int
	xmanagerServiceAccount           string
	audience                         string
	defaultExecutionEnvironment      string
	verifyingKeyFile                 string
	kueueSyncTickDuration            time.Duration
	maxConcurrentKueueSyncReconciles int
	maxRecentKeys                    int
	startupPodErrorCheckTime         time.Duration
	startupPodErrorCheckEnabled      bool
	kubeAPIQPS                       float64
	kubeAPIBurst                     int
	useRedisQueue                    bool
	statusMessageHistoryEnabled      bool
)

// Redis flags.
var (
	redisAddr                    string
	redisMaxRetries              int
	redisProcessTimeout          time.Duration
	redisAbandonedMessageTimeout time.Duration
	redisReadBatchSize           int64
)

func init() {
	flag.StringVar(&port, "port", "8080", "The HTTP server port to listen on.")
	flag.BoolVar(&createNewNamespaces, "create_new_namespaces", true, "Whether a new namespace can be created when creating new Work Units.")
	flag.StringVar(&spannerDatabaseName, "spanner_database_name", "projects/xmc-test-project-1/instances/xmc/databases/experiment-db", "The name of the Spanner database.")
	flag.StringVar(&quotaProjectID, "quota_project_id", "xmc-test-project-1", "The ID of the quota project to use.")
	flag.DurationVar(&batcherTickDuration, "batcher_tick_duration", 250*time.Millisecond, "The tick duration for the batch processor.")
	flag.IntVar(&maxConcurrentReconciles, "max_concurrent_reconciles", 10, "The maximum number of concurrent reconciles.")
	flag.IntVar(&maxParallelExperiments, "max_parallel_experiments", 10, "The maximum number of goroutines concurrently processing experiment updates.")
	flag.StringVar(&xmanagerServiceAccount, "xmanager_service_account", "", "The service account to use for OIDC authentication.")
	flag.StringVar(&audience, "audience", "", "The audience to use for OIDC authentication. If empty, it defaults to https://actuator.xmanager.svc.cluster.local:<port>.")
	flag.StringVar(&defaultExecutionEnvironment, "default_execution_environment", "gke", "The execution environment to use if not specified in the request.")
	flag.StringVar(&verifyingKeyFile, "verifying_key_file", "/etc/verification-key/tls.crt", "Path to the key for verifying message signatures.")
	flag.DurationVar(&kueueSyncTickDuration, "kueue_sync_tick_duration", 60*time.Second, "The tick duration for kueue sync.")
	flag.IntVar(&maxConcurrentKueueSyncReconciles, "max_concurrent_kueue_sync_reconciles", 1000, "The maximum number of concurrent work unit updates in kueue sync.")
	flag.IntVar(&maxRecentKeys, "max_recent_keys", 2, "The maximum number of recent keys to keep for key rotation.")
	flag.BoolVar(&detectInadmissibleWorkUnits, "detect_inadmissible_work_units", false, "Whether to detect inadmissible work units by inspecting Workload objects.")
	flag.DurationVar(&startupPodErrorCheckTime, "startup_pod_error_check_time", 2*time.Minute, "The time to wait before checking for pod errors during startup.")
	flag.BoolVar(&startupPodErrorCheckEnabled, "startup_pod_error_check_enabled", false, "Whether to enable checking for pod errors during startup.")
	flag.Float64Var(&kubeAPIQPS, "kube_api_qps", 100, "Maximum QPS to the API server.")
	flag.IntVar(&kubeAPIBurst, "kube_api_burst", 200, "Maximum burst for throttle.")
	flag.BoolVar(&useRedisQueue, "use_redis_queue", false, "Whether to use Redis queue.")
	flag.BoolVar(&statusMessageHistoryEnabled, "status_message_history_enabled", false, "Whether to enable status message history.")
	initRedisFlags()
}

func initRedisFlags() {
	flag.StringVar(&redisAddr, "redis_addr", "", "The address of the Redis instance.")
	flag.IntVar(&redisMaxRetries, "redis_max_retries", constants.RedisMaxRetries, "The maximum number of retries for a Redis task.")
	flag.DurationVar(&redisProcessTimeout, "redis_process_timeout", constants.RedisProcessTimeout, "The timeout for processing a Redis task.")
	flag.DurationVar(&redisAbandonedMessageTimeout, "redis_abandoned_message_timeout", constants.RedisAbandonedMessageTimeout, "The timeout for abandoned Redis messages.")
	flag.Int64Var(&redisReadBatchSize, "redis_read_batch_size", constants.RedisReadBatchSize, "The batch size for reading Redis messages.")
}

// validate ensures that the parsed flag values are valid.
func validate() error {
	if p, err := strconv.Atoi(port); err != nil || p <= 0 || p > 65535 {
		return fmt.Errorf("invalid -port: %q; must be an integer between 1 and 65535", port)
	}
	if xmanagerServiceAccount == "" {
		return fmt.Errorf("missing -xmanager_service_account")
	}
	if useRedisQueue && redisAddr == "" {
		return fmt.Errorf("-redis_addr must be provided if -use_redis_queue is true")
	}
	if !useRedisQueue && redisAddr != "" {
		return fmt.Errorf("-redis_addr must not be provided if -use_redis_queue is false")
	}

	return nil
}

// Load returns a Config struct populated with the values from the parsed flags.
func Load() Config {
	ctx := context.Background()
	if err := validate(); err != nil {
		logging.Fatal(ctx, "Invalid configuration", "error", err)
	}

	aud := audience
	if aud == "" {
		logging.Warn(ctx, "Audience is not set, using default")
		aud = fmt.Sprintf("https://actuator.xmanager.svc.cluster.local:%s", port)
	}

	return Config{
		Port: port,
		RuntimeConfig: k8sclient.ActuatorRuntimeConfig{
			CreateNewNamespaces:         createNewNamespaces,
			SpannerDatabaseName:         spannerDatabaseName,
			QuotaProjectID:              quotaProjectID,
			DefaultExecutionEnvironment: defaultExecutionEnvironment,
		},
		BatcherTickDuration:              batcherTickDuration,
		MaxConcurrentReconciles:          maxConcurrentReconciles,
		MaxParallelExperiments:           maxParallelExperiments,
		Audience:                         aud,
		XManagerServiceAccount:           xmanagerServiceAccount,
		VerifyingKeyFile:                 verifyingKeyFile,
		KueueSyncTickDuration:            kueueSyncTickDuration,
		MaxConcurrentKueueSyncReconciles: maxConcurrentKueueSyncReconciles,
		MaxRecentKeys:                    maxRecentKeys,
		DetectInadmissibleWorkUnits:      detectInadmissibleWorkUnits,
		StartupPodErrorCheckTime:         startupPodErrorCheckTime,
		StartupPodErrorCheckEnabled:      startupPodErrorCheckEnabled,
		KubeAPIQPS:                       kubeAPIQPS,
		KubeAPIBurst:                     kubeAPIBurst,
		UseRedisQueue:                    useRedisQueue,
		StatusMessageHistoryEnabled:      statusMessageHistoryEnabled,
		RedisConfig: RedisConfig{
			RedisAddr:                    redisAddr,
			RedisMaxRetries:              redisMaxRetries,
			RedisProcessTimeout:          redisProcessTimeout,
			RedisAbandonedMessageTimeout: redisAbandonedMessageTimeout,
			RedisReadBatchSize:           redisReadBatchSize,
		},
	}
}
