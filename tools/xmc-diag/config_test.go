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
	"testing"
)

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
