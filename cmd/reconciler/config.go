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
	"xmanager_cloud/internal/configutil"
	"xmanager_cloud/kubernetes/actuator/k8s_client/k8sclient"
)

// Config holds the configuration for the Reconciler.
type Config struct {
	RuntimeConfig                    k8sclient.ActuatorRuntimeConfig
	BatcherTickDuration              time.Duration
	MaxConcurrentReconciles          int
	MaxParallelExperiments           int
	KueueSyncTickDuration            time.Duration
	MaxConcurrentKueueSyncReconciles int
	StartupPodErrorCheckTime         time.Duration
	KubeAPIQPS                       float64
	KubeAPIBurst                     int
	MldiagnosticsEnvironment         string
	MetricsPort                      int
}

// Flags and ConfigMap configurations.
var (
	createNewNamespaces              bool
	spannerDatabaseName              string
	quotaProjectID                   string
	batcherTickDuration              time.Duration
	maxConcurrentReconciles          int
	maxParallelExperiments           int
	defaultExecutionEnvironment      string
	kueueSyncTickDuration            time.Duration
	maxConcurrentKueueSyncReconciles int
	startupPodErrorCheckTime         time.Duration
	kubeAPIQPS                       float64
	kubeAPIBurst                     int
	mldiagnosticsEnvironment         string
	metricsPort                      int
)

// init runs when the package is imported. It defines CLI flags and loads ConfigMaps.
func init() {
	// CLI Flags
	flag.BoolVar(&createNewNamespaces, "create_new_namespaces", true, "Whether a new namespace can be created when creating new Work Units.")
	flag.IntVar(&maxConcurrentReconciles, "max_concurrent_reconciles", 10, "The maximum number of concurrent reconciles.")
	flag.IntVar(&maxParallelExperiments, "max_parallel_experiments", 10, "The maximum number of goroutines concurrently processing experiment updates.")
	flag.StringVar(&defaultExecutionEnvironment, "default_execution_environment", "gke", "The execution environment to use if not specified in the request.")
	flag.StringVar(&mldiagnosticsEnvironment, "mldiagnostics_environment", "prod", "The ML Diagnostics (Hypercompute Cluster ML Run) control-plane environment: one of autopush, staging, or prod.")
	flag.IntVar(&metricsPort, "metrics_port", 8082, "The port on which the reconciler serves Prometheus metrics.")
	registerConfigs()
}

// registerConfigs loads ConfigMap managed values using configutil.
func registerConfigs() {
	configutil.StringEnv(&spannerDatabaseName, "spanner_database_name", "projects/xmc-test-project-1/instances/xmc/databases/experiment-db")
	configutil.StringEnv(&quotaProjectID, "quota_project_id", "xmc-test-project-1")
	configutil.DurationEnv(&batcherTickDuration, "batcher_tick_duration", 250*time.Millisecond)
	configutil.DurationEnv(&kueueSyncTickDuration, "kueue_sync_tick_duration", 60*time.Second)
	configutil.IntEnv(&maxConcurrentKueueSyncReconciles, "max_concurrent_kueue_sync_reconciles", 1000)
	configutil.DurationEnv(&startupPodErrorCheckTime, "startup_pod_error_check_time", 2*time.Minute)
	configutil.Float64Env(&kubeAPIQPS, "kube_api_qps", 100)
	configutil.IntEnv(&kubeAPIBurst, "kube_api_burst", 200)
	configutil.IntEnv(&metricsPort, "metrics_port", 8082)
}

// Load returns a Config struct populated with values from ConfigMaps and parsed flags.
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
		StartupPodErrorCheckTime:         startupPodErrorCheckTime,
		KubeAPIQPS:                       kubeAPIQPS,
		KubeAPIBurst:                     kubeAPIBurst,
		MldiagnosticsEnvironment:         mldiagnosticsEnvironment,
		MetricsPort:                      metricsPort,
	}
}
