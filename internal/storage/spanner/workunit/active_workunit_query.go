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
	"cloud.google.com/go/spanner"
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
	where := safesql.New(" WHERE wu.QueueName IN UNNEST(@queueNames) AND wu.IsPendingOrRunningJob IS NOT NULL")
	count := spanner.Statement{
		SQL:    safesql.Concat(safesql.New("SELECT COUNT(*) FROM WorkUnits wu"), where).String(),
		Params: map[string]any{"queueNames": queueNames},
	}
	fetchSQL := safesql.Concat(
		safesql.New("SELECT wu.BitReversedXid, wu.WorkUnitId, COALESCE(e.Creator, '') AS Creator, COALESCE(e.Title, '') AS ExperimentTitle FROM WorkUnits wu JOIN Experiments e ON wu.BitReversedXid = e.BitReversedXid"),
		where,
		activeWorkUnitOrder(query),
		safesql.New(" LIMIT @limit OFFSET @offset"),
	)
	fetch := spanner.Statement{
		SQL:    fetchSQL.String(),
		Params: map[string]any{"queueNames": queueNames, "limit": limit, "offset": offset},
	}
	return count, fetch
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
