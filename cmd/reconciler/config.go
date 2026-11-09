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
	"flag"
	"time"

	"xmanager_cloud/cmd/actuator/k8s_client"
	"xmanager_cloud/internal/configutil"
)

// Config holds the configuration for the Reconciler.
type Config struct {
	RuntimeConfig                    k8sclient.ActuatorRuntimeConfig
	QuotaProjectID                   string
	BatcherTickDuration              time.Duration
	MaxConcurrentReconciles          int
	MaxParallelExperiments           int
	KueueSyncTickDuration            time.Duration
	MaxConcurrentKueueSyncReconciles int
	StartupPodErrorCheckTime         time.Duration
	KubeAPIQPS                       float64
	KubeAPIBurst                     int
	MldiagnosticsEnvironment         string
	MLRunProject                     string
	MLRunLocation                    string
	MLRunImpersonateSA               string
	MetricsPort                      int
	ReconciliationTimeout            time.Duration
}

// Flags and ConfigMap configurations.
var (
	createNewNamespaces              bool
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
	mlRunProject                     string
	mlRunLocation                    string
	mlRunImpersonateSA               string
	metricsPort                      int
	reconciliationTimeout            time.Duration
)

// init runs when the package is imported. It defines CLI flags and loads ConfigMaps.
func init() {
	// CLI Flags
	flag.BoolVar(&createNewNamespaces, "create_new_namespaces", true, "Whether a new namespace can be created when creating new Work Units.")
	flag.IntVar(&maxConcurrentReconciles, "max_concurrent_reconciles", 10, "The maximum number of concurrent reconciles.")
	flag.IntVar(&maxParallelExperiments, "max_parallel_experiments", 10, "The maximum number of goroutines concurrently processing experiment updates.")
	flag.StringVar(&defaultExecutionEnvironment, "default_execution_environment", "gke", "The execution environment to use if not specified in the request.")
	flag.StringVar(&mldiagnosticsEnvironment, "mldiagnostics_environment", "prod", "The ML Diagnostics (Hypercompute Cluster ML Run) control-plane environment: one of autopush, staging, or prod.")
	flag.StringVar(&mlRunProject, "mlrun_project", "", "Optional target GCP project for the Hypercompute Cluster ML Run API (defaults to cluster project).")
	flag.StringVar(&mlRunLocation, "mlrun_location", "", "Optional target location for the Hypercompute Cluster ML Run API (defaults to cluster location). MultiKueue worker clusters must be in this location.")
	flag.StringVar(&mlRunImpersonateSA, "mlrun_impersonate_sa", "", "Optional service account to impersonate for the Hypercompute Cluster ML Run API.")
	flag.IntVar(&metricsPort, "metrics_port", 8082, "The port on which the reconciler serves Prometheus metrics.")
	registerConfigs()
}

// registerConfigs loads ConfigMap managed values using configutil.
func registerConfigs() {
	configutil.StringEnv(&quotaProjectID, "quota_project_id", "")
	configutil.DurationEnv(&batcherTickDuration, "batcher_tick_duration", 250*time.Millisecond)
	configutil.DurationEnv(&kueueSyncTickDuration, "kueue_sync_tick_duration", 60*time.Second)
	configutil.IntEnv(&maxConcurrentKueueSyncReconciles, "max_concurrent_kueue_sync_reconciles", 1000)
	configutil.DurationEnv(&startupPodErrorCheckTime, "startup_pod_error_check_time", 2*time.Minute)
	configutil.Float64Env(&kubeAPIQPS, "kube_api_qps", 100)
	configutil.IntEnv(&kubeAPIBurst, "kube_api_burst", 200)
	configutil.StringEnv(&mlRunProject, "mlrun_project", "")
	configutil.StringEnv(&mlRunLocation, "mlrun_location", "")
	configutil.StringEnv(&mlRunImpersonateSA, "mlrun_impersonate_sa", "")
	configutil.IntEnv(&metricsPort, "metrics_port", 8082)
	configutil.DurationEnv(&reconciliationTimeout, "reconciliation_timeout", 2*time.Minute)
}

// Load returns a Config struct populated with values from ConfigMaps and parsed flags.
func Load() Config {
	return Config{
		RuntimeConfig: k8sclient.ActuatorRuntimeConfig{
			CreateNewNamespaces:         createNewNamespaces,
			DefaultExecutionEnvironment: defaultExecutionEnvironment,
		},
		QuotaProjectID:                   quotaProjectID,
		BatcherTickDuration:              batcherTickDuration,
		MaxConcurrentReconciles:          maxConcurrentReconciles,
		MaxParallelExperiments:           maxParallelExperiments,
		KueueSyncTickDuration:            kueueSyncTickDuration,
		MaxConcurrentKueueSyncReconciles: maxConcurrentKueueSyncReconciles,
		StartupPodErrorCheckTime:         startupPodErrorCheckTime,
		KubeAPIQPS:                       kubeAPIQPS,
		KubeAPIBurst:                     kubeAPIBurst,
		MldiagnosticsEnvironment:         mldiagnosticsEnvironment,
		MLRunProject:                     mlRunProject,
		MLRunLocation:                    mlRunLocation,
		MLRunImpersonateSA:               mlRunImpersonateSA,
		MetricsPort:                      metricsPort,
		ReconciliationTimeout:            reconciliationTimeout,
	}
}
