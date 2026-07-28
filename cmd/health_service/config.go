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
)

// Config holds the configuration for the health service.
type Config struct {
	Port        string
	GRPCPort    string
	ProjectID   string
	TLSCertFile string
	TLSKeyFile  string
}

var (
	port        string
	grpcPort    string
	projectID   string
	tlsCertFile string
	tlsKeyFile  string
)

func init() {
	defaultPort := os.Getenv("PORT")
	if defaultPort == "" {
		defaultPort = "8080"
	}
	flag.StringVar(&port, "port", defaultPort, "The HTTP server port to listen on.")
	flag.StringVar(&grpcPort, "grpc_port", "50051", "The gRPC server port to listen on.")
	flag.StringVar(&projectID, "project_id", os.Getenv("GOOGLE_CLOUD_PROJECT"), "The GCP project ID.")
	flag.StringVar(&tlsCertFile, "tls_cert_file", "", "The TLS certificate file path.")
	flag.StringVar(&tlsKeyFile, "tls_key_file", "", "The TLS private key file path.")
}

// LoadConfig loads and returns the configuration.
func LoadConfig() *Config {
	return &Config{
		Port:        port,
		GRPCPort:    grpcPort,
		ProjectID:   projectID,
		TLSCertFile: tlsCertFile,
		TLSKeyFile:  tlsKeyFile,
	}
}
