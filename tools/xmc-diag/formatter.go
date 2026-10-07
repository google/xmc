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
	if report.Redpanda.BrokerCount > 0 {
		sb.WriteString(fmt.Sprintf("  Broker Count: %d\n", report.Redpanda.BrokerCount))
	}
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

	writeComponentsSection(&sb, report.Components)
	return sb.String(), nil
}

func writeComponentsSection(sb *strings.Builder, comp ComponentsReport) {
	if comp.Status == "" {
		return
	}
	fmt.Fprintf(sb, "\n[Kubernetes Components Diagnostics] Status: %s\n  Healthy: %t\n", comp.Status, comp.Healthy)
	if comp.Namespace != "" {
		fmt.Fprintf(sb, "  Namespace: %s\n", comp.Namespace)
	}
	if comp.Skipped {
		sb.WriteString("  Skipped: true\n")
	}
	for _, c := range comp.Components {
		fmt.Fprintf(sb, "  Component %s: enabled=%t state=%s ready=%t healthy=%t\n", c.Name, c.Enabled, c.State, c.Ready, c.Healthy)
	}
	for _, kv := range [][2]string{{"Reason", comp.Reason}, {"Error", comp.Error}} {
		if kv[1] != "" {
			fmt.Fprintf(sb, "  %s: %s\n", kv[0], kv[1])
		}
	}
}

// RunDiagnostics executes checks for ClickHouse, Redpanda, and Component CRs and builds overall report.
func RunDiagnostics(ctx context.Context, cfg *Config, chQuerier ClickHouseQuerier, chConnErr error, rpQuerier RedpandaQuerier, rpConnErr error, compCheckers ...*ComponentChecker) *DiagReport {
	chRep := CheckClickHouse(ctx, chQuerier, chConnErr, cfg.SkipSmokeTests)
	rpRep := CheckRedpanda(ctx, rpQuerier, rpConnErr, cfg.TopicName, cfg.ConsumerGroup, cfg.SkipSmokeTests)
	var checker *ComponentChecker
	if len(compCheckers) > 0 {
		checker = compCheckers[0]
	}
	compRep := CheckComponents(ctx, checker, cfg)

	overall := StatusHealthy
	if chRep.Status == StatusFailed || rpRep.Status == StatusFailed || compRep.Status == StatusFailed {
		overall = StatusFailed
	} else if chRep.Status == StatusDegraded || rpRep.Status == StatusDegraded || compRep.Status == StatusDegraded {
		overall = StatusDegraded
	}

	return &DiagReport{
		OverallStatus: overall,
		Timestamp:     time.Now().UTC(),
		ClickHouse:    chRep,
		Redpanda:      rpRep,
		Components:    compRep,
	}
}
