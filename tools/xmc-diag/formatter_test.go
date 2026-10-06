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
	"strings"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kadm"
)

func TestRunDiagnostics_OverallStatus(t *testing.T) {
	ctx := context.Background()
	cfg := &Config{TopicName: "xmc-metrics", ConsumerGroup: "xmc-group", SkipSmokeTests: true}

	chMock := &MockClickHouseQuerier{version: "mock-clickhouse-24.3"}
	rpMock := &MockRedpandaQuerier{
		brokerCount: 3,
		topicDetails: kadm.TopicDetails{
			"xmc-metrics": kadm.TopicDetail{
				Topic: "xmc-metrics",
				Partitions: kadm.PartitionDetails{
					0: kadm.PartitionDetail{Partition: 0, Leader: 1, Replicas: []int32{1}},
				},
			},
		},
	}

	report := RunDiagnostics(ctx, cfg, chMock, nil, rpMock, nil)
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
			Version:   "mock-clickhouse-24.3",
			Reason:    "missing required schema tables: xmc_metrics_mv",
		},
		Redpanda: RedpandaReport{
			Status:          StatusHealthy,
			Connected:       true,
			BrokerCount:     3,
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
	if !strings.Contains(textOut, "Broker Count: 3") {
		t.Errorf("text output missing broker count: %s", textOut)
	}

	jsonOut, err := FormatReport(report, "json")
	if err != nil {
		t.Fatalf("FormatReport json failed: %v", err)
	}
	if !strings.Contains(jsonOut, `"reason": "missing required schema tables: xmc_metrics_mv"`) {
		t.Errorf("json output missing reason field: %s", jsonOut)
	}
}
