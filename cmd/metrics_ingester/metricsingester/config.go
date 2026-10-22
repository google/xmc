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

// Package metricsingester delivers metric records from a Pub/Sub subscription into ClickHouse.
//
// It is the consumer half of the Pub/Sub message bus: the Ingestion Gateway publishes serialized
// metrics.Metric messages to a topic, and this package inserts them into the Null-engine
// xmc_metrics_pubsub_queue table, whose materialized view populates xmc_metrics. It plays the role
// that ClickHouse's native Kafka table engine plays for the Redpanda message bus.
package metricsingester

import (
	"errors"
	"flag"
	"os"
	"strings"
	"time"
)

const (
	defaultPort          = "8080"
	defaultBatchSize     = 1000
	defaultBatchInterval = time.Second
)

// Config holds the validated runtime configuration for the metrics ingester.
type Config struct {
	Port           string
	ProjectID      string
	SubscriptionID string
	BatchSize      int
	BatchInterval  time.Duration
}

// Flags holds the CLI flag value bindings for FlagSet registration.
type Flags Config

// RegisterFlags registers the metrics ingester CLI flags on fs and returns their bindings.
//
// The string flags default to empty so that LoadConfig can tell an unset flag from an explicit one
// and fall back to the environment only for the former.
func RegisterFlags(fs *flag.FlagSet) *Flags {
	f := &Flags{}
	fs.StringVar(&f.Port, "port", "", "The HTTP health server port (default $PORT, then 8080).")
	fs.StringVar(&f.ProjectID, "project_id", "", "GCP project ID that owns the subscription (default $GOOGLE_CLOUD_PROJECT).")
	fs.StringVar(&f.SubscriptionID, "subscription_id", "", "Pub/Sub subscription ID on the metrics topic (default $PUBSUB_SUBSCRIPTION_ID).")
	// The interval matches kafka_flush_interval_ms on xmc_metrics_queue so both message buses trade
	// the same latency for ClickHouse insert batching.
	fs.IntVar(&f.BatchSize, "batch_size", defaultBatchSize, "Maximum number of messages per ClickHouse insert.")
	fs.DurationVar(&f.BatchInterval, "batch_interval", defaultBatchInterval, "Target time a received message waits before its batch is inserted; longer if a previous insert is still running.")
	return f
}

// LoadConfig resolves f against the environment and validates the result.
//
// Precedence for string settings is flag, then environment variable, then built-in default.
func LoadConfig(f *Flags) (*Config, error) {
	cfg := &Config{
		Port:           firstNonEmpty(f.Port, os.Getenv("PORT"), defaultPort),
		ProjectID:      firstNonEmpty(f.ProjectID, os.Getenv("GOOGLE_CLOUD_PROJECT")),
		SubscriptionID: firstNonEmpty(f.SubscriptionID, os.Getenv("PUBSUB_SUBSCRIPTION_ID")),
		BatchSize:      f.BatchSize,
		BatchInterval:  f.BatchInterval,
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) validate() error {
	if c.ProjectID == "" {
		return errors.New("project_id is required (flag -project_id or GOOGLE_CLOUD_PROJECT)")
	}
	if c.SubscriptionID == "" {
		return errors.New("subscription_id is required (flag -subscription_id or PUBSUB_SUBSCRIPTION_ID)")
	}
	if c.BatchSize <= 0 {
		return errors.New("batch_size must be positive")
	}
	if c.BatchInterval <= 0 {
		return errors.New("batch_interval must be positive")
	}
	return nil
}

// firstNonEmpty returns the first of values that is non-empty after trimming whitespace.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
