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
// Package main implements the Actuator Server for XManager Cloud.
package main

import (
	"flag"
	"fmt"
	"log/slog"
	"net/http"

	"xmanager_cloud/logging/logging"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	ctrl "sigs.k8s.io/controller-runtime"
)

func main() {
	flag.Parse()
	ctx := ctrl.SetupSignalHandler()

	cfg := Load()

	gceMeta, err := getGceMetadata(ctx)
	if err != nil {
		logging.Fatal(ctx, "Failed to get GCE Metadata", "error", err)
	}
	projectID := gceMeta.ProjectID

	logger := logging.NewGCPLogger(slog.LevelInfo, projectID)
	slog.SetDefault(logger)
	ctx = logging.WithLogger(ctx, logger)

	prodServer, err := NewProd(ctx, &cfg, projectID)
	if err != nil {
		logging.Fatal(ctx, "Failed to initialize production server", "error", err)
	}
	defer func() {
		if err := prodServer.Close(); err != nil {
			logging.Warn(ctx, "Failed to close production server", "error", err)
		}
	}()

	mux := setupMux(prodServer)

	addr := fmt.Sprintf(":%s", cfg.Port)
	logging.Info(ctx, "Actuator server listening on", "addr", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		logging.Fatal(ctx, "Server failed to start", "error", err)
	}
}

// setupMux configures the HTTP routes for the actuator service.
func setupMux(prodServer *ProdServer) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", livenessCheck())
	if prodServer != nil {
		mux.HandleFunc("/readyz", activeHealthCheck(prodServer.HealthCheckers()))
	} else {
		mux.HandleFunc("/readyz", activeHealthCheck(nil, nil, nil))
	}
	mux.Handle("/metrics", promhttp.Handler())
	return mux
}
