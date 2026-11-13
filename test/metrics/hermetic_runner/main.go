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

	"xmanager_cloud/cmd/ingestion_gateway/ingestiongateway"
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

type gatewayPool struct {
	servers     []*http.Server
	gatewayURLs []string
	producers   []ingestiongateway.MetricProducer
}

func (g *gatewayPool) closeProducers() {
	for _, p := range g.producers {
		p.Close()
	}
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

	schemaPath, err := locateSchemaFile()
	if err != nil {
		return fmt.Errorf("failed to locate schema.sql: %w", err)
	}

	harness, err := infra.StartHarness(ctx, schemaPath)
	if err != nil {
		return fmt.Errorf("failed to start harness: %w", err)
	}
	defer harness.Teardown(context.Background())

	pool, err := startGatewayServers(*numGateways, harness.RedpandaBrokers)
	if err != nil {
		return err
	}
	defer pool.closeProducers()

	if err := emitReadinessConfig(harness, pool.gatewayURLs); err != nil {
		return err
	}

	waitForShutdownSignal()
	shutdownServers(pool.servers)
	return nil
}

func startGatewayServers(numGateways int, brokers []string) (*gatewayPool, error) {
	pool := &gatewayPool{}
	for i := 0; i < numGateways; i++ {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, fmt.Errorf("failed to listen on port for gateway %d: %w", i, err)
		}

		cfg := &ingestiongateway.Config{
			RedpandaBrokers: brokers,
			TopicName:       "xmc-metrics",
		}
		producer, err := ingestiongateway.NewKafkaProducer(cfg, nil, infra.KafkaDialer(brokers))
		if err != nil {
			return nil, fmt.Errorf("failed to create kafka producer for gateway %d: %w", i, err)
		}
		pool.producers = append(pool.producers, producer)

		gMetrics := ingestiongateway.NewGatewayMetrics(nil)
		srvCfg := ingestiongateway.DefaultServerConfig(fmt.Sprintf("%d", listener.Addr().(*net.TCPAddr).Port))
		srvCfg.TopicName = "xmc-metrics"
		srvCfg.Metrics = gMetrics
		srvCfg.Producer = producer

		server := ingestiongateway.NewHTTPServer(srvCfg)
		pool.servers = append(pool.servers, server)
		pool.gatewayURLs = append(pool.gatewayURLs, fmt.Sprintf("http://%s", listener.Addr().String()))

		go func(s *http.Server, l net.Listener) {
			_ = s.Serve(l)
		}(server, listener)
	}
	return pool, nil
}

func emitReadinessConfig(harness *infra.Harness, gatewayURLs []string) error {
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
	fmt.Println(string(jsonBytes))
	return nil
}

func waitForShutdownSignal() {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

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
}

func shutdownServers(servers []*http.Server) {
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	for _, s := range servers {
		_ = s.Shutdown(shutdownCtx)
	}
}
