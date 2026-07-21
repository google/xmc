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
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/sasl/plain"
	"github.com/twmb/franz-go/pkg/sasl/scram"
	"go/google"
	"xmanager_cloud/logging/logging"
)

// TopologyQuerier abstracts Redpanda metadata querying for testability.
type TopologyQuerier interface {
	ListTopics(ctx context.Context, topics ...string) (kadm.TopicDetails, error)
}

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

	opts := buildKgoOptions(cfg)
	kClient, err := kgo.NewClient(opts...)
	if err != nil {
		logging.Fatal(ctx, "failed to create franz-go client", "error", err)
	}
	defer kClient.Close()

	adminClient := kadm.NewClient(kClient)
	if err := ValidateTopicTopology(ctx, adminClient, cfg.TopicName, cfg.MinPartitions, cfg.MinReplicas); err != nil {
		logging.Fatal(ctx, "topic topology validation failed", "error", err)
	}
	logging.Info(ctx, "Topic topology validation passed", "topic", cfg.TopicName, "min_partitions", cfg.MinPartitions, "min_replicas", cfg.MinReplicas)

	server := startHTTPServer(ctx, cfg.Port)
	awaitShutdown(ctx, server)
}

func buildKgoOptions(cfg *Config) []kgo.Opt {
	opts := []kgo.Opt{
		kgo.SeedBrokers(cfg.RedpandaBrokers...),
		kgo.RecordPartitioner(kgo.StickyKeyPartitioner(nil)),
	}

	if cfg.SASLEnabled {
		switch strings.ToUpper(cfg.SASLMechanism) {
		case "SCRAM-SHA-256":
			opts = append(opts, kgo.SASL(scram.Auth{User: cfg.SASLUser, Pass: cfg.SASLPassword}.AsSha256Mechanism()))
		case "SCRAM-SHA-512":
			opts = append(opts, kgo.SASL(scram.Auth{User: cfg.SASLUser, Pass: cfg.SASLPassword}.AsSha512Mechanism()))
		case "PLAIN":
			opts = append(opts, kgo.SASL(plain.Auth{User: cfg.SASLUser, Pass: cfg.SASLPassword}.AsMechanism()))
		}
	}

	return opts
}

// ValidateTopicTopology asserts topic existence, partition count >= minPartitions, and replication factor >= minReplicas.
func ValidateTopicTopology(ctx context.Context, querier TopologyQuerier, topicName string, minPartitions, minReplicas int) error {
	details, err := querier.ListTopics(ctx, topicName)
	if err != nil {
		return fmt.Errorf("failed to query topic metadata: %w", err)
	}

	detail, ok := details[topicName]
	if !ok {
		return fmt.Errorf("required topic %q does not exist in cluster", topicName)
	}

	if detail.Err != nil {
		return fmt.Errorf("topic %q metadata error: %w", topicName, detail.Err)
	}

	numPartitions := len(detail.Partitions)
	if numPartitions < minPartitions {
		return fmt.Errorf("topic %q has %d partitions, expected >= %d", topicName, numPartitions, minPartitions)
	}

	for pID, pDetail := range detail.Partitions {
		if pDetail.Err != nil {
			return fmt.Errorf("partition %d metadata error: %w", pID, pDetail.Err)
		}
		numReplicas := len(pDetail.Replicas)
		if numReplicas < minReplicas {
			return fmt.Errorf("partition %d of topic %q has %d replicas, expected >= %d", pID, topicName, numReplicas, minReplicas)
		}
	}

	return nil
}

func startHTTPServer(ctx context.Context, port string) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	server := &http.Server{
		Addr:              net.JoinHostPort("", port),
		Handler:           mux,
		ReadHeaderTimeout: 3 * time.Second,
	}

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
