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
	if bucketSize > 1 {
		points = collapseSeriesBuckets(points)
	}
	return scatterPlots(plot.GetPlotId(), points), nil
}

// collapseSeriesBuckets merges adjacent bucketed points sharing (metric, work unit, step) across
// executions, jobs, and replicas into one work-unit-level bucket point.
func collapseSeriesBuckets(points []metricsquery.MetricPoint) []metricsquery.MetricPoint {
	if len(points) == 0 {
		return nil
	}
	collapsed := make([]metricsquery.MetricPoint, 0, len(points))
	start := 0
	for i := 1; i <= len(points); i++ {
		if i < len(points) && sameSeriesStep(points[start], points[i]) {
			continue
		}
		collapsed = append(collapsed, mergeBucketPoints(points[start:i]))
		start = i
	}
	return collapsed
}

func sameSeriesStep(a, b metricsquery.MetricPoint) bool {
	return a.XID == b.XID && a.MetricName == b.MetricName && a.WorkUnitID == b.WorkUnitID && a.Step == b.Step
}

func mergeBucketPoints(bucket []metricsquery.MetricPoint) metricsquery.MetricPoint {
	merged := metricsquery.MetricPoint{
		XID:        bucket[0].XID,
		WorkUnitID: bucket[0].WorkUnitID,
		MetricName: bucket[0].MetricName,
		Step:       bucket[0].Step,
		Timestamp:  bucket[0].Timestamp,
		Avg:        bucket[0].Avg,
		Min:        bucket[0].Min,
		Max:        bucket[0].Max,
		Count:      max(bucket[0].Count, 1),
	}
	if len(bucket) == 1 {
		return merged
	}
	weightedSum := merged.Avg * float64(merged.Count)
	for _, p := range bucket[1:] {
		weight := max(p.Count, 1)
		weightedSum += p.Avg * float64(weight)
		merged.Count += weight
		merged.Min = min(merged.Min, p.Min)
		merged.Max = max(merged.Max, p.Max)
		if p.Timestamp.After(merged.Timestamp) {
			merged.Timestamp = p.Timestamp
		}
	}
	merged.Avg = weightedSum / float64(merged.Count)
	return merged
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
