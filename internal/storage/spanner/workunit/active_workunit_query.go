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
	"fmt"
	"strings"

	"cloud.google.com/go/spanner"
	"xmanager_cloud/internal/safesql"
	"xmanager_cloud/internal/storage"
)

func activeWorkUnitStatements(queueNames []string, limit, offset int64, query storage.ActiveWorkUnitQuery) (spanner.Statement, spanner.Statement, error) {
	order, err := activeWorkUnitOrder(query.OrderBy)
	if err != nil {
		return spanner.Statement{}, spanner.Statement{}, err
	}
	where := safesql.New(" WHERE wu.QueueName IN UNNEST(@queueNames) AND wu.IsPendingOrRunningJob IS NOT NULL")
	count := spanner.Statement{
		SQL:    safesql.Concat(safesql.New("SELECT COUNT(*) FROM WorkUnits wu"), where).String(),
		Params: map[string]any{"queueNames": queueNames},
	}
	fetchSQL := safesql.Concat(
		safesql.New("SELECT wu.BitReversedXid, wu.WorkUnitId, COALESCE(e.Creator, '') AS Creator, COALESCE(e.Title, '') AS ExperimentTitle FROM WorkUnits wu JOIN Experiments e ON wu.BitReversedXid = e.BitReversedXid"),
		where,
		order,
		safesql.New(" LIMIT @limit OFFSET @offset"),
	)
	fetch := spanner.Statement{
		SQL:    fetchSQL.String(),
		Params: map[string]any{"queueNames": queueNames, "limit": limit, "offset": offset},
	}
	return count, fetch, nil
}

var (
	activeWorkUnitSortCols = map[string]safesql.TrustedSQLString{
		"author":   safesql.New("COALESCE(e.Creator, '')"),
		"priority": safesql.New("wu.Priority"),
		"status":   safesql.New("CASE wu.ExecutionState WHEN 'RUNNING' THEN 0 WHEN 'STARTING' THEN 1 WHEN 'INITIALIZING' THEN 2 ELSE 3 END"),
	}
	activeWorkUnitSortDirs = map[string]safesql.TrustedSQLString{
		"asc":  safesql.New(" ASC"),
		"desc": safesql.New(" DESC"),
	}
)

func activeWorkUnitOrder(orderBy string) (safesql.TrustedSQLString, error) {
	if orderBy == "" {
		return safesql.New(" ORDER BY wu.SortPhase DESC, wu.PositionInQueue ASC, wu.CreateTime ASC, wu.WorkUnitId ASC, wu.Xid ASC"), nil
	}
	field, dir, ok := strings.Cut(orderBy, " ")
	colSQL, colOK := activeWorkUnitSortCols[field]
	dirSQL, dirOK := activeWorkUnitSortDirs[dir]
	if !ok || !colOK || !dirOK {
		return safesql.EmptyString(), fmt.Errorf("unsupported order_by %q", orderBy)
	}
	return safesql.Concat(safesql.New(" ORDER BY "), colSQL, dirSQL, safesql.New(", wu.WorkUnitId ASC, wu.Xid ASC")), nil
}
