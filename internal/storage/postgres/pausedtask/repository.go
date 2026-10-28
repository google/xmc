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
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/safesql"
	"xmanager_cloud/internal/storage"
	postgresdata "xmanager_cloud/internal/storage/postgres/data"
	"xmanager_cloud/internal/storage/postgres/pgdb"
)

// entityName names paused tasks in translated storage errors.
const entityName = "paused task"

// pendingTasksSQL joins each pending task with its parent WorkUnit, ordered for replay. WorkUnits
// columns that collide with PausedTasks columns are aliased with a WorkUnit prefix.
const pendingTasksSQL = `SELECT
  p.Xid, p.WorkUnitId, p.TaskId, p.Target, p.ActionProto, p.OperationName, p.TraceId, p.Priority, p.CreateTime,
  w.WorkUnitType, w.Priority AS WorkUnitPriority, w.Urls, w.Tags, w.Notes, w.Executable,
  w.ExecutionCreateTime, w.ExecutionUpdateTime, w.ExecutionCompleteTime, w.ExecutionId,
  w.ExecutionMetadata, w.WorkUnitStatus, w.CreateTime AS WorkUnitCreateTime, w.UpdateTime AS WorkUnitUpdateTime
FROM PausedTasks AS p
JOIN WorkUnits AS w ON w.Xid = p.Xid AND w.WorkUnitId = p.WorkUnitId
WHERE p.IsPending IS NOT NULL
ORDER BY p.Priority DESC, p.CreateTime ASC`

var (
	// listPendingSQL returns all pending tasks.
	listPendingSQL = safesql.New(pendingTasksSQL)
	// listPendingLimitSQL returns at most $1 pending tasks.
	listPendingLimitSQL = safesql.New(pendingTasksSQL + "\nLIMIT $1")
)

// insertTaskSQL inserts a new pending paused task.
var insertTaskSQL = safesql.New(`INSERT INTO PausedTasks (Xid, WorkUnitId, TaskId, Target, ActionProto, OperationName, TraceId, Priority, CreateTime)
VALUES (@xid, @workunitid, @taskid, @target, @actionproto, @operationname, @traceid, @priority, @createtime)`)

// resolveTaskSQL soft-deletes a paused task by recording its resolution, overwriting any previous one.
var resolveTaskSQL = safesql.New(`UPDATE PausedTasks
SET ProcessedTime = @processedtime, ResolutionStatus = @resolutionstatus, ResumptionRunId = @resumptionrunid
WHERE Xid = @xid AND WorkUnitId = @workunitid AND TaskId = @taskid`)

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
// populated with the current state of its parent WorkUnit via a single join.
// A limit <= 0 returns all pending tasks.
func (r *Repository) ListPending(ctx context.Context, limit int) ([]*storage.PausedTask, error) {
	sql, args := listPendingSQL, []any{}
	if limit > 0 {
		sql, args = listPendingLimitSQL, []any{limit}
	}
	pending, err := pgdb.QueryRows[postgresdata.PendingPausedTaskRow](ctx, r.db, sql.String(), entityName, args...)
	if err != nil {
		return nil, err
	}
	var tasks []*storage.PausedTask
	for _, row := range pending {
		task, err := postgresdata.PendingPausedTaskRowToTask(row)
		if err != nil {
			return nil, fmt.Errorf("failed to decode pending paused task %q: %w", row.TaskID, err)
		}
		tasks = append(tasks, task)
	}
	return tasks, nil
}

// Create inserts a new paused task within the transaction. TaskID, Action, and CreateTime must be
// provided by the caller. A missing parent WorkUnit is reported as NotFound, matching Spanner.
func (r *TxRepository) Create(ctx context.Context, task *storage.PausedTask) error {
	if err := validateTask(task); err != nil {
		return err
	}
	row, err := postgresdata.PausedTaskToRow(task)
	if err != nil {
		return pgdb.WrapStatusError(err.Error(), grpcerr.ErrInvalidArgument, err)
	}
	_, err = r.tx.Exec(ctx, insertTaskSQL.String(), row.GetInsertArgs())
	if pgdb.IsForeignKeyViolation(err) {
		return pgdb.WrapStatusError(fmt.Sprintf("parent work unit of %s not found: %v", entityName, err), grpcerr.ErrNotFound, err)
	}
	return pgdb.TranslateError(err, entityName)
}

// MarkProcessed soft-deletes a batch of paused tasks within the transaction. Calling MarkProcessed
// on an already-processed task overwrites its resolution fields (last-write-wins). If any task does
// not exist, it returns a NotFound error and the caller's transaction must be rolled back.
func (r *TxRepository) MarkProcessed(ctx context.Context, resolutions []storage.PausedTaskResolution) error {
	for _, res := range resolutions {
		if res.ProcessedTime.IsZero() {
			return fmt.Errorf("%w: processed time cannot be zero", grpcerr.ErrInvalidArgument)
		}
	}
	// Update rows in primary-key order so concurrent batches acquire row locks in the same order.
	// The stable sort keeps duplicate keys in caller order, so the last resolution wins.
	for _, res := range slices.SortedStableFunc(slices.Values(resolutions), compareResolutionKeys) {
		if err := r.resolve(ctx, res); err != nil {
			return err
		}
	}
	return nil
}

// resolve records res on its paused task, returning NotFound when the task does not exist.
func (r *TxRepository) resolve(ctx context.Context, res storage.PausedTaskResolution) error {
	tag, err := r.tx.Exec(ctx, resolveTaskSQL.String(), resolutionArgs(res))
	if err != nil {
		return pgdb.TranslateError(err, entityName)
	}
	if tag.RowsAffected() == 0 {
		msg := fmt.Sprintf("%s %d/%d/%q not found", entityName, res.XID, res.WID, res.TaskID)
		return pgdb.WrapStatusError(msg, grpcerr.ErrNotFound, storage.ErrPausedTaskNotFound)
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

func compareResolutionKeys(a, b storage.PausedTaskResolution) int {
	return cmp.Or(cmp.Compare(a.XID, b.XID), cmp.Compare(a.WID, b.WID), cmp.Compare(a.TaskID, b.TaskID))
}

// resolutionArgs binds res to resolveTaskSQL, storing empty resolution fields as NULL.
func resolutionArgs(res storage.PausedTaskResolution) pgx.StrictNamedArgs {
	return pgx.StrictNamedArgs{
		"xid": res.XID, "workunitid": res.WID, "taskid": res.TaskID, "processedtime": res.ProcessedTime,
		"resolutionstatus": nullText(string(res.ResolutionStatus)), "resumptionrunid": nullText(res.ResumptionRunID),
	}
}

// nullText maps an empty string to SQL NULL, matching the nullable resolution columns.
func nullText(s string) pgtype.Text {
	return pgtype.Text{String: s, Valid: s != ""}
}
