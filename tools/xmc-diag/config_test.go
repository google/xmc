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
		"--clickhouse_host=localhost",
		"--clickhouse_port=9000",
		"--clickhouse_user=testuser",
		"--redpanda_brokers=b1:9092, b2:9092",
		"--output=json",
	}

	cfg, err := ParseFlags(args)
	if err != nil {
		t.Fatalf("ParseFlags failed: %v", err)
	}

	if cfg.ClickHouseHost != "localhost" {
		t.Errorf("ClickHouseHost = %q, want localhost", cfg.ClickHouseHost)
	}
	if cfg.ClickHousePort != 9000 {
		t.Errorf("ClickHousePort = %d, want 9000", cfg.ClickHousePort)
	}
	if cfg.ClickHouseUser != "testuser" {
		t.Errorf("ClickHouseUser = %q, want testuser", cfg.ClickHouseUser)
	}
	if len(cfg.RedpandaBrokers) != 2 || cfg.RedpandaBrokers[0] != "b1:9092" || cfg.RedpandaBrokers[1] != "b2:9092" {
		t.Errorf("RedpandaBrokers = %v, want [b1:9092 b2:9092]", cfg.RedpandaBrokers)
	}
	if cfg.OutputFormat != "json" {
		t.Errorf("OutputFormat = %q, want json", cfg.OutputFormat)
	}
}

func TestParseFlags_Defaults(t *testing.T) {
	cfg, err := ParseFlags([]string{})
	if err != nil {
		t.Fatalf("ParseFlags failed: %v", err)
	}

	if cfg.ClickHousePort != 0 {
		t.Errorf("Default ClickHousePort = %d, want 0", cfg.ClickHousePort)
	}
	if cfg.ClickHouseUser != "" {
		t.Errorf("Default ClickHouseUser = %q, want empty string", cfg.ClickHouseUser)
	}
	if cfg.ClickHouseDatabase != "" {
		t.Errorf("Default ClickHouseDatabase = %q, want empty string", cfg.ClickHouseDatabase)
	}
	if cfg.DialWithSeedOnly {
		t.Errorf("Default DialWithSeedOnly = true, want false")
	}
}

func TestParseFlags_ValidationErrors(t *testing.T) {
	_, err := ParseFlags([]string{"--sasl_enabled=true", "--sasl_mechanism=INVALID"})
	if err == nil {
		t.Errorf("ParseFlags with invalid SASL mechanism wanted error, got nil")
	}

	_, err = ParseFlags([]string{"--output=xml"})
	if err == nil {
		t.Errorf("ParseFlags with invalid output format wanted error, got nil")
	}
}
