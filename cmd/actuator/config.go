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
	"fmt"
	"strconv"
	"time"

	"go/flag"
	"xmanager_cloud/internal/configutil"
	"xmanager_cloud/kubernetes/actuator/k8s_client/k8sclient"
	"xmanager_cloud/kubernetes/common/constants"
	"xmanager_cloud/logging/logging"
)

// RedisConfig holds the configuration for the Redis queue.
type RedisConfig struct {
	RedisAddr                    string
	RedisMaxRetries              int
	RedisProcessTimeout          time.Duration
	RedisAbandonedMessageTimeout time.Duration
	RedisReadBatchSize           int64
}

// Config holds the configuration for the Actuator server.
type Config struct {
	Port                        string
	RuntimeConfig               k8sclient.ActuatorRuntimeConfig
	Audience                    string
	XManagerServiceAccount      string
	VerifyingKeyFile            string
	MaxRecentKeys               int
	StatusMessageHistoryEnabled bool
	RedisConfig                 RedisConfig
}

var (
	port                        string
	createNewNamespaces         bool
	spannerDatabaseName         string
	quotaProjectID              string
	xmanagerServiceAccount      string
	audience                    string
	defaultExecutionEnvironment string
	verifyingKeyFile            string
	maxRecentKeys               int
	statusMessageHistoryEnabled bool
	metricsEnabled              bool
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
	// Register actual CLI flags.
	flag.BoolVar(&createNewNamespaces, "create_new_namespaces", true, "Whether a new namespace can be created when creating new Work Units.")
	flag.StringVar(&defaultExecutionEnvironment, "default_execution_environment", "gke", "The execution environment to use if not specified in the request.")
	flag.StringVar(&verifyingKeyFile, "verifying_key_file", "/etc/verification-key/tls.crt", "Path to the key for verifying message signatures.")
	flag.IntVar(&maxRecentKeys, "max_recent_keys", 2, "The maximum number of recent keys to keep for key rotation.")
	flag.BoolVar(&metricsEnabled, "metrics_enabled", false, "Whether to enable XMC metrics in jobs.")
	registerConfigs()
}

// registerConfigs loads ConfigMap managed values using configutil.
func registerConfigs() {
	configutil.StringEnv(&port, "port", "8080")
	configutil.StringEnv(&spannerDatabaseName, "spanner_database_name", "projects/xmc-test-project-1/instances/xmc/databases/experiment-db")
	configutil.StringEnv(&quotaProjectID, "quota_project_id", "xmc-test-project-1")
	configutil.StringEnv(&xmanagerServiceAccount, "xmanager_service_account", "")
	configutil.StringEnv(&audience, "audience", "")
	configutil.BoolEnv(&statusMessageHistoryEnabled, "status_message_history_enabled", true)
	configutil.StringEnv(&redisAddr, "redis_addr", "")
	configutil.IntEnv(&redisMaxRetries, "redis_max_retries", constants.RedisMaxRetries)
	configutil.DurationEnv(&redisProcessTimeout, "redis_process_timeout", constants.RedisProcessTimeout)
	configutil.DurationEnv(&redisAbandonedMessageTimeout, "redis_abandoned_message_timeout", constants.RedisAbandonedMessageTimeout)
	configutil.Int64Env(&redisReadBatchSize, "redis_read_batch_size", constants.RedisReadBatchSize)
}

func validate() error {
	if p, err := strconv.Atoi(port); err != nil || p <= 0 || p > 65535 {
		return fmt.Errorf("invalid -port: %q; must be an integer between 1 and 65535", port)
	}
	if xmanagerServiceAccount == "" {
		return fmt.Errorf("missing -xmanager_service_account")
	}
	if redisAddr == "" {
		return fmt.Errorf("-redis_addr must be provided")
	}
	return nil
}

// Load returns a Config struct populated with the values from ConfigMaps and parsed flags.
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
			MetricsEnabled:              metricsEnabled,
		},
		Audience:                    aud,
		XManagerServiceAccount:      xmanagerServiceAccount,
		VerifyingKeyFile:            verifyingKeyFile,
		MaxRecentKeys:               maxRecentKeys,
		StatusMessageHistoryEnabled: statusMessageHistoryEnabled,
		RedisConfig: RedisConfig{
			RedisAddr:                    redisAddr,
			RedisMaxRetries:              redisMaxRetries,
			RedisProcessTimeout:          redisProcessTimeout,
			RedisAbandonedMessageTimeout: redisAbandonedMessageTimeout,
			RedisReadBatchSize:           redisReadBatchSize,
		},
	}
}
