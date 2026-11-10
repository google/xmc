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
	"fmt"
	"strings"
	"time"
)

// MetricFilter selects rows from the metrics table. Every non-empty field narrows the selection.
type MetricFilter struct {
	// XID is the experiment whose metrics are selected.
	XID int64
	// WorkUnitID restricts rows to one work unit. Zero or negative selects every work unit.
	WorkUnitID int64
	// MetricNames restricts rows to the listed metrics. Empty selects every metric.
	MetricNames []string
	// Ranges bound rows by step or timestamp. All ranges apply together (AND).
	Ranges []Range
}

// Range bounds rows by step and/or timestamp, inclusive at both ends. A nil bound is open.
type Range struct {
	StartStep *int64
	StartTime *time.Time
	EndStep   *int64
	EndTime   *time.Time
}

type queryPredicate struct {
	whereClause string
	args        []any
}

// buildWhereClause constructs a parameterized WHERE predicate for the metrics table.
func buildWhereClause(f MetricFilter) queryPredicate {
	clauses := []string{"xid = ?"}
	args := []any{f.XID}

	if f.WorkUnitID > 0 {
		clauses = append(clauses, "work_unit_id = ?")
		args = append(args, f.WorkUnitID)
	}

	clauses, args = appendMetricClauses(f.MetricNames, clauses, args)
	for _, r := range f.Ranges {
		clauses, args = appendRangeClauses(r, clauses, args)
	}

	return queryPredicate{
		whereClause: strings.Join(clauses, " AND "),
		args:        args,
	}
}

func appendMetricClauses(names []string, clauses []string, args []any) ([]string, []any) {
	switch len(names) {
	case 0:
		return clauses, args
	case 1:
		return append(clauses, "metric_name = ?"), append(args, names[0])
	}

	placeholders := make([]string, len(names))
	for i, name := range names {
		placeholders[i] = "?"
		args = append(args, name)
	}
	clauses = append(clauses, fmt.Sprintf("metric_name IN (%s)", strings.Join(placeholders, ", ")))
	return clauses, args
}

// appendRangeClauses appends the start bounds of r followed by its end bounds.
func appendRangeClauses(r Range, clauses []string, args []any) ([]string, []any) {
	if r.StartStep != nil {
		clauses, args = append(clauses, "step >= ?"), append(args, *r.StartStep)
	}
	if r.StartTime != nil {
		clauses, args = append(clauses, "timestamp >= ?"), append(args, *r.StartTime)
	}
	if r.EndStep != nil {
		clauses, args = append(clauses, "step <= ?"), append(args, *r.EndStep)
	}
	if r.EndTime != nil {
		clauses, args = append(clauses, "timestamp <= ?"), append(args, *r.EndTime)
	}
	return clauses, args
}
