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

// Package workunit contains the concrete PostgreSQL repository implementation for work units and
// work unit executions. It is the PostgreSQL counterpart of internal/storage/spanner/workunit and
// mirrors its row-to-proto conversions and error semantics.
//
// WorkUnits columns that Cloud Spanner derives with generated-column expressions (QueueName,
// ExecutionState, ExecutionStatusMessageSeverity and PositionInQueue) are computed here on every
// write; see deriveExecutionColumns. TIMESTAMPTZ columns have microsecond precision, so column
// timestamps are stored truncated to microseconds, whereas Spanner keeps nanoseconds.
package workunit

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/safesql"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/postgres/pgdb"
	"xmanager_cloud/internal/validator"
)

// Entity names used in translated storage errors.
const (
	workUnitEntity          = "work unit"
	workUnitExecutionEntity = "work unit execution"
	experimentEntity        = "experiment"
)

var (
	// errNilPool is returned by Repository methods when the repository was created without a pool.
	errNilPool = pgdb.WrapStatusError("postgres pool must not be nil", grpcerr.ErrFailedPrecondition, nil)
	// errNilTx is returned by TxRepository methods when the repository is not bound to a transaction.
	errNilTx = pgdb.WrapStatusError("transaction must not be nil", grpcerr.ErrFailedPrecondition, nil)
)

// insertWorkUnitExecutionSQL archives one execution in the WorkUnitExecutions table.
var insertWorkUnitExecutionSQL = safesql.New(`INSERT INTO WorkUnitExecutions (Xid, WorkUnitId, ExecutionId, ExecutionMetadata, WorkUnitStatus, CreateTime, UpdateTime, CompleteTime)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`)

// updateCurrentExecutionSQL replaces the current execution of a work unit, including the columns
// derived from the execution status, and sets the work unit's UpdateTime to the execution's.
var updateCurrentExecutionSQL = safesql.New(`UPDATE WorkUnits SET
  ExecutionId = $3,
  ExecutionCreateTime = $4,
  ExecutionUpdateTime = $5,
  ExecutionCompleteTime = $6,
  ExecutionMetadata = $7,
  WorkUnitStatus = $8,
  ExecutionState = $9,
  ExecutionStatusMessageSeverity = $10,
  PositionInQueue = $11,
  UpdateTime = $12
WHERE Xid = $1 AND WorkUnitId = $2`)

var (
	_ storage.WorkUnitStore        = (*Repository)(nil)
	_ storage.WorkUnitTxRepository = (*TxRepository)(nil)
)

// Repository manages persistence for work unit entities in PostgreSQL.
type Repository struct {
	// db runs the single-statement reads of the repository; NewRepository binds it to pool.
	db pgdb.DBTX
	// pool starts the transactions of self-contained read-modify-write operations (UpdateWorkUnit)
	// and of multi-statement snapshot reads (ListWorkUnitExecutions, FetchActiveWorkUnitsByQueues).
	pool *pgxpool.Pool
}

// TxRepository manages transactional persistence for work unit entities in PostgreSQL.
type TxRepository struct {
	tx pgdb.DBTX
}

// NewRepository creates a new PostgreSQL WorkUnit repository instance bound to pool.
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{db: pgdb.FromPool(pool), pool: pool}
}

// WithTx returns a wrapper bound to the read-write transaction tx.
func (r *Repository) WithTx(tx pgdb.DBTX) *TxRepository {
	return &TxRepository{tx: tx}
}

// requireDB returns errNilPool when the repository has no database to query.
func (r *Repository) requireDB() error {
	if r == nil || r.db == nil {
		return errNilPool
	}
	return nil
}

// requireTx returns errNilTx when the repository is not bound to a transaction.
func (r *TxRepository) requireTx() error {
	if r == nil || r.tx == nil {
		return errNilTx
	}
	return nil
}

// workUnitEntityName names one work unit in translated storage errors.
func workUnitEntityName(xid, wid int64) string {
	return fmt.Sprintf("%s %s", workUnitEntity, validator.ConstructWorkUnitName(xid, wid))
}

// SaveWorkUnitExecution archives execution in the WorkUnitExecutions table within the transaction.
// Archiving an execution ID that already exists for the work unit fails with AlreadyExists.
func (r *TxRepository) SaveWorkUnitExecution(ctx context.Context, execution *essmsgpb.WorkUnitExecution) error {
	if execution == nil {
		return fmt.Errorf("execution must not be nil: %w", grpcerr.ErrInvalidArgument)
	}
	if err := r.requireTx(); err != nil {
		return err
	}
	xid, wid, eid, err := validator.ValidateWorkUnitExecutionName(execution.GetName())
	if err != nil {
		return fmt.Errorf("failed to create mutation for work unit execution: failed to validate WorkUnitExecution name %q: %v: %w", execution.GetName(), err, grpcerr.ErrInternal)
	}
	protos, err := marshalExecutionProtos(execution)
	if err != nil {
		return fmt.Errorf("failed to create mutation for work unit execution: %v: %w", err, grpcerr.ErrInternal)
	}
	_, err = r.tx.Exec(ctx, insertWorkUnitExecutionSQL.String(),
		xid, wid, eid, protos.metadata, protos.status,
		execution.GetCreateTime().AsTime(), execution.GetUpdateTime().AsTime(), nullableTimestamp(execution.GetCompleteTime()))
	return pgdb.TranslateError(err, workUnitExecutionEntity)
}

// UpdateCurrentWorkUnitExecution replaces the current execution of work unit (xid, wid) with
// execution under execution ID eid within the transaction, recomputing the columns derived from
// the execution status. It fails with NotFound when the work unit does not exist.
func (r *TxRepository) UpdateCurrentWorkUnitExecution(ctx context.Context, xid, wid, eid int64, execution *essmsgpb.WorkUnitExecution) error {
	if execution == nil {
		return fmt.Errorf("execution must not be nil: %w", grpcerr.ErrInvalidArgument)
	}
	if err := r.requireTx(); err != nil {
		return err
	}
	ctx = logging.WithWorkUnitName(ctx, validator.ConstructWorkUnitName(xid, wid))
	protos, err := marshalExecutionProtos(execution)
	if err != nil {
		return fmt.Errorf("failed to create mutation for current work unit execution: %v: %w", err, grpcerr.ErrInternal)
	}
	derived := deriveExecutionColumns(execution.GetStatus())
	tag, err := r.tx.Exec(ctx, updateCurrentExecutionSQL.String(),
		xid, wid, eid,
		execution.GetCreateTime().AsTime(), execution.GetUpdateTime().AsTime(), nullableTimestamp(execution.GetCompleteTime()),
		protos.metadata, protos.status,
		derived.state, derived.severity, derived.position,
		execution.GetUpdateTime().AsTime())
	if err != nil {
		logging.Error(ctx, "Failed to update current work unit execution", "error", err)
		return pgdb.TranslateError(err, workUnitEntityName(xid, wid))
	}
	if tag.RowsAffected() == 0 {
		return pgdb.WrapStatusError(fmt.Sprintf("%s not found", workUnitEntityName(xid, wid)), grpcerr.ErrNotFound, nil)
	}
	return nil
}
