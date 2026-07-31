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
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"golang/protobuf/v2/proto/proto"

	dashboardpb "xmanager_cloud/dashboard_service/proto/api_go_proto"
	servicepb "xmanager_cloud/dashboard_service/proto/service_go_proto"
)

type mockConn struct {
	driver.Conn
	queryFunc func(ctx context.Context, query string, args ...any) (driver.Rows, error)
	closeFunc func() error
}

func (m *mockConn) Query(ctx context.Context, query string, args ...any) (driver.Rows, error) {
	if m.queryFunc != nil {
		return m.queryFunc(ctx, query, args...)
	}
	return nil, nil
}

func (m *mockConn) Close() error {
	if m.closeFunc != nil {
		return m.closeFunc()
	}
	return nil
}

func newBaseChart() *dashboardpb.Chart {
	return &dashboardpb.Chart{
		Name: "test_chart",
		Xid:  100,
		Wid:  1,
		Plots: []*dashboardpb.PlotSpec{
			{
				PlotId: "p1",
				Type:   dashboardpb.PlotType_SUMMARY_BOX,
				Data:   &dashboardpb.DataSpec{Metrics: []string{"loss"}},
			},
			{
				PlotId: "p2",
				Type:   dashboardpb.PlotType_BAR,
				Data:   &dashboardpb.DataSpec{Metrics: []string{"cat_metric"}},
			},
		},
	}
}

func newMockQueryConn() *mockConn {
	return &mockConn{
		queryFunc: func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
			if strings.Contains(query, "latest_value") {
				return &mockRows{
					data: [][]any{
						{"loss", 0.5, 0.1, 0.9, 0.45, int64(100), int64(1700000000000000)},
					},
				}, nil
			}
			return &mockRows{
				data: [][]any{
					{"cat_metric", "cat1", uint64(42)},
				},
			}, nil
		},
	}
}

func checkChartData(t *testing.T, res *dashboardpb.ChartData, wantPlots int, wantIDs ...string) {
	t.Helper()
	if res == nil {
		t.Fatal("expected non-nil ChartData")
	}
	if res.Name != "test_chart" || res.Xid != 100 || res.Wid != 1 {
		t.Errorf("got chart metadata %v, want test_chart/100/1", res)
	}
	if len(res.PlotsData) != wantPlots {
		t.Fatalf("got %d plots, want %d", len(res.PlotsData), wantPlots)
	}
	for i, id := range wantIDs {
		if res.PlotsData[i].PlotId != id {
			t.Errorf("got plot ID %q at index %d, want %q", res.PlotsData[i].PlotId, i, id)
		}
	}
}

func TestNewClient_TableNameResolutionOrder(t *testing.T) {
	t.Run("1_ConstructorArg", func(t *testing.T) {
		t.Setenv("CLICKHOUSE_METRICS_TABLE", "env_table")
		client := NewClient(nil, "arg_table")
		if client.tableName != "arg_table" {
			t.Errorf("got tableName %q, want %q", client.tableName, "arg_table")
		}
	})

	t.Run("2_EnvVar", func(t *testing.T) {
		t.Setenv("CLICKHOUSE_METRICS_TABLE", "env_table")
		client := NewClient(nil)
		if client.tableName != "env_table" {
			t.Errorf("got tableName %q, want %q", client.tableName, "env_table")
		}
	})

	t.Run("3_DefaultHardcoded", func(t *testing.T) {
		t.Setenv("CLICKHOUSE_METRICS_TABLE", "")
		client := NewClient(nil)
		if client.tableName != "xmc_metrics" {
			t.Errorf("got tableName %q, want %q", client.tableName, "xmc_metrics")
		}
	})
}

func TestNewClientWithFallback_Failure(t *testing.T) {
	t.Setenv("CLICKHOUSE_HOST", "127.0.0.1")
	t.Setenv("CLICKHOUSE_PORT", "1")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	client, err := NewClientWithFallback(ctx)
	if err == nil {
		if client != nil {
			_ = client.Close()
		}
		t.Fatal("expected error from NewClientWithFallback when database is unavailable")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected error chain to contain context.Canceled, got %v", err)
	}
}

func TestXMCClient_Close(t *testing.T) {
	tests := []struct {
		name    string
		client  *XMCClient
		wantErr bool
	}{
		{
			name: "non_nil_connection_success",
			client: NewClient(&mockConn{
				closeFunc: func() error { return nil },
			}),
			wantErr: false,
		},
		{
			name: "non_nil_connection_error",
			client: NewClient(&mockConn{
				closeFunc: func() error { return errors.New("close error") },
			}),
			wantErr: true,
		},
		{
			name:    "nil_connection",
			client:  NewClient(nil),
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.client.Close()
			if (err != nil) != tt.wantErr {
				t.Errorf("Close() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestFilterPlots(t *testing.T) {
	plots := []*dashboardpb.PlotSpec{
		{PlotId: "p1"},
		{PlotId: "p2"},
		{PlotId: "p3"},
	}

	filtered := filterPlots(plots, []string{"p1", "p3"})
	if len(filtered) != 2 {
		t.Fatalf("got %d plots, want 2", len(filtered))
	}
	if filtered[0].PlotId != "p1" || filtered[1].PlotId != "p3" {
		t.Errorf("got plot IDs [%s, %s], want [p1, p3]", filtered[0].PlotId, filtered[1].PlotId)
	}

	allPlots := filterPlots(plots, nil)
	if len(allPlots) != 3 {
		t.Errorf("got %d plots, want 3 when allowedIDs is nil", len(allPlots))
	}
}

func TestGetChartData_NilValidation(t *testing.T) {
	client := NewClient(nil)
	ctx := context.Background()

	if _, err := client.GetChartData(ctx, &servicepb.GetChartDataRequest{}, nil); err == nil {
		t.Error("expected error for nil chart, got nil")
	}
}

func TestXMCClient_GetChartData(t *testing.T) {
	baseChart := newBaseChart()
	baseReq := &servicepb.GetChartDataRequest{PlotIds: []string{"p1", "p2"}}
	conn := newMockQueryConn()

	tests := []struct {
		name      string
		client    *XMCClient
		req       *servicepb.GetChartDataRequest
		chart     *dashboardpb.Chart
		wantErr   bool
		errSubstr string
		wantPlots int
		wantIDs   []string
	}{
		{
			name:      "success_all_plots",
			client:    NewClient(conn),
			req:       baseReq,
			chart:     baseChart,
			wantPlots: 2,
			wantIDs:   []string{"p1", "p2"},
		},
		{
			name:   "success_filtered_plots",
			client: NewClient(conn),
			req: proto.Modify(baseReq, func(r *servicepb.GetChartDataRequest) {
				r.PlotIds = []string{"p1"}
			}),
			chart:     baseChart,
			wantPlots: 1,
			wantIDs:   []string{"p1"},
		},
		{
			name:      "success_nil_request",
			client:    NewClient(conn),
			req:       nil,
			chart:     baseChart,
			wantPlots: 2,
			wantIDs:   []string{"p1", "p2"},
		},
		{
			name:      "nil_chart_error",
			client:    NewClient(&mockConn{}),
			req:       baseReq,
			chart:     nil,
			wantErr:   true,
			errSubstr: "chart cannot be nil",
		},
		{
			name: "query_execution_error",
			client: NewClient(&mockConn{
				queryFunc: func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
					return nil, errors.New("query failed")
				},
			}),
			req:       baseReq,
			chart:     baseChart,
			wantErr:   true,
			errSubstr: "failed fetching data for plot",
		},
		{
			name:   "translation_error",
			client: NewClient(&mockConn{}),
			req:    baseReq,
			chart: proto.Modify(baseChart, func(c *dashboardpb.Chart) {
				c.Plots[0].Data.Metrics = nil
			}),
			wantErr:   true,
			errSubstr: "plot spec or metrics cannot be empty",
		},
		{
			name: "mapper_error_in_plot",
			client: NewClient(&mockConn{
				queryFunc: func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
					return &mockRows{err: errors.New("scan failed")}, nil
				},
			}),
			req:       baseReq,
			chart:     baseChart,
			wantErr:   true,
			errSubstr: "failed fetching data for plot",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := tt.client.GetChartData(context.Background(), tt.req, tt.chart)
			if (err != nil) != tt.wantErr {
				t.Fatalf("GetChartData() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				if tt.errSubstr != "" && !strings.Contains(err.Error(), tt.errSubstr) {
					t.Errorf("got error %q, want substring %q", err.Error(), tt.errSubstr)
				}
				return
			}
			checkChartData(t, res, tt.wantPlots, tt.wantIDs...)
		})
	}
}

func TestXMCClient_FetchPlotData(t *testing.T) {
	basePlot := &dashboardpb.PlotSpec{
		PlotId: "plot-sum",
		Type:   dashboardpb.PlotType_SUMMARY_BOX,
		Data:   &dashboardpb.DataSpec{Metrics: []string{"loss"}},
	}
	conn := newMockQueryConn()

	tests := []struct {
		name      string
		client    *XMCClient
		plot      *dashboardpb.PlotSpec
		wantErr   bool
		errSubstr string
		wantID    string
		wantType  dashboardpb.PlotType
	}{
		{
			name:     "success_summary_box",
			client:   NewClient(conn),
			plot:     basePlot,
			wantID:   "plot-sum",
			wantType: dashboardpb.PlotType_SUMMARY_BOX,
		},
		{
			name:   "success_bar_plot",
			client: NewClient(conn),
			plot: &dashboardpb.PlotSpec{
				PlotId: "plot-bar",
				Type:   dashboardpb.PlotType_BAR,
				Data:   &dashboardpb.DataSpec{Metrics: []string{"cat_metric"}},
			},
			wantID:   "plot-bar",
			wantType: dashboardpb.PlotType_BAR,
		},
		{
			name:      "nil_connection_error",
			client:    NewClient(nil),
			plot:      basePlot,
			wantErr:   true,
			errSubstr: "clickhouse connection is not initialized",
		},
		{
			name:   "translate_query_error",
			client: NewClient(&mockConn{}),
			plot: proto.Modify(basePlot, func(p *dashboardpb.PlotSpec) {
				p.Data.Metrics = nil
			}),
			wantErr:   true,
			errSubstr: "plot spec or metrics cannot be empty",
		},
		{
			name: "query_execution_error",
			client: NewClient(&mockConn{
				queryFunc: func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
					return nil, errors.New("connection refused")
				},
			}),
			plot:      basePlot,
			wantErr:   true,
			errSubstr: "query execution failed: connection refused",
		},
		{
			name: "map_plot_rows_error",
			client: NewClient(&mockConn{
				queryFunc: func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
					return &mockRows{err: errors.New("scan error")}, nil
				},
			}),
			plot:      basePlot,
			wantErr:   true,
			errSubstr: "scan error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := tt.client.fetchPlotData(context.Background(), nil, 100, 1, tt.plot)
			if (err != nil) != tt.wantErr {
				t.Fatalf("fetchPlotData() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				if tt.errSubstr != "" && !strings.Contains(err.Error(), tt.errSubstr) {
					t.Errorf("got error %q, want substring %q", err.Error(), tt.errSubstr)
				}
				return
			}
			if res == nil || res.PlotId != tt.wantID || res.Type != tt.wantType {
				t.Errorf("got plot data %v, want id %q type %v", res, tt.wantID, tt.wantType)
			}
		})
	}
}
