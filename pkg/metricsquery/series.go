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

// rawSeriesSQL selects every matching point; avg, min, and max all carry the raw metric value.
// Placeholders: table, WHERE clause.
const rawSeriesSQL = `
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
`

// bucketedSeriesSQL aggregates matching points into fixed-width step buckets.
// Placeholders: integer division function, bucket size (twice), table, WHERE clause.
const bucketedSeriesSQL = `
	SELECT
		%s(step, %d) * %d AS bucket_step,
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
`

// MetricPoint is one point of a metric series. For raw series Avg, Min, and Max are equal; for
// bucketed series Step is the bucket start and Timestamp is the latest timestamp in the bucket.
type MetricPoint struct {
	XID        int64
	WorkUnitID int64
	MetricName string
	Step       int64
	Timestamp  time.Time
	Avg        float64
	Min        float64
	Max        float64
}

// QuerySeries returns the points matching f, ordered by metric name, work unit, and step. A
// bucketSize of 1 or less returns raw points; larger values aggregate points into step buckets of
// that width.
func (q *Querier) QuerySeries(ctx context.Context, f MetricFilter, bucketSize int64) ([]MetricPoint, error) {
	pred := buildWhereClause(f)
	return queryAll(ctx, q.backend, "series", q.seriesSQL(pred.whereClause, bucketSize), pred.args, scanMetricPoint)
}

func (q *Querier) seriesSQL(where string, bucketSize int64) string {
	if bucketSize <= 1 {
		return fmt.Sprintf(rawSeriesSQL, q.table, where)
	}
	return fmt.Sprintf(bucketedSeriesSQL, intDivFunc(q.backend.dialect()), bucketSize, bucketSize, q.table, where)
}

// intDivFunc returns the integer division function name for d.
func intDivFunc(d dialect) string {
	if d == dialectBigQuery {
		return "DIV"
	}
	return "intDiv"
}

func scanMetricPoint(r rows) (MetricPoint, error) {
	var p MetricPoint
	err := r.Scan(&p.Step, &p.Avg, &p.Min, &p.Max, &p.Timestamp, &p.MetricName, &p.WorkUnitID, &p.XID)
	return p, err
}
