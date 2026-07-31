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

package clickhouse

import (
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	dashboardpb "xmanager_cloud/dashboard_service/proto/api_go_proto"
	servicepb "xmanager_cloud/dashboard_service/proto/service_go_proto"
)

func TestTranslatePlotQuery(t *testing.T) {
	const tableName = "test_metrics"
	basePlot := &dashboardpb.PlotSpec{
		PlotId: "plot-1",
		Data: &dashboardpb.DataSpec{
			Metrics: []string{"loss/train"},
		},
	}

	tests := []struct {
		name        string
		plot        *dashboardpb.PlotSpec
		req         *servicepb.GetChartDataRequest
		wantSQLSub  []string
		wantArgsLen int
		wantErr     bool
	}{
		{
			name: "SummaryBox",
			plot: &dashboardpb.PlotSpec{
				PlotId: "p-summary",
				Type:   dashboardpb.PlotType_SUMMARY_BOX,
				Data:   basePlot.Data,
			},
			wantSQLSub: []string{
				"SELECT",
				"argMax(metric_value, step) AS latest_value",
				"FROM test_metrics",
				"WHERE xid = ? AND work_unit_id = ? AND metric_name IN (?)",
			},
			wantArgsLen: 3,
		},
		{
			name: "ScatterStandard",
			plot: &dashboardpb.PlotSpec{
				PlotId: "p-scatter",
				Type:   dashboardpb.PlotType_SCATTER,
				Data:   basePlot.Data,
			},
			wantSQLSub: []string{
				"SELECT",
				"execution_id",
				"replica_id",
				"step AS iteration",
				"GROUP BY metric_name, work_unit_id, xid, execution_id, replica_id, step, timestamp",
			},
			wantArgsLen: 3,
		},
		{
			name: "ScatterLTTB",
			plot: &dashboardpb.PlotSpec{
				PlotId: "p-scatter-lttb",
				Type:   dashboardpb.PlotType_SCATTER,
				Data:   basePlot.Data,
			},
			req: &servicepb.GetChartDataRequest{
				ChartWidthPixels: 800,
			},
			wantSQLSub: []string{
				"lttb(800)(step, metric_value) AS points",
				"GROUP BY metric_name, work_unit_id, xid, execution_id, replica_id",
			},
			wantArgsLen: 3,
		},
		{
			name: "Bar",
			plot: &dashboardpb.PlotSpec{
				PlotId: "p-bar",
				Type:   dashboardpb.PlotType_BAR,
				Data:   basePlot.Data,
			},
			wantSQLSub: []string{
				"toString(metric_value) AS category_or_bin",
				"count() AS value_or_count",
			},
			wantArgsLen: 3,
		},
		{
			name: "Histogram",
			plot: &dashboardpb.PlotSpec{
				PlotId: "p-hist",
				Type:   dashboardpb.PlotType_HISTOGRAM,
				Data:   basePlot.Data,
			},
			wantSQLSub: []string{
				"histogram(10)(metric_value) AS histogram_buckets",
			},
			wantArgsLen: 3,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := TranslatePlotQuery(tc.plot, 1001, 2001, tc.req, tableName)
			if (err != nil) != tc.wantErr {
				t.Fatalf("TranslatePlotQuery() error = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			for _, sub := range tc.wantSQLSub {
				if !strings.Contains(got.SQL, sub) {
					t.Errorf("SQL missing substring %q\nGot SQL:\n%s", sub, got.SQL)
				}
			}
			if len(got.Args) != tc.wantArgsLen {
				t.Errorf("got %d args, want %d: %v", len(got.Args), tc.wantArgsLen, got.Args)
			}
		})
	}
}

func TestTranslatePlotQuery_RangeFilters(t *testing.T) {
	plot := &dashboardpb.PlotSpec{
		PlotId: "p-range",
		Type:   dashboardpb.PlotType_SCATTER,
		Data: &dashboardpb.DataSpec{
			Metrics: []string{"accuracy"},
			Ranges: []*dashboardpb.DataSpec_DataRange{
				{
					Start: &dashboardpb.DataSpec_DataRange_StartIteration{StartIteration: 10},
					End:   &dashboardpb.DataSpec_DataRange_EndIteration{EndIteration: 100},
				},
			},
		},
	}

	req := &servicepb.GetChartDataRequest{
		StartTime: timestamppb.New(time.Unix(1700000000, 0)),
	}

	got, err := TranslatePlotQuery(plot, 1001, 2001, req, "xmc_metrics")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(got.SQL, "step >= ?") || !strings.Contains(got.SQL, "step <= ?") || !strings.Contains(got.SQL, "timestamp >= ?") {
		t.Errorf("missing range filter SQL, got:\n%s", got.SQL)
	}
	if !strings.Contains(got.SQL, "((step >= ? AND step <= ?))") {
		t.Errorf("missing grouped range filter SQL, got:\n%s", got.SQL)
	}
	if len(got.Args) != 6 {
		t.Errorf("got %d args, want 6: %v", len(got.Args), got.Args)
	}
}

func TestValidateTableName(t *testing.T) {
	tests := []struct {
		name      string
		tableName string
		wantErr   bool
	}{
		{
			name:      "ValidSimpleName",
			tableName: "xmc_metrics",
			wantErr:   false,
		},
		{
			name:      "ValidQualifiedName",
			tableName: "default.xmc_metrics",
			wantErr:   false,
		},
		{
			name:      "ValidAlphanumericWithUnderscore",
			tableName: "test_metrics_v2",
			wantErr:   false,
		},
		{
			name:      "InvalidEmptyName",
			tableName: "",
			wantErr:   true,
		},
		{
			name:      "InvalidWithSpaces",
			tableName: "xmc metrics",
			wantErr:   true,
		},
		{
			name:      "InvalidWithSemicolon",
			tableName: "xmc_metrics; DROP TABLE users; --",
			wantErr:   true,
		},
		{
			name:      "InvalidWithQuotes",
			tableName: "`xmc_metrics`",
			wantErr:   true,
		},
		{
			name:      "InvalidWithComment",
			tableName: "xmc_metrics--",
			wantErr:   true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateTableName(tc.tableName)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ValidateTableName(%q) error = %v, wantErr %v", tc.tableName, err, tc.wantErr)
			}
		})
	}
}

func TestTranslatePlotQuery_InvalidTableName(t *testing.T) {
	plot := &dashboardpb.PlotSpec{
		PlotId: "p-invalid-table",
		Type:   dashboardpb.PlotType_SUMMARY_BOX,
		Data: &dashboardpb.DataSpec{
			Metrics: []string{"loss/train"},
		},
	}

	_, err := TranslatePlotQuery(plot, 1001, 2001, nil, "invalid; DROP TABLE xmc_metrics; --")
	if err == nil {
		t.Fatalf("expected error for invalid table name, got nil")
	}
}
