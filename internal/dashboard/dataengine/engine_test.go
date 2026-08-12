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
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	dpb "xmanager_cloud/dashboard_service/proto/api_go_proto"
	spb "xmanager_cloud/dashboard_service/proto/service_go_proto"
)

func TestNew(t *testing.T) {
	tests := []struct {
		name      string
		conn      ClickHouseConn
		opts      []Option
		wantErr   bool
		wantTable string
	}{
		{
			name:      "valid connection with defaults",
			conn:      &mockClickHouseConn{},
			wantErr:   false,
			wantTable: "xmc_metrics",
		},
		{
			name:      "custom table name option",
			conn:      &mockClickHouseConn{},
			opts:      []Option{WithTableName("custom_metrics")},
			wantErr:   false,
			wantTable: "custom_metrics",
		},
		{
			name:    "nil connection returns error",
			conn:    nil,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			engine, err := New(tt.conn, tt.opts...)
			if (err != nil) != tt.wantErr {
				t.Fatalf("New() error = %v, wantErr = %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if engine.tableName != tt.wantTable {
				t.Errorf("engine.tableName = %q, want %q", engine.tableName, tt.wantTable)
			}
		})
	}
}

func TestFetchChartData(t *testing.T) {
	sampleTime := time.Date(2026, 8, 11, 10, 0, 0, 0, time.UTC)

	tests := []struct {
		name           string
		chart          *dpb.Chart
		req            *spb.GetChartDataRequest
		queryResponses map[string][][]any
		wantErr        bool
		wantPlots      int
	}{
		{
			name: "chart with summary box and scatter plot",
			chart: &dpb.Chart{
				Name: "experiments/1001/charts/chart-1",
				Xid:  1001,
				Wid:  1,
				Plots: []*dpb.PlotSpec{
					{
						PlotId: "summary_kpi",
						Type:   dpb.PlotType_SUMMARY_BOX,
						Data: &dpb.DataSpec{
							Metrics: []string{"loss"},
						},
					},
					{
						PlotId: "scatter_curve",
						Type:   dpb.PlotType_SCATTER,
						Data: &dpb.DataSpec{
							Metrics: []string{"accuracy"},
						},
					},
				},
			},
			req: &spb.GetChartDataRequest{
				Name:             "experiments/1001/charts/chart-1",
				ChartWidthPixels: 1000,
			},
			queryResponses: map[string][][]any{
				"summary": {
					{"loss", 0.05, 0.02, 0.99, 0.25, int64(500), sampleTime},
				},
				"scatter": {
					{int64(100), 0.85, 0.85, 0.85, sampleTime, "accuracy", int64(1), int64(1001)},
					{int64(200), 0.92, 0.92, 0.92, sampleTime, "accuracy", int64(1), int64(1001)},
				},
			},
			wantPlots: 2,
		},
		{
			name:    "nil chart returns error",
			chart:   nil,
			req:     nil,
			wantErr: true,
		},
		{
			name: "unsupported plot type returns error",
			chart: &dpb.Chart{
				Name: "experiments/1001/charts/chart-bar",
				Plots: []*dpb.PlotSpec{
					{
						PlotId: "bar_plot",
						Type:   dpb.PlotType_BAR,
						Data: &dpb.DataSpec{
							Metrics: []string{"loss"},
						},
					},
				},
			},
			req:     nil,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockConn := &mockClickHouseConn{
				queryFn: func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
					if tt.queryResponses == nil {
						return nil, fmt.Errorf("unexpected query: %s", query)
					}
					if strings.Contains(query, "argMax") {
						return &mockRows{data: tt.queryResponses["summary"]}, nil
					}
					return &mockRows{data: tt.queryResponses["scatter"]}, nil
				},
			}

			engine, err := New(mockConn)
			if err != nil {
				t.Fatalf("New() unexpected error: %v", err)
			}

			chartData, err := engine.FetchChartData(context.Background(), tt.chart, tt.req)
			if (err != nil) != tt.wantErr {
				t.Fatalf("FetchChartData() error = %v, wantErr = %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}

			if chartData.GetName() != tt.chart.GetName() {
				t.Errorf("chartData.Name = %q, want %q", chartData.GetName(), tt.chart.GetName())
			}
			if len(chartData.GetPlotsData()) != tt.wantPlots {
				t.Errorf("got %d plots data, want %d", len(chartData.GetPlotsData()), tt.wantPlots)
			}
		})
	}
}
