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

// listWorkUnitExecutionsSQL reads the archived executions of a work unit, newest execution first.
var listWorkUnitExecutionsSQL = safesql.New(`SELECT
  Xid,
  WorkUnitId,
  ExecutionId,
  ExecutionMetadata,
  WorkUnitStatus,
  CreateTime,
  UpdateTime,
  CompleteTime
FROM WorkUnitExecutions
WHERE Xid = $1 AND WorkUnitId = $2
ORDER BY ExecutionId DESC`)

// ListWorkUnitExecutions retrieves the executions of work unit (xid, wid): the current execution
// first, followed by the archived executions with the most recent first. Both are read from one
// database snapshot. It fails with NotFound when the work unit does not exist.
func (r *Repository) ListWorkUnitExecutions(ctx context.Context, xid int64, wid int64) ([]*essmsgpb.WorkUnitExecution, error) {
	ctx = logging.WithWorkUnitName(ctx, validator.ConstructWorkUnitName(xid, wid))
	logging.Info(ctx, "Fetch work unit executions", "xid", xid, "wid", wid)

	var executions []*essmsgpb.WorkUnitExecution
	readErr := r.readSnapshot(ctx, func(ctx context.Context, tx pgx.Tx) error {
		executions = nil

		// 1. Fetch the current work unit execution.
		current, err := fetchCurrentWorkUnitExecution(ctx, tx, xid, wid)
		if err != nil {
			return fmt.Errorf("failed to fetch work unit: %w", err)
		}
		executions = append(executions, current)

		// 2. Fetch the archived work unit executions.
		archived, err := listArchivedExecutions(ctx, tx, xid, wid)
		if err != nil {
			return err
		}
		executions = append(executions, archived...)
		return nil
	})
	if readErr != nil {
		return nil, readErr
	}

	logging.Info(ctx, "Fetched work unit executions", "workUnitExecutions", executions)
	return executions, nil
}

// listArchivedExecutions reads the WorkUnitExecutions rows of work unit (xid, wid) on db.
func listArchivedExecutions(ctx context.Context, db pgdb.DBTX, xid, wid int64) ([]*essmsgpb.WorkUnitExecution, error) {
	rows, err := db.Query(ctx, listWorkUnitExecutionsSQL.String(), xid, wid)
	if err != nil {
		logging.Error(ctx, "Failed to read work unit execution rows", "error", err)
		return nil, fmt.Errorf("failed to read work unit execution rows: %w", pgdb.TranslateError(err, workUnitExecutionEntity))
	}
	executionRows, err := pgx.CollectRows(rows, pgx.RowToAddrOfStructByNameLax[workUnitExecutionRow])
	if err != nil {
		logging.Error(ctx, "Failed to read work unit execution rows", "error", err)
		return nil, fmt.Errorf("failed to read work unit execution rows: %w", pgdb.TranslateError(err, workUnitExecutionEntity))
	}
	executions := make([]*essmsgpb.WorkUnitExecution, 0, len(executionRows))
	for _, row := range executionRows {
		execution, err := row.toProto(essmsgpb.WorkUnitExecution_INACTIVE)
		if err != nil {
			return nil, fmt.Errorf("failed to read work unit execution rows: %w", err)
		}
		executions = append(executions, execution)
	}
	return executions, nil
}
