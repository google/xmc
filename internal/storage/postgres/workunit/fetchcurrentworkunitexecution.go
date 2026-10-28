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
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/safesql"
	"xmanager_cloud/internal/storage/postgres/pgdb"
	"xmanager_cloud/internal/validator"
)

// selectCurrentExecutionSQL reads the current execution columns of a work unit, aliased to the
// WorkUnitExecutions column names so that they scan into workUnitExecutionRow.
var selectCurrentExecutionSQL = safesql.New(`SELECT
  Xid,
  WorkUnitId,
  ExecutionId,
  ExecutionCreateTime AS CreateTime,
  ExecutionUpdateTime AS UpdateTime,
  ExecutionCompleteTime AS CompleteTime,
  ExecutionMetadata,
  WorkUnitStatus
FROM WorkUnits
WHERE Xid = $1 AND WorkUnitId = $2`)

// FetchCurrentWorkUnitExecution retrieves the current execution of work unit (xid, wid). It fails
// with NotFound when the work unit does not exist.
func (r *Repository) FetchCurrentWorkUnitExecution(ctx context.Context, xid int64, wid int64) (*essmsgpb.WorkUnitExecution, error) {
	if err := r.requireDB(); err != nil {
		return nil, err
	}
	ctx = logging.WithWorkUnitName(ctx, validator.ConstructWorkUnitName(xid, wid))
	return fetchCurrentWorkUnitExecution(ctx, r.db, xid, wid)
}

// fetchCurrentWorkUnitExecution reads the current execution of work unit (xid, wid) on db.
func fetchCurrentWorkUnitExecution(ctx context.Context, db pgdb.DBTX, xid, wid int64) (*essmsgpb.WorkUnitExecution, error) {
	logging.Info(ctx, "Fetch current work unit execution row", "xid", xid, "wid", wid)

	rows, err := db.Query(ctx, selectCurrentExecutionSQL.String(), xid, wid)
	if err != nil {
		logging.Error(ctx, "Failed to read WorkUnit row", "error", err)
		return nil, pgdb.TranslateError(err, workUnitEntityName(xid, wid))
	}
	row, err := pgx.CollectExactlyOneRow(rows, pgx.RowToAddrOfStructByNameLax[workUnitExecutionRow])
	if err != nil {
		if !pgdb.IsNotFound(err) {
			logging.Error(ctx, "Failed to read WorkUnit row", "error", err)
		}
		return nil, pgdb.TranslateError(err, workUnitEntityName(xid, wid))
	}
	execution, err := row.toCurrentExecutionProto()
	if err != nil {
		return nil, fmt.Errorf("failed to convert WorkUnit row to proto: %w", err)
	}

	logging.Info(ctx, "Fetched current work unit execution from storage", "work_unit_execution", execution)
	return execution, nil
}
