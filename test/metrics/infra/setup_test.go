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

package infra

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestHermeticInfraSetup(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	// Find root relative schema path
	schemaPath := filepath.Join("..", "..", "..", "deploy", "clickhouse", "schema.sql")
	if _, err := os.Stat(schemaPath); err != nil {
		t.Logf("schema.sql not found at %s, proceeding without schema", schemaPath)
		schemaPath = ""
	}

	harness, err := StartHarness(ctx, schemaPath)
	if err != nil {
		t.Fatalf("StartHarness failed: %v", err)
	}
	defer func() {
		if err := harness.Teardown(ctx); err != nil {
			t.Errorf("Teardown failed: %v", err)
		}
	}()

	if len(harness.RedpandaBrokers) == 0 {
		t.Fatal("Expected non-empty RedpandaBrokers")
	}

	if err := VerifyTCPConnectivity(ctx, harness.RedpandaBrokers[0]); err != nil {
		t.Fatalf("Redpanda TCP ping failed: %v", err)
	}

	if err := harness.ClickHouseConn.Ping(ctx); err != nil {
		t.Fatalf("ClickHouse ping failed: %v", err)
	}

	var count uint64
	row := harness.ClickHouseConn.QueryRow(ctx, "SELECT count() FROM xmc_metrics")
	if err := row.Scan(&count); err != nil {
		t.Fatalf("Failed to query xmc_metrics table: %v", err)
	}
	t.Logf("Successfully verified xmc_metrics table in hermetic ClickHouse container, initial row count = %d", count)
}

func TestClickHouseInsertAndSelect(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	schemaPath := filepath.Join("..", "..", "..", "deploy", "clickhouse", "schema.sql")
	harness, err := StartHarness(ctx, schemaPath)
	if err != nil {
		t.Fatalf("StartHarness failed: %v", err)
	}
	defer func() {
		if err := harness.Teardown(ctx); err != nil {
			t.Errorf("Teardown failed: %v", err)
		}
	}()

	err = harness.ClickHouseConn.Exec(ctx, `
		INSERT INTO xmc_metrics (xid, work_unit_id, execution_id, replica_id, metric_name, metric_value, step, timestamp)
		VALUES (1001, 1, 1, '0', 'hello_world_metric', 42.5, 10, now64(6, 'UTC'))
	`)
	if err != nil {
		t.Fatalf("Failed to insert dummy metric record: %v", err)
	}

	var count uint64
	row := harness.ClickHouseConn.QueryRow(ctx, "SELECT count() FROM xmc_metrics WHERE metric_name = 'hello_world_metric'")
	if err := row.Scan(&count); err != nil {
		t.Fatalf("Failed to query inserted metric record: %v", err)
	}

	if count != 1 {
		t.Fatalf("Expected count 1 for metric_name 'hello_world_metric', got %d", count)
	}
	t.Logf("Successfully inserted and queried hello-world metric record from hermetic ClickHouse container")
}
