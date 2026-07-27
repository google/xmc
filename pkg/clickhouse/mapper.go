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

	for rows.Next() {
		var metricName string
		var wid, xid, iteration int64
		var ts MicroOrTime
		var avgVal, minVal, maxVal float64

		if err := rows.Scan(&metricName, &wid, &xid, &iteration, &ts, &avgVal, &minVal, &maxVal); err != nil {
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
	var wid, xid int64
	var rawPoints [][2]float64

	if err := rows.Scan(&metricName, &wid, &xid, &rawPoints); err != nil {
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
		})
	}

	return seriesData, nil
}

// MapBarRows scans bar/histogram query result rows into ChartData_BarData.
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

// MicroOrTime is a flexible scanner for ClickHouse timestamps (microseconds int64 or time.Time).
type MicroOrTime struct {
	Time time.Time
}

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

func (m MicroOrTime) ToProtoTimestamp() *timestamppb.Timestamp {
	if m.Time.IsZero() {
		return nil
	}
	return timestamppb.New(m.Time)
}
