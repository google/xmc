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

	"xmanager_cloud/internal/configutil"
)

// Config holds configuration parameters for rod_data_provider service.
type Config struct {
	Port                string
	GRPCPort            string
	ProjectID           string
	QuotaProjectID      string
	SpannerDatabaseName string
}

var (
	port                string
	grpcPort            string
	projectID           string
	quotaProjectID      string
	spannerDatabaseName string
)

func init() {
	defaultPort := os.Getenv("PORT")
	if defaultPort == "" {
		defaultPort = "8080"
	}
	flag.StringVar(&port, "port", defaultPort, "The HTTP server port to listen on.")
	flag.StringVar(&grpcPort, "grpc_port", "50051", "The gRPC server port to listen on.")
	flag.StringVar(&projectID, "project_id", os.Getenv("GOOGLE_CLOUD_PROJECT"), "The GCP project ID.")
	flag.StringVar(&quotaProjectID, "quota_project_id", os.Getenv("QUOTA_PROJECT_ID"), "The GCP quota project ID.")

	configutil.StringEnv(&spannerDatabaseName, "spanner_database_name", "")
}

// LoadConfig loads and returns service configuration.
func LoadConfig() *Config {
	return &Config{
		Port:                port,
		GRPCPort:            grpcPort,
		ProjectID:           projectID,
		QuotaProjectID:      quotaProjectID,
		SpannerDatabaseName: spannerDatabaseName,
	}
}
