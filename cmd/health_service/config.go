// Copyright 2026 Google LLC
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
	"flag"
	"os"

	"xmanager_cloud/internal/configutil"
)

// Config holds the configuration for the health service.
type Config struct {
	Port          string
	GRPCPort      string
	ProjectID     string
	TLSCertFile   string
	TLSKeyFile    string
	EssURL        string
	XidAddr       string
	ActuatorURL   string
	ReconcilerURL string
}

var cfg Config

func init() {
	// 1. Load configurations from environment variables
	configutil.StringEnv(&cfg.EssURL, "health_ess_url", "http://http-service:8080")
	configutil.StringEnv(&cfg.XidAddr, "health_xid_addr", "xid-svc:50051")
	configutil.StringEnv(&cfg.ActuatorURL, "health_actuator_url", "http://actuator-service:8080")
	configutil.StringEnv(&cfg.ReconcilerURL, "health_reconciler_url", "http://reconciler-svc:8080")

	defaultPort := os.Getenv("PORT")
	if defaultPort == "" {
		defaultPort = "8080"
	}

	// 2. Register flags using loaded configurations as default values
	flag.StringVar(&cfg.Port, "port", defaultPort, "The HTTP server port to listen on.")
	flag.StringVar(&cfg.GRPCPort, "grpc_port", "50051", "The gRPC server port to listen on.")
	flag.StringVar(&cfg.ProjectID, "project_id", os.Getenv("GOOGLE_CLOUD_PROJECT"), "The GCP project ID.")
	flag.StringVar(&cfg.TLSCertFile, "tls_cert_file", "", "The TLS certificate file path.")
	flag.StringVar(&cfg.TLSKeyFile, "tls_key_file", "", "The TLS private key file path.")

	flag.StringVar(&cfg.EssURL, "ess_url", cfg.EssURL, "The URL of the Experiment State Server.")
	flag.StringVar(&cfg.XidAddr, "xid_addr", cfg.XidAddr, "The address of the XID service.")
	flag.StringVar(&cfg.ActuatorURL, "actuator_url", cfg.ActuatorURL, "The URL of the Actuator service.")
	flag.StringVar(&cfg.ReconcilerURL, "reconciler_url", cfg.ReconcilerURL, "The URL of the Reconciler service.")
}

// LoadConfig loads and returns the configuration.
func LoadConfig() *Config {
	return &cfg
}
