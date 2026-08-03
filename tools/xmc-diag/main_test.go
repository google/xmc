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
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kadm"
)

type MockClickHouseQuerier struct {
	pingErr        error
	version        string
	versionErr     error
	missingTables  []string
	checkTablesErr error
	latency        time.Duration
	latencyErr     error
}

func (m *MockClickHouseQuerier) Ping(ctx context.Context) error {
	return m.pingErr
}

func (m *MockClickHouseQuerier) SelectVersion(ctx context.Context) (string, error) {
	return m.version, m.versionErr
}

func (m *MockClickHouseQuerier) CheckTablesExist(ctx context.Context, tables []string) ([]string, error) {
	return m.missingTables, m.checkTablesErr
}

func (m *MockClickHouseQuerier) MeasureLatency(ctx context.Context) (time.Duration, error) {
	return m.latency, m.latencyErr
}

type MockRedpandaQuerier struct {
	pingLat      time.Duration
	pingErr      error
	topicDetails kadm.TopicDetails
	topicErr     error
	endOffsets   kadm.ListedOffsets
	endErr       error
	offsetResp   kadm.OffsetResponses
	offsetErr    error
}

func (m *MockRedpandaQuerier) Ping(ctx context.Context) (time.Duration, error) {
	return m.pingLat, m.pingErr
}

func (m *MockRedpandaQuerier) ListTopics(ctx context.Context, topics ...string) (kadm.TopicDetails, error) {
	return m.topicDetails, m.topicErr
}

func (m *MockRedpandaQuerier) ListEndOffsets(ctx context.Context, topics ...string) (kadm.ListedOffsets, error) {
	return m.endOffsets, m.endErr
}

func (m *MockRedpandaQuerier) FetchOffsets(ctx context.Context, group string) (kadm.OffsetResponses, error) {
	return m.offsetResp, m.offsetErr
}

func TestParseFlags(t *testing.T) {
	args := []string{
		"--clickhouse_host=10.0.0.1",
		"--clickhouse_port=9001",
		"--redpanda_brokers=10.0.0.2:9092,10.0.0.3:9092",
		"--topic_name=custom-metrics",
		"--consumer_group=custom-group",
		"--output=json",
		"--skip_smoke_tests=true",
	}

	cfg, err := ParseFlags(args)
	if err != nil {
		t.Fatalf("ParseFlags failed: %v", err)
	}

	if cfg.ClickHouseHost != "10.0.0.1" {
		t.Errorf("ClickHouseHost = %q, want %q", cfg.ClickHouseHost, "10.0.0.1")
	}
	if cfg.ClickHousePort != 9001 {
		t.Errorf("ClickHousePort = %d, want 9001", cfg.ClickHousePort)
	}
	if len(cfg.RedpandaBrokers) != 2 || cfg.RedpandaBrokers[0] != "10.0.0.2:9092" {
		t.Errorf("RedpandaBrokers = %v, want 2 brokers", cfg.RedpandaBrokers)
	}
	if cfg.TopicName != "custom-metrics" {
		t.Errorf("TopicName = %q, want %q", cfg.TopicName, "custom-metrics")
	}
	if cfg.ConsumerGroup != "custom-group" {
		t.Errorf("ConsumerGroup = %q, want %q", cfg.ConsumerGroup, "custom-group")
	}
	if cfg.OutputFormat != "json" {
		t.Errorf("OutputFormat = %q, want %q", cfg.OutputFormat, "json")
	}
	if !cfg.SkipSmokeTests {
		t.Errorf("SkipSmokeTests = false, want true")
	}
}

func TestCheckClickHouse_Healthy(t *testing.T) {
	ctx := context.Background()
	mock := &MockClickHouseQuerier{
		version: "24.3.1",
		latency: 12 * time.Millisecond,
	}

	rep := CheckClickHouse(ctx, mock, false)
	if rep.Status != StatusHealthy {
		t.Errorf("Status = %q, want %q", rep.Status, StatusHealthy)
	}
	if !rep.Connected {
		t.Errorf("Connected = false, want true")
	}
	if rep.Version != "24.3.1" {
		t.Errorf("Version = %q, want 24.3.1", rep.Version)
	}
	if len(rep.MissingTables) > 0 {
		t.Errorf("MissingTables = %v, want empty", rep.MissingTables)
	}
}

func TestCheckClickHouse_MissingTables_DegradedReason(t *testing.T) {
	ctx := context.Background()
	mock := &MockClickHouseQuerier{
		version:       "24.3.1",
		missingTables: []string{"xmc_metrics_mv"},
		latency:       10 * time.Millisecond,
	}

	rep := CheckClickHouse(ctx, mock, false)
	if rep.Status != StatusDegraded {
		t.Errorf("Status = %q, want %q", rep.Status, StatusDegraded)
	}
	if len(rep.MissingTables) != 1 || rep.MissingTables[0] != "xmc_metrics_mv" {
		t.Errorf("MissingTables = %v, want [xmc_metrics_mv]", rep.MissingTables)
	}
	if !strings.Contains(rep.Reason, "missing required schema tables: xmc_metrics_mv") {
		t.Errorf("Reason = %q, expected missing schema tables explanation", rep.Reason)
	}
}

func TestCheckClickHouse_PingFailed(t *testing.T) {
	ctx := context.Background()
	mock := &MockClickHouseQuerier{
		pingErr: errors.New("connection refused"),
	}

	rep := CheckClickHouse(ctx, mock, false)
	if rep.Status != StatusFailed {
		t.Errorf("Status = %q, want %q", rep.Status, StatusFailed)
	}
	if rep.Connected {
		t.Errorf("Connected = true, want false")
	}
	if !strings.Contains(rep.Reason, "Failed to ping ClickHouse endpoint") {
		t.Errorf("Reason = %q, expected ping failure explanation", rep.Reason)
	}
}

func TestCalculateConsumerLag(t *testing.T) {
	topic := "xmc-metrics"
	group := "xmc-ingester"

	endOffsets := kadm.ListedOffsets{
		topic: {
			1: kadm.ListedOffset{Topic: topic, Partition: 1, Offset: 2500},
			0: kadm.ListedOffset{Topic: topic, Partition: 0, Offset: 1000},
		},
	}

	groupOffsets := kadm.OffsetResponses{
		topic: {
			0: kadm.OffsetResponse{Offset: kadm.Offset{Topic: topic, Partition: 0, At: 800}},
			1: kadm.OffsetResponse{Offset: kadm.Offset{Topic: topic, Partition: 1, At: 2000}},
		},
	}

	lagRep := CalculateConsumerLag(endOffsets, groupOffsets, topic, group)
	if lagRep.TotalLag != 700 {
		t.Errorf("TotalLag = %d, want 700 (200 + 500)", lagRep.TotalLag)
	}
	if len(lagRep.Partitions) != 2 {
		t.Fatalf("Partitions len = %d, want 2", len(lagRep.Partitions))
	}
	if lagRep.Partitions[0].Partition != 0 || lagRep.Partitions[1].Partition != 1 {
		t.Errorf("Partitions not sorted deterministically: %v", lagRep.Partitions)
	}
}

func TestCheckRedpanda_Healthy(t *testing.T) {
	ctx := context.Background()
	topic := "xmc-metrics"
	group := "xmc-ingester"

	mock := &MockRedpandaQuerier{
		pingLat: 5 * time.Millisecond,
		topicDetails: kadm.TopicDetails{
			topic: kadm.TopicDetail{
				Topic: topic,
				Partitions: kadm.PartitionDetails{
					0: kadm.PartitionDetail{Partition: 0, Leader: 1, Replicas: []int32{1}},
				},
			},
		},
		endOffsets: kadm.ListedOffsets{
			topic: {
				0: kadm.ListedOffset{Topic: topic, Partition: 0, Offset: 500},
			},
		},
		offsetResp: kadm.OffsetResponses{
			topic: {
				0: kadm.OffsetResponse{Offset: kadm.Offset{Topic: topic, Partition: 0, At: 500}},
			},
		},
	}

	rep := CheckRedpanda(ctx, mock, topic, group, false)
	if rep.Status != StatusHealthy {
		t.Errorf("Status = %q, want %q", rep.Status, StatusHealthy)
	}
	if !rep.Connected {
		t.Errorf("Connected = false, want true")
	}
	if rep.TopicPartitions != 1 {
		t.Errorf("TopicPartitions = %d, want 1", rep.TopicPartitions)
	}
	if rep.Lag.TotalLag != 0 {
		t.Errorf("TotalLag = %d, want 0", rep.Lag.TotalLag)
	}
}

func TestCheckRedpanda_DegradedReason(t *testing.T) {
	ctx := context.Background()
	topic := "xmc-metrics"
	group := "xmc-ingester"

	mock := &MockRedpandaQuerier{
		pingLat: 5 * time.Millisecond,
		topicDetails: kadm.TopicDetails{
			topic: kadm.TopicDetail{
				Topic: topic,
				Partitions: kadm.PartitionDetails{
					0: kadm.PartitionDetail{Partition: 0, Leader: 1, Replicas: []int32{1}},
				},
			},
		},
		endOffsets: kadm.ListedOffsets{
			topic: {
				0: kadm.ListedOffset{Topic: topic, Partition: 0, Offset: 500},
			},
		},
		offsetErr: errors.New("group offsets fetch error"),
	}

	rep := CheckRedpanda(ctx, mock, topic, group, false)
	if rep.Status != StatusDegraded {
		t.Errorf("Status = %q, want %q", rep.Status, StatusDegraded)
	}
	if !strings.Contains(rep.Reason, "failed fetching consumer group offsets for \"xmc-ingester\"") {
		t.Errorf("Reason = %q, expected group offset error reason", rep.Reason)
	}
}

func TestRunDiagnostics_OverallStatus(t *testing.T) {
	ctx := context.Background()
	cfg := &Config{TopicName: "xmc-metrics", ConsumerGroup: "xmc-group", SkipSmokeTests: true}

	chMock := &MockClickHouseQuerier{version: "24.1.0"}
	rpMock := &MockRedpandaQuerier{
		topicDetails: kadm.TopicDetails{
			"xmc-metrics": kadm.TopicDetail{
				Topic: "xmc-metrics",
				Partitions: kadm.PartitionDetails{
					0: kadm.PartitionDetail{Partition: 0, Leader: 1, Replicas: []int32{1}},
				},
			},
		},
	}

	report := RunDiagnostics(ctx, cfg, chMock, rpMock)
	if report.OverallStatus != StatusHealthy {
		t.Errorf("OverallStatus = %q, want %q", report.OverallStatus, StatusHealthy)
	}
}

func TestFormatReport(t *testing.T) {
	report := &DiagReport{
		OverallStatus: StatusDegraded,
		Timestamp:     time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC),
		ClickHouse: ClickHouseReport{
			Status:    StatusDegraded,
			Connected: true,
			Version:   "24.3.1",
			Reason:    "missing required schema tables: xmc_metrics_mv",
		},
		Redpanda: RedpandaReport{
			Status:          StatusHealthy,
			Connected:       true,
			TopicPartitions: 4,
			Lag: LagReport{
				ConsumerGroup: "xmc-group",
				Topic:         "xmc-metrics",
				TotalLag:      50,
			},
		},
	}

	textOut, err := FormatReport(report, "text")
	if err != nil {
		t.Fatalf("FormatReport text failed: %v", err)
	}
	if !strings.Contains(textOut, "Status: DEGRADED") {
		t.Errorf("text output missing degraded status: %s", textOut)
	}
	if !strings.Contains(textOut, "Reason: missing required schema tables: xmc_metrics_mv") {
		t.Errorf("text output missing degradation reason: %s", textOut)
	}

	jsonOut, err := FormatReport(report, "json")
	if err != nil {
		t.Fatalf("FormatReport json failed: %v", err)
	}
	if !strings.Contains(jsonOut, `"reason": "missing required schema tables: xmc_metrics_mv"`) {
		t.Errorf("json output missing reason field: %s", jsonOut)
	}
}
