// Copyright 2026 Google LLC
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
	"os"

	"xmanager_cloud/cmd/dashboard_service/server"
	"xmanager_cloud/internal/configutil"
)

// Config holds the configuration for the dashboard service.
type Config struct {
	Port                string
	GRPCPort            string
	ProjectID           string
	XidServiceAddr      string
	MetricsStore        string
	ComponentsNamespace string
}

var cfg Config

func init() {
	// 1. Load configurations from ConfigMap (via environment variables)
	loadEnvConfig()

	defaultPort := os.Getenv("PORT")
	if defaultPort == "" {
		defaultPort = "8080"
	}

	// 2. Register flags using loaded configurations as default values
	flag.StringVar(&cfg.Port, "port", defaultPort, "The HTTP server port to listen on.")
	flag.StringVar(&cfg.GRPCPort, "grpc_port", "50051", "The gRPC server port to listen on.")
	flag.StringVar(&cfg.ProjectID, "project_id", os.Getenv("GOOGLE_CLOUD_PROJECT"), "The GCP project ID.")
	flag.StringVar(&cfg.MetricsStore, "metrics_store", cfg.MetricsStore, "Metrics storage backend: clickhouse or bigquery (default auto-detect).")
}

func loadEnvConfig() {
	configutil.StringEnv(&cfg.XidServiceAddr, "dashboard_xid_service_addr", "xid-svc:50051")
	configutil.StringEnv(&cfg.MetricsStore, "METRICS_STORE", "")
	configutil.StringEnv(&cfg.ComponentsNamespace, "components_namespace", "xmc")
}

// LoadConfig loads and returns the configuration.
func LoadConfig() *server.Config {
	return &server.Config{
		Port:                cfg.Port,
		GRPCPort:            cfg.GRPCPort,
		ProjectID:           cfg.ProjectID,
		XidServiceAddr:      cfg.XidServiceAddr,
		MetricsStore:        cfg.MetricsStore,
		ComponentsNamespace: cfg.ComponentsNamespace,
	}
}
