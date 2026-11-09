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

func TestQuerySummaryByMetricSQL(t *testing.T) {
	tests := []struct {
		name    string
		dialect dialect
		wantSQL string
	}{
		{
			name:    "clickhouse uses argMax",
			dialect: dialectClickHouse,
			wantSQL: "SELECT metric_name, argMax(metric_value, (timestamp, step)) AS latest_val, min(metric_value) AS min_val, " +
				"max(metric_value) AS max_val, avg(metric_value) AS mean_val, argMax(step, (timestamp, step)) AS latest_step, " +
				"max(timestamp) AS latest_ts FROM tbl WHERE xid = ? AND metric_name IN (?, ?) " +
				"GROUP BY metric_name ORDER BY metric_name ASC;",
		},
		{
			name:    "bigquery uses ARRAY_AGG",
			dialect: dialectBigQuery,
			wantSQL: "SELECT metric_name, latest.val AS latest_val, min_val, max_val, mean_val, latest.stp AS latest_step, latest_ts " +
				"FROM ( SELECT metric_name, ARRAY_AGG(STRUCT(metric_value AS val, step AS stp) ORDER BY timestamp DESC, step DESC LIMIT 1)[OFFSET(0)] AS latest, " +
				"MIN(metric_value) AS min_val, MAX(metric_value) AS max_val, AVG(metric_value) AS mean_val, MAX(timestamp) AS latest_ts " +
				"FROM tbl WHERE xid = ? AND metric_name IN (?, ?) GROUP BY metric_name ) ORDER BY metric_name ASC;",
		},
	}
	filter := MetricFilter{XID: 1001, MetricNames: []string{"acc", "loss"}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q, rec := newRecordingQuerier(t, tt.dialect, "tbl", queryRecorder{rows: &metricsquerytest.Rows{}})
			if _, err := q.QuerySummaryByMetric(context.Background(), filter); err != nil {
				t.Fatalf("QuerySummaryByMetric() error: %v", err)
			}
			if got := normalizeSQL(rec.query); got != tt.wantSQL {
				t.Errorf("QuerySummaryByMetric() SQL =\n%s\nwant\n%s", got, tt.wantSQL)
			}
			if diff := cmp.Diff([]any{int64(1001), "acc", "loss"}, rec.args); diff != "" {
				t.Errorf("QuerySummaryByMetric() args mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestQuerySummaryByMetricRows(t *testing.T) {
	ts := time.Date(2026, 8, 11, 10, 0, 0, 0, time.UTC)
	queryErr := errors.New("connection refused")
	iterErr := errors.New("stream reset")
	tests := []struct {
		name    string
		rec     queryRecorder
		want    []MetricSummary
		wantErr error
	}{
		{
			name: "maps rows in order",
			rec: queryRecorder{rows: &metricsquerytest.Rows{Data: [][]any{
				{"acc", 0.9, 0.1, 0.95, 0.5, int64(500), ts},
				{"loss", 0.05, 0.01, 0.99, 0.25, int64(400), ts},
			}}},
			want: []MetricSummary{
				{MetricName: "acc", Latest: 0.9, Min: 0.1, Max: 0.95, Mean: 0.5, LatestStep: 500, LatestTimestamp: ts},
				{MetricName: "loss", Latest: 0.05, Min: 0.01, Max: 0.99, Mean: 0.25, LatestStep: 400, LatestTimestamp: ts},
			},
		},
		{name: "empty result", rec: queryRecorder{rows: &metricsquerytest.Rows{}}},
		{name: "query error", rec: queryRecorder{err: queryErr}, wantErr: queryErr},
		{name: "rows iteration error", rec: queryRecorder{rows: &metricsquerytest.Rows{IterErr: iterErr}}, wantErr: iterErr},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q, _ := newRecordingQuerier(t, dialectBigQuery, "tbl", tt.rec)
			got, err := q.QuerySummaryByMetric(context.Background(), MetricFilter{XID: 1001})
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("QuerySummaryByMetric() error = %v, want %v", err, tt.wantErr)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("QuerySummaryByMetric() mismatch (-want +got):\n%s", diff)
			}
			if tt.rec.rows != nil && !tt.rec.rows.Closed() {
				t.Error("QuerySummaryByMetric() left rows open")
			}
		})
	}
}

func TestQuerySummaryByMetricScanError(t *testing.T) {
	rows := &metricsquerytest.Rows{Data: [][]any{{"loss", "not-a-float", 0.0, 0.0, 0.0, int64(1), time.Time{}}}}
	q, _ := newRecordingQuerier(t, dialectClickHouse, "tbl", queryRecorder{rows: rows})
	if _, err := q.QuerySummaryByMetric(context.Background(), MetricFilter{XID: 1}); err == nil || !strings.Contains(err.Error(), "failed to scan summary row") {
		t.Errorf("QuerySummaryByMetric() error = %v, want scan error", err)
	}
	if !rows.Closed() {
		t.Error("QuerySummaryByMetric() left rows open after scan error")
	}
}
