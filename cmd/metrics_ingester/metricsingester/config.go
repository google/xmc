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

// Package metricsingester delivers metric records from a Pub/Sub subscription into ClickHouse or
// BigQuery.
//
// It is the consumer half of the Pub/Sub message bus: the Ingestion Gateway publishes serialized
// metrics.Metric messages to a topic, and this package batches and writes them either into
// ClickHouse's xmc_metrics_pubsub_queue landing table (whose materialized view populates
// xmc_metrics) or directly into BigQuery's xmc_metrics table via the Storage Write API after
// applying the same MV normalization rules in Go.
//
// Running this consumer as a separate service rather than inside the Ingestion Gateway keeps
// storage credentials and egress off the workload-reachable gateway pods, and lets the consumer
// run a small replica count tuned for large batch writes independently of gateway HTTP autoscaling.
package metricsingester

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
)

const (
	// StoreClickHouse selects ClickHouse as the metrics storage backend.
	StoreClickHouse = "clickhouse"
	// StoreBigQuery selects Google Cloud BigQuery as the metrics storage backend.
	StoreBigQuery = "bigquery"

	defaultPort            = "8080"
	defaultBatchSize       = 1000
	defaultBatchInterval   = time.Second
	defaultMetricsStore    = StoreClickHouse
	defaultBigQueryDataset = "xmc_metrics"
	defaultBigQueryTable   = "xmc_metrics"
)

// Config holds the validated runtime configuration for the metrics ingester.
type Config struct {
	Port              string
	ProjectID         string
	SubscriptionID    string
	MetricsStore      string
	BigQueryProjectID string
	BigQueryDataset   string
	BigQueryTable     string
	BatchSize         int
	BatchInterval     time.Duration
}

// Flags holds the CLI flag value bindings for FlagSet registration.
type Flags Config

// RegisterFlags registers the metrics ingester CLI flags on fs and returns their bindings.
//
// String flags default to empty so that LoadConfig can fall back to environment variables when
// flags are omitted.
func RegisterFlags(fs *flag.FlagSet) *Flags {
	f := &Flags{}
	fs.StringVar(&f.Port, "port", "", "The HTTP health server port (default $PORT, then 8080).")
	fs.StringVar(&f.ProjectID, "project_id", "", "GCP project ID that owns the subscription (default $GOOGLE_CLOUD_PROJECT).")
	fs.StringVar(&f.SubscriptionID, "subscription_id", "", "Pub/Sub subscription ID on the metrics topic (default $PUBSUB_SUBSCRIPTION_ID).")
	fs.StringVar(&f.MetricsStore, "metrics_store", "", "Metrics storage backend: clickhouse or bigquery (default $METRICS_STORE, then clickhouse).")
	fs.StringVar(&f.BigQueryProjectID, "bigquery_project_id", "", "GCP project ID that owns the BigQuery dataset (default $BIGQUERY_PROJECT_ID, then -project_id).")
	fs.StringVar(&f.BigQueryDataset, "bigquery_dataset", "", "BigQuery dataset ID for metrics storage (default $BIGQUERY_DATASET, then xmc_metrics).")
	fs.StringVar(&f.BigQueryTable, "bigquery_table", "", "BigQuery table ID for metrics storage (default $BIGQUERY_TABLE, then xmc_metrics).")
	// The interval matches kafka_flush_interval_ms on xmc_metrics_queue so both message buses trade
	// the same latency for batch writes.
	fs.IntVar(&f.BatchSize, "batch_size", defaultBatchSize, "Maximum number of messages per batch write.")
	fs.DurationVar(&f.BatchInterval, "batch_interval", defaultBatchInterval, "Target time a received message waits before its batch is written; longer if a previous write is still running.")
	return f
}

// LoadConfig resolves f against the environment and validates the result.
//
// Precedence for string settings is flag, then environment variable, then built-in default.
func LoadConfig(f *Flags) (*Config, error) {
	projectID := firstNonEmpty(f.ProjectID, os.Getenv("GOOGLE_CLOUD_PROJECT"))
	cfg := &Config{
		Port:              firstNonEmpty(f.Port, os.Getenv("PORT"), defaultPort),
		ProjectID:         projectID,
		SubscriptionID:    firstNonEmpty(f.SubscriptionID, os.Getenv("PUBSUB_SUBSCRIPTION_ID")),
		MetricsStore:      strings.ToLower(firstNonEmpty(f.MetricsStore, os.Getenv("METRICS_STORE"), defaultMetricsStore)),
		BigQueryProjectID: firstNonEmpty(f.BigQueryProjectID, os.Getenv("BIGQUERY_PROJECT_ID"), projectID),
		BigQueryDataset:   firstNonEmpty(f.BigQueryDataset, os.Getenv("BIGQUERY_DATASET"), defaultBigQueryDataset),
		BigQueryTable:     firstNonEmpty(f.BigQueryTable, os.Getenv("BIGQUERY_TABLE"), defaultBigQueryTable),
		BatchSize:         f.BatchSize,
		BatchInterval:     f.BatchInterval,
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
	return c.validateStore()
}

func (c *Config) validateStore() error {
	switch c.MetricsStore {
	case StoreClickHouse:
		return nil
	case StoreBigQuery:
		return c.validateBigQuery()
	default:
		return fmt.Errorf("unsupported metrics_store %q (must be %q or %q)", c.MetricsStore, StoreClickHouse, StoreBigQuery)
	}
}

func (c *Config) validateBigQuery() error {
	for _, field := range []struct{ name, val string }{
		{"bigquery_project_id", c.BigQueryProjectID},
		{"bigquery_dataset", c.BigQueryDataset},
		{"bigquery_table", c.BigQueryTable},
	} {
		if field.val == "" || strings.Contains(field.val, "/") {
			return fmt.Errorf("%s must be a non-empty BigQuery identifier without '/'", field.name)
		}
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
