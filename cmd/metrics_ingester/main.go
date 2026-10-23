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

// Package main provides the entrypoint binary for the Metrics Ingester service.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"cloud.google.com/go/pubsub"
	"xmanager_cloud/cmd/metrics_ingester/metricsingester"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/pkg/clickhouse"
)

func main() {
	flags := metricsingester.RegisterFlags(flag.CommandLine)
	flag.Parse()

	cfg, err := metricsingester.LoadConfig(flags)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		os.Exit(1)
	}

	logger := logging.NewGCPLogger(slog.LevelInfo, cfg.ProjectID)
	slog.SetDefault(logger)
	ctx := logging.WithLogger(context.Background(), logger)
	logging.Info(ctx, "Initializing Metrics Ingester service", "project", cfg.ProjectID, "subscription", cfg.SubscriptionID,
		"batch_size", cfg.BatchSize, "batch_interval", cfg.BatchInterval)

	if err := run(ctx, cfg); err != nil {
		logging.Fatal(ctx, "Metrics Ingester stopped", "error", err)
	}
}

// run connects both ends of the pipeline and ingests until SIGINT/SIGTERM or a permanent receive
// failure. Dependencies are checked up front so misconfiguration fails at startup.
func run(ctx context.Context, cfg *metricsingester.Config) error {
	chConn, err := clickhouse.Connect(ctx)
	if err != nil {
		return fmt.Errorf("failed to connect to clickhouse: %w", err)
	}
	defer chConn.Close()

	psClient, err := pubsub.NewClient(ctx, cfg.ProjectID)
	if err != nil {
		return fmt.Errorf("failed to create pub/sub client: %w", err)
	}
	defer psClient.Close()

	sub, err := metricsingester.OpenSubscription(ctx, psClient, cfg)
	if err != nil {
		return err
	}

	ingester := metricsingester.NewIngester(sub, metricsingester.NewClickHouseWriter(chConn), cfg)
	server := startHealthServer(ctx, cfg.Port, metricsingester.NewHealthMux(ingester, chConn))
	defer shutdownHealthServer(ctx, server)

	runCtx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return ingester.Run(runCtx)
}

func startHealthServer(ctx context.Context, port string, handler http.Handler) *http.Server {
	server := &http.Server{Addr: ":" + port, Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		logging.Info(ctx, "Health server listening", "port", port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logging.Fatal(ctx, "Health server listener error", "error", err)
		}
	}()
	return server
}

func shutdownHealthServer(ctx context.Context, server *http.Server) {
	logging.Info(ctx, "Shutting down Metrics Ingester")
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logging.Error(ctx, "Health server shutdown error", "error", err)
	}
}
