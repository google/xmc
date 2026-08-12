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

package dataengine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// MetricRecord represents an individual row in the metric seed dataset.
type MetricRecord struct {
	Xid          int64   `json:"xid"`
	WorkUnitId   int64   `json:"work_unit_id"`
	ExecutionId  int64   `json:"execution_id"`
	JobName      string  `json:"job_name"`
	ReplicaId    string  `json:"replica_id"`
	MetricName   string  `json:"metric_name"`
	MetricValue  float64 `json:"metric_value"`
	Step         int64   `json:"step"`
	TimestampStr string  `json:"timestamp"`
}

// EnsureSchema ensures the target metrics MergeTree table exists.
func EnsureSchema(ctx context.Context, conn driver.Conn, tableName string) error {
	if tableName == "" {
		tableName = "xmc_metrics"
	}

	query := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s
		(
			xid Int64,
			work_unit_id Int64,
			execution_id Int64,
			job_name String,
			replica_id String,
			metric_name LowCardinality(String),
			metric_value Float64,
			step Int64,
			timestamp DateTime64(6, 'UTC'),
			created_at DateTime64(6, 'UTC') DEFAULT now64(6, 'UTC')
		)
		ENGINE = MergeTree()
		PARTITION BY toYYYYMM(timestamp)
		PRIMARY KEY (xid, metric_name, timestamp)
		ORDER BY (xid, metric_name, timestamp, work_unit_id, execution_id, job_name, replica_id, step);
	`, tableName)

	return conn.Exec(ctx, query)
}

// FindFile locates a file checking relative paths and Bazel runfiles.
func FindFile(filename string) (string, error) {
	if srcDir := os.Getenv("TEST_SRCDIR"); srcDir != "" {
		workspace := os.Getenv("TEST_WORKSPACE")
		if workspace == "" {
			workspace = "xmanager_cloud"
		}
		path := filepath.Join(srcDir, workspace, filename)
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}

	candidates := []string{
		filename,
		filepath.Join("test/dataengine", filename),
		filepath.Join("..", filename),
		filepath.Join("..", "..", filename),
		filepath.Join("..", "..", "..", filename),
	}

	for _, cand := range candidates {
		if _, err := os.Stat(cand); err == nil {
			return cand, nil
		}
	}

	return "", fmt.Errorf("file %q not found in search paths", filename)
}

// SeedDataset loads seed records from a JSON file and inserts them into ClickHouse in batch.
func SeedDataset(ctx context.Context, conn driver.Conn, tableName string, seedFilePath string) (int, error) {
	if tableName == "" {
		tableName = "xmc_metrics"
	}

	resolvedPath, err := FindFile(seedFilePath)
	if err != nil {
		return 0, fmt.Errorf("failed to locate seed file: %w", err)
	}

	data, err := os.ReadFile(resolvedPath)
	if err != nil {
		return 0, fmt.Errorf("failed to read seed file %q: %w", resolvedPath, err)
	}

	var records []MetricRecord
	if err := json.Unmarshal(data, &records); err != nil {
		return 0, fmt.Errorf("failed to parse seed JSON: %w", err)
	}

	if len(records) == 0 {
		return 0, nil
	}

	batchQuery := fmt.Sprintf(`
		INSERT INTO %s (xid, work_unit_id, execution_id, job_name, replica_id, metric_name, metric_value, step, timestamp)
	`, tableName)

	batch, err := conn.PrepareBatch(ctx, batchQuery)
	if err != nil {
		return 0, fmt.Errorf("failed to prepare ClickHouse batch: %w", err)
	}

	for _, r := range records {
		ts, err := time.Parse(time.RFC3339Nano, r.TimestampStr)
		if err != nil {
			ts, err = time.Parse(time.RFC3339, r.TimestampStr)
			if err != nil {
				ts = time.Now().UTC()
			}
		}

		err = batch.Append(
			r.Xid,
			r.WorkUnitId,
			r.ExecutionId,
			r.JobName,
			r.ReplicaId,
			r.MetricName,
			r.MetricValue,
			r.Step,
			ts,
		)
		if err != nil {
			return 0, fmt.Errorf("failed to append row to batch: %w", err)
		}
	}

	if err := batch.Send(); err != nil {
		return 0, fmt.Errorf("failed to send batch to ClickHouse: %w", err)
	}

	return len(records), nil
}

// CleanupTestMetrics purges test data by xid list.
func CleanupTestMetrics(ctx context.Context, conn driver.Conn, tableName string, xids ...int64) error {
	if len(xids) == 0 {
		return nil
	}
	if tableName == "" {
		tableName = "xmc_metrics"
	}

	for _, xid := range xids {
		query := fmt.Sprintf("ALTER TABLE %s DELETE WHERE xid = %d SETTINGS mutations_sync = 1", tableName, xid)
		if err := conn.Exec(ctx, query); err != nil {
			// Fallback without settings if unsupported
			fallbackQuery := fmt.Sprintf("ALTER TABLE %s DELETE WHERE xid = %d", tableName, xid)
			if fallbackErr := conn.Exec(ctx, fallbackQuery); fallbackErr != nil {
				return fmt.Errorf("failed to delete test data for xid %d: %w", xid, fallbackErr)
			}
		}
	}
	return nil
}
