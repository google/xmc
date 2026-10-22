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

import "time"

const (
	// DefaultBatchSize is the default maximum number of messages per ClickHouse insert.
	DefaultBatchSize = 1000
	// DefaultBatchInterval matches the Kafka table's kafka_flush_interval_ms.
	DefaultBatchInterval = time.Second
)

// Config holds the runtime configuration for the metrics ingester.
type Config struct {
	ProjectID      string
	SubscriptionID string
	BatchSize      int
	BatchInterval  time.Duration
}
