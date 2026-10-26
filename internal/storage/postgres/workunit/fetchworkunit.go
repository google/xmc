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

var (
	// selectWorkUnitSQL reads one work unit without locking it.
	selectWorkUnitSQL = safesql.New("SELECT " + workUnitColumns + `
FROM WorkUnits wu
WHERE wu.Xid = $1 AND wu.WorkUnitId = $2`)

	// lockWorkUnitSQL reads one work unit and locks it until the transaction ends. FOR NO KEY
	// UPDATE, unlike FOR UPDATE, does not conflict with the FOR KEY SHARE locks that foreign-key
	// checks take on the row when its executions, operations or paused tasks are inserted.
	lockWorkUnitSQL = safesql.New("SELECT " + workUnitColumns + `
FROM WorkUnits wu
WHERE wu.Xid = $1 AND wu.WorkUnitId = $2
FOR NO KEY UPDATE`)

	// lockUpdatingWorkUnitsSQL reads the executables and current executions of a batch of work
	// units and locks them until the transaction ends, in primary-key order so that concurrent
	// transactions acquire the row locks in the same order.
	lockUpdatingWorkUnitsSQL = safesql.New(`SELECT
  Xid,
  WorkUnitId,
  ExecutionId,
  ExecutionCreateTime AS CreateTime,
  ExecutionUpdateTime AS UpdateTime,
  ExecutionCompleteTime AS CompleteTime,
  ExecutionMetadata,
  WorkUnitStatus,
  Executable
FROM WorkUnits
WHERE Xid = $1 AND WorkUnitId = ANY($2)
ORDER BY Xid, WorkUnitId
FOR NO KEY UPDATE`)
)

// updatingWorkUnitRow is one row of lockUpdatingWorkUnitsSQL.
type updatingWorkUnitRow struct {
	workUnitExecutionRow
	Executable []byte
}

// FetchWorkUnit retrieves a work unit by its unique ID without locking it, so callers that write
// the work unit back must read it with TxRepository.FetchWorkUnit instead.
func (r *Repository) FetchWorkUnit(ctx context.Context, xid int64, wid int64) (*essmsgpb.WorkUnit, error) {
	if err := r.requireDB(); err != nil {
		return nil, err
	}
	return fetchWorkUnit(ctx, r.db, selectWorkUnitSQL, xid, wid)
}

// FetchWorkUnit retrieves a work unit by its unique ID within the transaction and locks its row
// until the transaction ends, so that concurrent read-modify-write transactions on the same work
// unit run one after another instead of overwriting each other's updates.
func (r *TxRepository) FetchWorkUnit(ctx context.Context, xid, wid int64) (*essmsgpb.WorkUnit, error) {
	if err := r.requireTx(); err != nil {
		return nil, err
	}
	return fetchWorkUnit(ctx, r.tx, lockWorkUnitSQL, xid, wid)
}

// fetchWorkUnit runs query, which must select workUnitColumns of work unit (xid, wid), on db and
// converts the row into a proto. It fails with NotFound when the work unit does not exist.
func fetchWorkUnit(ctx context.Context, db pgdb.DBTX, query safesql.TrustedSQLString, xid, wid int64) (*essmsgpb.WorkUnit, error) {
	ctx = logging.WithWorkUnitName(ctx, validator.ConstructWorkUnitName(xid, wid))
	logging.Info(ctx, "Fetch work unit row")

	rows, err := db.Query(ctx, query.String(), xid, wid)
	if err != nil {
		logging.Error(ctx, "Failed to read work unit row", "error", err)
		return nil, pgdb.TranslateError(err, workUnitEntityName(xid, wid))
	}
	row, err := pgx.CollectExactlyOneRow(rows, pgx.RowToAddrOfStructByNameLax[workUnitRow])
	if err != nil {
		if !pgdb.IsNotFound(err) {
			logging.Error(ctx, "Failed to read work unit row", "error", err)
		}
		return nil, pgdb.TranslateError(err, workUnitEntityName(xid, wid))
	}
	workUnit, err := row.toProto()
	if err != nil {
		return nil, fmt.Errorf("failed to convert WorkUnit row to proto: %w", err)
	}

	logging.Info(ctx, "Fetched work unit from storage", "work_unit", workUnit)
	return workUnit, nil
}

// FetchUpdatingWorkUnits retrieves the work units of experiment xid whose IDs are in wids, keyed
// by work unit ID, and locks their rows until the transaction ends. Each returned work unit holds
// only its name, executable and current execution; IDs without a work unit are omitted.
func (r *TxRepository) FetchUpdatingWorkUnits(ctx context.Context, xid int64, wids []int64) (map[int64]*essmsgpb.WorkUnit, error) {
	if err := r.requireTx(); err != nil {
		return nil, err
	}
	ctx = logging.WithExperimentName(ctx, validator.ConstructExperimentName(xid))
	logging.Info(ctx, "Fetch work units for update", "work_unit_ids", wids)

	rows, err := r.tx.Query(ctx, lockUpdatingWorkUnitsSQL.String(), xid, wids)
	if err != nil {
		logging.Error(ctx, "Failed to read work unit rows for update", "error", err)
		return nil, pgdb.TranslateError(err, workUnitEntity)
	}
	updatingRows, err := pgx.CollectRows(rows, pgx.RowToAddrOfStructByNameLax[updatingWorkUnitRow])
	if err != nil {
		logging.Error(ctx, "Failed to read work unit rows for update", "error", err)
		return nil, pgdb.TranslateError(err, workUnitEntity)
	}

	workUnits := make(map[int64]*essmsgpb.WorkUnit, len(updatingRows))
	for _, row := range updatingRows {
		workUnit, err := row.toProto()
		if err != nil {
			logging.Error(ctx, "Failed to convert work unit row for update", "work_unit_id", row.WorkUnitID, "error", err)
			return nil, err
		}
		workUnits[row.WorkUnitID] = workUnit
	}
	return workUnits, nil
}

// toProto converts the row into the partial WorkUnit proto that FetchUpdatingWorkUnits returns.
// Like the Spanner implementation, it converts the current execution with the WorkUnitExecutions
// row conversion, so the execution reports the INACTIVE state.
func (row *updatingWorkUnitRow) toProto() (*essmsgpb.WorkUnit, error) {
	execution, err := row.workUnitExecutionRow.toProto(essmsgpb.WorkUnitExecution_INACTIVE)
	if err != nil {
		return nil, fmt.Errorf("failed to convert row to WorkUnitExecution proto: %w", err)
	}
	executable := &essmsgpb.Executable{}
	if err := unmarshalColumn("Executable", row.Executable, executable); err != nil {
		return nil, fmt.Errorf("failed to scan Executable column: %w", err)
	}
	return &essmsgpb.WorkUnit{
		Name:                     validator.ConstructWorkUnitName(row.XID, row.WorkUnitID),
		Executable:               executable,
		CurrentWorkUnitExecution: execution,
	}, nil
}
