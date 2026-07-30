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

// Package config provides functionality to load application configuration from command-line flags and ConfigMaps.
package config

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strconv"

	"go/log"
	"xmanager_cloud/auth/auth"
	"xmanager_cloud/internal/configutil"
	"xmanager_cloud/logging/logging"
)

// Config holds the configuration for the XidService server.
type Config struct {
	Port                  string
	SpannerDatabaseName   string
	QuotaProjectID        string
	Audience              string
	IAPIssuer             string
	HTTPAudience          string
	GRPCAudience          string
	DisableAuthentication bool
}

var cfg Config

// init runs when the package is imported. It defines the command-line flags and loads ConfigMaps.
func init() {
	cfg.registerFlags(nil)
	cfg.registerConfigs()
	if err := cfg.validate(); err != nil {
		log.Fatalf("Invalid configuration: %v", err)
	}
}

// Load returns a Config struct populated with values from ConfigMaps/envs and parsed flags.
func Load() Config {
	logger := logging.NewGCPLogger(slog.LevelInfo, cfg.QuotaProjectID)
	slog.SetDefault(logger)
	ctx := logging.WithLogger(context.Background(), logger)

	logging.Info(ctx, "Load IAP Issuer")
	cfg.IAPIssuer = os.Getenv("IAP_EXPECTED_ISSUER")
	if cfg.IAPIssuer == "" {
		logging.Warn(ctx, "IAP_EXPECTED_ISSUER environment variable is not set or empty, using default issuer")
		cfg.IAPIssuer = auth.DefaultIAPIssuer
	}
	logging.Info(ctx, "Loaded IAP Issuer", "issuer", cfg.IAPIssuer)

	cfg.HTTPAudience = os.Getenv("IAP_EXPECTED_HTTP_AUDIENCE")
	if cfg.HTTPAudience == "" {
		logging.Fatal(ctx, "IAP_EXPECTED_HTTP_AUDIENCE environment variable is not set or empty")
	}

	cfg.GRPCAudience = os.Getenv("IAP_EXPECTED_GRPC_AUDIENCE")
	if cfg.GRPCAudience == "" {
		logging.Fatal(ctx, "IAP_EXPECTED_GRPC_AUDIENCE environment variable is not set or empty")
	}

	return cfg
}

// registerFlags registers only the flags that are NOT managed by ConfigMaps.
func (c *Config) registerFlags(fs *flag.FlagSet) {
	if fs == nil {
		fs = flag.CommandLine
	}
	fs.BoolVar(&c.DisableAuthentication, "xid_disable_authentication", false, "Disables authentication for testing.")
}

// registerConfigs loads ConfigMap managed values using configutil.
func (c *Config) registerConfigs() {
	configutil.StringVar(&c.Port, "xid_grpc_port", "50051")
	configutil.StringVar(&c.QuotaProjectID, "xid_quota_project_id", "xmc-test-project-1")
	configutil.StringVar(&c.SpannerDatabaseName, "xid_spanner_database_name", "projects/xmc-test-project-1/instances/xmc/databases/xids-db")
	configutil.StringVar(&c.Audience, "xid_audience", "https://test-xmanager-gcp.corp.goog")
}

// validate checks if the Config is valid.
func (c Config) validate() error {
	portNum, err := strconv.Atoi(c.Port)
	if err != nil {
		log.Errorf("Invalid port %q: must be a number", c.Port)
		return fmt.Errorf("invalid port %q: must be a number", c.Port)
	}
	if portNum < 1 || portNum > 65535 {
		log.Errorf("Invalid port %d: must be between 1 and 65535", portNum)
		return fmt.Errorf("invalid port %d: must be between 1 and 65535", portNum)
	}

	return nil
}
