// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"context"
	"time"

	"github.com/twmb/franz-go/pkg/kadm"
)

// DiagnosticStatus represents status of a check: HEALTHY, DEGRADED, FAILED.
type DiagnosticStatus string

const (
	StatusHealthy  DiagnosticStatus = "HEALTHY"
	StatusDegraded DiagnosticStatus = "DEGRADED"
	StatusFailed   DiagnosticStatus = "FAILED"
)

// PartitionLag holds offset details and lag for a single topic partition.
type PartitionLag struct {
	Partition int32 `json:"partition"`
	HighWater int64 `json:"high_watermark"`
	Committed int64 `json:"committed_offset"`
	Lag       int64 `json:"lag"`
}

// LagReport holds lag details for a consumer group.
type LagReport struct {
	ConsumerGroup string         `json:"consumer_group"`
	Topic         string         `json:"topic"`
	TotalLag      int64          `json:"total_lag"`
	Partitions    []PartitionLag `json:"partitions"`
}

// ClickHouseReport holds diagnostic results for ClickHouse.
type ClickHouseReport struct {
	Status        DiagnosticStatus `json:"status"`
	Connected     bool             `json:"connected"`
	Version       string           `json:"version,omitempty"`
	MissingTables []string         `json:"missing_tables,omitempty"`
	QueryLatency  string           `json:"query_latency,omitempty"`
	Reason        string           `json:"reason,omitempty"`
	Error         string           `json:"error,omitempty"`
}

// RedpandaReport holds diagnostic results for Redpanda/Kafka.
type RedpandaReport struct {
	Status          DiagnosticStatus `json:"status"`
	Connected       bool             `json:"connected"`
	BrokerCount     int              `json:"broker_count"`
	TopicPartitions int              `json:"topic_partitions"`
	Lag             LagReport        `json:"lag_report"`
	PingLatency     string           `json:"ping_latency,omitempty"`
	Reason          string           `json:"reason,omitempty"`
	Error           string           `json:"error,omitempty"`
}

type ComponentStatus struct {
	Name      string `json:"name"`
	Enabled   bool   `json:"enabled"`
	State     string `json:"state"`
	Stability string `json:"stability,omitempty"`
	Ready     bool   `json:"ready"`
	Healthy   bool   `json:"healthy"`
	Message   string `json:"message,omitempty"`
}

type ComponentsReport struct {
	Status     DiagnosticStatus  `json:"status"`
	Healthy    bool              `json:"healthy"`
	Skipped    bool              `json:"skipped,omitempty"`
	Namespace  string            `json:"namespace,omitempty"`
	Components []ComponentStatus `json:"components,omitempty"`
	Reason     string            `json:"reason,omitempty"`
	Error      string            `json:"error,omitempty"`
}

// DiagReport aggregates diagnostic results across all infrastructure components.
type DiagReport struct {
	OverallStatus DiagnosticStatus `json:"overall_status"`
	Timestamp     time.Time        `json:"timestamp"`
	ClickHouse    ClickHouseReport `json:"clickhouse"`
	Redpanda      RedpandaReport   `json:"redpanda"`
	Components    ComponentsReport `json:"components"`
}

// ClickHouseQuerier abstracts ClickHouse database queries for testing.
type ClickHouseQuerier interface {
	Ping(ctx context.Context) error
	SelectVersion(ctx context.Context) (string, error)
	CheckTablesExist(ctx context.Context, tables []string) ([]string, error)
	MeasureLatency(ctx context.Context) (time.Duration, error)
}

// RedpandaPingResult contains the broker count and latency for a ping.
type RedpandaPingResult struct {
	BrokerCount int
	Latency     time.Duration
}

// RedpandaQuerier abstracts Redpanda/Kafka queries for testing.
type RedpandaQuerier interface {
	Ping(ctx context.Context) (RedpandaPingResult, error)
	ListTopics(ctx context.Context, topics ...string) (kadm.TopicDetails, error)
	ListEndOffsets(ctx context.Context, topics ...string) (kadm.ListedOffsets, error)
	FetchOffsets(ctx context.Context, group string) (kadm.OffsetResponses, error)
}
