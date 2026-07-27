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
// Package config provides the configuration for the Experiment State Server.
package config

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"xmanager_cloud/auth/auth"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/task_queue/client"
)

// Config holds the configuration for the Experiment State Server.
type Config struct {
	GRPCPort                    string
	HTTPPort                    string
	ProjectID                   string
	QuotaProjectID              string
	SpannerDatabaseName         string
	XidServiceAddr              string
	EndpointURL                 string
	ActuatorURL                 string
	ActuatorAudience            string
	IAPIssuer                   string
	HTTPAudience                string
	GRPCAudience                string
	DisableAuthentication       bool
	TLSCertFile                 string
	TLSKeyFile                  string
	InvokerSA                   string
	DefaultExecutionEnvironment string
	SigningKeyFile              string
	SignatureTTLSeconds         int
	AllowedOrigins              string
	StatusMessageHistoryEnabled bool
	RedisAddr                   string
}

var cfg Config

// init runs when the package is imported. It defines all the command-line flags
// for the application. The actual parsing is triggered in main().
func init() {
	flag.StringVar(&cfg.GRPCPort, "ess_grpc_port", "50051", "The gRPC server port to listen on.")
	flag.StringVar(&cfg.HTTPPort, "ess_http_port", "8080", "The HTTP server port to listen on.")
	flag.StringVar(&cfg.ProjectID, "ess_gcp_project_id", "xmc-test-project-1", "The GCP project ID where the service is running.")
	flag.StringVar(&cfg.QuotaProjectID, "ess_quota_project_id", "xmc-test-project-1", "The ID of the quota project to use.")
	flag.StringVar(
		&cfg.SpannerDatabaseName,
		"ess_spanner_database_name",
		"projects/xmc-test-project-1/instances/xmc/databases/experiment-db",
		"The ID of the Spanner database to use.",
	)
	flag.StringVar(&cfg.XidServiceAddr, "ess_xid_service_addr", "localhost:50053", "The address (host:port) of the XID service.")
	flag.StringVar(&cfg.EndpointURL, "ess_endpoint_url", "http://localhost:50051", "The public-facing URL of this service, for constructing resource names.")
	flag.StringVar(&cfg.ActuatorURL, "ess_actuator_url", "https://actuator.xmanager.xmc.svc.cluster.local/actuate", "The URL of the Actuator service.")
	flag.StringVar(&cfg.ActuatorAudience, "ess_actuator_audience", "", "The audience for OIDC tokens to authenticate to Actuator.")
	flag.StringVar(&cfg.TLSCertFile, "ess_tls_cert_file", "/etc/tls/tls.crt", "Path to the TLS certificate file")
	flag.StringVar(&cfg.TLSKeyFile, "ess_tls_key_file", "/etc/tls/tls.key", "Path to the TLS key file")
	flag.StringVar(&cfg.InvokerSA, "ess_service_account", "", "The service account used to invoke Cloud Tasks.")
	flag.StringVar(&cfg.DefaultExecutionEnvironment, "ess_default_execution_environment", "gke", "The default execution environment to use if not specified in the request.")
	flag.StringVar(&cfg.SigningKeyFile, "ess_signing_key_file", "/etc/signing-key/tls.key", "Path to the messages signing key.")
	flag.IntVar(&cfg.SignatureTTLSeconds, "ess_signature_ttl_seconds", client.DefaultSignatureTTLSeconds, fmt.Sprintf("The expiration time (in seconds) for task signatures. Default: %d seconds.", client.DefaultSignatureTTLSeconds))
	flag.StringVar(&cfg.AllowedOrigins, "ess_allowed_origins", "", "Comma-separated list of allowed origins for CORS.")
	// Feature flags.
	flag.BoolVar(&cfg.StatusMessageHistoryEnabled, "ess_status_message_history_enabled", true, "Whether to enable status message history.")
	flag.StringVar(&cfg.RedisAddr, "redis_addr", "", "The address of the Redis instance.")
}

// Load returns a Config struct populated with the values from the parsed flags.
func Load() Config {
	if err := validate(); err != nil {
		logging.Fatal(context.Background(), "Invalid configuration", "error", err)
	}

	logger := logging.NewGCPLogger(slog.LevelInfo, cfg.ProjectID)
	slog.SetDefault(logger)
	ctx := logging.WithLogger(context.Background(), logger)

	logging.Info(ctx, "Load IAP HTTP Audience", "audience", cfg.HTTPAudience)
	cfg.HTTPAudience = os.Getenv("IAP_EXPECTED_HTTP_AUDIENCE")
	if cfg.HTTPAudience == "" {
		logging.Fatal(ctx, "IAP_EXPECTED_HTTP_AUDIENCE environment variable is not set or empty")
	}
	logging.Info(ctx, "Loaded IAP HTTP Audience", "audience", cfg.HTTPAudience)

	logging.Info(ctx, "Load IAP Issuer")
	cfg.IAPIssuer = os.Getenv("IAP_EXPECTED_ISSUER")
	if cfg.IAPIssuer == "" {
		logging.Warn(ctx, "IAP_EXPECTED_ISSUER environment variable is not set or empty, using default issuer")
		cfg.IAPIssuer = auth.DefaultIAPIssuer
	}
	logging.Info(ctx, "Loaded IAP Issuer", "issuer", cfg.IAPIssuer)

	logging.Info(ctx, "Load IAP GRPC Audience", "audience", cfg.GRPCAudience)
	cfg.GRPCAudience = os.Getenv("IAP_EXPECTED_GRPC_AUDIENCE")
	if cfg.GRPCAudience == "" {
		logging.Fatal(ctx, "IAP_EXPECTED_GRPC_AUDIENCE environment variable is not set or empty")
	}
	logging.Info(ctx, "Loaded IAP GRPC Audience", "audience", cfg.GRPCAudience)
	return cfg
}

func validate() error {
	if cfg.ProjectID == "" {
		return fmt.Errorf("failed to load configuration, project ID is empty")
	}
	if cfg.RedisAddr == "" {
		return fmt.Errorf("-redis_addr must be provided")
	}
	return nil
}
