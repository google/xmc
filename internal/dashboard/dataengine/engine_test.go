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

	dpb "xmanager_cloud/api/protos/dashboard"
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
			var gotSQL string
			if m, ok := tt.conn.(*mockClickHouseConn); ok {
				m.queryFn = func(_ context.Context, query string, _ ...any) (driver.Rows, error) {
					gotSQL = query
					return &mockRows{}, nil
				}
			}
			engine, err := New(tt.conn, tt.opts...)
			if (err != nil) != tt.wantErr {
				t.Fatalf("New() error = %v, wantErr = %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if _, err := engine.fetchSummaryBox(context.Background(), 1, 0, &dpb.PlotSpec{Data: &dpb.DataSpec{}}); err != nil {
				t.Fatalf("fetchSummaryBox() error: %v", err)
			}
			if wantFrom := "FROM " + tt.wantTable + " WHERE"; !strings.Contains(strings.Join(strings.Fields(gotSQL), " "), wantFrom) {
				t.Errorf("engine SQL = %q, want it to contain %q", gotSQL, wantFrom)
			}
		})
	}
}

func TestPing(t *testing.T) {
	pingErr := fmt.Errorf("connection refused")
	newEngine := func(conn ClickHouseConn) *DataEngine {
		engine, err := New(conn)
		if err != nil {
			t.Fatalf("New() error: %v", err)
		}
		return engine
	}
	tests := []struct {
		name    string
		engine  *DataEngine
		wantErr bool
	}{
		{
			name:   "reachable",
			engine: newEngine(&mockClickHouseConn{}),
		},
		{
			name: "unreachable",
			engine: newEngine(&mockClickHouseConn{
				pingFn: func(context.Context) error { return pingErr },
			}),
			wantErr: true,
		},
		{
			// The engine is optional at startup, so callers can hold a nil one; readiness must
			// hear about that rather than take down the process on a nil dereference.
			name:    "nil engine",
			engine:  nil,
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.engine.Ping(context.Background())

			if gotErr := err != nil; gotErr != tc.wantErr {
				t.Errorf("Ping() = %v, want error presence %t", err, tc.wantErr)
			}
		})
	}
}

func TestFetchChartData_NilEngine(t *testing.T) {
	var engine *DataEngine

	chartData, err := engine.FetchChartData(context.Background(), &dpb.Chart{
		Name: "experiments/1001/workUnits/1/charts/chart-1",
		Plots: []*dpb.PlotSpec{
			{
				PlotId: "scatter_curve",
				Type:   dpb.PlotType_SCATTER,
				Data:   &dpb.DataSpec{Metrics: []string{"accuracy"}},
			},
		},
	}, nil)

	if err == nil {
		t.Fatal("FetchChartData() on a nil engine returned no error, want one")
	}
	if chartData != nil {
		t.Errorf("FetchChartData() = %v, want nil", chartData)
	}
}

func TestFetchChartData(t *testing.T) {
	sampleTime := time.Date(2026, 8, 11, 10, 0, 0, 0, time.UTC)

	tests := []struct {
		name           string
		chart          *dpb.Chart
		req            *dpb.GetChartDataRequest
		queryResponses map[string][][]any
		wantErr        bool
		wantPlots      int
	}{
		{
			name: "chart with summary box and scatter plot",
			chart: &dpb.Chart{
				Name: "experiments/1001/workUnits/1/charts/chart-1",
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
			req: &dpb.GetChartDataRequest{
				Name:             "experiments/1001/workUnits/1/charts/chart-1",
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
				Name: "experiments/1001/workUnits/1/charts/chart-bar",
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
						return &mockRows{Data: tt.queryResponses["summary"]}, nil
					}
					return &mockRows{Data: tt.queryResponses["scatter"]}, nil
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

func TestDataEngineCloseAndMultiPlotOrdering(t *testing.T) {
	t.Parallel()
	var nilEngine *DataEngine
	if err := nilEngine.Close(); err != nil {
		t.Errorf("nilEngine.Close() = %v, want nil", err)
	}
	if err := (&DataEngine{}).Close(); err != nil {
		t.Errorf("empty DataEngine.Close() = %v, want nil", err)
	}

	sampleTime := time.Date(2026, 8, 11, 10, 0, 0, 0, time.UTC)
	closable := &closableMockConn{}
	closable.queryFn = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		return &mockRows{Data: [][]any{{"only", 0.1, 0.1, 0.1, 0.1, int64(10), sampleTime}}}, nil
	}
	engine, err := New(closable)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer func() {
		if err := engine.Close(); err != nil || !closable.closed.Load() {
			t.Errorf("engine.Close() err=%v, closed=%v", err, closable.closed.Load())
		}
	}()

	emptyData, err := engine.FetchChartData(context.Background(), &dpb.Chart{Name: "empty"}, nil)
	if err != nil || len(emptyData.GetPlotsData()) != 0 {
		t.Fatalf("empty chart FetchChartData() = (%v, %v), want 0 plots", emptyData, err)
	}
	singleData, err := engine.FetchChartData(context.Background(), &dpb.Chart{
		Name: "single", Xid: 1, Wid: 1,
		Plots: []*dpb.PlotSpec{{PlotId: "p-only", Type: dpb.PlotType_SUMMARY_BOX, Data: &dpb.DataSpec{Metrics: []string{"only"}}}},
	}, nil)
	if err != nil || len(singleData.GetPlotsData()) != 1 {
		t.Fatalf("single plot FetchChartData() = (%v, %v), want 1 plot", singleData, err)
	}

	slowGate := make(chan struct{})
	orderedConn := &mockClickHouseConn{
		queryFn: func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
			if strings.Contains(query, "argMax") {
				<-slowGate
				return &mockRows{Data: [][]any{{"loss", 0.1, 0.1, 0.1, 0.1, int64(10), sampleTime}}}, nil
			}
			close(slowGate)
			return &mockRows{Data: [][]any{{int64(20), 0.9, 0.9, 0.9, sampleTime, "acc", int64(1), int64(1)}}}, nil
		},
	}
	orderedEngine, _ := New(orderedConn)
	got, err := orderedEngine.FetchChartData(context.Background(), &dpb.Chart{
		Name: "ordered", Xid: 1, Wid: 1,
		Plots: []*dpb.PlotSpec{
			{PlotId: "first_slow_summary", Type: dpb.PlotType_SUMMARY_BOX, Data: &dpb.DataSpec{Metrics: []string{"loss"}}},
			{PlotId: "second_fast_scatter", Type: dpb.PlotType_SCATTER, Data: &dpb.DataSpec{Metrics: []string{"acc"}}},
		},
	}, nil)
	if err != nil {
		t.Fatalf("ordered FetchChartData() error: %v", err)
	}
	if len(got.GetPlotsData()) != 2 || got.GetPlotsData()[0].GetPlotId() != "first_slow_summary" || got.GetPlotsData()[1].GetPlotId() != "second_fast_scatter" {
		t.Errorf("unexpected plot ordering: %+v", got.GetPlotsData())
	}

	errConn := &mockClickHouseConn{
		queryFn: func(context.Context, string, ...any) (driver.Rows, error) { return nil, fmt.Errorf("query failed") },
	}
	errEngine, _ := New(errConn)
	if _, err := errEngine.FetchChartData(context.Background(), &dpb.Chart{
		Name: "err", Xid: 1, Wid: 1,
		Plots: []*dpb.PlotSpec{
			{PlotId: "p1", Type: dpb.PlotType_SCATTER, Data: &dpb.DataSpec{Metrics: []string{"a"}}},
			{PlotId: "p2", Type: dpb.PlotType_SCATTER, Data: &dpb.DataSpec{Metrics: []string{"b"}}},
		},
	}, nil); err == nil {
		t.Error("multi-plot FetchChartData() with failing query succeeded, want error")
	}

	var capturedSQL string
	clampEngine, _ := New(&mockClickHouseConn{
		queryFn: func(_ context.Context, query string, _ ...any) (driver.Rows, error) {
			capturedSQL = query
			return &mockRows{}, nil
		},
	})
	if _, err := clampEngine.FetchChartData(context.Background(), &dpb.Chart{
		Name: "clamp", Xid: 1, Wid: 1,
		Plots: []*dpb.PlotSpec{{
			PlotId: "p1",
			Type:   dpb.PlotType_SCATTER,
			Data: &dpb.DataSpec{
				Metrics: []string{"loss"},
				Ranges:  []*dpb.DataSpec_DataRange{{End: &dpb.DataSpec_DataRange_EndStep{EndStep: 8000}}},
			},
		}},
	}, &dpb.GetChartDataRequest{ChartWidthPixels: 100000}); err != nil {
		t.Fatalf("clamped FetchChartData() error: %v", err)
	}
	if !strings.Contains(capturedSQL, "intDiv(step, 2)") {
		t.Errorf("oversized ChartWidthPixels SQL = %q, want bucket size 2 after clamping to %d", capturedSQL, maxChartWidthPixels)
	}
}
