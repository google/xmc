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
)

// MapPlotRows parses ClickHouse driver.Rows for a plot and constructs a PlotData message.
func MapPlotRows(plot *dashboardpb.PlotSpec, rows driver.Rows) (*dashboardpb.ChartData_PlotData, error) {
	if plot == nil {
		return nil, fmt.Errorf("plot cannot be nil")
	}
	if rows == nil {
		return nil, fmt.Errorf("rows cannot be nil")
	}

	plotData := &dashboardpb.ChartData_PlotData{
		PlotId: plot.GetPlotId(),
		Type:   plot.GetType(),
	}

	switch plot.GetType() {
	case dashboardpb.PlotType_SUMMARY_BOX:
		summary, err := mapSummaryRows(rows)
		if err != nil {
			return nil, err
		}
		plotData.Content = &dashboardpb.ChartData_PlotData_SummaryData{SummaryData: summary}
	case dashboardpb.PlotType_SCATTER:
		series, err := mapScatterRows(rows)
		if err != nil {
			return nil, err
		}
		plotData.Content = &dashboardpb.ChartData_PlotData_ScatterSeries{ScatterSeries: series}
	case dashboardpb.PlotType_BAR:
		bar, err := mapBarRows(rows)
		if err != nil {
			return nil, err
		}
		plotData.Content = &dashboardpb.ChartData_PlotData_BarData{BarData: bar}
	case dashboardpb.PlotType_HISTOGRAM:
		hist, err := mapHistogramRows(rows)
		if err != nil {
			return nil, err
		}
		plotData.Content = &dashboardpb.ChartData_PlotData_BarData{BarData: hist}
	default:
		return nil, fmt.Errorf("unsupported plot type: %v", plot.GetType())
	}

	return plotData, nil
}

func mapSummaryRows(rows driver.Rows) (*dashboardpb.ChartData_SummaryBoxValue, error) {
	if !rows.Next() {
		return &dashboardpb.ChartData_SummaryBoxValue{}, nil
	}

	var (
		metricName      string
		latestVal       float64
		minVal          float64
		maxVal          float64
		meanVal         float64
		latestIter      int64
		latestTimestamp int64
	)

	if err := rows.Scan(&metricName, &latestVal, &minVal, &maxVal, &meanVal, &latestIter, &latestTimestamp); err != nil {
		return nil, fmt.Errorf("failed to scan summary box row: %w", err)
	}

	return &dashboardpb.ChartData_SummaryBoxValue{
		MetricName:      metricName,
		LatestValue:     latestVal,
		MinValue:        minVal,
		MaxValue:        maxVal,
		MeanValue:       meanVal,
		LatestIteration: latestIter,
		LatestTimestamp: timestamppb.New(time.UnixMicro(latestTimestamp)),
	}, nil
}

func mapScatterRows(rows driver.Rows) (*dashboardpb.ChartData_SeriesData, error) {
	cols := rows.Columns()
	series := &dashboardpb.ChartData_SeriesData{}

	for rows.Next() {
		if len(cols) == 6 {
			if err := scanDownsampledRow(rows, series); err != nil {
				return nil, err
			}
		} else if len(cols) == 9 {
			if err := scanWindowedRow(rows, series); err != nil {
				return nil, err
			}
		} else {
			if err := scanStandardRow(rows, series); err != nil {
				return nil, err
			}
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error: %w", err)
	}

	return series, nil
}

func scanStandardRow(rows driver.Rows, series *dashboardpb.ChartData_SeriesData) error {
	var (
		metricName string
		wid        int64
		xid        int64
		execID     int64
		replicaID  string
		iteration  int64
		tsMicro    int64
		avgVal     float64
		minVal     float64
		maxVal     float64
	)

	if err := rows.Scan(&metricName, &wid, &xid, &execID, &replicaID, &iteration, &tsMicro, &avgVal, &minVal, &maxVal); err != nil {
		return fmt.Errorf("failed to scan scatter standard row: %w", err)
	}

	series.MetricName = metricName
	series.WorkUnitId = fmt.Sprintf("%d", wid)
	series.Xid = xid

	point := &dashboardpb.ChartData_SeriesPoint{
		Iteration: iteration,
		Timestamp: timestamppb.New(time.UnixMicro(tsMicro)),
		AvgValue:  avgVal,
		MinValue:  minVal,
		MaxValue:  maxVal,
	}
	series.Points = append(series.Points, point)
	return nil
}

func scanWindowedRow(rows driver.Rows, series *dashboardpb.ChartData_SeriesData) error {
	var (
		metricName string
		wid        int64
		xid        int64
		execID     int64
		replicaID  string
		winTime    time.Time
		avgVal     float64
		minVal     float64
		maxVal     float64
	)

	if err := rows.Scan(&metricName, &wid, &xid, &execID, &replicaID, &winTime, &avgVal, &minVal, &maxVal); err != nil {
		return fmt.Errorf("failed to scan scatter windowed row: %w", err)
	}

	series.MetricName = metricName
	series.WorkUnitId = fmt.Sprintf("%d", wid)
	series.Xid = xid

	point := &dashboardpb.ChartData_SeriesPoint{
		Timestamp: timestamppb.New(winTime),
		AvgValue:  avgVal,
		MinValue:  minVal,
		MaxValue:  maxVal,
	}
	series.Points = append(series.Points, point)
	return nil
}

func scanDownsampledRow(rows driver.Rows, series *dashboardpb.ChartData_SeriesData) error {
	var (
		metricName string
		wid        int64
		xid        int64
		execID     int64
		replicaID  string
		pts        [][2]float64
	)

	if err := rows.Scan(&metricName, &wid, &xid, &execID, &replicaID, &pts); err != nil {
		return fmt.Errorf("failed to scan scatter downsampled row: %w", err)
	}

	series.MetricName = metricName
	series.WorkUnitId = fmt.Sprintf("%d", wid)
	series.Xid = xid

	for _, pt := range pts {
		point := &dashboardpb.ChartData_SeriesPoint{
			Iteration: int64(pt[0]),
			AvgValue:  pt[1],
			MinValue:  pt[1],
			MaxValue:  pt[1],
		}
		series.Points = append(series.Points, point)
	}
	return nil
}

func mapBarRows(rows driver.Rows) (*dashboardpb.ChartData_BarData, error) {
	bar := &dashboardpb.ChartData_BarData{}
	for rows.Next() {
		var (
			metricName string
			cat        string
			countVal   float64
		)
		if err := rows.Scan(&metricName, &cat, &countVal); err != nil {
			return nil, fmt.Errorf("failed to scan bar row: %w", err)
		}
		bar.MetricName = metricName
		bar.Buckets = append(bar.Buckets, &dashboardpb.ChartData_BarBucket{
			CategoryOrBin: cat,
			ValueOrCount:  countVal,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("bar rows error: %w", err)
	}
	return bar, nil
}

func mapHistogramRows(rows driver.Rows) (*dashboardpb.ChartData_BarData, error) {
	bar := &dashboardpb.ChartData_BarData{}
	for rows.Next() {
		var (
			metricName string
			buckets    [][3]float64
		)
		if err := rows.Scan(&metricName, &buckets); err != nil {
			return nil, fmt.Errorf("failed to scan histogram row: %w", err)
		}
		bar.MetricName = metricName
		for _, b := range buckets {
			label := fmt.Sprintf("[%.2f, %.2f)", b[0], b[1])
			bar.Buckets = append(bar.Buckets, &dashboardpb.ChartData_BarBucket{
				CategoryOrBin: label,
				ValueOrCount:  b[2],
			})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("histogram rows error: %w", err)
	}
	return bar, nil
}
