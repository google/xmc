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

package metricsquery

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"xmanager_cloud/pkg/metricsquery/metricsquerytest"
)

// normalizeSQL collapses whitespace so SQL can be compared independent of indentation.
func normalizeSQL(sql string) string { return strings.Join(strings.Fields(sql), " ") }

func TestQuerySeriesSQL(t *testing.T) {
	const (
		rawSQL = "SELECT step AS bucket_step, metric_value AS avg_val, metric_value AS min_val, " +
			"metric_value AS max_val, CAST(1 AS BIGINT) AS sample_count, timestamp AS max_ts, " +
			"metric_name, work_unit_id, xid, execution_id, job_name, replica_id FROM tbl " +
			"WHERE xid = ? AND work_unit_id = ? AND metric_name = ? " +
			"ORDER BY metric_name ASC, work_unit_id ASC, step ASC, timestamp ASC, " +
			"execution_id ASC, job_name ASC, replica_id ASC;"
		bucketedSQL = "SELECT %DIV%(step, 100) * 100 AS bucket_step, avg(metric_value) AS avg_val, " +
			"min(metric_value) AS min_val, max(metric_value) AS max_val, " +
			"CAST(count(*) AS BIGINT) AS sample_count, max(timestamp) AS max_ts, " +
			"metric_name, work_unit_id, xid, execution_id, job_name, replica_id FROM tbl " +
			"WHERE xid = ? AND work_unit_id = ? AND metric_name = ? " +
			"GROUP BY bucket_step, metric_name, work_unit_id, xid, execution_id, job_name, replica_id " +
			"ORDER BY metric_name ASC, work_unit_id ASC, bucket_step ASC, " +
			"execution_id ASC, job_name ASC, replica_id ASC;"
	)
	filter := MetricFilter{XID: 1001, WorkUnitID: 7, MetricNames: []string{"loss"}}
	tests := []struct {
		name       string
		dialect    dialect
		bucketSize int64
		wantSQL    string
	}{
		{name: "clickhouse raw", dialect: dialectClickHouse, bucketSize: 1, wantSQL: rawSQL},
		{name: "clickhouse zero bucket is raw", dialect: dialectClickHouse, bucketSize: 0, wantSQL: rawSQL},
		{name: "clickhouse bucketed uses intDiv", dialect: dialectClickHouse, bucketSize: 100, wantSQL: strings.ReplaceAll(bucketedSQL, "%DIV%", "intDiv")},
		{name: "bigquery raw", dialect: dialectBigQuery, bucketSize: 1, wantSQL: rawSQL},
		{name: "bigquery negative bucket is raw", dialect: dialectBigQuery, bucketSize: -5, wantSQL: rawSQL},
		{name: "bigquery bucketed uses DIV", dialect: dialectBigQuery, bucketSize: 100, wantSQL: strings.ReplaceAll(bucketedSQL, "%DIV%", "DIV")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q, rec := newRecordingQuerier(t, tt.dialect, "tbl", queryRecorder{rows: &metricsquerytest.Rows{}})
			if _, err := q.QuerySeries(context.Background(), filter, tt.bucketSize); err != nil {
				t.Fatalf("QuerySeries() error: %v", err)
			}
			if got := normalizeSQL(rec.query); got != tt.wantSQL {
				t.Errorf("QuerySeries() SQL =\n%s\nwant\n%s", got, tt.wantSQL)
			}
			if diff := cmp.Diff([]any{int64(1001), int64(7), "loss"}, rec.args); diff != "" {
				t.Errorf("QuerySeries() args mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestQuerySeriesRows(t *testing.T) {
	ts := time.Date(2026, 8, 11, 10, 0, 0, 0, time.UTC)
	ts2 := ts.Add(30 * time.Second)
	queryErr := errors.New("table not found")
	iterErr := errors.New("stream reset")
	tests := []struct {
		name       string
		bucketSize int64
		rec        queryRecorder
		want       []MetricPoint
		wantErr    error
	}{
		{
			name:       "maps raw rows in order",
			bucketSize: 1,
			rec: queryRecorder{rows: &metricsquerytest.Rows{Data: [][]any{
				{int64(0), 0.9, 0.9, 0.9, int64(1), ts, "loss", int64(1), int64(1001), int64(1), "worker", "0"},
				{int64(100), 0.7, 0.7, 0.7, int64(1), ts2, "loss", int64(2), int64(1001), int64(1), "worker", "0"},
			}}},
			want: []MetricPoint{
				{XID: 1001, WorkUnitID: 1, ExecutionID: 1, JobName: "worker", ReplicaID: "0", MetricName: "loss", Step: 0, Timestamp: ts, Avg: 0.9, Min: 0.9, Max: 0.9, Count: 1},
				{XID: 1001, WorkUnitID: 2, ExecutionID: 1, JobName: "worker", ReplicaID: "0", MetricName: "loss", Step: 100, Timestamp: ts2, Avg: 0.7, Min: 0.7, Max: 0.7, Count: 1},
			},
		},
		{
			name:       "maps bucketed multi-replica multi-execution multi-job rows",
			bucketSize: 100,
			rec: queryRecorder{rows: &metricsquerytest.Rows{Data: [][]any{
				{int64(0), 0.85, 0.75, 0.95, int64(10), ts, "loss", int64(1), int64(1001), int64(1), "trainer", "0"},
				{int64(0), 0.90, 0.80, 1.00, int64(8), ts2, "loss", int64(1), int64(1001), int64(1), "trainer", "1"},
				{int64(0), 0.65, 0.60, 0.70, int64(4), ts2, "loss", int64(1), int64(1001), int64(2), "evaluator", "0"},
			}}},
			want: []MetricPoint{
				{XID: 1001, WorkUnitID: 1, ExecutionID: 1, JobName: "trainer", ReplicaID: "0", MetricName: "loss", Step: 0, Timestamp: ts, Avg: 0.85, Min: 0.75, Max: 0.95, Count: 10},
				{XID: 1001, WorkUnitID: 1, ExecutionID: 1, JobName: "trainer", ReplicaID: "1", MetricName: "loss", Step: 0, Timestamp: ts2, Avg: 0.90, Min: 0.80, Max: 1.00, Count: 8},
				{XID: 1001, WorkUnitID: 1, ExecutionID: 2, JobName: "evaluator", ReplicaID: "0", MetricName: "loss", Step: 0, Timestamp: ts2, Avg: 0.65, Min: 0.60, Max: 0.70, Count: 4},
			},
		},
		{name: "empty result", bucketSize: 1, rec: queryRecorder{rows: &metricsquerytest.Rows{}}},
		{name: "query error", bucketSize: 1, rec: queryRecorder{err: queryErr}, wantErr: queryErr},
		{name: "rows iteration error", bucketSize: 1, rec: queryRecorder{rows: &metricsquerytest.Rows{IterErr: iterErr}}, wantErr: iterErr},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q, _ := newRecordingQuerier(t, dialectClickHouse, "tbl", tt.rec)
			got, err := q.QuerySeries(context.Background(), MetricFilter{XID: 1001}, tt.bucketSize)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("QuerySeries() error = %v, want %v", err, tt.wantErr)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("QuerySeries() mismatch (-want +got):\n%s", diff)
			}
			if tt.rec.rows != nil && !tt.rec.rows.Closed() {
				t.Error("QuerySeries() left rows open")
			}
		})
	}
}

func TestQuerySeriesScanError(t *testing.T) {
	rows := &metricsquerytest.Rows{Data: [][]any{{"not-a-step"}}}
	q, _ := newRecordingQuerier(t, dialectBigQuery, "tbl", queryRecorder{rows: rows})
	if _, err := q.QuerySeries(context.Background(), MetricFilter{XID: 1}, 1); err == nil || !strings.Contains(err.Error(), "failed to scan series row") {
		t.Errorf("QuerySeries() error = %v, want scan error", err)
	}
	if !rows.Closed() {
		t.Error("QuerySeries() left rows open after scan error")
	}
}
