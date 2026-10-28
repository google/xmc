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

// Package pausedtask provides the PostgreSQL implementation for persisting paused actuation tasks.
package pausedtask

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/proto"
	actuatortaskpb "xmanager_cloud/api/protos/task_queue"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/safesql"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/postgres/pgdb"
	"xmanager_cloud/internal/validator"
)

// entityName names paused tasks in translated storage errors.
const entityName = "paused task"

// pendingTasksSQL joins each pending task with its parent WorkUnit. The WorkUnits columns are
// listed in the order workUnitRow scans them. The ORDER BY matches the PausedTasksUnprocessed index
// and the contract of storage.PausedTaskRepository.ListPending; the primary-key tie-breaker makes
// the order of tasks with equal priority and creation time deterministic.
var pendingTasksSQL = safesql.New(`SELECT
  p.TaskId, p.Target, p.ActionProto, p.OperationName, p.TraceId, p.Priority, p.CreateTime,
  w.Xid, w.WorkUnitId, w.WorkUnitType, w.Priority, w.Urls, w.Tags, w.Notes, w.Executable,
  w.ExecutionCreateTime, w.ExecutionUpdateTime, w.ExecutionCompleteTime, w.ExecutionId,
  w.ExecutionMetadata, w.WorkUnitStatus, w.CreateTime, w.UpdateTime
FROM PausedTasks AS p
JOIN WorkUnits AS w ON w.Xid = p.Xid AND w.WorkUnitId = p.WorkUnitId
WHERE p.IsPending IS NOT NULL
ORDER BY p.Priority DESC, p.CreateTime ASC, p.Xid ASC, p.WorkUnitId ASC, p.TaskId ASC`)

// pendingTasksLimitSQL is pendingTasksSQL capped at $1 rows.
var pendingTasksLimitSQL = safesql.Concat(pendingTasksSQL, safesql.New("\nLIMIT $1"))

// insertTaskSQL inserts a pending task. ProcessedTime and the resolution columns stay NULL, so the
// generated IsPending column is TRUE.
var insertTaskSQL = safesql.New(`INSERT INTO PausedTasks (Xid, WorkUnitId, TaskId, Target, ActionProto, OperationName, TraceId, Priority, CreateTime)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`)

// markProcessedSQL soft-deletes one task by setting its resolution columns, which clears the
// generated IsPending column.
var markProcessedSQL = safesql.New(`UPDATE PausedTasks
SET ProcessedTime = $4, ResolutionStatus = $5, ResumptionRunId = $6
WHERE Xid = $1 AND WorkUnitId = $2 AND TaskId = $3`)

var (
	_ storage.PausedTaskRepository   = (*Repository)(nil)
	_ storage.PausedTaskTxRepository = (*TxRepository)(nil)
)

// Repository persists paused tasks in PostgreSQL.
type Repository struct {
	db pgdb.DBTX
}

// TxRepository persists paused task writes within a bound read-write transaction.
type TxRepository struct {
	tx pgdb.DBTX
}

// NewRepository creates a PostgreSQL-backed paused task Repository bound to pool.
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{db: pgdb.FromPool(pool)}
}

// WithTx returns a wrapper bound to the read-write transaction tx.
func (r *Repository) WithTx(tx pgdb.DBTX) *TxRepository {
	return &TxRepository{tx: tx}
}

// ListPending retrieves pending paused tasks ordered by Priority DESC, CreateTime ASC, each
// populated with the current state of its parent WorkUnit via a single join. A limit <= 0 returns
// all pending tasks. It returns a nil slice when no task is pending.
func (r *Repository) ListPending(ctx context.Context, limit int) ([]*storage.PausedTask, error) {
	rows, err := r.queryPending(ctx, limit)
	if err != nil {
		return nil, pgdb.TranslateError(fmt.Errorf("failed to query pending paused tasks: %w", err), entityName)
	}
	defer rows.Close()

	var tasks []*storage.PausedTask
	for rows.Next() {
		task, err := scanPausedTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	if err := rows.Err(); err != nil {
		return nil, pgdb.TranslateError(fmt.Errorf("failed to query pending paused tasks: %w", err), entityName)
	}
	return tasks, nil
}

// queryPending runs pendingTasksSQL, capped at limit rows when limit > 0.
func (r *Repository) queryPending(ctx context.Context, limit int) (pgx.Rows, error) {
	if limit > 0 {
		return r.db.Query(ctx, pendingTasksLimitSQL.String(), int64(limit))
	}
	return r.db.Query(ctx, pendingTasksSQL.String())
}

// Create inserts a new paused task in the transaction. TaskID, Action, and CreateTime must be
// provided by the caller. A duplicate (XID, WID, TaskID) key fails with AlreadyExists
// (storage.ErrPausedTaskAlreadyExists) and a missing parent WorkUnit with NotFound; either error
// aborts the surrounding transaction.
func (r *TxRepository) Create(ctx context.Context, task *storage.PausedTask) error {
	if err := validateTask(task); err != nil {
		return err
	}
	action, err := marshalProto(task.Action)
	if err != nil {
		return fmt.Errorf("failed to marshal paused task action: %w", err)
	}
	_, err = r.tx.Exec(ctx, insertTaskSQL.String(),
		task.XID, task.WID, task.TaskID, task.Target, action, task.OperationName, task.TraceID, task.Priority, task.CreateTime)
	return translateInsertError(err, task)
}

// MarkProcessed soft-deletes a batch of paused tasks in the transaction. Each resolution must have
// a non-zero ProcessedTime. Calling MarkProcessed on an already-processed task overwrites its
// resolution fields (last-write-wins). A missing task fails with NotFound
// (storage.ErrPausedTaskNotFound), which aborts the surrounding transaction so that none of the
// batch is applied.
func (r *TxRepository) MarkProcessed(ctx context.Context, resolutions []storage.PausedTaskResolution) error {
	if len(resolutions) == 0 {
		return nil
	}
	for _, res := range resolutions {
		if res.ProcessedTime.IsZero() {
			return fmt.Errorf("%w: processed time cannot be zero", grpcerr.ErrInvalidArgument)
		}
	}
	// Each UPDATE locks its row until the transaction ends. Updating in primary-key order makes
	// concurrent batches acquire those locks in the same order, so they cannot deadlock. The sort
	// is stable so that duplicate keys within one batch keep their last-write-wins order.
	sorted := slices.Clone(resolutions)
	slices.SortStableFunc(sorted, compareResolutionKeys)
	for _, res := range sorted {
		tag, err := r.tx.Exec(ctx, markProcessedSQL.String(),
			res.XID, res.WID, res.TaskID, res.ProcessedTime, nullText(string(res.ResolutionStatus)), nullText(res.ResumptionRunID))
		if err != nil {
			return pgdb.TranslateError(err, entityName)
		}
		if tag.RowsAffected() == 0 {
			return pgdb.WrapStatusError(
				fmt.Sprintf("paused task %s of work unit %s not found", res.TaskID, validator.ConstructWorkUnitName(res.XID, res.WID)),
				storage.ErrPausedTaskNotFound, nil)
		}
	}
	return nil
}

func validateTask(task *storage.PausedTask) error {
	if task == nil {
		return fmt.Errorf("%w: task cannot be nil", grpcerr.ErrInvalidArgument)
	}
	if task.Action == nil {
		return fmt.Errorf("%w: task action cannot be nil", grpcerr.ErrInvalidArgument)
	}
	if task.TaskID == "" {
		return fmt.Errorf("%w: task ID cannot be empty", grpcerr.ErrInvalidArgument)
	}
	if task.CreateTime.IsZero() {
		return fmt.Errorf("%w: create time cannot be zero", grpcerr.ErrInvalidArgument)
	}
	return nil
}

// translateInsertError maps the constraint violations of insertTaskSQL to the status codes the
// Cloud Spanner implementation produces: a duplicate primary key is AlreadyExists and a missing
// parent WorkUnit, which Spanner reports for its interleaved table, is NotFound.
func translateInsertError(err error, task *storage.PausedTask) error {
	switch {
	case err == nil:
		return nil
	case pgdb.IsUniqueViolation(err):
		return pgdb.WrapStatusError(
			fmt.Sprintf("paused task %s of work unit %s already exists: %v", task.TaskID, validator.ConstructWorkUnitName(task.XID, task.WID), err),
			storage.ErrPausedTaskAlreadyExists, err)
	case pgdb.IsForeignKeyViolation(err):
		return pgdb.WrapStatusError(
			fmt.Sprintf("parent work unit %s of paused task %s not found: %v", validator.ConstructWorkUnitName(task.XID, task.WID), task.TaskID, err),
			grpcerr.ErrNotFound, err)
	default:
		return pgdb.TranslateError(err, entityName)
	}
}

// compareResolutionKeys orders resolutions by the PausedTasks primary key (Xid, WorkUnitId, TaskId).
func compareResolutionKeys(a, b storage.PausedTaskResolution) int {
	return cmp.Or(cmp.Compare(a.XID, b.XID), cmp.Compare(a.WID, b.WID), cmp.Compare(a.TaskID, b.TaskID))
}

// scanPausedTask decodes a pendingTasksSQL row into a storage.PausedTask with its parent WorkUnit.
func scanPausedTask(row pgx.Row) (*storage.PausedTask, error) {
	var (
		task   storage.PausedTask
		action []byte
		wu     workUnitRow
	)
	targets := append([]any{&task.TaskID, &task.Target, &action, &task.OperationName, &task.TraceID, &task.Priority, &task.CreateTime},
		wu.scanTargets()...)
	if err := row.Scan(targets...); err != nil {
		return nil, pgdb.TranslateError(fmt.Errorf("failed to read paused task row: %w", err), entityName)
	}
	task.XID = wu.xid
	task.WID = wu.wid
	task.CreateTime = task.CreateTime.UTC()
	task.Action = &actuatortaskpb.Action{}
	if err := proto.Unmarshal(action, task.Action); err != nil {
		return nil, fmt.Errorf("failed to unmarshal paused task action: %w", err)
	}
	workUnit, err := wu.toProto()
	if err != nil {
		return nil, fmt.Errorf("failed to decode parent work unit: %w", err)
	}
	task.WorkUnit = workUnit
	return &task, nil
}

// marshalProto serializes m for a NOT NULL BYTEA column: a message without set fields encodes to
// a zero-length value rather than the nil slice that pgx would send as SQL NULL.
func marshalProto(m proto.Message) ([]byte, error) {
	b, err := proto.Marshal(m)
	if err != nil {
		return nil, err
	}
	if b == nil {
		b = []byte{}
	}
	return b, nil
}

// nullText maps an empty string to SQL NULL, matching the nullable resolution columns.
func nullText(s string) pgtype.Text {
	return pgtype.Text{String: s, Valid: s != ""}
}
