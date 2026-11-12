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

	"xmanager_cloud/internal/safesql"
	"xmanager_cloud/internal/storage"
)

type activeWorkUnitStatement struct {
	sql  safesql.TrustedSQLString
	args []any
}

func activeWorkUnitStatements(queueNames []string, limit, offset int64, query storage.ActiveWorkUnitQuery) (activeWorkUnitStatement, activeWorkUnitStatement, error) {
	order, err := activeWorkUnitOrder(query.OrderBy)
	if err != nil {
		return activeWorkUnitStatement{}, activeWorkUnitStatement{}, err
	}
	where := safesql.New(" WHERE wu.QueueName = ANY($1) AND wu.IsPendingOrRunningJob IS NOT NULL")
	count := activeWorkUnitStatement{
		sql:  safesql.Concat(safesql.New("SELECT COUNT(*) FROM WorkUnits wu"), where),
		args: []any{queueNames},
	}
	fetchSQL := safesql.Concat(
		safesql.New(`SELECT wu.Xid, wu.WorkUnitId, wu.WorkUnitType, wu.Priority, wu.Urls, wu.Tags, wu.Notes, wu.Executable, wu.ExecutionCreateTime, wu.ExecutionUpdateTime, wu.ExecutionCompleteTime, wu.ExecutionId, wu.ExecutionMetadata, wu.WorkUnitStatus, wu.CreateTime, wu.UpdateTime, COALESCE(e.Creator, '') AS Creator, COALESCE(e.Title, '') AS experiment_title FROM WorkUnits wu JOIN Experiments e ON wu.Xid = e.Xid`),
		where,
		order,
		safesql.New(" LIMIT $2 OFFSET $3"),
	)
	fetch := activeWorkUnitStatement{sql: fetchSQL, args: []any{queueNames, limit, offset}}
	return count, fetch, nil
}

func activeWorkUnitOrder(orderBy string) (safesql.TrustedSQLString, error) {
	statusRank := safesql.New("CASE wu.ExecutionState WHEN 4 THEN 0 WHEN 3 THEN 1 WHEN 1 THEN 2 ELSE 3 END")
	var field safesql.TrustedSQLString
	switch orderBy {
	case "":
		field = safesql.New("wu.SortPhase DESC, wu.PositionInQueue ASC, wu.CreateTime ASC")
	case "author asc":
		field = safesql.New(`COALESCE(e.Creator, '') COLLATE "C" ASC`)
	case "author desc":
		field = safesql.New(`COALESCE(e.Creator, '') COLLATE "C" DESC`)
	case "priority asc":
		field = safesql.New("wu.Priority ASC")
	case "priority desc":
		field = safesql.New("wu.Priority DESC")
	case "status asc":
		field = safesql.Concat(statusRank, safesql.New(" ASC"))
	case "status desc":
		field = safesql.Concat(statusRank, safesql.New(" DESC"))
	default:
		return safesql.EmptyString(), fmt.Errorf("unsupported order_by %q", orderBy)
	}
	return safesql.Concat(safesql.New(" ORDER BY "), field, safesql.New(", wu.WorkUnitId ASC, wu.Xid ASC")), nil
}
