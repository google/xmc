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
	"maps"

	"cloud.google.com/go/spanner"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/safesql"
	"xmanager_cloud/internal/storage"
)

var (
	schedulingOrderSQL = safesql.New(" ORDER BY wu.SortPhase DESC, wu.PositionInQueue ASC, wu.CreateTime ASC, wu.WorkUnitId ASC, wu.Xid ASC")
	tieBreakerSQL      = safesql.New(", wu.WorkUnitId ASC, wu.Xid ASC")

	activeWorkUnitSortCols = map[storage.ActiveWorkUnitSortField]safesql.TrustedSQLString{
		storage.ActiveWorkUnitSortAuthor:   safesql.New("COALESCE(e.Creator, '')"),
		storage.ActiveWorkUnitSortPriority: safesql.New("wu.Priority"),
		storage.ActiveWorkUnitSortStatus:   safesql.New("CASE wu.ExecutionState WHEN 'RUNNING' THEN 0 WHEN 'STARTING' THEN 1 WHEN 'INITIALIZING' THEN 2 ELSE 3 END"),
	}
)

func activeWorkUnitStatements(queueNames []string, limit, offset int64, query storage.ActiveWorkUnitQuery) (spanner.Statement, spanner.Statement) {
	baseWhere := safesql.New(" WHERE wu.QueueName IN UNNEST(@queueNames) AND wu.IsPendingOrRunningJob IS NOT NULL")
	filterSQL, filterParams := activeWorkUnitPredicates(query.Filter)

	countFrom := safesql.New("SELECT COUNT(*) FROM WorkUnits wu")
	if query.Filter.HasAuthor {
		countFrom = safesql.Concat(countFrom, safesql.New(" JOIN Experiments e ON wu.BitReversedXid = e.BitReversedXid"))
	}
	countParams := map[string]any{"queueNames": queueNames}
	maps.Copy(countParams, filterParams)
	count := spanner.Statement{
		SQL:    safesql.Concat(countFrom, baseWhere, filterSQL).String(),
		Params: countParams,
	}

	fetchParams := map[string]any{"queueNames": queueNames, "limit": limit, "offset": offset}
	maps.Copy(fetchParams, filterParams)
	fetchSQL := safesql.Concat(
		safesql.New("SELECT wu.BitReversedXid, wu.WorkUnitId, COALESCE(e.Creator, '') AS Creator, COALESCE(e.Title, '') AS ExperimentTitle FROM WorkUnits wu JOIN Experiments e ON wu.BitReversedXid = e.BitReversedXid"),
		baseWhere,
		filterSQL,
		activeWorkUnitOrder(query),
		safesql.New(" LIMIT @limit OFFSET @offset"),
	)
	fetch := spanner.Statement{
		SQL:    fetchSQL.String(),
		Params: fetchParams,
	}
	return count, fetch
}

func activeWorkUnitPredicates(filter storage.ActiveWorkUnitFilter) (safesql.TrustedSQLString, map[string]any) {
	var parts []safesql.TrustedSQLString
	params := map[string]any{}
	if filter.HasAuthor {
		parts = append(parts, safesql.New(" AND STRPOS(LOWER(COALESCE(e.Creator, '')), LOWER(@author)) > 0"))
		params["author"] = filter.AuthorSubstring
	}
	if filter.Status != essmsgpb.WorkUnitStatus_WORK_UNIT_STATE_UNSPECIFIED {
		parts = append(parts, safesql.New(" AND wu.ExecutionState = @status"))
		params["status"] = filter.Status.String()
	}
	return safesql.Concat(parts...), params
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
