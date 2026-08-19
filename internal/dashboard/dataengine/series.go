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
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	dpb "xmanager_cloud/dashboard_service/proto/api_go_proto"
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

	pred := buildWhereClause(xid, wid, plot.GetData(), plot.GetData().GetRanges())
	bucketSize := calculateBucketSize(plot.GetData().GetRanges(), widthPixels)

	var query string
	if bucketSize > 1 {
		// Server-side downsampling using ClickHouse intDiv bucketing
		query = fmt.Sprintf(`
			SELECT
				intDiv(step, %d) * %d AS bucket_step,
				avg(metric_value) AS avg_val,
				min(metric_value) AS min_val,
				max(metric_value) AS max_val,
				max(timestamp) AS max_ts,
				metric_name,
				work_unit_id,
				xid
			FROM %s
			WHERE %s
			GROUP BY bucket_step, metric_name, work_unit_id, xid
			ORDER BY metric_name ASC, work_unit_id ASC, bucket_step ASC;
		`, bucketSize, bucketSize, e.tableName, pred.whereClause)
	} else {
		// Raw identity series points
		query = fmt.Sprintf(`
			SELECT
				step AS bucket_step,
				metric_value AS avg_val,
				metric_value AS min_val,
				metric_value AS max_val,
				timestamp AS max_ts,
				metric_name,
				work_unit_id,
				xid
			FROM %s
			WHERE %s
			ORDER BY metric_name ASC, work_unit_id ASC, step ASC, timestamp ASC;
		`, e.tableName, pred.whereClause)
	}

	rows, err := e.conn.Query(ctx, query, pred.args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query series data: %w", err)
	}
	defer rows.Close()

	type seriesKey struct {
		metricName string
		workUnitID int64
	}
	seriesMap := make(map[seriesKey]*dpb.ChartData_SeriesData)
	var orderedKeys []seriesKey

	for rows.Next() {
		var (
			step               int64
			avgVal, minVal     float64
			maxVal             float64
			ts                 time.Time
			metricName         string
			workUnitID, rowXID int64
		)
		if err := rows.Scan(&step, &avgVal, &minVal, &maxVal, &ts, &metricName, &workUnitID, &rowXID); err != nil {
			return nil, fmt.Errorf("failed to scan series row: %w", err)
		}

		key := seriesKey{metricName: metricName, workUnitID: workUnitID}
		sd, exists := seriesMap[key]
		if !exists {
			sd = &dpb.ChartData_SeriesData{
				MetricName: metricName,
				WorkUnitId: strconv.FormatInt(workUnitID, 10),
				Xid:        rowXID,
				Points:     make([]*dpb.ChartData_SeriesPoint, 0),
			}
			seriesMap[key] = sd
			orderedKeys = append(orderedKeys, key)
		}

		sd.Points = append(sd.Points, &dpb.ChartData_SeriesPoint{
			Step:      step,
			Timestamp: timestamppb.New(ts),
			AvgValue:  avgVal,
			MinValue:  minVal,
			MaxValue:  maxVal,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("series rows iteration error: %w", err)
	}

	var plotsData []*dpb.ChartData_PlotData
	for _, k := range orderedKeys {
		plotsData = append(plotsData, &dpb.ChartData_PlotData{
			PlotId: plot.GetPlotId(),
			Type:   dpb.PlotType_SCATTER,
			Content: &dpb.ChartData_PlotData_ScatterSeries{
				ScatterSeries: seriesMap[k],
			},
		})
	}

	return plotsData, nil
}
