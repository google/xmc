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

// Package workunit contains the concrete PostgreSQL repository implementation for work units and work unit executions.
package workunit

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kylelemons/godebug/diff"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/query_generator/querytools"
	"xmanager_cloud/internal/safesql"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/data"
	postgresdata "xmanager_cloud/internal/storage/postgres/data"
	"xmanager_cloud/internal/storage/postgres/pgdb"
	"xmanager_cloud/internal/storage/postgres/schema/workunitschema"
	"xmanager_cloud/internal/validator"
)

const entityName = "work unit"

var (
	selectWorkUnitSQL = safesql.New(`SELECT Xid, WorkUnitId, WorkUnitType, Priority, Urls, Tags, Notes, Executable, ExecutionCreateTime, ExecutionUpdateTime, ExecutionCompleteTime, ExecutionId, ExecutionMetadata, WorkUnitStatus, CreateTime, UpdateTime FROM WorkUnits WHERE Xid = $1 AND WorkUnitId = $2`)
	lockWorkUnitSQL   = safesql.Concat(selectWorkUnitSQL, safesql.New(" FOR NO KEY UPDATE"))

	selectCurrentExecutionSQL  = safesql.New(`SELECT Xid, WorkUnitId, ExecutionId, ExecutionCreateTime, ExecutionUpdateTime, ExecutionCompleteTime, ExecutionMetadata, WorkUnitStatus FROM WorkUnits WHERE Xid = $1 AND WorkUnitId = $2`)
	listWorkUnitExecutionsSQL  = safesql.New(`SELECT Xid, WorkUnitId, ExecutionId, ExecutionMetadata, WorkUnitStatus, CreateTime, UpdateTime, CompleteTime FROM WorkUnitExecutions WHERE Xid = $1 AND WorkUnitId = $2 ORDER BY ExecutionId DESC`)
	selectUpdatingWorkUnitsSQL = safesql.New(`SELECT Xid, WorkUnitId, ExecutionId, ExecutionCreateTime, ExecutionUpdateTime, ExecutionCompleteTime, ExecutionMetadata, WorkUnitStatus, Executable FROM WorkUnits WHERE Xid = $1 AND WorkUnitId = ANY($2) ORDER BY WorkUnitId FOR NO KEY UPDATE`)

	lockExperimentSQL = safesql.New(`SELECT 1 FROM Experiments WHERE Xid = $1 FOR NO KEY UPDATE`)
	nextWorkUnitIDSQL = safesql.New(`SELECT COALESCE(MAX(WorkUnitId), 0) + 1 FROM WorkUnits WHERE Xid = $1`)

	insertWorkUnitSQL = safesql.New(`INSERT INTO WorkUnits (Xid, WorkUnitId, WorkUnitType, Priority, Urls, Tags, Notes, Executable, ExecutionCreateTime, ExecutionUpdateTime, ExecutionCompleteTime, ExecutionId, ExecutionMetadata, WorkUnitStatus, QueueName, ExecutionState, ExecutionStatusMessageSeverity, PositionInQueue, CreateTime, UpdateTime)
VALUES (@xid, @workunitid, @workunittype, @priority, @urls, @tags, @notes, @executable, @executioncreatetime, @executionupdatetime, @executioncompletetime, @executionid, @executionmetadata, @workunitstatus, @queuename, @executionstate, @executionstatusmessageseverity, @positioninqueue, @createtime, @updatetime)`)

	updateWorkUnitSQL = safesql.New(`UPDATE WorkUnits SET Priority = @priority, Urls = @urls, Tags = @tags, Notes = @notes, UpdateTime = @updatetime WHERE Xid = @xid AND WorkUnitId = @workunitid`)

	insertWorkUnitExecutionSQL = safesql.New(`INSERT INTO WorkUnitExecutions (Xid, WorkUnitId, ExecutionId, ExecutionMetadata, WorkUnitStatus, CreateTime, UpdateTime, CompleteTime)
VALUES (@xid, @workunitid, @executionid, @executionmetadata, @workunitstatus, @createtime, @updatetime, @completetime)`)

	updateCurrentExecutionSQL = safesql.New(`UPDATE WorkUnits SET ExecutionId = @executionid, ExecutionCreateTime = @executioncreatetime, ExecutionUpdateTime = @executionupdatetime, ExecutionCompleteTime = @executioncompletetime, ExecutionMetadata = @executionmetadata, WorkUnitStatus = @workunitstatus, ExecutionState = @executionstate, ExecutionStatusMessageSeverity = @executionstatusmessageseverity, PositionInQueue = @positioninqueue, UpdateTime = @updatetime WHERE Xid = @xid AND WorkUnitId = @workunitid`)

	listWorkUnitMessagesSQL = safesql.New(`SELECT WorkUnitStatus FROM WorkUnits WHERE Xid = $1 AND ExecutionStatusMessageSeverity = $2 ORDER BY WorkUnitId ASC`)
)

var (
	_ storage.WorkUnitStore        = (*Repository)(nil)
	_ storage.WorkUnitTxRepository = (*TxRepository)(nil)
)

// Repository manages persistence for work unit entities in PostgreSQL.
type Repository struct {
	pool              *pgxpool.Pool
	db                pgdb.DBTX
	workUnitsQueryGen querytools.QueryGenerator
}

// TxRepository manages transactional persistence for work unit entities in PostgreSQL.
type TxRepository struct {
	tx pgdb.DBTX
}

// NewRepository creates a new PostgreSQL WorkUnit repository instance bound to pool.
func NewRepository(pool *pgxpool.Pool) *Repository {
	qg, err := querytools.NewQueryGenerator(workunitschema.WorkUnitTables, nil, nil, nil, querytools.DeduplicateSelects(true))
	if err != nil {
		panic(fmt.Sprintf("workunitschema.WorkUnitTables is invalid: %v", err))
	}
	return &Repository{pool: pool, db: pgdb.FromPool(pool), workUnitsQueryGen: qg}
}

// WithTx returns a wrapper bound to the read-write transaction tx.
func (r *Repository) WithTx(tx pgdb.DBTX) *TxRepository {
	return &TxRepository{tx: tx}
}

// FetchWorkUnit retrieves a work unit by its unique ID.
func (r *Repository) FetchWorkUnit(ctx context.Context, xid int64, wid int64) (*essmsgpb.WorkUnit, error) {
	ctx = logging.WithWorkUnitName(ctx, validator.ConstructWorkUnitName(xid, wid))
	return queryWorkUnit(ctx, r.db, selectWorkUnitSQL, xid, wid)
}

// UpdateWorkUnit updates a work unit with the given update mask.
func (r *Repository) UpdateWorkUnit(ctx context.Context, update *storage.WorkUnitUpdate) (storage.UpdatedWorkUnits, error) {
	if err := validateWorkUnitUpdate(update); err != nil {
		return storage.UpdatedWorkUnits{}, err
	}
	ctx = logging.WithWorkUnitName(ctx, validator.ConstructWorkUnitName(update.XID, update.WID))
	var res storage.UpdatedWorkUnits
	var mutated bool
	err := pgdb.RunInTx(ctx, r.pool, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		res, mutated, err = applyWorkUnitUpdate(ctx, tx, update)
		return err
	})
	if err != nil {
		return storage.UpdatedWorkUnits{}, err
	}
	logWorkUnitUpdate(ctx, mutated, res.Original, res.Updated)
	return res, nil
}

// FetchCurrentWorkUnitExecution retrieves the current work unit execution.
func (r *Repository) FetchCurrentWorkUnitExecution(ctx context.Context, xid int64, wid int64) (*essmsgpb.WorkUnitExecution, error) {
	ctx = logging.WithWorkUnitName(ctx, validator.ConstructWorkUnitName(xid, wid))
	return queryCurrentExecution(ctx, r.db, xid, wid)
}

// ListWorkUnitExecutions retrieves the historical work unit executions for a given work unit.
func (r *Repository) ListWorkUnitExecutions(ctx context.Context, xid int64, wid int64) ([]*essmsgpb.WorkUnitExecution, error) {
	ctx = logging.WithWorkUnitName(ctx, validator.ConstructWorkUnitName(xid, wid))
	var executions []*essmsgpb.WorkUnitExecution
	err := pgdb.RunInReadOnlyTx(ctx, r.pool, func(ctx context.Context, tx pgx.Tx) error {
		current, err := queryCurrentExecution(ctx, tx, xid, wid)
		if err != nil {
			return err
		}
		historical, err := queryHistoricalExecutions(ctx, tx, xid, wid)
		if err != nil {
			return err
		}
		executions = append([]*essmsgpb.WorkUnitExecution{current}, historical...)
		return nil
	})
	return executions, err
}

// FetchNextWorkUnitID retrieves the next available work unit ID for an experiment within the transaction.
func (r *TxRepository) FetchNextWorkUnitID(ctx context.Context, xid int64) (int64, error) {
	if err := requireDB(r.tx); err != nil {
		return 0, err
	}
	if _, err := r.tx.Exec(ctx, lockExperimentSQL.String(), xid); err != nil {
		return 0, pgdb.TranslateError(err, "experiment")
	}
	var nextID int64
	if err := r.tx.QueryRow(ctx, nextWorkUnitIDSQL.String(), xid).Scan(&nextID); err != nil {
		return 0, pgdb.TranslateError(err, entityName)
	}
	return nextID, nil
}

// InsertWorkUnit inserts a new work unit and its initial execution within the transaction.
func (r *TxRepository) InsertWorkUnit(ctx context.Context, wu *essmsgpb.WorkUnit) error {
	if wu == nil {
		return pgdb.WrapStatusError("work unit must not be nil", grpcerr.ErrInvalidArgument, nil)
	}
	if err := requireDB(r.tx); err != nil {
		return err
	}
	row, err := postgresdata.WorkUnitProtoToRow(wu)
	if err != nil {
		return err
	}
	if _, err := r.tx.Exec(ctx, lockExperimentSQL.String(), row.Xid); err != nil {
		return pgdb.TranslateError(err, "experiment")
	}
	_, err = r.tx.Exec(ctx, insertWorkUnitSQL.String(), row.GetInsertArgs())
	return pgdb.TranslateError(err, entityName)
}

// FetchWorkUnit retrieves a work unit by its unique ID within the transaction.
func (r *TxRepository) FetchWorkUnit(ctx context.Context, xid, wid int64) (*essmsgpb.WorkUnit, error) {
	ctx = logging.WithWorkUnitName(ctx, validator.ConstructWorkUnitName(xid, wid))
	return queryWorkUnit(ctx, r.tx, lockWorkUnitSQL, xid, wid)
}

// FetchUpdatingWorkUnits retrieves a batch of work units for update by their unique IDs within the transaction.
func (r *TxRepository) FetchUpdatingWorkUnits(ctx context.Context, xid int64, wids []int64) (map[int64]*essmsgpb.WorkUnit, error) {
	if err := requireDB(r.tx); err != nil {
		return nil, err
	}
	if len(wids) == 0 {
		return map[int64]*essmsgpb.WorkUnit{}, nil
	}
	rawRows, err := pgdb.QueryRows[postgresdata.WorkUnitRow](ctx, r.tx, selectUpdatingWorkUnitsSQL.String(), entityName, xid, wids)
	if err != nil {
		return nil, err
	}
	workUnits := make(map[int64]*essmsgpb.WorkUnit, len(rawRows))
	for _, row := range rawRows {
		wu, err := postgresdata.UpdatingWorkUnitRowToProto(row)
		if err != nil {
			return nil, err
		}
		workUnits[row.WorkUnitID] = wu
	}
	return workUnits, nil
}

// SaveWorkUnitExecution saves a work unit execution within the transaction.
func (r *TxRepository) SaveWorkUnitExecution(ctx context.Context, execution *essmsgpb.WorkUnitExecution) error {
	if execution == nil {
		return pgdb.WrapStatusError("execution must not be nil", grpcerr.ErrInvalidArgument, nil)
	}
	if err := requireDB(r.tx); err != nil {
		return err
	}
	row, err := postgresdata.WorkUnitExecutionProtoToRow(execution)
	if err != nil {
		return pgdb.WrapStatusError("failed to convert work unit execution", grpcerr.ErrInternal, err)
	}
	_, err = r.tx.Exec(ctx, insertWorkUnitExecutionSQL.String(), row.GetInsertArgs())
	return pgdb.TranslateError(err, "work unit execution")
}

// UpdateCurrentWorkUnitExecution updates the current work unit execution within the transaction.
func (r *TxRepository) UpdateCurrentWorkUnitExecution(ctx context.Context, xid, wid, eid int64, execution *essmsgpb.WorkUnitExecution) error {
	if execution == nil {
		return pgdb.WrapStatusError("execution must not be nil", grpcerr.ErrInvalidArgument, nil)
	}
	if err := requireDB(r.tx); err != nil {
		return err
	}
	row, err := postgresdata.CurrentWorkUnitExecutionProtoToRow(xid, wid, eid, execution)
	if err != nil {
		return pgdb.WrapStatusError("failed to convert current work unit execution", grpcerr.ErrInternal, err)
	}
	tag, err := r.tx.Exec(ctx, updateCurrentExecutionSQL.String(), row.GetCurrentExecutionUpdateArgs())
	if err != nil {
		return pgdb.TranslateError(err, entityName)
	}
	if tag.RowsAffected() == 0 {
		return pgdb.TranslateError(pgx.ErrNoRows, entityName)
	}
	return nil
}

// ListWorkUnitMessagesBySeverity retrieves work unit status messages matching a given severity level within the transaction.
func (r *TxRepository) ListWorkUnitMessagesBySeverity(ctx context.Context, xid int64, severity essmsgpb.StructuredMessage_Severity) ([]*essmsgpb.StructuredMessage, error) {
	rawRows, err := pgdb.QueryRows[postgresdata.WorkUnitRow](ctx, r.tx, listWorkUnitMessagesSQL.String(), entityName, xid, int64(severity))
	if err != nil {
		return nil, err
	}
	var messages []*essmsgpb.StructuredMessage
	for _, row := range rawRows {
		statusProto, err := postgresdata.UnmarshalProto[essmsgpb.WorkUnitStatus](row.WorkUnitStatus)
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal WorkUnitStatus: %w", err)
		}
		if msg := statusProto.GetSystemMessage(); msg != nil && msg.GetSeverity() == severity {
			messages = append(messages, msg)
		}
		if msg := statusProto.GetUserMessage(); msg != nil && msg.GetSeverity() == severity {
			messages = append(messages, msg)
		}
	}
	return messages, nil
}

func requireDB(db pgdb.DBTX) error {
	if db == nil {
		return pgdb.WrapStatusError("database handle must not be nil", grpcerr.ErrFailedPrecondition, nil)
	}
	return nil
}

func queryWorkUnit(ctx context.Context, db pgdb.DBTX, query safesql.TrustedSQLString, xid, wid int64) (*essmsgpb.WorkUnit, error) {
	row, err := pgdb.QueryRow[postgresdata.WorkUnitRow](ctx, db, query.String(), entityName, xid, wid)
	if err != nil {
		return nil, err
	}
	return postgresdata.WorkUnitRowToProto(row)
}

func queryCurrentExecution(ctx context.Context, db pgdb.DBTX, xid, wid int64) (*essmsgpb.WorkUnitExecution, error) {
	row, err := pgdb.QueryRow[postgresdata.WorkUnitRow](ctx, db, selectCurrentExecutionSQL.String(), entityName, xid, wid)
	if err != nil {
		return nil, err
	}
	return postgresdata.WorkUnitCurrentExecutionRowToProto(row)
}

func queryHistoricalExecutions(ctx context.Context, db pgdb.DBTX, xid, wid int64) ([]*essmsgpb.WorkUnitExecution, error) {
	rawRows, err := pgdb.QueryRows[postgresdata.WorkUnitExecutionRow](ctx, db, listWorkUnitExecutionsSQL.String(), entityName, xid, wid)
	if err != nil {
		return nil, err
	}
	res := make([]*essmsgpb.WorkUnitExecution, 0, len(rawRows))
	for _, row := range rawRows {
		wue, err := postgresdata.WorkUnitExecutionRowToProto(row)
		if err != nil {
			return nil, err
		}
		res = append(res, wue)
	}
	return res, nil
}

func validateWorkUnitUpdate(u *storage.WorkUnitUpdate) error {
	if u == nil || u.WorkUnit == nil || len(u.UpdateMask.GetPaths()) == 0 {
		return pgdb.WrapStatusError("update, work unit, and update mask must not be empty", grpcerr.ErrInvalidArgument, nil)
	}
	return nil
}

func applyWorkUnitUpdate(ctx context.Context, tx pgx.Tx, update *storage.WorkUnitUpdate) (storage.UpdatedWorkUnits, bool, error) {
	originalWU, err := queryWorkUnit(ctx, tx, lockWorkUnitSQL, update.XID, update.WID)
	if err != nil {
		return storage.UpdatedWorkUnits{}, false, err
	}
	updatedWU, err := data.MergeWorkUnits(originalWU, update.WorkUnit, update.UpdateMask)
	if err != nil {
		return storage.UpdatedWorkUnits{}, false, fmt.Errorf("failed to merge WorkUnit: %w", err)
	}
	res := storage.UpdatedWorkUnits{Original: originalWU, Updated: updatedWU}
	if proto.Equal(originalWU, updatedWU) {
		return res, false, nil
	}
	updatedWU.UpdateTime = timestamppb.New(time.Now().UTC().Truncate(time.Microsecond))
	row, err := postgresdata.WorkUnitProtoToUpdateRow(updatedWU)
	if err != nil {
		return storage.UpdatedWorkUnits{}, false, err
	}
	if _, err := tx.Exec(ctx, updateWorkUnitSQL.String(), row.GetUpdateArgs()); err != nil {
		return storage.UpdatedWorkUnits{}, false, pgdb.TranslateError(err, entityName)
	}
	return res, true, nil
}

func logWorkUnitUpdate(ctx context.Context, mutated bool, original, updated *essmsgpb.WorkUnit) {
	if !mutated {
		logging.Info(ctx, "WorkUnit not updated; no changes detected", "updatedWorkUnit", updated)
		return
	}
	logging.Info(ctx, fmt.Sprintf("WorkUnit updated successfully: %s", diff.Diff(prototext.Format(original), prototext.Format(updated))), "updatedWorkUnit", updated)
}
