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
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"cloud.google.com/go/pubsub"
	"xmanager_cloud/cmd/metrics_ingester/metricsingester"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/pkg/clickhouse"
)

func main() {
	cfg := &metricsingester.Config{}
	flag.StringVar(&cfg.ProjectID, "project_id", os.Getenv("GOOGLE_CLOUD_PROJECT"), "GCP project ID that owns the subscription.")
	flag.StringVar(&cfg.SubscriptionID, "subscription_id", os.Getenv("PUBSUB_SUBSCRIPTION_ID"), "Pub/Sub subscription ID on the metrics topic.")
	flag.IntVar(&cfg.BatchSize, "batch_size", metricsingester.DefaultBatchSize, "Maximum number of messages per ClickHouse insert.")
	flag.DurationVar(&cfg.BatchInterval, "batch_interval", metricsingester.DefaultBatchInterval, "Target time a received message waits before its batch is inserted; longer if a previous insert is still running.")
	flag.Parse()

	if cfg.ProjectID == "" || cfg.SubscriptionID == "" {
		fmt.Fprintln(os.Stderr, "project_id and subscription_id are required")
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
// failure.
func run(ctx context.Context, cfg *metricsingester.Config) error {
	if cfg.BatchSize <= 0 || cfg.BatchInterval <= 0 {
		return errors.New("batch_size and batch_interval must be positive")
	}
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

	ingester := metricsingester.NewIngester(psClient.Subscription(cfg.SubscriptionID), metricsingester.NewClickHouseWriter(chConn), cfg)
	runCtx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return ingester.Run(runCtx)
}
