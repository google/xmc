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

// Package main implements the Ingestion Gateway service for XMC Metrics.
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

	"github.com/twmb/franz-go/pkg/kadm"
	"go/google"
	"xmanager_cloud/logging/logging"
)

func main() {
	google.Init()
	flag.Parse()

	cfg, err := LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		os.Exit(1)
	}

	logger := logging.NewGCPLogger(slog.LevelInfo, cfg.ProjectID)
	ctx := logging.WithLogger(context.Background(), logger)

	logging.Info(ctx, "Initializing Ingestion Gateway service", "brokers", cfg.RedpandaBrokers, "topic", cfg.TopicName)

	gMetrics := NewGatewayMetrics(nil)

	kClient, err := NewKafkaProducer(cfg, gMetrics)
	if err != nil {
		logging.Fatal(ctx, "failed to create franz-go client", "error", err)
	}
	defer kClient.Close()

	adminClient := kadm.NewClient(kClient)
	if err := ValidateTopicTopology(ctx, adminClient, cfg.TopicName, cfg.MinPartitions, cfg.MinReplicas); err != nil {
		logging.Fatal(ctx, "topic topology validation failed", "error", err)
	}
	logging.Info(ctx, "Topic topology validation passed", "topic", cfg.TopicName, "min_partitions", cfg.MinPartitions, "min_replicas", cfg.MinReplicas)

	server := startHTTPServer(ctx, cfg.Port, cfg.TopicName, kClient)
	awaitShutdown(ctx, server)
}

func startHTTPServer(ctx context.Context, port string, topicName string, optionalProducer ...MetricProducer) *http.Server {
	srvCfg := DefaultServerConfig(port)
	srvCfg.TopicName = topicName
	if len(optionalProducer) > 0 {
		srvCfg.Producer = optionalProducer[0]
	}
	server := NewHTTPServer(srvCfg)

	go func() {
		logging.Info(ctx, "Ingestion Gateway server listening", "port", port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logging.Fatal(ctx, "HTTP server listener error", "error", err)
		}
	}()

	return server
}

func awaitShutdown(ctx context.Context, server *http.Server) {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	logging.Info(ctx, "Shutting down Ingestion Gateway server gracefully...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		logging.Error(ctx, "HTTP server shutdown error", "error", err)
	}
}
