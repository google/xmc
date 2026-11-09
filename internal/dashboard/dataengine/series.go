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
	"strconv"

	"google.golang.org/protobuf/types/known/timestamppb"

	dpb "xmanager_cloud/api/protos/dashboard"
	"xmanager_cloud/pkg/metricsquery"
)

const defaultTargetPoints = 1000

// calculateBucketSize determines the step bucket width for dynamic downsampling.
func calculateBucketSize(ranges []*dpb.DataSpec_DataRange, targetPoints int32) int64 {
	if targetPoints <= 0 {
		targetPoints = defaultTargetPoints
	}
	for _, r := range ranges {
		if r == nil {
			continue
		}
		startStep := int64(0)
		isValidStart := false
		if r.GetStart() == nil {
			isValidStart = true
		} else if s, ok := r.GetStart().(*dpb.DataSpec_DataRange_StartStep); ok {
			startStep = s.StartStep
			isValidStart = true
		}
		if e, ok := r.GetEnd().(*dpb.DataSpec_DataRange_EndStep); ok {
			if isValidStart && e.EndStep > startStep {
				span := e.EndStep - startStep
				bucket := span / int64(targetPoints)
				if bucket > 1 {
					return bucket
				}
			}
		}
	}
	return 1
}

// fetchSeriesData queries and aggregates time-series points for scatter plots.
func (e *DataEngine) fetchSeriesData(ctx context.Context, xid int64, wid int64, plot *dpb.PlotSpec, widthPixels int32) ([]*dpb.ChartData_PlotData, error) {
	if plot == nil || plot.GetData() == nil {
		return nil, fmt.Errorf("plot or data spec cannot be nil")
	}

	bucketSize := calculateBucketSize(plot.GetData().GetRanges(), widthPixels)
	points, err := e.querier.QuerySeries(ctx, metricFilter(xid, wid, plot.GetData()), bucketSize)
	if err != nil {
		return nil, err
	}
	return scatterPlots(plot.GetPlotId(), points), nil
}

// scatterPlots groups ordered points into one scatter series per (metric, work unit), keeping the
// order in which each series first appears.
func scatterPlots(plotID string, points []metricsquery.MetricPoint) []*dpb.ChartData_PlotData {
	type seriesKey struct {
		metricName string
		workUnitID int64
	}
	seriesMap := make(map[seriesKey]*dpb.ChartData_SeriesData)
	var plotsData []*dpb.ChartData_PlotData
	for _, p := range points {
		key := seriesKey{metricName: p.MetricName, workUnitID: p.WorkUnitID}
		sd, exists := seriesMap[key]
		if !exists {
			sd = &dpb.ChartData_SeriesData{
				MetricName: p.MetricName,
				WorkUnitId: strconv.FormatInt(p.WorkUnitID, 10),
				Xid:        p.XID,
				Points:     make([]*dpb.ChartData_SeriesPoint, 0),
			}
			seriesMap[key] = sd
			plotsData = append(plotsData, &dpb.ChartData_PlotData{
				PlotId:  plotID,
				Type:    dpb.PlotType_SCATTER,
				Content: &dpb.ChartData_PlotData_ScatterSeries{ScatterSeries: sd},
			})
		}
		sd.Points = append(sd.Points, &dpb.ChartData_SeriesPoint{
			Step:      p.Step,
			Timestamp: timestamppb.New(p.Timestamp),
			AvgValue:  p.Avg,
			MinValue:  p.Min,
			MaxValue:  p.Max,
		})
	}
	return plotsData
}
