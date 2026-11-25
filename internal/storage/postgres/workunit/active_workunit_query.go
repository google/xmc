// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package workunit

import (
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/safesql"
	"xmanager_cloud/internal/storage"
)

type activeWorkUnitStatement struct {
	sql  safesql.TrustedSQLString
	args []any
}

var (
	schedulingOrderSQL = safesql.New(" ORDER BY wu.SortPhase DESC, wu.PositionInQueue ASC, wu.CreateTime ASC, wu.WorkUnitId ASC, wu.Xid ASC")
	tieBreakerSQL      = safesql.New(", wu.WorkUnitId ASC, wu.Xid ASC")

	activeWorkUnitSortCols = map[storage.ActiveWorkUnitSortField]safesql.TrustedSQLString{
		storage.ActiveWorkUnitSortAuthor:   safesql.New(`COALESCE(e.Creator, '') COLLATE "C"`),
		storage.ActiveWorkUnitSortPriority: safesql.New("wu.Priority"),
		// WorkUnitState enum values: RUNNING=4, STARTING=3, INITIALIZING=1, SCHEDULING=2.
		storage.ActiveWorkUnitSortStatus: safesql.New("CASE wu.ExecutionState WHEN 4 THEN 0 WHEN 3 THEN 1 WHEN 1 THEN 2 ELSE 3 END"),
	}
)

func activeWorkUnitStatements(queueNames []string, limit, offset int64, query storage.ActiveWorkUnitQuery) (activeWorkUnitStatement, activeWorkUnitStatement) {
	baseWhere := safesql.New(" WHERE wu.QueueName = ANY($1) AND wu.IsPendingOrRunningJob IS NOT NULL")

	countFilterSQL, countFilterArgs := activeWorkUnitPredicates(query.Filter, 2)
	countFrom := safesql.New("SELECT COUNT(*) FROM WorkUnits wu")
	if query.Filter.HasAuthor {
		countFrom = safesql.Concat(countFrom, safesql.New(" JOIN Experiments e ON wu.Xid = e.Xid"))
	}
	count := activeWorkUnitStatement{
		sql:  safesql.Concat(countFrom, baseWhere, countFilterSQL),
		args: append([]any{queueNames}, countFilterArgs...),
	}

	fetchFilterSQL, fetchFilterArgs := activeWorkUnitPredicates(query.Filter, 4)
	fetchSQL := safesql.Concat(
		safesql.New(`SELECT wu.Xid, wu.WorkUnitId, wu.WorkUnitType, wu.Priority, wu.Urls, wu.Tags, wu.Notes, wu.Executable, wu.ExecutionCreateTime, wu.ExecutionUpdateTime, wu.ExecutionCompleteTime, wu.ExecutionId, wu.ExecutionMetadata, wu.WorkUnitStatus, wu.CreateTime, wu.UpdateTime, wu.Parameters, wu.TrialId, COALESCE(e.Creator, '') AS Creator, COALESCE(e.Title, '') AS experiment_title FROM WorkUnits wu JOIN Experiments e ON wu.Xid = e.Xid`),
		baseWhere,
		fetchFilterSQL,
		activeWorkUnitOrder(query),
		safesql.New(" LIMIT $2 OFFSET $3"),
	)
	fetch := activeWorkUnitStatement{
		sql:  fetchSQL,
		args: append([]any{queueNames, limit, offset}, fetchFilterArgs...),
	}
	return count, fetch
}

func activeWorkUnitPredicates(filter storage.ActiveWorkUnitFilter, argOffset int) (safesql.TrustedSQLString, []any) {
	var parts []safesql.TrustedSQLString
	var args []any
	nextArg := argOffset
	if filter.HasAuthor {
		parts = append(parts, safesql.Concat(
			safesql.New(" AND STRPOS(LOWER(COALESCE(e.Creator, '')), LOWER("),
			safesql.NewBindingParam("$", uint64(nextArg)),
			safesql.New(")) > 0"),
		))
		args = append(args, filter.AuthorSubstring)
		nextArg++
	}
	if filter.Status != essmsgpb.WorkUnitStatus_WORK_UNIT_STATE_UNSPECIFIED {
		parts = append(parts, safesql.Concat(
			safesql.New(" AND wu.ExecutionState = "),
			safesql.NewBindingParam("$", uint64(nextArg)),
		))
		args = append(args, int32(filter.Status))
	}
	return safesql.Concat(parts...), args
}

func activeWorkUnitOrder(q storage.ActiveWorkUnitQuery) safesql.TrustedSQLString {
	col, ok := activeWorkUnitSortCols[q.SortField]
	if !ok {
		return schedulingOrderSQL
	}
	dir := safesql.New(" ASC")
	if q.Descending {
		dir = safesql.New(" DESC")
	}
	return safesql.Concat(safesql.New(" ORDER BY "), col, dir, tieBreakerSQL)
}
