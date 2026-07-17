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
	"time"

	"go/flag"
	k8sclient "xmanager_cloud/cmd/actuator/k8s_client"
)

// Config holds the configuration for the Reconciler.
type Config struct {
	RuntimeConfig                    k8sclient.ActuatorRuntimeConfig
	BatcherTickDuration              time.Duration
	MaxConcurrentReconciles          int
	MaxParallelExperiments           int
	KueueSyncTickDuration            time.Duration
	MaxConcurrentKueueSyncReconciles int
	DetectInadmissibleWorkUnits      bool
	StartupPodErrorCheckTime         time.Duration
	StartupPodErrorCheckEnabled      bool
	StatusMessageHistoryEnabled      bool
	KubeAPIQPS                       float64
	KubeAPIBurst                     int
}

// flags
var (
	createNewNamespaces              bool
	detectInadmissibleWorkUnits      bool
	spannerDatabaseName              string
	quotaProjectID                   string
	batcherTickDuration              time.Duration
	maxConcurrentReconciles          int
	maxParallelExperiments           int
	defaultExecutionEnvironment      string
	kueueSyncTickDuration            time.Duration
	maxConcurrentKueueSyncReconciles int
	startupPodErrorCheckTime         time.Duration
	startupPodErrorCheckEnabled      bool
	kubeAPIQPS                       float64
	kubeAPIBurst                     int
	statusMessageHistoryEnabled      bool
)

func init() {
	flag.BoolVar(&createNewNamespaces, "create_new_namespaces", true, "Whether a new namespace can be created when creating new Work Units.")
	flag.BoolVar(&detectInadmissibleWorkUnits, "detect_inadmissible_work_units", true, "Whether to detect inadmissible work units by inspecting Workload objects.")
	flag.StringVar(&spannerDatabaseName, "spanner_database_name", "projects/xmc-test-project-1/instances/xmc/databases/experiment-db", "The name of the Spanner database.")
	flag.StringVar(&quotaProjectID, "quota_project_id", "xmc-test-project-1", "The ID of the quota project to use.")
	flag.DurationVar(&batcherTickDuration, "batcher_tick_duration", 250*time.Millisecond, "The tick duration for the batch processor.")
	flag.IntVar(&maxConcurrentReconciles, "max_concurrent_reconciles", 10, "The maximum number of concurrent reconciles.")
	flag.IntVar(&maxParallelExperiments, "max_parallel_experiments", 10, "The maximum number of goroutines concurrently processing experiment updates.")
	flag.StringVar(&defaultExecutionEnvironment, "default_execution_environment", "gke", "The execution environment to use if not specified in the request.")
	flag.DurationVar(&kueueSyncTickDuration, "kueue_sync_tick_duration", 60*time.Second, "The tick duration for kueue sync.")
	flag.IntVar(&maxConcurrentKueueSyncReconciles, "max_concurrent_kueue_sync_reconciles", 1000, "The maximum number of concurrent work unit updates in kueue sync.")
	flag.DurationVar(&startupPodErrorCheckTime, "startup_pod_error_check_time", 2*time.Minute, "The time to wait before checking for pod errors during startup.")
	flag.BoolVar(&startupPodErrorCheckEnabled, "startup_pod_error_check_enabled", true, "Whether to enable checking for pod errors during startup.")
	flag.Float64Var(&kubeAPIQPS, "kube_api_qps", 100, "Maximum QPS to the API server.")
	flag.IntVar(&kubeAPIBurst, "kube_api_burst", 200, "Maximum burst for throttle.")
	flag.BoolVar(&statusMessageHistoryEnabled, "status_message_history_enabled", true, "Whether to enable status message history.")
}

func Load() Config {
	return Config{
		RuntimeConfig: k8sclient.ActuatorRuntimeConfig{
			CreateNewNamespaces:         createNewNamespaces,
			SpannerDatabaseName:         spannerDatabaseName,
			QuotaProjectID:              quotaProjectID,
			DefaultExecutionEnvironment: defaultExecutionEnvironment,
		},
		BatcherTickDuration:              batcherTickDuration,
		MaxConcurrentReconciles:          maxConcurrentReconciles,
		MaxParallelExperiments:           maxParallelExperiments,
		KueueSyncTickDuration:            kueueSyncTickDuration,
		MaxConcurrentKueueSyncReconciles: maxConcurrentKueueSyncReconciles,
		DetectInadmissibleWorkUnits:      detectInadmissibleWorkUnits,
		StartupPodErrorCheckTime:         startupPodErrorCheckTime,
		StartupPodErrorCheckEnabled:      startupPodErrorCheckEnabled,
		KubeAPIQPS:                       kubeAPIQPS,
		KubeAPIBurst:                     kubeAPIBurst,
		StatusMessageHistoryEnabled:      statusMessageHistoryEnabled,
	}
}
