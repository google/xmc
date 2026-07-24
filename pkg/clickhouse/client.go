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

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	dashboardpb "xmanager_cloud/dashboard_service/proto/api_go_proto"
)

// Client is the interface for querying ClickHouse metrics for XMC dashboards.
type Client interface {
	GetChartData(ctx context.Context, req *dashboardpb.GetChartDataRequest, chart *dashboardpb.Chart) (*dashboardpb.ChartData, error)
	Close() error
}

// XMCClient implements Client using a ClickHouse driver connection.
type XMCClient struct {
	conn      driver.Conn
	tableName string
}

// NewClient returns a new XMCClient instance.
func NewClient(conn driver.Conn, tableName string) *XMCClient {
	if tableName == "" {
		tableName = "xmc_metrics"
	}
	return &XMCClient{
		conn:      conn,
		tableName: tableName,
	}
}

// Close closes the underlying ClickHouse connection.
func (c *XMCClient) Close() error {
	if c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

// GetChartData translates chart plots into ClickHouse SQL, queries the database, and maps results to ChartData.
func (c *XMCClient) GetChartData(ctx context.Context, req *dashboardpb.GetChartDataRequest, chart *dashboardpb.Chart) (*dashboardpb.ChartData, error) {
	if chart == nil {
		return nil, fmt.Errorf("chart cannot be nil")
	}

	translator := NewTranslator(c.tableName)
	chartData := &dashboardpb.ChartData{
		Name:      chart.GetName(),
		Xid:       chart.GetXid(),
		Wid:       chart.GetWid(),
		PlotsData: make([]*dashboardpb.ChartData_PlotData, 0, len(chart.GetPlots())),
	}

	if req != nil && req.GetXid() != 0 {
		chartData.Xid = req.GetXid()
	}
	if req != nil && req.GetWid() != 0 {
		chartData.Wid = req.GetWid()
	}

	for _, plot := range chart.GetPlots() {
		query, args, err := translator.TranslatePlot(req, chart, plot)
		if err != nil {
			return nil, fmt.Errorf("failed to translate plot %s: %w", plot.GetPlotId(), err)
		}

		rows, err := c.conn.Query(ctx, query, args...)
		if err != nil {
			return nil, fmt.Errorf("failed to query clickhouse for plot %s: %w", plot.GetPlotId(), err)
		}

		plotData, err := MapPlotRows(plot, rows)
		rows.Close()
		if err != nil {
			return nil, fmt.Errorf("failed to map rows for plot %s: %w", plot.GetPlotId(), err)
		}

		chartData.PlotsData = append(chartData.PlotsData, plotData)
	}

	return chartData, nil
}
