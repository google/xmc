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
	"testing"

	dashboardpb "xmanager_cloud/dashboard_service/proto/api_go_proto"
)

func TestTranslatePlot(t *testing.T) {
	translator := NewTranslator("custom_metrics")

	chart := &dashboardpb.Chart{
		Name: "test-chart",
		Xid:  101,
		Wid:  202,
	}

	tests := []struct {
		name      string
		req       *dashboardpb.GetChartDataRequest
		plot      *dashboardpb.PlotSpec
		wantQuery string
		wantErr   bool
	}{
		{
			name: "SUMMARY_BOX query",
			plot: &dashboardpb.PlotSpec{
				PlotId: "summary-1",
				Type:   dashboardpb.PlotType_SUMMARY_BOX,
				Data: &dashboardpb.DataSpec{
					Metrics: []string{"accuracy", "loss"},
				},
			},
			wantQuery: "SELECT metric_name, argMax(metric_value, step) AS latest_value, min(metric_value) AS min_value, " +
				"max(metric_value) AS max_value, avg(metric_value) AS mean_value, max(step) AS latest_iteration, " +
				"argMax(timestamp, step) AS latest_timestamp FROM custom_metrics WHERE xid = ? AND work_unit_id = ? AND metric_name IN (?) GROUP BY metric_name",
		},
		{
			name: "SCATTER standard series query",
			plot: &dashboardpb.PlotSpec{
				PlotId: "scatter-1",
				Type:   dashboardpb.PlotType_SCATTER,
				Data: &dashboardpb.DataSpec{
					Metrics: []string{"loss"},
				},
			},
			wantQuery: "SELECT metric_name, work_unit_id AS wid, xid, execution_id, replica_id, step AS iteration, " +
				"timestamp, avg(metric_value) AS avg_value, min(metric_value) AS min_value, max(metric_value) AS max_value FROM custom_metrics WHERE xid = ? AND work_unit_id = ? AND metric_name IN (?) GROUP BY metric_name, work_unit_id, xid, execution_id, replica_id, step, timestamp ORDER BY step ASC",
		},
		{
			name: "SCATTER downsampled query",
			req: &dashboardpb.GetChartDataRequest{
				DownsamplePoints: 300,
			},
			plot: &dashboardpb.PlotSpec{
				PlotId: "scatter-downsample",
				Type:   dashboardpb.PlotType_SCATTER,
				Data: &dashboardpb.DataSpec{
					Metrics: []string{"loss"},
				},
			},
			wantQuery: "SELECT metric_name, work_unit_id AS wid, xid, execution_id, replica_id, " +
				"lttb(?)(step, metric_value) AS downsampled_points FROM custom_metrics WHERE xid = ? AND work_unit_id = ? AND metric_name IN (?) GROUP BY metric_name, work_unit_id, xid, execution_id, replica_id",
		},
		{
			name: "SCATTER time interval query",
			req: &dashboardpb.GetChartDataRequest{
				IntervalSeconds: 60,
			},
			plot: &dashboardpb.PlotSpec{
				PlotId: "scatter-interval",
				Type:   dashboardpb.PlotType_SCATTER,
				Data: &dashboardpb.DataSpec{
					Metrics: []string{"loss"},
				},
			},
			wantQuery: "SELECT metric_name, work_unit_id AS wid, xid, execution_id, replica_id, " +
				"toStartOfInterval(fromUnixTimestamp64Micro(timestamp), INTERVAL ? SECOND) AS interval_window, " +
				"avg(metric_value) AS avg_value, min(metric_value) AS min_value, max(metric_value) AS max_value FROM custom_metrics WHERE xid = ? AND work_unit_id = ? AND metric_name IN (?) GROUP BY metric_name, work_unit_id, xid, execution_id, replica_id, interval_window ORDER BY interval_window ASC",
		},
		{
			name: "BAR categorical query",
			plot: &dashboardpb.PlotSpec{
				PlotId: "bar-1",
				Type:   dashboardpb.PlotType_BAR,
				Data: &dashboardpb.DataSpec{
					Metrics: []string{"error_code"},
				},
			},
			wantQuery: "SELECT metric_name, toString(metric_value) AS category_or_bin, count() AS value_or_count FROM custom_metrics WHERE xid = ? AND work_unit_id = ? AND metric_name IN (?) GROUP BY metric_name, category_or_bin",
		},
		{
			name: "HISTOGRAM continuous binning query",
			req: &dashboardpb.GetChartDataRequest{
				NumBins: 15,
			},
			plot: &dashboardpb.PlotSpec{
				PlotId: "hist-1",
				Type:   dashboardpb.PlotType_HISTOGRAM,
				Data: &dashboardpb.DataSpec{
					Metrics: []string{"latency"},
				},
			},
			wantQuery: "SELECT metric_name, histogram(?)(metric_value) AS histogram_buckets FROM custom_metrics WHERE xid = ? AND work_unit_id = ? AND metric_name IN (?) GROUP BY metric_name",
		},
		{
			name: "No metrics error",
			plot: &dashboardpb.PlotSpec{
				PlotId: "invalid",
				Type:   dashboardpb.PlotType_SUMMARY_BOX,
				Data:   &dashboardpb.DataSpec{},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			query, args, err := translator.TranslatePlot(tt.req, chart, tt.plot)
			if (err != nil) != tt.wantErr {
				t.Fatalf("TranslatePlot() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if query != tt.wantQuery {
				t.Errorf("TranslatePlot() query = %q, want %q", query, tt.wantQuery)
			}
			if len(args) == 0 {
				t.Errorf("TranslatePlot() expected non-empty args")
			}
		})
	}
}
