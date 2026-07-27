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
	"fmt"
	"os"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	dashboardpb "xmanager_cloud/dashboard_service/proto/api_go_proto"
)

// Client defines the interface for interacting with ClickHouse to pull chart data.
type Client interface {
	GetChartData(ctx context.Context, req *dashboardpb.GetChartDataRequest, chart *dashboardpb.Chart) (*dashboardpb.ChartData, error)
	Close() error
}

// XMCClient implements Client using a ClickHouse driver connection.
type XMCClient struct {
	conn      driver.Conn
	tableName string
}

// NewClient initializes an XMCClient with an existing driver connection.
func NewClient(conn driver.Conn) *XMCClient {
	tableName := os.Getenv("CLICKHOUSE_METRICS_TABLE")
	if tableName == "" {
		tableName = "xmc_metrics"
	}
	return &XMCClient{
		conn:      conn,
		tableName: tableName,
	}
}

// NewClientWithFallback connects to ClickHouse using multi-strategy authentication fallback.
func NewClientWithFallback(ctx context.Context) (*XMCClient, error) {
	conn, err := ConnectWithFallback(ctx)
	if err != nil {
		return nil, err
	}
	return NewClient(conn), nil
}

// Close closes the underlying ClickHouse connection.
func (c *XMCClient) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// GetChartData queries ClickHouse for all (or filtered) plots within a chart and returns ChartData.
func (c *XMCClient) GetChartData(ctx context.Context, req *dashboardpb.GetChartDataRequest, chart *dashboardpb.Chart) (*dashboardpb.ChartData, error) {
	if req == nil || chart == nil {
		return nil, fmt.Errorf("request and chart must non-nil")
	}

	plots := filterPlots(chart.Plots, req.PlotIds)
	chartData := &dashboardpb.ChartData{
		Name: chart.Name,
		Xid:  chart.Xid,
		Wid:  chart.Wid,
	}

	for _, plot := range plots {
		plotData, err := c.fetchPlotData(ctx, req, chart.Xid, chart.Wid, plot)
		if err != nil {
			return nil, fmt.Errorf("failed fetching data for plot %q: %w", plot.PlotId, err)
		}
		if plotData != nil {
			chartData.PlotsData = append(chartData.PlotsData, plotData)
		}
	}

	return chartData, nil
}

func filterPlots(plots []*dashboardpb.PlotSpec, allowedIDs []string) []*dashboardpb.PlotSpec {
	if len(allowedIDs) == 0 {
		return plots
	}

	idMap := make(map[string]bool, len(allowedIDs))
	for _, id := range allowedIDs {
		idMap[id] = true
	}

	var filtered []*dashboardpb.PlotSpec
	for _, p := range plots {
		if idMap[p.PlotId] {
			filtered = append(filtered, p)
		}
	}

	return filtered
}

func (c *XMCClient) fetchPlotData(ctx context.Context, req *dashboardpb.GetChartDataRequest, xid, wid int64, plot *dashboardpb.PlotSpec) (*dashboardpb.ChartData_PlotData, error) {
	q, err := TranslatePlotQuery(plot, xid, wid, req, c.tableName)
	if err != nil {
		return nil, err
	}

	rows, err := c.conn.Query(ctx, q.SQL, q.Args...)
	if err != nil {
		return nil, fmt.Errorf("query execution failed: %w", err)
	}
	defer rows.Close()

	return mapPlotRows(rows, req, plot)
}

func mapPlotRows(rows driver.Rows, req *dashboardpb.GetChartDataRequest, plot *dashboardpb.PlotSpec) (*dashboardpb.ChartData_PlotData, error) {
	plotData := &dashboardpb.ChartData_PlotData{
		PlotId: plot.PlotId,
		Type:   plot.Type,
	}

	switch plot.Type {
	case dashboardpb.PlotType_SUMMARY_BOX:
		summary, err := MapSummaryRows(rows)
		if err != nil {
			return nil, err
		}
		plotData.Content = &dashboardpb.ChartData_PlotData_SummaryData{SummaryData: summary}

	case dashboardpb.PlotType_SCATTER:
		series, err := mapScatterContent(rows, req)
		if err != nil {
			return nil, err
		}
		plotData.Content = &dashboardpb.ChartData_PlotData_ScatterSeries{ScatterSeries: series}

	case dashboardpb.PlotType_BAR, dashboardpb.PlotType_HISTOGRAM:
		bar, err := MapBarRows(rows)
		if err != nil {
			return nil, err
		}
		plotData.Content = &dashboardpb.ChartData_PlotData_BarData{BarData: bar}
	}

	return plotData, nil
}

func mapScatterContent(rows driver.Rows, req *dashboardpb.GetChartDataRequest) (*dashboardpb.ChartData_SeriesData, error) {
	if req != nil && req.ChartWidthPixels > 0 {
		return MapScatterLTTBRows(rows)
	}
	return MapScatterRows(rows)
}
