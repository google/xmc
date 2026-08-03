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
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// FormatReport converts DiagReport to formatted text or JSON output string.
func FormatReport(report *DiagReport, format string) (string, error) {
	if format == "json" {
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return "", fmt.Errorf("failed serializing report to json: %w", err)
		}
		return string(data), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("=== XMC Infrastructure Diagnostics (Status: %s) ===\n", report.OverallStatus))
	sb.WriteString(fmt.Sprintf("Timestamp: %s\n\n", report.Timestamp.Format(time.RFC3339)))

	sb.WriteString(fmt.Sprintf("[ClickHouse Diagnostics] Status: %s\n", report.ClickHouse.Status))
	sb.WriteString(fmt.Sprintf("  Connected: %t\n", report.ClickHouse.Connected))
	if report.ClickHouse.Version != "" {
		sb.WriteString(fmt.Sprintf("  Version: %s\n", report.ClickHouse.Version))
	}
	if len(report.ClickHouse.MissingTables) > 0 {
		sb.WriteString(fmt.Sprintf("  Missing Tables: %s\n", strings.Join(report.ClickHouse.MissingTables, ", ")))
	} else if report.ClickHouse.Connected {
		sb.WriteString("  Schema Tables: All required tables present (xmc_metrics, xmc_metrics_queue, xmc_metrics_mv)\n")
	}
	if report.ClickHouse.QueryLatency != "" {
		sb.WriteString(fmt.Sprintf("  Query Smoke Latency: %s\n", report.ClickHouse.QueryLatency))
	}
	if report.ClickHouse.Reason != "" {
		sb.WriteString(fmt.Sprintf("  Reason: %s\n", report.ClickHouse.Reason))
	}
	if report.ClickHouse.Error != "" {
		sb.WriteString(fmt.Sprintf("  Error: %s\n", report.ClickHouse.Error))
	}

	sb.WriteString(fmt.Sprintf("\n[Redpanda/Kafka Diagnostics] Status: %s\n", report.Redpanda.Status))
	sb.WriteString(fmt.Sprintf("  Connected: %t\n", report.Redpanda.Connected))
	sb.WriteString(fmt.Sprintf("  Topic Partitions (%s): %d\n", report.Redpanda.Lag.Topic, report.Redpanda.TopicPartitions))
	sb.WriteString(fmt.Sprintf("  Consumer Group (%s) Aggregate Lag: %d records\n", report.Redpanda.Lag.ConsumerGroup, report.Redpanda.Lag.TotalLag))
	if report.Redpanda.PingLatency != "" {
		sb.WriteString(fmt.Sprintf("  Ping Smoke Latency: %s\n", report.Redpanda.PingLatency))
	}
	if report.Redpanda.Reason != "" {
		sb.WriteString(fmt.Sprintf("  Reason: %s\n", report.Redpanda.Reason))
	}
	if report.Redpanda.Error != "" {
		sb.WriteString(fmt.Sprintf("  Error: %s\n", report.Redpanda.Error))
	}

	return sb.String(), nil
}

// RunDiagnostics executes checks for ClickHouse and Redpanda and builds overall report.
func RunDiagnostics(ctx context.Context, cfg *Config, chQuerier ClickHouseQuerier, rpQuerier RedpandaQuerier) *DiagReport {
	chRep := CheckClickHouse(ctx, chQuerier, cfg.SkipSmokeTests)
	rpRep := CheckRedpanda(ctx, rpQuerier, cfg.TopicName, cfg.ConsumerGroup, cfg.SkipSmokeTests)

	overall := StatusHealthy
	if chRep.Status == StatusFailed || rpRep.Status == StatusFailed {
		overall = StatusFailed
	} else if chRep.Status == StatusDegraded || rpRep.Status == StatusDegraded {
		overall = StatusDegraded
	}

	return &DiagReport{
		OverallStatus: overall,
		Timestamp:     time.Now().UTC(),
		ClickHouse:    chRep,
		Redpanda:      rpRep,
	}
}
