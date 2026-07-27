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
	"fmt"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"google.golang.org/protobuf/types/known/timestamppb"

	dashboardpb "xmanager_cloud/dashboard_service/proto/api_go_proto"
	servicepb "xmanager_cloud/dashboard_service/proto/service_go_proto"
)

// MicroOrTime implements sql.Scanner for timestamps returned as time.Time or integer microseconds.
type MicroOrTime struct {
	Time time.Time
}

// Scan scans a value into MicroOrTime.
func (m *MicroOrTime) Scan(value any) error {
	switch v := value.(type) {
	case time.Time:
		m.Time = v
		return nil
	case int64:
		m.Time = time.UnixMicro(v)
		return nil
	case uint64:
		m.Time = time.UnixMicro(int64(v))
		return nil
	default:
		return fmt.Errorf("cannot scan %T into MicroOrTime", value)
	}
}

// ToProtoTimestamp converts MicroOrTime into a proto Timestamp.
func (m MicroOrTime) ToProtoTimestamp() *timestamppb.Timestamp {
	if m.Time.IsZero() {
		return nil
	}
	return timestamppb.New(m.Time)
}

// MapPlotRows maps ClickHouse driver.Rows into a PlotData proto message.
func MapPlotRows(plot *dashboardpb.PlotSpec, rows driver.Rows, req *servicepb.GetChartDataRequest) (*dashboardpb.ChartData_PlotData, error) {
	if plot == nil {
		return nil, fmt.Errorf("plot cannot be nil")
	}
	if rows == nil {
		return nil, fmt.Errorf("rows cannot be nil")
	}

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
		series, err := mapScatterContent(rows, plot, req)
		if err != nil {
			return nil, err
		}
		plotData.Content = &dashboardpb.ChartData_PlotData_ScatterSeries{ScatterSeries: series}

	case dashboardpb.PlotType_BAR:
		bar, err := MapBarRows(rows)
		if err != nil {
			return nil, err
		}
		plotData.Content = &dashboardpb.ChartData_PlotData_BarData{BarData: bar}

	case dashboardpb.PlotType_HISTOGRAM:
		hist, err := MapHistogramRows(rows)
		if err != nil {
			return nil, err
		}
		plotData.Content = &dashboardpb.ChartData_PlotData_BarData{BarData: hist}

	default:
		return nil, fmt.Errorf("unsupported plot type: %v", plot.Type)
	}

	return plotData, nil
}

func mapScatterContent(rows driver.Rows, plot *dashboardpb.PlotSpec, req *servicepb.GetChartDataRequest) (*dashboardpb.ChartData_SeriesData, error) {
	if isDownsampledScatter(plot, req) {
		return MapScatterLTTBRows(rows)
	}
	return MapScatterRows(rows)
}

// MapSummaryRows scans a summary box query result row into ChartData_SummaryBoxValue.
func MapSummaryRows(rows driver.Rows) (*dashboardpb.ChartData_SummaryBoxValue, error) {
	if !rows.Next() {
		return nil, nil
	}

	var metricName string
	var latestVal, minVal, maxVal, meanVal float64
	var latestIteration int64
	var latestTimestamp MicroOrTime

	if err := rows.Scan(&metricName, &latestVal, &minVal, &maxVal, &meanVal, &latestIteration, &latestTimestamp); err != nil {
		return nil, fmt.Errorf("failed to scan summary row: %w", err)
	}

	return &dashboardpb.ChartData_SummaryBoxValue{
		MetricName:      metricName,
		LatestValue:     latestVal,
		MinValue:        minVal,
		MaxValue:        maxVal,
		MeanValue:       meanVal,
		LatestIteration: latestIteration,
		LatestTimestamp: latestTimestamp.ToProtoTimestamp(),
	}, nil
}

// MapScatterRows scans scatter query result rows into ChartData_SeriesData.
func MapScatterRows(rows driver.Rows) (*dashboardpb.ChartData_SeriesData, error) {
	var seriesData *dashboardpb.ChartData_SeriesData
	cols := rows.Columns()

	for rows.Next() {
		var metricName string
		var wid, xid, execID, iteration int64
		var replicaID string
		var ts MicroOrTime
		var avgVal, minVal, maxVal float64

		var err error
		if len(cols) == 10 {
			err = rows.Scan(&metricName, &wid, &xid, &execID, &replicaID, &iteration, &ts, &avgVal, &minVal, &maxVal)
		} else {
			err = rows.Scan(&metricName, &wid, &xid, &iteration, &ts, &avgVal, &minVal, &maxVal)
		}
		if err != nil {
			return nil, fmt.Errorf("failed to scan scatter row: %w", err)
		}

		if seriesData == nil {
			seriesData = &dashboardpb.ChartData_SeriesData{
				MetricName: metricName,
				WorkUnitId: fmt.Sprintf("%d", wid),
				Xid:        xid,
			}
		}

		seriesData.Points = append(seriesData.Points, &dashboardpb.ChartData_SeriesPoint{
			Iteration: iteration,
			Timestamp: ts.ToProtoTimestamp(),
			AvgValue:  avgVal,
			MinValue:  minVal,
			MaxValue:  maxVal,
		})
	}

	return seriesData, nil
}

// MapScatterLTTBRows scans LTTB tuple points array rows into ChartData_SeriesData.
func MapScatterLTTBRows(rows driver.Rows) (*dashboardpb.ChartData_SeriesData, error) {
	if !rows.Next() {
		return nil, nil
	}

	var metricName string
	var wid, xid, execID int64
	var replicaID string
	var rawPoints [][2]float64
	cols := rows.Columns()

	var err error
	if len(cols) == 6 {
		err = rows.Scan(&metricName, &wid, &xid, &execID, &replicaID, &rawPoints)
	} else {
		err = rows.Scan(&metricName, &wid, &xid, &rawPoints)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to scan lttb scatter row: %w", err)
	}

	seriesData := &dashboardpb.ChartData_SeriesData{
		MetricName: metricName,
		WorkUnitId: fmt.Sprintf("%d", wid),
		Xid:        xid,
	}

	for _, pt := range rawPoints {
		seriesData.Points = append(seriesData.Points, &dashboardpb.ChartData_SeriesPoint{
			Iteration: int64(pt[0]),
			AvgValue:  pt[1],
			MinValue:  pt[1],
			MaxValue:  pt[1],
		})
	}

	return seriesData, nil
}

// MapBarRows scans categorical bar chart result rows into ChartData_BarData.
func MapBarRows(rows driver.Rows) (*dashboardpb.ChartData_BarData, error) {
	var barData *dashboardpb.ChartData_BarData

	for rows.Next() {
		var metricName, category string
		var count uint64

		if err := rows.Scan(&metricName, &category, &count); err != nil {
			return nil, fmt.Errorf("failed to scan bar row: %w", err)
		}

		if barData == nil {
			barData = &dashboardpb.ChartData_BarData{
				MetricName: metricName,
			}
		}

		barData.Buckets = append(barData.Buckets, &dashboardpb.ChartData_BarBucket{
			CategoryOrBin: category,
			ValueOrCount:  float64(count),
		})
	}

	return barData, nil
}

// MapHistogramRows scans continuous numeric distribution histogram result rows into ChartData_BarData.
func MapHistogramRows(rows driver.Rows) (*dashboardpb.ChartData_BarData, error) {
	return MapBarRows(rows)
}
