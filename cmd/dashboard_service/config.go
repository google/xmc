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
)

var (
	port                string
	grpcPort            string
	projectID           string
	spannerDatabaseName string
	xidServiceAddr      string
)

func init() {
	flag.StringVar(&port, "port", "", "The HTTP server port to listen on.")
	flag.StringVar(&grpcPort, "grpc_port", "", "The gRPC server port to listen on.")
	flag.StringVar(&projectID, "project_id", "", "The GCP project ID.")
	flag.StringVar(&spannerDatabaseName, "spanner_database_name", "", "The Spanner database resource name.")
	flag.StringVar(&xidServiceAddr, "xid_service_addr", "", "The XID service gRPC address.")
}

func getSpannerDatabaseName() string {
	if spannerDatabaseName != "" {
		return spannerDatabaseName
	}
	return os.Getenv("SPANNER_DATABASE_NAME")
}

// LoadConfig loads and returns the configuration.
func LoadConfig() *server.Config {
	p := port
	if p == "" {
		p = os.Getenv("PORT")
	}
	if p == "" {
		p = "8080"
	}

	gp := grpcPort
	if gp == "" {
		gp = os.Getenv("GRPC_PORT")
	}
	if gp == "" {
		gp = "50051"
	}

	proj := projectID
	if proj == "" {
		proj = os.Getenv("GOOGLE_CLOUD_PROJECT")
	}

	xidAddr := xidServiceAddr
	if xidAddr == "" {
		xidAddr = os.Getenv("XID_SERVICE_ADDR")
	}
	if xidAddr == "" {
		xidAddr = "localhost:8082"
	}

	return &server.Config{
		Port:           p,
		GRPCPort:       gp,
		ProjectID:      proj,
		XidServiceAddr: xidAddr,
	}
}
