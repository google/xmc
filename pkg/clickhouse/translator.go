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

// TranslatedQuery contains generated ClickHouse SQL string and parameterized arguments.
type TranslatedQuery struct {
	SQL  string
	Args []any
}

// TranslatePlotQuery converts a PlotSpec and GetChartDataRequest into ClickHouse SQL & args.
func TranslatePlotQuery(plot *dashboardpb.PlotSpec, xid, wid int64, req *dashboardpb.GetChartDataRequest, tableName string) (*TranslatedQuery, error) {
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
	case dashboardpb.PlotType_BAR, dashboardpb.PlotType_HISTOGRAM:
		return translateBarQuery(plot, xid, wid, req, tableName)
	default:
		return nil, fmt.Errorf("unsupported plot type: %v", plot.Type)
	}
}

func buildBaseWhere(xid, wid int64, metrics []string, req *dashboardpb.GetChartDataRequest) (string, []any) {
	whereClauses := []string{"xid = ?", "work_unit_id = ?", "metric_name IN (?)"}
	args := []any{xid, wid, metrics}

	if req == nil {
		return strings.Join(whereClauses, " AND "), args
	}

	if req.StartIteration > 0 {
		whereClauses = append(whereClauses, "step >= ?")
		args = append(args, req.StartIteration)
	}
	if req.EndIteration > 0 {
		whereClauses = append(whereClauses, "step <= ?")
		args = append(args, req.EndIteration)
	}
	if req.StartTime != nil && req.StartTime.Seconds > 0 {
		whereClauses = append(whereClauses, "timestamp >= ?")
		args = append(args, req.StartTime.AsTime().UnixMicro())
	}
	if req.EndTime != nil && req.EndTime.Seconds > 0 {
		whereClauses = append(whereClauses, "timestamp <= ?")
		args = append(args, req.EndTime.AsTime().UnixMicro())
	}

	return strings.Join(whereClauses, " AND "), args
}

func translateSummaryBoxQuery(plot *dashboardpb.PlotSpec, xid, wid int64, req *dashboardpb.GetChartDataRequest, tableName string) (*TranslatedQuery, error) {
	whereSQL, args := buildBaseWhere(xid, wid, plot.Data.Metrics, req)
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

func translateScatterQuery(plot *dashboardpb.PlotSpec, xid, wid int64, req *dashboardpb.GetChartDataRequest, tableName string) (*TranslatedQuery, error) {
	if req != nil && req.ChartWidthPixels > 0 {
		return translateScatterLTTBQuery(plot, xid, wid, req, tableName)
	}

	whereSQL, args := buildBaseWhere(xid, wid, plot.Data.Metrics, req)
	sql := fmt.Sprintf(`SELECT
  metric_name,
  work_unit_id AS wid,
  xid,
  step AS iteration,
  timestamp,
  avg(metric_value) AS avg_value,
  min(metric_value) AS min_value,
  max(metric_value) AS max_value
FROM %s
WHERE %s
GROUP BY metric_name, work_unit_id, xid, step, timestamp
ORDER BY step ASC`, tableName, whereSQL)

	return &TranslatedQuery{SQL: sql, Args: args}, nil
}

func translateScatterLTTBQuery(plot *dashboardpb.PlotSpec, xid, wid int64, req *dashboardpb.GetChartDataRequest, tableName string) (*TranslatedQuery, error) {
	whereSQL, baseArgs := buildBaseWhere(xid, wid, plot.Data.Metrics, req)
	args := append([]any{req.ChartWidthPixels}, baseArgs...)

	sql := fmt.Sprintf(`SELECT
  metric_name,
  work_unit_id AS wid,
  xid,
  lttb(?)(step, metric_value) AS points
FROM %s
WHERE %s
GROUP BY metric_name, work_unit_id, xid`, tableName, whereSQL)

	return &TranslatedQuery{SQL: sql, Args: args}, nil
}

func translateBarQuery(plot *dashboardpb.PlotSpec, xid, wid int64, req *dashboardpb.GetChartDataRequest, tableName string) (*TranslatedQuery, error) {
	whereSQL, args := buildBaseWhere(xid, wid, plot.Data.Metrics, req)
	sql := fmt.Sprintf(`SELECT
  metric_name,
  toString(metric_value) AS category_or_bin,
  count() AS value_or_count
FROM %s
WHERE %s
GROUP BY metric_name, category_or_bin`, tableName, whereSQL)

	return &TranslatedQuery{SQL: sql, Args: args}, nil
}
