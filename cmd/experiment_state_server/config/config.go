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

	"xmanager_cloud/internal/auth"
	"xmanager_cloud/internal/configutil"
	"xmanager_cloud/internal/featureflags"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/task_queue"
)

// Config holds the configuration for the Experiment State Server.
type Config struct {
	GRPCPort                    string
	HTTPPort                    string
	ProjectID                   string
	QuotaProjectID              string
	XidServiceAddr              string
	EndpointURL                 string
	ActuatorURL                 string
	ActuatorAudience            string
	IAPIssuer                   string
	HTTPAudience                string
	GRPCAudience                string
	TLSCertFile                 string
	TLSKeyFile                  string
	InvokerSA                   string
	DefaultExecutionEnvironment string
	SigningKeyFile              string
	SignatureTTLSeconds         int
	AllowedOrigins              string
	RedisAddr                   string
	DashboardServiceAddr        string
	EnableComponents            bool
	JWKSURL                     string
	FeatureFlags                featureflags.Flags
}

var cfg Config

// init runs when the package is imported. It defines the command-line flags and loads ConfigMaps.
func init() {
	flag.StringVar(&cfg.GRPCPort, "ess_grpc_port", "50051", "The gRPC server port to listen on.")
	flag.StringVar(&cfg.HTTPPort, "ess_http_port", "8080", "The HTTP server port to listen on.")
	flag.StringVar(&cfg.ActuatorAudience, "ess_actuator_audience", "", "The audience for OIDC tokens to authenticate to Actuator.")
	flag.StringVar(&cfg.JWKSURL, "ess_jwks_url", "", "The JWKS endpoint URL for verifying OIDC tokens.")
	flag.StringVar(&cfg.TLSCertFile, "ess_tls_cert_file", "/etc/tls/tls.crt", "Path to the TLS certificate file")
	flag.StringVar(&cfg.TLSKeyFile, "ess_tls_key_file", "/etc/tls/tls.key", "Path to the TLS key file")
	flag.StringVar(&cfg.DefaultExecutionEnvironment, "ess_default_execution_environment", "gke", "The default execution environment to use if not specified in the request.")
	flag.StringVar(&cfg.SigningKeyFile, "ess_signing_key_file", "/etc/signing-key/tls.key", "Path to the messages signing key.")
	flag.BoolVar(&cfg.EnableComponents, "enable_components", false, "Enable the Components Management Service (CMS) in Experiment State Server.")
	cfg.registerConfigs()
}

// registerConfigs loads ConfigMap managed values using configutil.
func (c *Config) registerConfigs() {
	configutil.StringEnv(&c.ProjectID, "ess_gcp_project_id", "")
	configutil.StringEnv(&c.QuotaProjectID, "ess_quota_project_id", "")
	configutil.StringEnv(&c.XidServiceAddr, "ess_xid_service_addr", "localhost:50053")
	configutil.StringEnv(&c.EndpointURL, "ess_endpoint_url", "http://localhost:50051")
	configutil.StringEnv(&c.ActuatorURL, "ess_actuator_url", "https://actuator.xmanager.xmc.svc.cluster.local/actuate")
	configutil.StringEnv(&c.InvokerSA, "ess_service_account", "")
	configutil.IntEnv(&c.SignatureTTLSeconds, "ess_signature_ttl_seconds", client.DefaultSignatureTTLSeconds)
	configutil.StringEnv(&c.AllowedOrigins, "ess_allowed_origins", "")
	configutil.StringEnv(&c.RedisAddr, "redis_addr", "")
	configutil.StringEnv(&c.DashboardServiceAddr, "ess_dashboard_service_addr", "")
	configutil.BoolEnv(&c.EnableComponents, "enable_components", false)
	configutil.StringEnv(&c.JWKSURL, "ess_jwks_url", "")
	c.FeatureFlags = featureflags.Load()
}

// Load returns a Config struct populated with the values from ConfigMaps and parsed flags.
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
