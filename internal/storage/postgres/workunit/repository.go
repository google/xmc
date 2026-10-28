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
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/safesql"
	"xmanager_cloud/internal/storage"
	postgresdata "xmanager_cloud/internal/storage/postgres/data"
	"xmanager_cloud/internal/storage/postgres/pgdb"
	"xmanager_cloud/internal/validator"
)

const entityName = "work unit"

var (
	selectWorkUnitSQL = safesql.New(`SELECT Xid, WorkUnitId, WorkUnitType, Priority, Urls, Tags, Notes, Executable, ExecutionCreateTime, ExecutionUpdateTime, ExecutionCompleteTime, ExecutionId, ExecutionMetadata, WorkUnitStatus, CreateTime, UpdateTime FROM WorkUnits WHERE Xid = $1 AND WorkUnitId = $2`)
	lockWorkUnitSQL   = safesql.Concat(selectWorkUnitSQL, safesql.New(" FOR NO KEY UPDATE"))

	selectCurrentExecutionSQL = safesql.New(`SELECT Xid, WorkUnitId, ExecutionId, ExecutionCreateTime, ExecutionUpdateTime, ExecutionCompleteTime, ExecutionMetadata, WorkUnitStatus FROM WorkUnits WHERE Xid = $1 AND WorkUnitId = $2`)
	listWorkUnitExecutionsSQL = safesql.New(`SELECT Xid, WorkUnitId, ExecutionId, ExecutionMetadata, WorkUnitStatus, CreateTime, UpdateTime, CompleteTime FROM WorkUnitExecutions WHERE Xid = $1 AND WorkUnitId = $2 ORDER BY ExecutionId DESC`)

	insertWorkUnitExecutionSQL = safesql.New(`INSERT INTO WorkUnitExecutions (Xid, WorkUnitId, ExecutionId, ExecutionMetadata, WorkUnitStatus, CreateTime, UpdateTime, CompleteTime)
VALUES (@xid, @workunitid, @executionid, @executionmetadata, @workunitstatus, @createtime, @updatetime, @completetime)`)

	updateCurrentExecutionSQL = safesql.New(`UPDATE WorkUnits SET ExecutionId = @executionid, ExecutionCreateTime = @executioncreatetime, ExecutionUpdateTime = @executionupdatetime, ExecutionCompleteTime = @executioncompletetime, ExecutionMetadata = @executionmetadata, WorkUnitStatus = @workunitstatus, ExecutionState = @executionstate, ExecutionStatusMessageSeverity = @executionstatusmessageseverity, PositionInQueue = @positioninqueue, UpdateTime = @updatetime WHERE Xid = @xid AND WorkUnitId = @workunitid`)
)

var (
	_ storage.WorkUnitStore        = (*Repository)(nil)
	_ storage.WorkUnitTxRepository = (*TxRepository)(nil)
)

// Repository manages persistence for work unit entities in PostgreSQL.
type Repository struct {
	pool *pgxpool.Pool
	db   pgdb.DBTX
}

// TxRepository manages transactional persistence for work unit entities in PostgreSQL.
type TxRepository struct {
	tx pgdb.DBTX
}

// NewRepository creates a new PostgreSQL WorkUnit repository instance bound to pool.
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool, db: pgdb.FromPool(pool)}
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
	return storage.UpdatedWorkUnits{}, status.Error(codes.Unimplemented, "postgres UpdateWorkUnit is not implemented")
}

// FetchCurrentWorkUnitExecution retrieves the current work unit execution.
func (r *Repository) FetchCurrentWorkUnitExecution(ctx context.Context, xid int64, wid int64) (*essmsgpb.WorkUnitExecution, error) {
	ctx = logging.WithWorkUnitName(ctx, validator.ConstructWorkUnitName(xid, wid))
	return queryCurrentExecution(ctx, r.db, xid, wid)
}

// ListWorkUnitExecutions retrieves the historical work unit executions for a given work unit.
func (r *Repository) ListWorkUnitExecutions(ctx context.Context, xid int64, wid int64) ([]*essmsgpb.WorkUnitExecution, error) {
	if r == nil || r.pool == nil {
		return nil, pgdb.WrapStatusError("postgres pool must not be nil", grpcerr.ErrFailedPrecondition, nil)
	}
	ctx = logging.WithWorkUnitName(ctx, validator.ConstructWorkUnitName(xid, wid))
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, pgdb.TranslateError(err, entityName)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	current, err := queryCurrentExecution(ctx, tx, xid, wid)
	if err != nil {
		return nil, err
	}
	historical, err := queryHistoricalExecutions(ctx, tx, xid, wid)
	if err != nil {
		return nil, err
	}
	return append([]*essmsgpb.WorkUnitExecution{current}, historical...), nil
}

// ListWorkUnits retrieves a list of work units matching the given filter.
func (r *Repository) ListWorkUnits(ctx context.Context, parent string, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration) ([]*essmsgpb.WorkUnit, string, error) {
	return nil, "", status.Error(codes.Unimplemented, "postgres ListWorkUnits is not implemented")
}

// FetchActiveWorkUnitsByQueues retrieves active work units associated with the requested queue names.
func (r *Repository) FetchActiveWorkUnitsByQueues(ctx context.Context, queueNames []string, pageSize int32, pageToken string) (*storage.ActiveWorkUnitsResult, error) {
	return nil, status.Error(codes.Unimplemented, "postgres FetchActiveWorkUnitsByQueues is not implemented")
}

// FetchNextWorkUnitID retrieves the next available work unit ID for an experiment within the transaction.
func (r *TxRepository) FetchNextWorkUnitID(ctx context.Context, xid int64) (int64, error) {
	return 0, status.Error(codes.Unimplemented, "postgres TxRepository.FetchNextWorkUnitID is not implemented")
}

// InsertWorkUnit inserts a new work unit and its initial execution within the transaction.
func (r *TxRepository) InsertWorkUnit(ctx context.Context, wu *essmsgpb.WorkUnit) error {
	return status.Error(codes.Unimplemented, "postgres TxRepository.InsertWorkUnit is not implemented")
}

// FetchWorkUnit retrieves a work unit by its unique ID within the transaction.
func (r *TxRepository) FetchWorkUnit(ctx context.Context, xid, wid int64) (*essmsgpb.WorkUnit, error) {
	ctx = logging.WithWorkUnitName(ctx, validator.ConstructWorkUnitName(xid, wid))
	return queryWorkUnit(ctx, r.tx, lockWorkUnitSQL, xid, wid)
}

// FetchUpdatingWorkUnits retrieves a batch of work units for update by their unique IDs within the transaction.
func (r *TxRepository) FetchUpdatingWorkUnits(ctx context.Context, xid int64, wids []int64) (map[int64]*essmsgpb.WorkUnit, error) {
	return nil, status.Error(codes.Unimplemented, "postgres TxRepository.FetchUpdatingWorkUnits is not implemented")
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
	return nil, status.Error(codes.Unimplemented, "postgres TxRepository.ListWorkUnitMessagesBySeverity is not implemented")
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
