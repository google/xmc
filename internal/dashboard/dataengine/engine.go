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

	"golang.org/x/sync/errgroup"
	dpb "xmanager_cloud/api/protos/dashboard"
	xmcbigquery "xmanager_cloud/pkg/bigquery"
)

const (
	defaultMetricsTable = "xmc_metrics"
	maxConcurrentPlots  = 8
	maxChartWidthPixels = 4000
)

// DataEngine orchestrates analytics queries against the configured metrics backend.
type DataEngine struct {
	backend   queryBackend
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
		backend:   clickHouseBackend{conn: conn},
		tableName: defaultMetricsTable,
	}
	for _, opt := range opts {
		opt(engine)
	}
	return engine, nil
}

// NewBigQuery creates a new DataEngine backed by a BigQuery connection.
func NewBigQuery(conn xmcbigquery.Conn, opts ...Option) (*DataEngine, error) {
	if conn == nil {
		return nil, fmt.Errorf("bigquery connection must not be nil")
	}
	tableName := conn.TableRef()
	if tableName == "" {
		tableName = defaultMetricsTable
	}
	engine := &DataEngine{backend: bigQueryBackend{conn: conn}, tableName: tableName}
	for _, opt := range opts {
		opt(engine)
	}
	return engine, nil
}

func (e *DataEngine) activeBackend() queryBackend {
	if e == nil {
		return nil
	}
	return e.backend
}

// Ping checks that the database connection behind the engine is still usable.
//
// A built engine says only that a connection was established once. Readiness needs to know whether
// queries would work now, and the only way to find that out is to ask the backend.
func (e *DataEngine) Ping(ctx context.Context) error {
	if e.activeBackend() == nil {
		return fmt.Errorf("data engine is not initialized")
	}
	return e.activeBackend().Ping(ctx)
}

// Close releases any underlying client resources held by the DataEngine.
func (e *DataEngine) Close() error {
	if e.activeBackend() == nil {
		return nil
	}
	return e.activeBackend().Close()
}

// FetchChartData compiles and executes queries for all plots within a Chart.
func (e *DataEngine) FetchChartData(ctx context.Context, chart *dpb.Chart, req *dpb.GetChartDataRequest) (*dpb.ChartData, error) {
	// The engine is optional at startup, so a caller can end up holding a nil one. Report that as an
	// error rather than letting the plot fetchers take down the process on a nil dereference.
	if e.activeBackend() == nil {
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
	plots := chart.GetPlots()
	if len(plots) == 0 {
		return chartData, nil
	}

	widthPixels := int32(0)
	if req != nil {
		widthPixels = min(req.GetChartWidthPixels(), maxChartWidthPixels)
	}
	if len(plots) == 1 {
		pd, err := e.fetchPlotData(ctx, chart.GetXid(), chart.GetWid(), plots[0], widthPixels)
		if err != nil {
			return nil, err
		}
		chartData.PlotsData = append(chartData.PlotsData, pd...)
		return chartData, nil
	}

	perPlot := make([][]*dpb.ChartData_PlotData, len(plots))
	g, groupCtx := errgroup.WithContext(ctx)
	g.SetLimit(maxConcurrentPlots)
	for i, plot := range plots {
		g.Go(func() error {
			pd, err := e.fetchPlotData(groupCtx, chart.GetXid(), chart.GetWid(), plot, widthPixels)
			if err != nil {
				return err
			}
			perPlot[i] = pd
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	for _, pd := range perPlot {
		chartData.PlotsData = append(chartData.PlotsData, pd...)
	}
	return chartData, nil
}

func (e *DataEngine) fetchPlotData(ctx context.Context, xid, wid int64, plot *dpb.PlotSpec, widthPixels int32) ([]*dpb.ChartData_PlotData, error) {
	switch plot.GetType() {
	case dpb.PlotType_SUMMARY_BOX:
		pd, err := e.fetchSummaryBox(ctx, xid, wid, plot)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch summary box for plot %q: %w", plot.GetPlotId(), err)
		}
		return pd, nil
	case dpb.PlotType_SCATTER, dpb.PlotType_PLOT_TYPE_UNSPECIFIED:
		pd, err := e.fetchSeriesData(ctx, xid, wid, plot, widthPixels)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch series data for plot %q: %w", plot.GetPlotId(), err)
		}
		return pd, nil
	default:
		return nil, fmt.Errorf("unsupported plot type: %v", plot.GetType())
	}
}
