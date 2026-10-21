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
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	k8sclient "xmanager_cloud/cmd/actuator/k8s_client"
	"xmanager_cloud/internal/configutil"
	"xmanager_cloud/internal/featureflags"
	"xmanager_cloud/internal/kubernetes/common/constants"
	"xmanager_cloud/internal/logging"
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
	Port                   string
	RuntimeConfig          k8sclient.ActuatorRuntimeConfig
	Audience               string
	XManagerServiceAccount string
	JWKSURL                string
	OIDCIssuerURL          string
	VerifyingKeyFile       string
	MaxRecentKeys          int
	RedisConfig            RedisConfig
	FeatureFlags           featureflags.Flags
}

var (
	port                        string
	createNewNamespaces         bool
	spannerDatabaseName         string
	quotaProjectID              string
	xmanagerServiceAccount      string
	audience                    string
	jwksURL                     string
	oidcIssuerURL               string
	defaultExecutionEnvironment string
	verifyingKeyFile            string
	maxRecentKeys               int
	metricsEnabled              bool
	allowPrivilegedContainers   bool
	allowHostNetwork            bool
	enableComponents            bool
	jobSetManagedBy             string
	featureFlags                featureflags.Flags
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
	flag.BoolVar(&allowPrivilegedContainers, "allow_privileged_containers", false, "Whether to allow containers to run in privileged mode when requested.")
	flag.BoolVar(&allowHostNetwork, "allow_host_network", false, "Whether to allow pods to run with host networking when requested.")
	flag.BoolVar(&enableComponents, "enable_components", false, "Whether the components feature is enabled.")
	flag.StringVar(&jobSetManagedBy, "jobset_managed_by", "", "Optional controller name to set on JobSet spec.managedBy (e.g. kueue.x-k8s.io/multikueue).")
	flag.StringVar(&jwksURL, "jwks_url", "", "The JWKS endpoint URL for verifying OIDC tokens.")
	flag.StringVar(&oidcIssuerURL, "oidc_issuer_url", "", "The OIDC issuer URL.")
	registerConfigs()
}

// registerConfigs loads ConfigMap managed values using configutil.
func registerConfigs() {
	configutil.StringEnv(&port, "port", "8080")
	configutil.StringEnv(&spannerDatabaseName, "spanner_database_name", "projects/xmc-test-project-1/instances/xmc/databases/experiment-db")
	configutil.StringEnv(&quotaProjectID, "quota_project_id", "xmc-test-project-1")
	configutil.StringEnv(&xmanagerServiceAccount, "xmanager_service_account", "")
	configutil.StringEnv(&audience, "audience", "")
	configutil.StringEnv(&jwksURL, "jwks_url", "")
	configutil.StringEnv(&oidcIssuerURL, "oidc_issuer_url", "")
	configutil.StringEnv(&redisAddr, "redis_addr", "")
	configutil.IntEnv(&redisMaxRetries, "redis_max_retries", constants.RedisMaxRetries)
	configutil.DurationEnv(&redisProcessTimeout, "redis_process_timeout", constants.RedisProcessTimeout)
	configutil.DurationEnv(&redisAbandonedMessageTimeout, "redis_abandoned_message_timeout", constants.RedisAbandonedMessageTimeout)
	configutil.Int64Env(&redisReadBatchSize, "redis_read_batch_size", constants.RedisReadBatchSize)
	configutil.BoolEnv(&enableComponents, "enable_components", false)
	configutil.StringEnv(&jobSetManagedBy, "jobset_managed_by", "")
	featureFlags = featureflags.Load()
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

	resolvedJWKSURL := jwksURL
	resolvedIssuer := oidcIssuerURL
	if resolvedIssuer == "" {
		resolvedIssuer = os.Getenv("IAP_EXPECTED_ISSUER")
	}
	if resolvedIssuer == "" {
		resolvedIssuer = os.Getenv("OIDC_ISSUER_URL")
	}
	if resolvedJWKSURL == "" && resolvedIssuer != "" {
		resolvedJWKSURL = fmt.Sprintf("%s/keys", strings.TrimSuffix(resolvedIssuer, "/"))
	}

	return Config{
		Port: port,
		RuntimeConfig: k8sclient.ActuatorRuntimeConfig{
			CreateNewNamespaces:         createNewNamespaces,
			SpannerDatabaseName:         spannerDatabaseName,
			QuotaProjectID:              quotaProjectID,
			DefaultExecutionEnvironment: defaultExecutionEnvironment,
			MetricsEnabled:              metricsEnabled,
			AllowPrivilegedContainers:   allowPrivilegedContainers,
			AllowHostNetwork:            allowHostNetwork,
			EnableComponents:            enableComponents,
			JobSetManagedBy:             jobSetManagedBy,
		},
		Audience:               aud,
		XManagerServiceAccount: xmanagerServiceAccount,
		JWKSURL:                resolvedJWKSURL,
		OIDCIssuerURL:          resolvedIssuer,
		VerifyingKeyFile:       verifyingKeyFile,
		MaxRecentKeys:          maxRecentKeys,
		RedisConfig: RedisConfig{
			RedisAddr:                    redisAddr,
			RedisMaxRetries:              redisMaxRetries,
			RedisProcessTimeout:          redisProcessTimeout,
			RedisAbandonedMessageTimeout: redisAbandonedMessageTimeout,
			RedisReadBatchSize:           redisReadBatchSize,
		},
		FeatureFlags: featureFlags,
	}
}
