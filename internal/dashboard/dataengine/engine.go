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

	dpb "xmanager_cloud/dashboard_service/proto/api_go_proto"
	spb "xmanager_cloud/dashboard_service/proto/service_go_proto"
)

const defaultMetricsTable = "xmc_metrics"

// DataEngine orchestrates analytics queries against ClickHouse.
type DataEngine struct {
	conn      ClickHouseConn
	tableName string
}

// Option configures DataEngine settings.
type Option func(*DataEngine)

// WithTableName overrides the default metrics table name.
func WithTableName(name string) Option {
	return func(e *DataEngine) {
		if name != "" {
			e.tableName = name
		}
	}
}

// New creates a new DataEngine instance.
func New(conn ClickHouseConn, opts ...Option) (*DataEngine, error) {
	if conn == nil {
		return nil, fmt.Errorf("clickhouse connection must not be nil")
	}
	engine := &DataEngine{
		conn:      conn,
		tableName: defaultMetricsTable,
	}
	for _, opt := range opts {
		opt(engine)
	}
	return engine, nil
}

// FetchChartData compiles and executes queries for all plots within a Chart.
func (e *DataEngine) FetchChartData(ctx context.Context, chart *dpb.Chart, req *spb.GetChartDataRequest) (*dpb.ChartData, error) {
	// The engine is optional at startup, so a caller can end up holding a nil one. Report that as an
	// error rather than letting the plot fetchers take down the process on a nil dereference.
	if e == nil {
		return nil, fmt.Errorf("data engine is not initialized")
	}
	if chart == nil {
		return nil, fmt.Errorf("chart specification must not be nil")
	}

	chartData := &dpb.ChartData{
		Name:      chart.GetName(),
		Xid:       chart.GetXid(),
		Wid:       chart.GetWid(),
		PlotsData: make([]*dpb.ChartData_PlotData, 0),
	}

	widthPixels := int32(0)
	if req != nil {
		widthPixels = req.GetChartWidthPixels()
	}

	for _, plot := range chart.GetPlots() {
		switch plot.GetType() {
		case dpb.PlotType_SUMMARY_BOX:
			pd, err := e.fetchSummaryBox(ctx, chart.GetXid(), chart.GetWid(), plot)
			if err != nil {
				return nil, fmt.Errorf("failed to fetch summary box for plot %q: %w", plot.GetPlotId(), err)
			}
			chartData.PlotsData = append(chartData.PlotsData, pd...)
		case dpb.PlotType_SCATTER, dpb.PlotType_PLOT_TYPE_UNSPECIFIED:
			pd, err := e.fetchSeriesData(ctx, chart.GetXid(), chart.GetWid(), plot, widthPixels)
			if err != nil {
				return nil, fmt.Errorf("failed to fetch series data for plot %q: %w", plot.GetPlotId(), err)
			}
			chartData.PlotsData = append(chartData.PlotsData, pd...)
		default:
			return nil, fmt.Errorf("unsupported plot type: %v", plot.GetType())
		}
	}

	return chartData, nil
}
