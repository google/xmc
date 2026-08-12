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
	"strings"

	dpb "xmanager_cloud/dashboard_service/proto/api_go_proto"
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

	if dataSpec != nil && len(dataSpec.GetMetrics()) > 0 {
		if len(dataSpec.GetMetrics()) == 1 {
			clauses = append(clauses, "metric_name = ?")
			args = append(args, dataSpec.GetMetrics()[0])
		} else {
			clauses = append(clauses, "metric_name IN (?)")
			args = append(args, dataSpec.GetMetrics())
		}
	}

	for _, r := range ranges {
		if r == nil {
			continue
		}
		if r.GetStart() != nil {
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
		}
		if r.GetEnd() != nil {
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
		}
	}

	return queryPredicate{
		whereClause: strings.Join(clauses, " AND "),
		args:        args,
	}
}
