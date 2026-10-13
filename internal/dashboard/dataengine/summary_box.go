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
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	dpb "xmanager_cloud/api/protos/dashboard"
)

// fetchSummaryBox queries and formats summary metric cards.
func (e *DataEngine) fetchSummaryBox(ctx context.Context, xid int64, wid int64, plot *dpb.PlotSpec) ([]*dpb.ChartData_PlotData, error) {
	if plot == nil || plot.GetData() == nil {
		return nil, fmt.Errorf("plot or data spec cannot be nil")
	}

	pred := buildWhereClause(xid, wid, plot.GetData(), plot.GetData().GetRanges())
	query := fmt.Sprintf(`
		SELECT
			metric_name,
			argMax(metric_value, timestamp) AS latest_val,
			min(metric_value) AS min_val,
			max(metric_value) AS max_val,
			avg(metric_value) AS mean_val,
			argMax(step, timestamp) AS latest_step,
			max(timestamp) AS latest_ts
		FROM %s
		WHERE %s
		GROUP BY metric_name
		ORDER BY metric_name ASC;
	`, e.tableName, pred.whereClause)

	rows, err := e.conn.Query(ctx, query, pred.args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query summary box metrics: %w", err)
	}
	defer rows.Close()

	var plotsData []*dpb.ChartData_PlotData
	for rows.Next() {
		var (
			metricName        string
			latestVal, minVal float64
			maxVal, meanVal   float64
			latestStep        int64
			latestTS          time.Time
		)
		if err := rows.Scan(&metricName, &latestVal, &minVal, &maxVal, &meanVal, &latestStep, &latestTS); err != nil {
			return nil, fmt.Errorf("failed to scan summary box row: %w", err)
		}

		plotsData = append(plotsData, &dpb.ChartData_PlotData{
			PlotId: plot.GetPlotId(),
			Type:   dpb.PlotType_SUMMARY_BOX,
			Content: &dpb.ChartData_PlotData_SummaryData{
				SummaryData: &dpb.ChartData_SummaryBoxValue{
					MetricName:      metricName,
					LatestValue:     latestVal,
					MinValue:        minVal,
					MaxValue:        maxVal,
					MeanValue:       meanVal,
					LatestStep:      latestStep,
					LatestTimestamp: timestamppb.New(latestTS),
				},
			},
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("summary box rows iteration error: %w", err)
	}

	return plotsData, nil
}
