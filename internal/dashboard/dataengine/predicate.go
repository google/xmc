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
	"fmt"
	"strings"

	dpb "xmanager_cloud/api/protos/dashboard"
)

type queryPredicate struct {
	whereClause string
	args        []any
}

// buildWhereClause constructs parameterized WHERE predicates for xmc_metrics.
func buildWhereClause(xid int64, wid int64, dataSpec *dpb.DataSpec, ranges []*dpb.DataSpec_DataRange) queryPredicate {
	var clauses []string
	var args []any

	clauses = append(clauses, "xid = ?")
	args = append(args, xid)

	if wid > 0 {
		clauses = append(clauses, "work_unit_id = ?")
		args = append(args, wid)
	}

	clauses, args = appendMetricClauses(dataSpec, clauses, args)
	clauses, args = appendRangeClauses(ranges, clauses, args)

	return queryPredicate{
		whereClause: strings.Join(clauses, " AND "),
		args:        args,
	}
}

func appendMetricClauses(dataSpec *dpb.DataSpec, clauses []string, args []any) ([]string, []any) {
	if dataSpec == nil || len(dataSpec.GetMetrics()) == 0 {
		return clauses, args
	}

	if len(dataSpec.GetMetrics()) == 1 {
		clauses = append(clauses, "metric_name = ?")
		args = append(args, dataSpec.GetMetrics()[0])
		return clauses, args
	}

	placeholders := make([]string, len(dataSpec.GetMetrics()))
	for i, m := range dataSpec.GetMetrics() {
		placeholders[i] = "?"
		args = append(args, m)
	}
	clauses = append(clauses, fmt.Sprintf("metric_name IN (%s)", strings.Join(placeholders, ", ")))
	return clauses, args
}

func appendRangeClauses(ranges []*dpb.DataSpec_DataRange, clauses []string, args []any) ([]string, []any) {
	for _, r := range ranges {
		if r == nil {
			continue
		}
		clauses, args = appendStartRangeClause(r, clauses, args)
		clauses, args = appendEndRangeClause(r, clauses, args)
	}
	return clauses, args
}

func appendStartRangeClause(r *dpb.DataSpec_DataRange, clauses []string, args []any) ([]string, []any) {
	if r.GetStart() == nil {
		return clauses, args
	}
	switch s := r.GetStart().(type) {
	case *dpb.DataSpec_DataRange_StartStep:
		clauses = append(clauses, "step >= ?")
		args = append(args, s.StartStep)
	case *dpb.DataSpec_DataRange_StartTime:
		if s.StartTime.IsValid() {
			clauses = append(clauses, "timestamp >= ?")
			args = append(args, s.StartTime.AsTime())
		}
	}
	return clauses, args
}

func appendEndRangeClause(r *dpb.DataSpec_DataRange, clauses []string, args []any) ([]string, []any) {
	if r.GetEnd() == nil {
		return clauses, args
	}
	switch e := r.GetEnd().(type) {
	case *dpb.DataSpec_DataRange_EndStep:
		clauses = append(clauses, "step <= ?")
		args = append(args, e.EndStep)
	case *dpb.DataSpec_DataRange_EndTime:
		if e.EndTime.IsValid() {
			clauses = append(clauses, "timestamp <= ?")
			args = append(args, e.EndTime.AsTime())
		}
	}
	return clauses, args
}
