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

package pausedtask

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	actuatortaskpb "xmanager_cloud/api/protos/task_queue"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/postgres/pgdb"
	postgrestest "xmanager_cloud/internal/storage/postgres/testing"
)

const (
	testXID int64 = 100
	testWID int64 = 200
)

var baseTime = time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)

func newTask(taskID string, priority int64) *storage.PausedTask {
	return &storage.PausedTask{
		XID:        testXID,
		WID:        testWID,
		TaskID:     taskID,
		Priority:   priority,
		CreateTime: baseTime,
		Action:     &actuatortaskpb.Action{Action: &actuatortaskpb.Action_Create{Create: &actuatortaskpb.CreateAction{}}},
	}
}

func resolution(taskID string, resolutionStatus storage.ResolutionStatus, runID string, processedTime time.Time) storage.PausedTaskResolution {
	return storage.PausedTaskResolution{
		XID: testXID, WID: testWID, TaskID: taskID, ResolutionStatus: resolutionStatus, ResumptionRunID: runID, ProcessedTime: processedTime,
	}
}

func pendingIDs(tasks []*storage.PausedTask) []string {
	ids := make([]string, len(tasks))
	for i, task := range tasks {
		ids[i] = task.TaskID
	}
	return ids
}

// newTestRepository returns a Repository backed by a fresh ESS test database that contains the
// parent WorkUnit referenced by newTask, together with the database's pool.
func newTestRepository(t *testing.T) (*Repository, *pgxpool.Pool) {
	t.Helper()
	_, essPool := postgrestest.Setup(t.Context(), t)
	postgrestest.SeedWorkUnit(t.Context(), t, essPool, testXID, testWID)
	return NewRepository(essPool), essPool
}

// runInTx runs fn with a TxRepository bound to a new read-write transaction on pool.
func runInTx(ctx context.Context, repo *Repository, pool *pgxpool.Pool, fn func(ctx context.Context, txRepo *TxRepository) error) error {
	return pgdb.RunInTx(ctx, pool, func(txCtx context.Context, tx pgx.Tx) error {
		return fn(txCtx, repo.WithTx(tx))
	})
}

func createInTx(ctx context.Context, repo *Repository, pool *pgxpool.Pool, task *storage.PausedTask) error {
	return runInTx(ctx, repo, pool, func(ctx context.Context, txRepo *TxRepository) error {
		return txRepo.Create(ctx, task)
	})
}

func markProcessedInTx(ctx context.Context, repo *Repository, pool *pgxpool.Pool, resolutions ...storage.PausedTaskResolution) error {
	return runInTx(ctx, repo, pool, func(ctx context.Context, txRepo *TxRepository) error {
		return txRepo.MarkProcessed(ctx, resolutions)
	})
}

func mustCreate(ctx context.Context, t *testing.T, repo *Repository, pool *pgxpool.Pool, tasks ...*storage.PausedTask) {
	t.Helper()
	for _, task := range tasks {
		if err := createInTx(ctx, repo, pool, task); err != nil {
			t.Fatalf("Create(%q) unexpected error: %v", task.TaskID, err)
		}
	}
}

func TestNewRepository_BindsPoolAndTx(t *testing.T) {
	pool := &pgxpool.Pool{}
	repo := NewRepository(pool)
	if repo.db != pool {
		t.Errorf("NewRepository(pool).db = %v, want %p", repo.db, pool)
	}

	// Any DBTX implementation can stand in for the transaction handle.
	var tx pgdb.DBTX = &pgxpool.Pool{}
	txRepo := repo.WithTx(tx)
	if txRepo.tx != tx {
		t.Errorf("WithTx(tx).tx = %v, want %v", txRepo.tx, tx)
	}
}

// Invalid input is rejected before any statement runs, so a nil transaction is never used.
func TestTxRepository_RejectsInvalidInputWithoutQuerying(t *testing.T) {
	ctx := context.Background()
	txRepo := NewRepository(nil).WithTx(nil)
	valid := func() *storage.PausedTask {
		return &storage.PausedTask{TaskID: "t1", CreateTime: time.Now(), Action: &actuatortaskpb.Action{}}
	}
	withoutAction, withoutID, withoutCreateTime := valid(), valid(), valid()
	withoutAction.Action = nil
	withoutID.TaskID = ""
	withoutCreateTime.CreateTime = time.Time{}

	tests := []struct {
		name string
		call func() error
	}{
		{name: "Create_nil_task", call: func() error { return txRepo.Create(ctx, nil) }},
		{name: "Create_nil_action", call: func() error { return txRepo.Create(ctx, withoutAction) }},
		{name: "Create_empty_task_id", call: func() error { return txRepo.Create(ctx, withoutID) }},
		{name: "Create_zero_create_time", call: func() error { return txRepo.Create(ctx, withoutCreateTime) }},
		{name: "MarkProcessed_zero_processed_time", call: func() error {
			return txRepo.MarkProcessed(ctx, []storage.PausedTaskResolution{{XID: 1, WID: 2, TaskID: "t1"}})
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.call(); !errors.Is(err, grpcerr.ErrInvalidArgument) {
				t.Errorf("error = %v, want %v", err, grpcerr.ErrInvalidArgument)
			}
		})
	}
}

func TestTxRepository_MarkProcessedEmptyIsNoOp(t *testing.T) {
	if err := NewRepository(nil).WithTx(nil).MarkProcessed(context.Background(), nil); err != nil {
		t.Errorf("MarkProcessed(nil) error = %v, want nil", err)
	}
}

func TestTxRepository_CreateErrors(t *testing.T) {
	ctx := t.Context()
	repo, pool := newTestRepository(t)
	mustCreate(ctx, t, repo, pool, newTask("task-1", 100))
	missingParent := newTask("task-2", 100)
	missingParent.WID = 999

	tests := []struct {
		name     string
		task     *storage.PausedTask
		wantCode codes.Code
	}{
		{name: "duplicate", task: newTask("task-1", 100), wantCode: codes.AlreadyExists},
		{name: "missing_parent_work_unit", task: missingParent, wantCode: codes.NotFound},
		{name: "invalid_task", task: nil, wantCode: codes.InvalidArgument},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := createInTx(ctx, repo, pool, tt.task); status.Code(err) != tt.wantCode {
				t.Errorf("Create() error = %v (code %v), want code %v", err, status.Code(err), tt.wantCode)
			}
		})
	}
}

func TestTxRepository_Create(t *testing.T) {
	errAbort := errors.New("abort")
	tests := []struct {
		name    string
		fnErr   error
		wantErr error
		wantIDs []string
	}{
		{name: "committed", wantIDs: []string{"task-1"}},
		{name: "rolled_back", fnErr: errAbort, wantErr: errAbort},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			repo, pool := newTestRepository(t)
			err := runInTx(ctx, repo, pool, func(ctx context.Context, txRepo *TxRepository) error {
				if err := txRepo.Create(ctx, newTask("task-1", 100)); err != nil {
					return err
				}
				return tt.fnErr
			})
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("RunInTx() error = %v, want %v", err, tt.wantErr)
			}
			assertPendingIDs(ctx, t, repo, tt.wantIDs)
		})
	}
}

func TestRepository_ListPending_RoundTrip(t *testing.T) {
	ctx := t.Context()
	repo, pool := newTestRepository(t)
	setParentWorkUnit(ctx, t, pool, "paused notes", essmsgpb.WorkUnitStatus_SCHEDULING_PAUSED)
	want := newTask("task-1", 100)
	want.Target = "experiments/100/workUnits/200/executions/1"
	want.OperationName = "experiments/100/workUnits/200/operations/op-1"
	want.TraceID = "trace-1"
	want.Action = &actuatortaskpb.Action{
		Action: &actuatortaskpb.Action_Restart{Restart: &actuatortaskpb.RestartAction{Author: proto.String("author@example.com")}},
	}
	mustCreate(ctx, t, repo, pool, want)

	got, err := repo.ListPending(ctx, 0)
	if err != nil || len(got) != 1 {
		t.Fatalf("ListPending() = (%v, %v), want 1 task and nil error", got, err)
	}
	if diff := cmp.Diff(want, got[0], protocmp.Transform(), cmpopts.IgnoreFields(storage.PausedTask{}, "WorkUnit")); diff != "" {
		t.Errorf("ListPending() mismatch (-want +got):\n%s", diff)
	}

	// Timestamps on the parent WorkUnit are non-deterministic, so only identity and state are checked.
	wu := got[0].WorkUnit
	wantWorkUnit := []any{"experiments/100/workUnits/200", "experiments/100/workUnits/200/executions/1", "paused notes", "SCHEDULING_PAUSED"}
	gotWorkUnit := []any{wu.GetName(), wu.GetCurrentWorkUnitExecution().GetName(), wu.GetNotes(), wu.GetCurrentWorkUnitExecution().GetStatus().GetState().String()}
	if diff := cmp.Diff(wantWorkUnit, gotWorkUnit); diff != "" {
		t.Errorf("ListPending() WorkUnit mismatch (-want +got):\n%s", diff)
	}
}

func TestRepository_ListPending_Ordering(t *testing.T) {
	ctx := t.Context()
	repo, pool := newTestRepository(t)
	highNew := newTask("high-new", 200)
	highNew.CreateTime = baseTime.Add(time.Minute)
	mustCreate(ctx, t, repo, pool, newTask("low", 50), newTask("high-old", 200), highNew, newTask("top", 300), newTask("done", 400))
	if err := markProcessedInTx(ctx, repo, pool, resolution("done", "", "", baseTime.Add(time.Hour))); err != nil {
		t.Fatalf("MarkProcessed() unexpected error: %v", err)
	}

	tests := []struct {
		name    string
		limit   int
		wantIDs []string
	}{
		{name: "unlimited", limit: 0, wantIDs: []string{"top", "high-old", "high-new", "low"}},
		{name: "negative_is_unlimited", limit: -1, wantIDs: []string{"top", "high-old", "high-new", "low"}},
		{name: "limited", limit: 2, wantIDs: []string{"top", "high-old"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := repo.ListPending(ctx, tt.limit)
			if err != nil {
				t.Fatalf("ListPending() unexpected error: %v", err)
			}
			if diff := cmp.Diff(tt.wantIDs, pendingIDs(got)); diff != "" {
				t.Errorf("ListPending() order mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestRepository_ListPending_Empty(t *testing.T) {
	repo, _ := newTestRepository(t)
	got, err := repo.ListPending(t.Context(), 0)
	if err != nil {
		t.Fatalf("ListPending() unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("ListPending() = %v, want nil", got)
	}
}

func TestTxRepository_MarkProcessed_PersistsResolution(t *testing.T) {
	tests := []struct {
		name string
		res  storage.PausedTaskResolution
	}{
		{name: "with_resolution_fields", res: resolution("t1", storage.ResolutionStatusReplayed, "run-1", baseTime.Add(time.Hour))},
		{name: "empty_resolution_fields", res: resolution("t1", "", "", baseTime.Add(time.Hour))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			repo, pool := newTestRepository(t)
			mustCreate(ctx, t, repo, pool, newTask("t1", 10), newTask("t2", 20))
			if err := markProcessedInTx(ctx, repo, pool, tt.res); err != nil {
				t.Fatalf("MarkProcessed() unexpected error: %v", err)
			}
			if diff := cmp.Diff(tt.res, readResolution(ctx, t, pool, "t1")); diff != "" {
				t.Errorf("persisted resolution mismatch (-want +got):\n%s", diff)
			}
			assertPendingIDs(ctx, t, repo, []string{"t2"})
		})
	}
}

func TestTxRepository_MarkProcessed_LastWriteWins(t *testing.T) {
	first := resolution("t1", storage.ResolutionStatusReplayed, "run-1", baseTime.Add(time.Hour))
	second := resolution("t1", storage.ResolutionStatusSkipped, "run-2", baseTime.Add(2*time.Hour))
	tests := []struct {
		name    string
		batches [][]storage.PausedTaskResolution
	}{
		{name: "separate_transactions", batches: [][]storage.PausedTaskResolution{{first}, {second}}},
		{name: "same_batch", batches: [][]storage.PausedTaskResolution{{first, second}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			repo, pool := newTestRepository(t)
			mustCreate(ctx, t, repo, pool, newTask("t1", 10))
			for _, batch := range tt.batches {
				if err := markProcessedInTx(ctx, repo, pool, batch...); err != nil {
					t.Fatalf("MarkProcessed(%v) unexpected error: %v", batch, err)
				}
			}
			if diff := cmp.Diff(second, readResolution(ctx, t, pool, "t1")); diff != "" {
				t.Errorf("persisted resolution mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestTxRepository_MarkProcessed_UnknownTaskIsAtomic(t *testing.T) {
	ctx := t.Context()
	repo, pool := newTestRepository(t)
	mustCreate(ctx, t, repo, pool, newTask("t1", 10))
	processedTime := baseTime.Add(time.Hour)

	err := markProcessedInTx(ctx, repo, pool,
		resolution("t1", storage.ResolutionStatusSkipped, "", processedTime),
		resolution("missing", storage.ResolutionStatusSkipped, "", processedTime))
	if status.Code(err) != codes.NotFound || !errors.Is(err, storage.ErrPausedTaskNotFound) {
		t.Errorf("MarkProcessed() error = %v (code %v), want %v (code %v)", err, status.Code(err), storage.ErrPausedTaskNotFound, codes.NotFound)
	}
	assertPendingIDs(ctx, t, repo, []string{"t1"})
}

func TestTxRepository_MarkProcessed(t *testing.T) {
	errAbort := errors.New("abort")
	resolutions := []storage.PausedTaskResolution{resolution("t1", storage.ResolutionStatusReplayed, "run-1", baseTime.Add(time.Hour))}

	tests := []struct {
		name        string
		resolutions []storage.PausedTaskResolution
		fnErr       error
		wantErr     error
		wantIDs     []string
	}{
		{name: "committed", resolutions: resolutions, wantIDs: []string{"t2"}},
		{name: "rolled_back", resolutions: resolutions, fnErr: errAbort, wantErr: errAbort, wantIDs: []string{"t2", "t1"}},
		{name: "empty", resolutions: nil, wantIDs: []string{"t2", "t1"}},
		{name: "zero_processed_time", resolutions: []storage.PausedTaskResolution{{XID: testXID, WID: testWID, TaskID: "t1"}}, wantErr: grpcerr.ErrInvalidArgument, wantIDs: []string{"t2", "t1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			repo, pool := newTestRepository(t)
			mustCreate(ctx, t, repo, pool, newTask("t1", 10), newTask("t2", 20))

			err := runInTx(ctx, repo, pool, func(ctx context.Context, txRepo *TxRepository) error {
				if err := txRepo.MarkProcessed(ctx, tt.resolutions); err != nil {
					return err
				}
				return tt.fnErr
			})
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("RunInTx() error = %v, want %v", err, tt.wantErr)
			}
			assertPendingIDs(ctx, t, repo, tt.wantIDs)
		})
	}
}

func assertPendingIDs(ctx context.Context, t *testing.T, repo *Repository, wantIDs []string) {
	t.Helper()
	got, err := repo.ListPending(ctx, 0)
	if err != nil {
		t.Fatalf("ListPending() unexpected error: %v", err)
	}
	if diff := cmp.Diff(wantIDs, pendingIDs(got), cmpopts.EquateEmpty()); diff != "" {
		t.Errorf("ListPending() IDs mismatch (-want +got):\n%s", diff)
	}
}

// setParentWorkUnit overwrites the notes and execution state of the seeded parent WorkUnit.
func setParentWorkUnit(ctx context.Context, t *testing.T, pool *pgxpool.Pool, notes string, state essmsgpb.WorkUnitStatus_WorkUnitState) {
	t.Helper()
	workUnitStatus, err := proto.Marshal(&essmsgpb.WorkUnitStatus{State: state})
	if err != nil {
		t.Fatalf("proto.Marshal() unexpected error: %v", err)
	}
	const query = "UPDATE WorkUnits SET Notes = $1, WorkUnitStatus = $2 WHERE Xid = $3 AND WorkUnitId = $4"
	if _, err := pool.Exec(ctx, query, notes, workUnitStatus, testXID, testWID); err != nil {
		t.Fatalf("Exec(%q) unexpected error: %v", query, err)
	}
}

// readResolution reads the soft-delete columns directly, since the Repository interface only
// exposes pending tasks.
func readResolution(ctx context.Context, t *testing.T, pool *pgxpool.Pool, taskID string) storage.PausedTaskResolution {
	t.Helper()
	const query = `SELECT Xid, WorkUnitId, TaskId, ResolutionStatus, ResumptionRunId, ProcessedTime
FROM PausedTasks WHERE Xid = $1 AND WorkUnitId = $2 AND TaskId = $3`
	var res storage.PausedTaskResolution
	var resolutionStatus, resumptionRunID pgtype.Text
	row := pool.QueryRow(ctx, query, testXID, testWID, taskID)
	if err := row.Scan(&res.XID, &res.WID, &res.TaskID, &resolutionStatus, &resumptionRunID, &res.ProcessedTime); err != nil {
		t.Fatalf("QueryRow(%q) unexpected error: %v", taskID, err)
	}
	res.ResolutionStatus = storage.ResolutionStatus(resolutionStatus.String)
	res.ResumptionRunID = resumptionRunID.String
	res.ProcessedTime = res.ProcessedTime.UTC()
	return res
}
