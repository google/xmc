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

// Package pausedtask provides the Cloud Spanner implementation for persisting paused actuation tasks.
package pausedtask

import (
	"context"
	"errors"
	"fmt"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/google/uuid"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/data"
	spannerdata "xmanager_cloud/internal/storage/spanner/data"
)

// TableName is the Spanner table name buffering tasks intercepted during a scheduling pause.
const TableName = "PausedTasks"

// UnprocessedIndexName is the null-filtered index ordering pending tasks for replay.
const UnprocessedIndexName = "PausedTasksUnprocessed"

// Spanner column names for the PausedTasks table.
const (
	ColXID              = "Xid"
	ColBitReversedXID   = "BitReversedXid"
	ColWorkUnitID       = "WorkUnitId"
	ColTaskID           = "TaskId"
	ColTarget           = "Target"
	ColActionProto      = "ActionProto"
	ColOperationName    = "OperationName"
	ColTraceID          = "TraceId"
	ColPriority         = "Priority"
	ColCreateTime       = "CreateTime"
	ColProcessedTime    = "ProcessedTime"
	ColResolutionStatus = "ResolutionStatus"
	ColResumptionRunID  = "ResumptionRunId"
	ColIsPending        = "IsPending"
)

var (
	// ErrInvalidArgument indicates a caller provided an invalid argument (such as a nil task or action).
	ErrInvalidArgument = errors.New("invalid argument")
)

var _ storage.PausedTaskRepository = (*Repository)(nil)
var _ storage.PausedTaskTxRepository = (*TxRepository)(nil)

var insertColumns = []string{
	ColXID, ColWorkUnitID, ColTaskID, ColTarget, ColActionProto,
	ColOperationName, ColTraceID, ColPriority, ColCreateTime,
}

// resolutionColumns starts with the primary key. BitReversedXid is generated but must be
// supplied as the key for Update mutations.
var resolutionColumns = []string{
	ColBitReversedXID, ColWorkUnitID, ColTaskID,
	ColProcessedTime, ColResolutionStatus, ColResumptionRunID,
}

// Aliases for PausedTasks columns whose names collide with WorkUnits columns in the join.
const (
	aliasTaskPriority   = "TaskPriority"
	aliasTaskCreateTime = "TaskCreateTime"
)

// pendingTasksSQL joins each pending task with its interleaved parent WorkUnit. WorkUnits
// columns keep their names so spannerdata.WorkUnitRowToProto can decode the joined row.
const pendingTasksSQL = `SELECT
  p.TaskId, p.Target, p.ActionProto, p.OperationName, p.TraceId,
  p.Priority AS TaskPriority, p.CreateTime AS TaskCreateTime,
  w.Xid, w.WorkUnitId, w.WorkUnitType, w.Priority, w.Urls, w.Tags, w.Notes, w.Executable,
  w.ExecutionCreateTime, w.ExecutionUpdateTime, w.ExecutionCompleteTime, w.ExecutionId,
  w.ExecutionMetadata, w.WorkUnitStatus, w.CreateTime, w.UpdateTime
FROM PausedTasks@{FORCE_INDEX=PausedTasksUnprocessed} AS p
JOIN WorkUnits AS w ON w.BitReversedXid = p.BitReversedXid AND w.WorkUnitId = p.WorkUnitId
WHERE p.IsPending IS NOT NULL
ORDER BY p.Priority DESC, p.CreateTime ASC`

// Repository persists paused tasks in Cloud Spanner.
type Repository struct {
	client *spanner.Client
}

// TxRepository buffers paused task writes within a bound read-write transaction.
type TxRepository struct {
	tx *spanner.ReadWriteTransaction
}

// NewRepository creates a Spanner-backed paused task Repository.
func NewRepository(client *spanner.Client) (*Repository, error) {
	if client == nil {
		return nil, fmt.Errorf("spanner client must not be nil")
	}
	return &Repository{client: client}, nil
}

// WithTx returns a repository bound to the given read-write transaction.
func (r *Repository) WithTx(tx *spanner.ReadWriteTransaction) *TxRepository {
	return &TxRepository{tx: tx}
}

// Create buffers the insert of a new paused task in the transaction. An empty TaskID is
// populated with a generated UUID and a zero CreateTime defaults to the current time; both stay
// stable if the transaction is retried. Constraint violations (e.g. a duplicate task) surface when
// the transaction commits.
func (r *TxRepository) Create(ctx context.Context, task *storage.PausedTask) error {
	if err := prepareNewTask(task); err != nil {
		return err
	}
	return r.tx.BufferWrite([]*spanner.Mutation{insertMutation(task)})
}

// MarkProcessed buffers the soft-delete of a batch of paused tasks in the transaction.
func (r *TxRepository) MarkProcessed(ctx context.Context, resolutions []storage.PausedTaskResolution) error {
	if len(resolutions) == 0 {
		return nil
	}
	return r.tx.BufferWrite(resolutionMutations(resolutions, time.Now().UTC()))
}

// ListPending retrieves pending paused tasks ordered by Priority DESC, CreateTime ASC, each
// populated with the current state of its parent WorkUnit via a single interleaved join.
// A limit <= 0 returns all pending tasks.
func (r *Repository) ListPending(ctx context.Context, limit int) ([]*storage.PausedTask, error) {
	var tasks []*storage.PausedTask
	err := r.client.Single().Query(ctx, listPendingStatement(limit)).Do(func(row *spanner.Row) error {
		task, err := rowToPausedTask(row)
		if err != nil {
			return err
		}
		tasks = append(tasks, task)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to query pending paused tasks: %w", err)
	}
	return tasks, nil
}

// prepareNewTask validates a new task and populates default TaskID and CreateTime values.
func prepareNewTask(task *storage.PausedTask) error {
	if task == nil {
		return fmt.Errorf("%w: task cannot be nil", ErrInvalidArgument)
	}
	if task.Action == nil {
		return fmt.Errorf("%w: task action cannot be nil", ErrInvalidArgument)
	}
	if task.TaskID == "" {
		task.TaskID = uuid.NewString()
	}
	if task.CreateTime.IsZero() {
		task.CreateTime = time.Now().UTC()
	}
	return nil
}

func insertMutation(task *storage.PausedTask) *spanner.Mutation {
	return spanner.Insert(TableName, insertColumns, []any{
		task.XID,
		task.WID,
		task.TaskID,
		task.Target,
		task.Action,
		task.OperationName,
		task.TraceID,
		task.Priority,
		task.CreateTime,
	})
}

func resolutionMutations(resolutions []storage.PausedTaskResolution, now time.Time) []*spanner.Mutation {
	mutations := make([]*spanner.Mutation, len(resolutions))
	for i, res := range resolutions {
		mutations[i] = resolutionMutation(res, now)
	}
	return mutations
}

func resolutionMutation(res storage.PausedTaskResolution, now time.Time) *spanner.Mutation {
	processedTime := res.ProcessedTime
	if processedTime.IsZero() {
		processedTime = now
	}
	return spanner.Update(TableName, resolutionColumns, []any{
		data.ReverseBits(res.XID),
		res.WID,
		res.TaskID,
		processedTime,
		spanner.NullString{StringVal: string(res.ResolutionStatus), Valid: res.ResolutionStatus != ""},
		spanner.NullString{StringVal: res.ResumptionRunID, Valid: res.ResumptionRunID != ""},
	})
}

func listPendingStatement(limit int) spanner.Statement {
	stmt := spanner.Statement{SQL: pendingTasksSQL}
	if limit > 0 {
		stmt.SQL += "\nLIMIT @limit"
		stmt.Params = map[string]any{"limit": int64(limit)}
	}
	return stmt
}

// rowToPausedTask decodes a pendingTasksSQL row into a storage.PausedTask with its parent WorkUnit.
func rowToPausedTask(row *spanner.Row) (*storage.PausedTask, error) {
	task := &storage.PausedTask{}
	fields := []struct {
		column string
		dst    any
	}{
		{ColXID, &task.XID},
		{ColWorkUnitID, &task.WID},
		{ColTaskID, &task.TaskID},
		{ColTarget, &task.Target},
		{ColActionProto, &task.Action},
		{ColOperationName, &task.OperationName},
		{ColTraceID, &task.TraceID},
		{aliasTaskPriority, &task.Priority},
		{aliasTaskCreateTime, &task.CreateTime},
	}
	for _, f := range fields {
		if err := row.ColumnByName(f.column, f.dst); err != nil {
			return nil, fmt.Errorf("failed to read paused task column %s: %w", f.column, err)
		}
	}

	workUnit, err := spannerdata.WorkUnitRowToProto(row)
	if err != nil {
		return nil, fmt.Errorf("failed to decode parent work unit: %w", err)
	}
	task.WorkUnit = workUnit
	return task, nil
}
