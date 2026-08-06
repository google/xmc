// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package main provides the hermetic runner entrypoint that provisions local test containers
// (Redpanda message broker and ClickHouse database) and launches N Go Ingestion Gateway server instances.
//
// Architecture & Lifecycle:
//  1. Spawns Docker containers via testcontainers-go (`infra.StartHarness`) and applies `deploy/clickhouse/schema.sql`.
//  2. Binds N (default 5) HTTP listener instances on local dynamic ports (`127.0.0.1:0`) to simulate multiple
//     Ingestion Gateway pod endpoints in a Kubernetes deployment.
//  3. Connects each Ingestion Gateway instance to Redpanda using franz-go producers.
//  4. Emits a single JSON payload to stdout containing endpoint URLs (`status: ready`, `gateway_urls`, `clickhouse_http_addr`).
//  5. Waits for stdin closure (parent process EOF) or OS termination signals (SIGINT/SIGTERM) to perform
//     graceful HTTP server shutdown and container teardown.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	google "go/google"
	ingestiongateway "xmanager_cloud/cmd/ingestion_gateway/ingestiongateway"
	"xmanager_cloud/test/metrics/infra"
)

// RunnerConfig represents the JSON readiness payload emitted on stdout for parent test runners to consume.
type RunnerConfig struct {
	Status               string   `json:"status"`
	RedpandaBrokers      []string `json:"redpanda_brokers"`
	ClickHouseNativeAddr string   `json:"clickhouse_native_addr"`
	ClickHouseHTTPAddr   string   `json:"clickhouse_http_addr"`
	GatewayURLs          []string `json:"gateway_urls"`
}

// locateSchemaFile resolves the absolute path to deploy/clickhouse/schema.sql within Bazel runfiles or workspace.
func locateSchemaFile() (string, error) {
	if srcDir := os.Getenv("TEST_SRCDIR"); srcDir != "" {
		workspace := os.Getenv("TEST_WORKSPACE")
		if workspace == "" {
			workspace = "xmanager_cloud"
		}
		path := filepath.Join(srcDir, workspace, "deploy", "clickhouse", "schema.sql")
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}
	candidates := []string{
		filepath.Join("..", "..", "deploy", "clickhouse", "schema.sql"),
		filepath.Join("..", "deploy", "clickhouse", "schema.sql"),
		filepath.Join("deploy", "clickhouse", "schema.sql"),
	}
	for _, cand := range candidates {
		if abs, err := filepath.Abs(cand); err == nil {
			if _, err := os.Stat(abs); err == nil {
				return abs, nil
			}
		}
	}
	return "", fmt.Errorf("schema.sql not found")
}

func main() {
	google.Init()
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	numGateways := flag.Int("num_gateways", 5, "Number of gateway HTTP server instances to spin up")
	flag.Parse()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Locate ClickHouse schema DDL file to initialize database tables during container startup.
	schemaPath, err := locateSchemaFile()
	if err != nil {
		return fmt.Errorf("failed to locate schema.sql: %w", err)
	}

	// Spin up Redpanda and ClickHouse Docker containers via testcontainers-go.
	harness, err := infra.StartHarness(ctx, schemaPath)
	if err != nil {
		return fmt.Errorf("failed to start harness: %w", err)
	}
	// Defer container teardown to ensure cleanup upon run() exit or shutdown.
	defer harness.Teardown(context.Background())

	var servers []*http.Server
	var listeners []net.Listener
	var gatewayURLs []string

	// Launch N Ingestion Gateway HTTP server instances on dynamic local ports.
	// Each instance acts as an independent gateway pod routing metrics batches to Redpanda.
	for i := 0; i < *numGateways; i++ {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return fmt.Errorf("failed to listen on port for gateway %d: %w", i, err)
		}
		listeners = append(listeners, listener)

		cfg := &ingestiongateway.Config{
			RedpandaBrokers: harness.RedpandaBrokers,
			TopicName:       "xmc-metrics",
		}
		// Create a dedicated franz-go Kafka producer for each gateway server instance.
		producer, err := ingestiongateway.NewKafkaProducer(cfg, nil, infra.KafkaDialer(harness.RedpandaBrokers))
		if err != nil {
			return fmt.Errorf("failed to create kafka producer for gateway %d: %w", i, err)
		}
		defer producer.Close()

		gMetrics := ingestiongateway.NewGatewayMetrics(nil)
		srvCfg := ingestiongateway.DefaultServerConfig(fmt.Sprintf("%d", listener.Addr().(*net.TCPAddr).Port))
		srvCfg.TopicName = "xmc-metrics"
		srvCfg.Metrics = gMetrics
		srvCfg.Producer = producer

		server := ingestiongateway.NewHTTPServer(srvCfg)
		servers = append(servers, server)
		gatewayURLs = append(gatewayURLs, fmt.Sprintf("http://%s", listener.Addr().String()))

		// Serve HTTP requests asynchronously for this gateway instance.
		go func(s *http.Server, l net.Listener) {
			_ = s.Serve(l)
		}(server, listener)
	}

	// Format readiness payload containing endpoint details for parent test runners.
	res := RunnerConfig{
		Status:               "ready",
		RedpandaBrokers:      harness.RedpandaBrokers,
		ClickHouseNativeAddr: harness.ClickHouseNativeAddr,
		ClickHouseHTTPAddr:   harness.ClickHouseHTTPAddr,
		GatewayURLs:          gatewayURLs,
	}

	jsonBytes, err := json.Marshal(res)
	if err != nil {
		return fmt.Errorf("failed to marshal response: %w", err)
	}
	// Print single readiness JSON line to stdout for parent process parsing.
	fmt.Println(string(jsonBytes))

	// Listen for OS signals (SIGINT/SIGTERM) to handle graceful shutdown.
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Listen for stdin closure (EOF) from parent test process to trigger automatic shutdown.
	doneChan := make(chan struct{})
	go func() {
		buf := make([]byte, 1)
		_, _ = os.Stdin.Read(buf)
		close(doneChan)
	}()

	select {
	case <-sigChan:
	case <-doneChan:
	}

	// Perform graceful HTTP server shutdown before tearing down containers.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	for _, s := range servers {
		_ = s.Shutdown(shutdownCtx)
	}
	return nil
}
