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

package metricsquery

import (
	"context"
	"fmt"
	"time"
)

// clickHouseSummarySQL summarizes each metric; "latest" is the value at the newest timestamp, with
// ties broken by the highest step (matching bigQuerySummarySQL). Ordering both argMax calls by the
// same (timestamp, step) tuple keeps latest_val and latest_step from the same row.
// Placeholders: table, WHERE clause.
const clickHouseSummarySQL = `
	SELECT
		metric_name,
		argMax(metric_value, (timestamp, step)) AS latest_val,
		min(metric_value) AS min_val,
		max(metric_value) AS max_val,
		avg(metric_value) AS mean_val,
		argMax(step, (timestamp, step)) AS latest_step,
		max(timestamp) AS latest_ts
	FROM %s
	WHERE %s
	GROUP BY metric_name
	ORDER BY metric_name ASC;
`

// bigQuerySummarySQL is the BigQuery equivalent of clickHouseSummarySQL; ties on the newest
// timestamp resolve to the highest step. Placeholders: table, WHERE clause.
const bigQuerySummarySQL = `
	SELECT metric_name, latest.val AS latest_val, min_val, max_val, mean_val, latest.stp AS latest_step, latest_ts
	FROM (
		SELECT metric_name,
			ARRAY_AGG(STRUCT(metric_value AS val, step AS stp) ORDER BY timestamp DESC, step DESC LIMIT 1)[OFFSET(0)] AS latest,
			MIN(metric_value) AS min_val, MAX(metric_value) AS max_val, AVG(metric_value) AS mean_val, MAX(timestamp) AS latest_ts
		FROM %s WHERE %s GROUP BY metric_name
	)
	ORDER BY metric_name ASC;
`

// MetricSummary aggregates every point of one metric that matches a MetricFilter. Latest and
// LatestStep come from the point with the newest timestamp.
type MetricSummary struct {
	MetricName      string
	Latest          float64
	Min             float64
	Max             float64
	Mean            float64
	LatestStep      int64
	LatestTimestamp time.Time
}

// QuerySummaryByMetric returns one summary per metric matching f, ordered by metric name.
func (q *Querier) QuerySummaryByMetric(ctx context.Context, f MetricFilter) ([]MetricSummary, error) {
	pred := buildWhereClause(f)
	query := fmt.Sprintf(summarySQL(q.backend.dialect()), q.table, pred.whereClause)
	return queryAll(ctx, q.backend, "summary", query, pred.args, scanMetricSummary)
}

// summarySQL returns the summary query template for d.
func summarySQL(d dialect) string {
	if d == dialectBigQuery {
		return bigQuerySummarySQL
	}
	return clickHouseSummarySQL
}

func scanMetricSummary(r rows) (MetricSummary, error) {
	var s MetricSummary
	err := r.Scan(&s.MetricName, &s.Latest, &s.Min, &s.Max, &s.Mean, &s.LatestStep, &s.LatestTimestamp)
	return s, err
}
