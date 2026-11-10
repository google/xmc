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

	"google.golang.org/protobuf/types/known/timestamppb"

	dpb "xmanager_cloud/api/protos/dashboard"
	"xmanager_cloud/pkg/metricsquery"
)

// fetchSummaryBox queries and formats summary metric cards.
func (e *DataEngine) fetchSummaryBox(ctx context.Context, xid int64, wid int64, plot *dpb.PlotSpec) ([]*dpb.ChartData_PlotData, error) {
	if plot == nil || plot.GetData() == nil {
		return nil, fmt.Errorf("plot or data spec cannot be nil")
	}

	summaries, err := e.querier.QuerySummaryByMetric(ctx, metricFilter(xid, wid, plot.GetData()))
	if err != nil {
		return nil, err
	}

	var plotsData []*dpb.ChartData_PlotData
	for _, s := range summaries {
		plotsData = append(plotsData, summaryBoxPlot(plot.GetPlotId(), s))
	}
	return plotsData, nil
}

func summaryBoxPlot(plotID string, s metricsquery.MetricSummary) *dpb.ChartData_PlotData {
	return &dpb.ChartData_PlotData{
		PlotId: plotID,
		Type:   dpb.PlotType_SUMMARY_BOX,
		Content: &dpb.ChartData_PlotData_SummaryData{
			SummaryData: &dpb.ChartData_SummaryBoxValue{
				MetricName:      s.MetricName,
				LatestValue:     s.Latest,
				MinValue:        s.Min,
				MaxValue:        s.Max,
				MeanValue:       s.Mean,
				LatestStep:      s.LatestStep,
				LatestTimestamp: timestamppb.New(s.LatestTimestamp),
			},
		},
	}
}
