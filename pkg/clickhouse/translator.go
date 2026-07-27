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
	servicepb "xmanager_cloud/dashboard_service/proto/service_go_proto"
)

// TranslatedQuery contains generated ClickHouse SQL string and parameterized arguments.
type TranslatedQuery struct {
	SQL  string
	Args []any
}

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

// TranslatePlot converts a PlotSpec and GetChartDataRequest into ClickHouse SQL and arguments.
func (t *Translator) TranslatePlot(plot *dashboardpb.PlotSpec, xid, wid int64, req *servicepb.GetChartDataRequest) (*TranslatedQuery, error) {
	return TranslatePlotQuery(plot, xid, wid, req, t.tableName)
}

// TranslatePlotQuery converts a PlotSpec and GetChartDataRequest into ClickHouse SQL and arguments.
func TranslatePlotQuery(plot *dashboardpb.PlotSpec, xid, wid int64, req *servicepb.GetChartDataRequest, tableName string) (*TranslatedQuery, error) {
	if plot == nil || plot.Data == nil || len(plot.Data.Metrics) == 0 {
		return nil, fmt.Errorf("plot spec or metrics cannot be empty")
	}

	if tableName == "" {
		tableName = "xmc_metrics"
	}

	switch plot.Type {
	case dashboardpb.PlotType_SUMMARY_BOX:
		return translateSummaryBoxQuery(plot, xid, wid, req, tableName)
	case dashboardpb.PlotType_SCATTER:
		return translateScatterQuery(plot, xid, wid, req, tableName)
	case dashboardpb.PlotType_BAR:
		return translateBarQuery(plot, xid, wid, req, tableName)
	case dashboardpb.PlotType_HISTOGRAM:
		return translateHistogramQuery(plot, xid, wid, req, tableName)
	default:
		return nil, fmt.Errorf("unsupported plot type: %v", plot.Type)
	}
}

func buildWhereClause(xid, wid int64, plot *dashboardpb.PlotSpec, req *servicepb.GetChartDataRequest) (string, []any) {
	clauses := []string{"xid = ?", "work_unit_id = ?", "metric_name IN (?)"}
	args := []any{xid, wid, plot.Data.Metrics}

	appendRequestFilters(req, &clauses, &args)
	appendPlotRangeFilters(plot, &clauses, &args)

	return strings.Join(clauses, " AND "), args
}

func appendRequestFilters(req *servicepb.GetChartDataRequest, clauses *[]string, args *[]any) {
	if req == nil {
		return
	}
	if req.GetStartIteration() != 0 {
		*clauses = append(*clauses, "step >= ?")
		*args = append(*args, req.GetStartIteration())
	}
	if req.GetEndIteration() != 0 {
		*clauses = append(*clauses, "step <= ?")
		*args = append(*args, req.GetEndIteration())
	}
	if req.GetStartTime() != nil {
		*clauses = append(*clauses, "timestamp >= ?")
		*args = append(*args, req.GetStartTime().AsTime().UnixMicro())
	}
	if req.GetEndTime() != nil {
		*clauses = append(*clauses, "timestamp <= ?")
		*args = append(*args, req.GetEndTime().AsTime().UnixMicro())
	}
}

func appendPlotRangeFilters(plot *dashboardpb.PlotSpec, clauses *[]string, args *[]any) {
	if plot == nil || plot.Data == nil || len(plot.Data.Ranges) == 0 {
		return
	}
	var rangeClauses []string
	for _, r := range plot.Data.Ranges {
		var rClauses []string
		if startIt := r.GetStartIteration(); startIt != 0 {
			rClauses = append(rClauses, "step >= ?")
			*args = append(*args, startIt)
		}
		if endIt := r.GetEndIteration(); endIt != 0 {
			rClauses = append(rClauses, "step <= ?")
			*args = append(*args, endIt)
		}
		if startTime := r.GetStartTime(); startTime != nil {
			rClauses = append(rClauses, "timestamp >= ?")
			*args = append(*args, startTime.AsTime().UnixMicro())
		}
		if endTime := r.GetEndTime(); endTime != nil {
			rClauses = append(rClauses, "timestamp <= ?")
			*args = append(*args, endTime.AsTime().UnixMicro())
		}
		if len(rClauses) > 0 {
			rangeClauses = append(rangeClauses, "("+strings.Join(rClauses, " AND ")+")")
		}
	}
	if len(rangeClauses) > 0 {
		*clauses = append(*clauses, "("+strings.Join(rangeClauses, " OR ")+")")
	}
}

func translateSummaryBoxQuery(plot *dashboardpb.PlotSpec, xid, wid int64, req *servicepb.GetChartDataRequest, tableName string) (*TranslatedQuery, error) {
	whereSQL, args := buildWhereClause(xid, wid, plot, req)
	sql := fmt.Sprintf(`SELECT
  metric_name,
  argMax(metric_value, step) AS latest_value,
  min(metric_value) AS min_value,
  max(metric_value) AS max_value,
  avg(metric_value) AS mean_value,
  max(step) AS latest_iteration,
  argMax(timestamp, step) AS latest_timestamp
FROM %s
WHERE %s
GROUP BY metric_name`, tableName, whereSQL)

	return &TranslatedQuery{SQL: sql, Args: args}, nil
}

func translateScatterQuery(plot *dashboardpb.PlotSpec, xid, wid int64, req *servicepb.GetChartDataRequest, tableName string) (*TranslatedQuery, error) {
	if isDownsampledScatter(plot, req) {
		return translateScatterLTTBQuery(plot, xid, wid, req, tableName)
	}

	whereSQL, args := buildWhereClause(xid, wid, plot, req)
	sql := fmt.Sprintf(`SELECT
  metric_name,
  work_unit_id AS wid,
  xid,
  execution_id,
  replica_id,
  step AS iteration,
  timestamp,
  avg(metric_value) AS avg_value,
  min(metric_value) AS min_value,
  max(metric_value) AS max_value
FROM %s
WHERE %s
GROUP BY metric_name, work_unit_id, xid, execution_id, replica_id, step, timestamp
ORDER BY step ASC`, tableName, whereSQL)

	return &TranslatedQuery{SQL: sql, Args: args}, nil
}

func isDownsampledScatter(plot *dashboardpb.PlotSpec, req *servicepb.GetChartDataRequest) bool {
	if req != nil && req.GetChartWidthPixels() > 0 {
		return true
	}
	if plot != nil && plot.Stats != nil && plot.Stats.Type == dashboardpb.StatSpec_DOWNSAMPLE {
		return true
	}
	return false
}

func translateScatterLTTBQuery(plot *dashboardpb.PlotSpec, xid, wid int64, req *servicepb.GetChartDataRequest, tableName string) (*TranslatedQuery, error) {
	whereSQL, args := buildWhereClause(xid, wid, plot, req)
	maxPoints := int32(500)
	if req != nil && req.GetChartWidthPixels() > 0 {
		maxPoints = req.GetChartWidthPixels()
	}

	sql := fmt.Sprintf(`SELECT
  metric_name,
  work_unit_id AS wid,
  xid,
  execution_id,
  replica_id,
  lttb(%d)(step, metric_value) AS points
FROM %s
WHERE %s
GROUP BY metric_name, work_unit_id, xid, execution_id, replica_id`, maxPoints, tableName, whereSQL)

	return &TranslatedQuery{SQL: sql, Args: args}, nil
}

func translateBarQuery(plot *dashboardpb.PlotSpec, xid, wid int64, req *servicepb.GetChartDataRequest, tableName string) (*TranslatedQuery, error) {
	whereSQL, args := buildWhereClause(xid, wid, plot, req)
	sql := fmt.Sprintf(`SELECT
  metric_name,
  toString(metric_value) AS category_or_bin,
  count() AS value_or_count
FROM %s
WHERE %s
GROUP BY metric_name, category_or_bin`, tableName, whereSQL)

	return &TranslatedQuery{SQL: sql, Args: args}, nil
}

func translateHistogramQuery(plot *dashboardpb.PlotSpec, xid, wid int64, req *servicepb.GetChartDataRequest, tableName string) (*TranslatedQuery, error) {
	whereSQL, args := buildWhereClause(xid, wid, plot, req)
	numBins := int32(10)

	sql := fmt.Sprintf(`SELECT
  metric_name,
  histogram(%d)(metric_value) AS histogram_buckets
FROM %s
WHERE %s
GROUP BY metric_name`, numBins, tableName, whereSQL)

	return &TranslatedQuery{SQL: sql, Args: args}, nil
}
