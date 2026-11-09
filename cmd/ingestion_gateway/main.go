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

// Package main provides the entrypoint binary for the Ingestion Gateway service.
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

	"xmanager_cloud/cmd/ingestion_gateway/ingestiongateway"
	"xmanager_cloud/internal/logging"
)

func main() {
	flags := ingestiongateway.RegisterFlags(flag.CommandLine)
	flag.Parse()

	cfg, err := ingestiongateway.LoadConfig(flags)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		os.Exit(1)
	}

	logger := logging.NewGCPLogger(slog.LevelInfo, cfg.ProjectID)
	slog.SetDefault(logger)
	ctx := logging.WithLogger(context.Background(), logger)

	resolveComponentConfig(ctx, cfg)
	logInitializing(ctx, cfg)

	gMetrics := ingestiongateway.NewGatewayMetrics(nil)

	// NewMetricProducer also asserts the destination topic is present and usable, so a
	// misconfigured message bus fails at startup rather than on the first ingestion request.
	producer, err := ingestiongateway.NewMetricProducer(ctx, cfg, gMetrics)
	if err != nil {
		logging.Fatal(ctx, "failed to create metric producer", "message_bus", cfg.MessageBus, "error", err)
	}
	defer producer.Close()
	logging.Info(ctx, "Message bus topic validation passed", "message_bus", cfg.MessageBus, "topic", cfg.TopicName)

	server := startHTTPServer(ctx, cfg.Port, cfg.TopicName, gMetrics, producer)
	awaitShutdown(ctx, server)
}

func resolveComponentConfig(ctx context.Context, cfg *ingestiongateway.Config, optionalGetter ...ingestiongateway.ComponentGetter) {
	if cfg == nil || cfg.MessageBus == ingestiongateway.MessageBusPubSub {
		return
	}
	var getter ingestiongateway.ComponentGetter
	if len(optionalGetter) > 0 && optionalGetter[0] != nil {
		getter = optionalGetter[0]
	} else {
		var err error
		getter, err = ingestiongateway.NewK8sComponentGetter()
		if err != nil {
			logging.Warn(ctx, "Failed to initialize Kubernetes client for component resolution; falling back to static flags",
				"fallback_brokers", cfg.RedpandaBrokers, "fallback_topic", cfg.TopicName, "error", err)
			return
		}
	}
	if err := ingestiongateway.ResolveRedpandaComponentConfig(ctx, cfg, getter); err != nil {
		logging.Warn(ctx, "Failed to resolve redpanda Component CR; falling back to static flags",
			"fallback_brokers", cfg.RedpandaBrokers, "fallback_topic", cfg.TopicName, "error", err)
	}
}

// logInitializing reports the startup configuration relevant to the selected message bus, omitting
// the settings the other backend does not use.
func logInitializing(ctx context.Context, cfg *ingestiongateway.Config) {
	if cfg.MessageBus == ingestiongateway.MessageBusPubSub {
		logging.Info(ctx, "Initializing Ingestion Gateway service", "message_bus", cfg.MessageBus, "project", cfg.ProjectID, "topic", cfg.TopicName)
		return
	}
	logging.Info(ctx, "Initializing Ingestion Gateway service", "message_bus", cfg.MessageBus, "brokers", cfg.RedpandaBrokers, "topic", cfg.TopicName,
		"min_partitions", cfg.MinPartitions, "min_replicas", cfg.MinReplicas)
}

func startHTTPServer(ctx context.Context, port string, topicName string, metrics *ingestiongateway.GatewayMetrics, optionalProducer ...ingestiongateway.MetricProducer) *http.Server {
	srvCfg := ingestiongateway.DefaultServerConfig(port)
	srvCfg.TopicName = topicName
	if metrics != nil {
		srvCfg.Metrics = metrics
	}
	if len(optionalProducer) > 0 {
		srvCfg.Producer = optionalProducer[0]
	}
	server := ingestiongateway.NewHTTPServer(srvCfg)

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
