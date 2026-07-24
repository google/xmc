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
	"strings"

	dashboardpb "xmanager_cloud/dashboard_service/proto/api_go_proto"
)

// Translator builds ClickHouse SQL queries from XMC proto specifications.
type Translator struct {
	tableName string
}

// NewTranslator initializes a Translator with a given table name.
func NewTranslator(tableName string) *Translator {
	if tableName == "" {
		tableName = "xmc_metrics"
	}
	return &Translator{tableName: tableName}
}

// TranslatePlot generates a parameterized SQL query and arguments for a single plot.
func (t *Translator) TranslatePlot(req *dashboardpb.GetChartDataRequest, chart *dashboardpb.Chart, plot *dashboardpb.PlotSpec) (string, []any, error) {
	if plot == nil {
		return "", nil, fmt.Errorf("plot cannot be nil")
	}

	metrics := plot.GetData().GetMetrics()
	if len(metrics) == 0 {
		return "", nil, fmt.Errorf("no metrics specified in plot data")
	}

	switch plot.GetType() {
	case dashboardpb.PlotType_SUMMARY_BOX:
		return t.translateSummaryBox(req, chart, plot)
	case dashboardpb.PlotType_SCATTER:
		return t.translateScatter(req, chart, plot)
	case dashboardpb.PlotType_BAR:
		return t.translateBar(req, chart, plot)
	case dashboardpb.PlotType_HISTOGRAM:
		return t.translateHistogram(req, chart, plot)
	default:
		return "", nil, fmt.Errorf("unsupported plot type: %v", plot.GetType())
	}
}

func (t *Translator) translateSummaryBox(req *dashboardpb.GetChartDataRequest, chart *dashboardpb.Chart, plot *dashboardpb.PlotSpec) (string, []any, error) {
	where, args := t.buildWhereClause(req, chart, plot)
	query := fmt.Sprintf(
		"SELECT metric_name, argMax(metric_value, step) AS latest_value, min(metric_value) AS min_value, "+
			"max(metric_value) AS max_value, avg(metric_value) AS mean_value, max(step) AS latest_iteration, "+
			"argMax(timestamp, step) AS latest_timestamp FROM %s %s GROUP BY metric_name",
		t.tableName, where,
	)
	return query, args, nil
}

func (t *Translator) translateScatter(req *dashboardpb.GetChartDataRequest, chart *dashboardpb.Chart, plot *dashboardpb.PlotSpec) (string, []any, error) {
	where, whereArgs := t.buildWhereClause(req, chart, plot)

	if req != nil && req.GetDownsamplePoints() > 0 || (plot.GetStats() != nil && plot.GetStats().GetType() == dashboardpb.StatSpec_DOWNSAMPLE) {
		maxPoints := int32(500)
		if req != nil && req.GetDownsamplePoints() > 0 {
			maxPoints = req.GetDownsamplePoints()
		}
		query := fmt.Sprintf(
			"SELECT metric_name, work_unit_id AS wid, xid, execution_id, replica_id, "+
				"lttb(?)(step, metric_value) AS downsampled_points FROM %s %s GROUP BY metric_name, work_unit_id, xid, execution_id, replica_id",
			t.tableName, where,
		)
		args := append([]any{maxPoints}, whereArgs...)
		return query, args, nil
	}

	if req != nil && req.GetIntervalSeconds() > 0 {
		query := fmt.Sprintf(
			"SELECT metric_name, work_unit_id AS wid, xid, execution_id, replica_id, "+
				"toStartOfInterval(fromUnixTimestamp64Micro(timestamp), INTERVAL ? SECOND) AS interval_window, "+
				"avg(metric_value) AS avg_value, min(metric_value) AS min_value, max(metric_value) AS max_value FROM %s %s "+
				"GROUP BY metric_name, work_unit_id, xid, execution_id, replica_id, interval_window ORDER BY interval_window ASC",
			t.tableName, where,
		)
		args := append([]any{req.GetIntervalSeconds()}, whereArgs...)
		return query, args, nil
	}

	query := fmt.Sprintf(
		"SELECT metric_name, work_unit_id AS wid, xid, execution_id, replica_id, step AS iteration, "+
			"timestamp, avg(metric_value) AS avg_value, min(metric_value) AS min_value, max(metric_value) AS max_value FROM %s %s "+
			"GROUP BY metric_name, work_unit_id, xid, execution_id, replica_id, step, timestamp ORDER BY step ASC",
		t.tableName, where,
	)
	return query, whereArgs, nil
}

func (t *Translator) translateBar(req *dashboardpb.GetChartDataRequest, chart *dashboardpb.Chart, plot *dashboardpb.PlotSpec) (string, []any, error) {
	where, args := t.buildWhereClause(req, chart, plot)
	query := fmt.Sprintf(
		"SELECT metric_name, toString(metric_value) AS category_or_bin, count() AS value_or_count FROM %s %s GROUP BY metric_name, category_or_bin",
		t.tableName, where,
	)
	return query, args, nil
}

func (t *Translator) translateHistogram(req *dashboardpb.GetChartDataRequest, chart *dashboardpb.Chart, plot *dashboardpb.PlotSpec) (string, []any, error) {
	numBins := int32(10)
	if req != nil && req.GetNumBins() > 0 {
		numBins = req.GetNumBins()
	}
	where, whereArgs := t.buildWhereClause(req, chart, plot)
	query := fmt.Sprintf(
		"SELECT metric_name, histogram(?)(metric_value) AS histogram_buckets FROM %s %s GROUP BY metric_name",
		t.tableName, where,
	)
	args := append([]any{numBins}, whereArgs...)
	return query, args, nil
}

func (t *Translator) buildWhereClause(req *dashboardpb.GetChartDataRequest, chart *dashboardpb.Chart, plot *dashboardpb.PlotSpec) (string, []any) {
	xid := chart.GetXid()
	if req != nil && req.GetXid() != 0 {
		xid = req.GetXid()
	}
	wid := chart.GetWid()
	if req != nil && req.GetWid() != 0 {
		wid = req.GetWid()
	}

	metrics := plot.GetData().GetMetrics()
	conditions := []string{"xid = ?", "work_unit_id = ?", "metric_name IN (?)"}
	args := []any{xid, wid, metrics}

	t.appendRequestFilters(req, &conditions, &args)
	t.appendPlotRangeFilters(plot, &conditions, &args)

	return "WHERE " + strings.Join(conditions, " AND "), args
}

func (t *Translator) appendRequestFilters(req *dashboardpb.GetChartDataRequest, conditions *[]string, args *[]any) {
	if req == nil {
		return
	}
	if req.GetStartIteration() != 0 {
		*conditions = append(*conditions, "step >= ?")
		*args = append(*args, req.GetStartIteration())
	}
	if req.GetEndIteration() != 0 {
		*conditions = append(*conditions, "step <= ?")
		*args = append(*args, req.GetEndIteration())
	}
	if req.GetStartTime() != nil {
		*conditions = append(*conditions, "timestamp >= ?")
		*args = append(*args, req.GetStartTime().AsTime().UnixMicro())
	}
	if req.GetEndTime() != nil {
		*conditions = append(*conditions, "timestamp <= ?")
		*args = append(*args, req.GetEndTime().AsTime().UnixMicro())
	}
}

func (t *Translator) appendPlotRangeFilters(plot *dashboardpb.PlotSpec, conditions *[]string, args *[]any) {
	if plot == nil || plot.GetData() == nil {
		return
	}
	for _, r := range plot.GetData().GetRanges() {
		if startIt := r.GetStartIteration(); startIt != 0 {
			*conditions = append(*conditions, "step >= ?")
			*args = append(*args, startIt)
		}
		if endIt := r.GetEndIteration(); endIt != 0 {
			*conditions = append(*conditions, "step <= ?")
			*args = append(*args, endIt)
		}
		if startTime := r.GetStartTime(); startTime != nil {
			*conditions = append(*conditions, "timestamp >= ?")
			*args = append(*args, startTime.AsTime().UnixMicro())
		}
		if endTime := r.GetEndTime(); endTime != nil {
			*conditions = append(*conditions, "timestamp <= ?")
			*args = append(*args, endTime.AsTime().UnixMicro())
		}
	}
}
