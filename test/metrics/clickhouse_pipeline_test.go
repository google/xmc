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

package metrics_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/protobuf/proto"

	metricspb "xmanager_cloud/api/protos/metrics"
	"xmanager_cloud/test/metrics/infra"
)

const (
	metricsTopic = "xmc-metrics"
	testTimeout  = 3 * time.Minute
	pollTimeout  = 45 * time.Second
)

func locateSchemaFile(t *testing.T) string {
	t.Helper()
	if srcDir := os.Getenv("TEST_SRCDIR"); srcDir != "" {
		workspace := os.Getenv("TEST_WORKSPACE")
		if workspace == "" {
			workspace = "xmanager_cloud"
		}
		path := filepath.Join(srcDir, workspace, "deploy", "clickhouse", "schema.sql")
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	candidates := []string{
		filepath.Join("..", "..", "deploy", "clickhouse", "schema.sql"),
		filepath.Join("..", "deploy", "clickhouse", "schema.sql"),
		filepath.Join("deploy", "clickhouse", "schema.sql"),
	}
	for _, cand := range candidates {
		if _, err := os.Stat(cand); err == nil {
			return cand
		}
	}
	t.Fatalf("schema.sql not found")
	return ""
}

func produceProtoMetrics(ctx context.Context, brokers []string, metrics []*metricspb.Metric) error {
	client, err := kgo.NewClient(
		kgo.SeedBrokers(brokers...),
		kgo.DefaultProduceTopic(metricsTopic),
		infra.KafkaDialer(brokers),
	)
	if err != nil {
		return err
	}
	defer client.Close()

	for _, m := range metrics {
		data, err := proto.Marshal(m)
		if err != nil {
			return err
		}
		r := &kgo.Record{Topic: metricsTopic, Value: data}
		res := client.ProduceSync(ctx, r)
		if err := res.FirstErr(); err != nil {
			return err
		}
	}
	return nil
}

func pollRecordCount(ctx context.Context, harness *infra.Harness, query string, expectedMin uint64) (uint64, error) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	timeout := time.NewTimer(pollTimeout)
	defer timeout.Stop()

	var lastErr error
	for {
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-timeout.C:
			var count uint64
			row := harness.ClickHouseConn.QueryRow(ctx, query)
			if err := row.Scan(&count); err != nil {
				return 0, fmt.Errorf("timeout waiting for records, query error: %w", err)
			}
			if count < expectedMin {
				if lastErr != nil {
					return count, fmt.Errorf("timeout waiting for %d records (got %d), query error: %w", expectedMin, count, lastErr)
				}
				return count, fmt.Errorf("timeout waiting for %d records (got %d)", expectedMin, count)
			}
			return count, nil
		case <-ticker.C:
			var count uint64
			row := harness.ClickHouseConn.QueryRow(ctx, query)
			if err := row.Scan(&count); err == nil {
				if count >= expectedMin {
					return count, nil
				}
			} else {
				lastErr = err
			}
		}
	}
}

func dumpClickHouseDiagnostics(ctx context.Context, t *testing.T, harness *infra.Harness) {
	t.Helper()
	rows, err := harness.ClickHouseConn.Query(ctx, "SELECT name, value, last_error_message FROM system.errors WHERE value > 0 LIMIT 10")
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var name, msg string
			var val uint64
			if err := rows.Scan(&name, &val, &msg); err == nil {
				t.Logf("ClickHouse System Error: %s (count=%d) msg=%s", name, val, msg)
			}
		}
	}

	kcRows, err := harness.ClickHouseConn.Query(ctx, "SELECT table, consumer_id, last_exception FROM system.kafka_consumers")
	if err == nil {
		defer kcRows.Close()
		for kcRows.Next() {
			var tbl, cid, ex string
			if err := kcRows.Scan(&tbl, &cid, &ex); err == nil {
				t.Logf("Kafka Consumer: table=%s id=%s last_exception=%s", tbl, cid, ex)
			}
		}
	}

	logRows, err := harness.ClickHouseConn.Query(ctx, "SELECT logger_name, level, message FROM system.text_log WHERE logger_name LIKE '%Kafka%' OR message LIKE '%kafka%' OR message LIKE '%Kafka%' ORDER BY event_time_microseconds DESC LIMIT 15")
	if err == nil {
		defer logRows.Close()
		for logRows.Next() {
			var loggerName, level, msg string
			if err := logRows.Scan(&loggerName, &level, &msg); err == nil {
				t.Logf("ClickHouse Log [%s][%s]: %s", loggerName, level, msg)
			}
		}
	}

	_ = harness.ClickHouseConn.Exec(ctx, "DETACH TABLE xmc_metrics_mv")
	qRow := harness.ClickHouseConn.QueryRow(ctx, "SELECT count() FROM xmc_metrics_queue SETTINGS stream_like_engine_allow_direct_select=1")
	var qCount uint64
	if err := qRow.Scan(&qCount); err != nil {
		t.Logf("Direct SELECT xmc_metrics_queue err: %v", err)
	} else {
		t.Logf("Direct SELECT xmc_metrics_queue count: %d", qCount)
	}
	_ = harness.ClickHouseConn.Exec(ctx, "ATTACH TABLE xmc_metrics_mv")
}

func TestClickHousePipelineIngestion(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	schemaPath := locateSchemaFile(t)
	harness, err := infra.StartHarness(ctx, schemaPath)
	if err != nil {
		t.Fatalf("StartHarness failed: %v", err)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		_ = harness.Teardown(cleanupCtx)
	}()

	nowMicro := time.Now().UnixMicro()
	validMetric := &metricspb.Metric{
		Xid:         10001,
		WorkUnitId:  1,
		ExecutionId: 10,
		ReplicaId:   "worker-0",
		MetricName:  "train_loss",
		MetricValue: 0.1234,
		Step:        100,
		Timestamp:   nowMicro,
	}

	if err := produceProtoMetrics(ctx, harness.RedpandaBrokers, []*metricspb.Metric{validMetric}); err != nil {
		t.Fatalf("Failed to produce metric: %v", err)
	}

	query := "SELECT count() FROM xmc_metrics WHERE xid = 10001 AND metric_name = 'train_loss'"
	count, err := pollRecordCount(ctx, harness, query, 1)
	if err != nil {
		t.Fatalf("Failed to query ClickHouse: %v", err)
	}
	if count != 1 {
		dumpClickHouseDiagnostics(ctx, t, harness)
		t.Fatalf("Expected exactly 1 ingested record in xmc_metrics, got %d", count)
	}

	var xid, workUnitID, executionID, step int64
	var replicaID, metricName string
	var metricValue float64
	var timestamp time.Time
	selectQuery := "SELECT xid, work_unit_id, execution_id, replica_id, metric_name, metric_value, step, timestamp FROM xmc_metrics WHERE xid = 10001 AND metric_name = 'train_loss' LIMIT 1"
	row := harness.ClickHouseConn.QueryRow(ctx, selectQuery)
	if err := row.Scan(&xid, &workUnitID, &executionID, &replicaID, &metricName, &metricValue, &step, &timestamp); err != nil {
		t.Fatalf("Failed to read record fields: %v", err)
	}

	if xid != 10001 || workUnitID != 1 || executionID != 10 || replicaID != "worker-0" || metricName != "train_loss" || metricValue != 0.1234 || step != 100 {
		t.Errorf("Ingested metric fields mismatch: got xid=%d, work_unit_id=%d, execution_id=%d, replica_id=%s, metric_name=%s, metric_value=%f, step=%d",
			xid, workUnitID, executionID, replicaID, metricName, metricValue, step)
	}

	if timestamp.UnixMicro() != nowMicro {
		t.Errorf("Ingested timestamp mismatch: got %v (micro=%d), expected %d", timestamp, timestamp.UnixMicro(), nowMicro)
	}
}

func TestClickHousePipelineRoutingAndFiltering(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	schemaPath := locateSchemaFile(t)
	harness, err := infra.StartHarness(ctx, schemaPath)
	if err != nil {
		t.Fatalf("StartHarness failed: %v", err)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		_ = harness.Teardown(cleanupCtx)
	}()

	nowMicro := time.Now().UnixMicro()

	validRecord1 := &metricspb.Metric{
		Xid:         20001,
		WorkUnitId:  2,
		ExecutionId: 20,
		ReplicaId:   "worker-0",
		MetricName:  "eval_accuracy",
		MetricValue: 0.95,
		Step:        50,
		Timestamp:   nowMicro,
	}
	invalidRecordZeroXID := &metricspb.Metric{
		Xid:         0, // Invalid: xid == 0
		WorkUnitId:  2,
		ExecutionId: 20,
		ReplicaId:   "worker-0",
		MetricName:  "eval_accuracy",
		MetricValue: 0.95,
		Step:        50,
		Timestamp:   nowMicro,
	}
	invalidRecordEmptyMetricName := &metricspb.Metric{
		Xid:         20002,
		WorkUnitId:  2,
		ExecutionId: 20,
		ReplicaId:   "worker-0",
		MetricName:  "", // Invalid: metric_name == ""
		MetricValue: 0.95,
		Step:        50,
		Timestamp:   nowMicro,
	}
	invalidRecordZeroExecutionID := &metricspb.Metric{
		Xid:         20003,
		WorkUnitId:  2,
		ExecutionId: 0, // Invalid: execution_id == 0
		ReplicaId:   "worker-0",
		MetricName:  "eval_accuracy",
		MetricValue: 0.95,
		Step:        50,
		Timestamp:   nowMicro,
	}
	validRecord2 := &metricspb.Metric{
		Xid:         20004,
		WorkUnitId:  2,
		ExecutionId: 20,
		ReplicaId:   "worker-1",
		MetricName:  "eval_loss",
		MetricValue: 0.05,
		Step:        50,
		Timestamp:   nowMicro,
	}
	recordWithBadTimestamp := &metricspb.Metric{
		Xid:         20005,
		WorkUnitId:  2,
		ExecutionId: 20,
		ReplicaId:   "worker-0",
		MetricName:  "eval_accuracy",
		MetricValue: 0.95,
		Step:        50,
		Timestamp:   0, // Out of bounds, should be sanitized to current time by MV
	}

	testBatch := []*metricspb.Metric{
		validRecord1,
		invalidRecordZeroXID,
		invalidRecordEmptyMetricName,
		invalidRecordZeroExecutionID,
		validRecord2,
		recordWithBadTimestamp,
	}

	if err := produceProtoMetrics(ctx, harness.RedpandaBrokers, testBatch); err != nil {
		t.Fatalf("Failed to produce test batch: %v", err)
	}

	query := "SELECT count() FROM xmc_metrics WHERE xid IN (20001, 20002, 20003, 20004, 20005, 0)"
	count, err := pollRecordCount(ctx, harness, query, 3)
	if err != nil {
		t.Fatalf("Failed to query ClickHouse for routed records: %v", err)
	}

	if count != 3 {
		t.Fatalf("Expected exactly 3 valid records in xmc_metrics after Materialized View filtering, got %d", count)
	}

	var validCount uint64
	countQuery := "SELECT count() FROM xmc_metrics WHERE xid IN (20001, 20004, 20005)"
	row := harness.ClickHouseConn.QueryRow(ctx, countQuery)
	if err := row.Scan(&validCount); err != nil || validCount < 3 {
		t.Errorf("Expected valid records (xid=20001, 20004, 20005) to exist, got count=%d, err=%v", validCount, err)
	}

	var invalidCount uint64
	invalidQuery := "SELECT count() FROM xmc_metrics WHERE xid IN (0, 20002, 20003)"
	row = harness.ClickHouseConn.QueryRow(ctx, invalidQuery)
	if err := row.Scan(&invalidCount); err != nil || invalidCount != 0 {
		t.Errorf("Expected invalid records (xid=0, 20002, 20003) to be filtered out, got count=%d, err=%v", invalidCount, err)
	}

	var badTS time.Time
	tsQuery := "SELECT timestamp FROM xmc_metrics WHERE xid = 20005 LIMIT 1"
	row = harness.ClickHouseConn.QueryRow(ctx, tsQuery)
	if err := row.Scan(&badTS); err != nil {
		t.Errorf("Failed to query bad timestamp record: %v", err)
	} else if time.Since(badTS) > 5*time.Minute {
		t.Errorf("Expected timestamp to be sanitized to current time, got %v", badTS)
	}
}
