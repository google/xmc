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

	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/safesql"
	"xmanager_cloud/internal/storage/postgres/pgdb"
	"xmanager_cloud/internal/validator"
)

// lockExperimentSQL locks the parent experiment row until the transaction ends. Work unit IDs are
// allocated with an aggregate that cannot be locked, so transactions that allocate or insert work
// unit IDs serialize on the parent instead. FOR NO KEY UPDATE does not conflict with the FOR KEY
// SHARE locks that inserting child rows takes on the parent.
var lockExperimentSQL = safesql.New(`SELECT 1 FROM Experiments WHERE Xid = $1 FOR NO KEY UPDATE`)

// insertWorkUnitSQL inserts a work unit together with its current execution and the columns
// derived from the executable and the execution status.
var insertWorkUnitSQL = safesql.New(`INSERT INTO WorkUnits (
  Xid, WorkUnitId, WorkUnitType, Priority, Urls, Tags, Notes, Executable,
  ExecutionCreateTime, ExecutionUpdateTime, ExecutionCompleteTime, ExecutionId, ExecutionMetadata, WorkUnitStatus,
  QueueName, ExecutionState, ExecutionStatusMessageSeverity, PositionInQueue,
  CreateTime, UpdateTime)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20)`)

// InsertWorkUnit inserts wu, whose current execution name provides the work unit and execution
// IDs, within the transaction. It first locks the parent experiment row, failing with NotFound
// when the experiment does not exist, so that it serializes with concurrent FetchNextWorkUnitID
// callers. Inserting an existing work unit ID fails with AlreadyExists.
func (r *TxRepository) InsertWorkUnit(ctx context.Context, wu *essmsgpb.WorkUnit) error {
	if wu == nil {
		return pgdb.WrapStatusError("work unit must not be nil", grpcerr.ErrInvalidArgument, nil)
	}
	if err := r.requireTx(); err != nil {
		return err
	}
	execution := wu.GetCurrentWorkUnitExecution()
	xid, wid, eid, err := validator.ValidateWorkUnitExecutionName(execution.GetName())
	if err != nil {
		return fmt.Errorf("failed to validate WorkUnitExecution name %q: %w", execution.GetName(), err)
	}
	ctx = logging.WithWorkUnitName(ctx, validator.ConstructWorkUnitName(xid, wid))

	urls, err := marshalURLs(wu.GetUrls())
	if err != nil {
		return err
	}
	executable, err := marshalColumn("Executable", wu.GetExecutable())
	if err != nil {
		return err
	}
	protos, err := marshalExecutionProtos(execution)
	if err != nil {
		return err
	}
	if err := r.lockExperiment(ctx, xid); err != nil {
		return err
	}

	derived := deriveExecutionColumns(execution.GetStatus())
	_, err = r.tx.Exec(ctx, insertWorkUnitSQL.String(),
		xid, wid, int64(wu.GetWorkUnitType()), int64(wu.GetPriority()), urls, tagsOrEmpty(wu.GetTags()), wu.GetNotes(), executable,
		execution.GetCreateTime().AsTime(), execution.GetUpdateTime().AsTime(), nullableTimestamp(execution.GetCompleteTime()), eid, protos.metadata, protos.status,
		queueName(wu.GetExecutable()), derived.state, derived.severity, derived.position,
		wu.GetCreateTime().AsTime(), wu.GetUpdateTime().AsTime())
	if err != nil {
		logging.Error(ctx, "failed to insert work unit", "error", err)
		return pgdb.TranslateError(err, workUnitEntityName(xid, wid))
	}
	return nil
}

// lockExperiment locks the Experiments row of experiment xid until the transaction ends. It fails
// with NotFound when the experiment does not exist, since a missing row cannot be locked.
func (r *TxRepository) lockExperiment(ctx context.Context, xid int64) error {
	var one int64
	err := r.tx.QueryRow(ctx, lockExperimentSQL.String(), xid).Scan(&one)
	if err != nil {
		if !pgdb.IsNotFound(err) {
			logging.Error(ctx, "Failed to lock experiment row", "error", err)
		}
		return pgdb.TranslateError(err, fmt.Sprintf("%s %s", experimentEntity, validator.ConstructExperimentName(xid)))
	}
	return nil
}
