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

// External test package, mirroring the Spanner repository tests that exercise the repository
// through its public API against a real database.
package pausedtask_test

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
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
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	pb "xmanager_cloud/api/protos/experiment"
	actuatortaskpb "xmanager_cloud/api/protos/task_queue"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/postgres/pausedtask"
	"xmanager_cloud/internal/storage/postgres/pgdb"
	postgrestest "xmanager_cloud/internal/storage/postgres/testing"
)

const (
	testXID int64 = 100
	testWID int64 = 200
)

var baseTime = time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)

// testRepository is a repository backed by a fresh ESS test database that contains the parent
// Experiment and WorkUnit referenced by newTask.
type testRepository struct {
	repo *pausedtask.Repository
	pool *pgxpool.Pool
	// workUnit is the proto that ListPending is expected to decode for the parent WorkUnit.
	workUnit *pb.WorkUnit
}

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

func pendingIDs(tasks []*storage.PausedTask) []string {
	ids := make([]string, len(tasks))
	for i, task := range tasks {
		ids[i] = task.TaskID
	}
	return ids
}

// newTestRepository returns a repository backed by a fresh database holding Experiment testXID and
// WorkUnit testWID in state SCHEDULING_PAUSED.
func newTestRepository(t *testing.T) *testRepository {
	t.Helper()
	ctx := t.Context()
	_, essPool := postgrestest.Setup(ctx, t)
	insertExperiment(ctx, t, essPool, testXID)
	wu := insertWorkUnit(ctx, t, essPool, testXID, testWID, pb.WorkUnitStatus_SCHEDULING_PAUSED)
	return &testRepository{repo: pausedtask.NewRepository(essPool), pool: essPool, workUnit: wu}
}

func mustMarshal(t *testing.T, m proto.Message) []byte {
	t.Helper()
	b, err := proto.Marshal(m)
	if err != nil {
		t.Fatalf("proto.Marshal(%T) unexpected error: %v", m, err)
	}
	if b == nil {
		return []byte{}
	}
	return b
}

// insertExperiment inserts a minimal Experiments row, the grandparent of paused tasks.
func insertExperiment(ctx context.Context, t *testing.T, pool *pgxpool.Pool, xid int64) {
	t.Helper()
	const sql = `INSERT INTO Experiments (Xid, Title, Creator, Priority, Urls, Tags, Notes, Resources, Status, ActiveState, LaunchState, CreateTime, UpdateTime)
VALUES ($1, 'test-title', 'test@example.com', 1, '{}', '{}', '', '{}', $2, $3, 0, $4, $4)`
	statusProto := &pb.ExperimentStatus{ActiveState: pb.ExperimentStatus_ACTIVE}
	if _, err := pool.Exec(ctx, sql, xid, mustMarshal(t, statusProto), int64(pb.ExperimentStatus_ACTIVE), baseTime); err != nil {
		t.Fatalf("insert Experiment %d unexpected error: %v", xid, err)
	}
}

// insertWorkUnit inserts a WorkUnits row in the given state and returns the WorkUnit proto that
// the repository is expected to decode from it.
func insertWorkUnit(ctx context.Context, t *testing.T, pool *pgxpool.Pool, xid, wid int64, state pb.WorkUnitStatus_WorkUnitState) *pb.WorkUnit {
	t.Helper()
	const sql = `INSERT INTO WorkUnits (Xid, WorkUnitId, WorkUnitType, Priority, Urls, Tags, Notes, Executable,
  ExecutionCreateTime, ExecutionUpdateTime, ExecutionCompleteTime, ExecutionId, ExecutionMetadata, WorkUnitStatus,
  QueueName, ExecutionState, CreateTime, UpdateTime)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9, NULL, $10, $11, $12, $13, $14, $9, $9)`
	const eid int64 = 1
	executable := &pb.Executable{SchedulingConstraints: &pb.SchedulingConstraints{QueueName: "test-queue"}}
	wuStatus := &pb.WorkUnitStatus{State: state}
	metadata := &pb.WorkUnitExecutionMetadata{}
	urls := []*pb.Url{{Url: "http://test.com"}}
	tags := []string{"tag1", "tag2"}

	rawURLs := make([][]byte, len(urls))
	for i, u := range urls {
		rawURLs[i] = mustMarshal(t, u)
	}
	_, err := pool.Exec(ctx, sql,
		xid, wid, int64(pb.WorkUnit_STANDARD), int64(pb.Priority_NORMAL), rawURLs, tags, "test-notes", mustMarshal(t, executable),
		baseTime, eid, mustMarshal(t, metadata), mustMarshal(t, wuStatus),
		executable.GetSchedulingConstraints().GetQueueName(), int64(state))
	if err != nil {
		t.Fatalf("insert WorkUnit %d/%d unexpected error: %v", xid, wid, err)
	}
	return &pb.WorkUnit{
		Name:         fmt.Sprintf("experiments/%d/workUnits/%d", xid, wid),
		WorkUnitType: pb.WorkUnit_STANDARD,
		Priority:     pb.Priority_NORMAL,
		Urls:         urls,
		Executable:   executable,
		Tags:         tags,
		Notes:        "test-notes",
		CurrentWorkUnitExecution: &pb.WorkUnitExecution{
			Name:       fmt.Sprintf("experiments/%d/workUnits/%d/executions/%d", xid, wid, eid),
			CreateTime: timestamppb.New(baseTime),
			UpdateTime: timestamppb.New(baseTime),
			Metadata:   metadata,
			Status:     wuStatus,
			State:      pb.WorkUnitExecution_ACTIVE,
		},
		CreateTime: timestamppb.New(baseTime),
		UpdateTime: timestamppb.New(baseTime),
	}
}

func (r *testRepository) createInTx(ctx context.Context, task *storage.PausedTask) error {
	return pgdb.RunInTx(ctx, r.pool, func(ctx context.Context, tx pgx.Tx) error {
		return r.repo.WithTx(tx).Create(ctx, task)
	})
}

func (r *testRepository) markProcessedInTx(ctx context.Context, resolutions []storage.PausedTaskResolution) error {
	return pgdb.RunInTx(ctx, r.pool, func(ctx context.Context, tx pgx.Tx) error {
		return r.repo.WithTx(tx).MarkProcessed(ctx, resolutions)
	})
}

func (r *testRepository) mustCreate(ctx context.Context, t *testing.T, tasks ...*storage.PausedTask) {
	t.Helper()
	for _, task := range tasks {
		if err := r.createInTx(ctx, task); err != nil {
			t.Fatalf("Create(%q) unexpected error: %v", task.TaskID, err)
		}
	}
}

func (r *testRepository) assertPendingIDs(ctx context.Context, t *testing.T, wantIDs []string) {
	t.Helper()
	got, err := r.repo.ListPending(ctx, 0)
	if err != nil {
		t.Fatalf("ListPending() unexpected error: %v", err)
	}
	if diff := cmp.Diff(wantIDs, pendingIDs(got), cmpopts.EquateEmpty()); diff != "" {
		t.Errorf("ListPending() IDs mismatch (-want +got):\n%s", diff)
	}
}

// readResolution reads the soft-delete columns directly, since the Repository interface only
// exposes pending tasks.
func (r *testRepository) readResolution(ctx context.Context, t *testing.T, taskID string) storage.PausedTaskResolution {
	t.Helper()
	const sql = `SELECT Xid, WorkUnitId, TaskId, ResolutionStatus, ResumptionRunId, ProcessedTime
FROM PausedTasks WHERE Xid = $1 AND WorkUnitId = $2 AND TaskId = $3`
	var (
		res                             storage.PausedTaskResolution
		resolutionStatus, resumptionRun pgtype.Text
		processedTime                   pgtype.Timestamptz
	)
	if err := r.pool.QueryRow(ctx, sql, testXID, testWID, taskID).Scan(&res.XID, &res.WID, &res.TaskID, &resolutionStatus, &resumptionRun, &processedTime); err != nil {
		t.Fatalf("read resolution of %q unexpected error: %v", taskID, err)
	}
	res.ResolutionStatus = storage.ResolutionStatus(resolutionStatus.String)
	res.ResumptionRunID = resumptionRun.String
	if processedTime.Valid {
		res.ProcessedTime = processedTime.Time.UTC()
	}
	return res
}

func TestTxRepository_CreateErrors(t *testing.T) {
	ctx := t.Context()
	r := newTestRepository(t)
	r.mustCreate(ctx, t, newTask("task-1", 100))

	noAction := newTask("task-2", 100)
	noAction.Action = nil
	zeroCreateTime := newTask("task-3", 100)
	zeroCreateTime.CreateTime = time.Time{}
	missingParent := newTask("task-4", 100)
	missingParent.WID = 999

	tests := []struct {
		name     string
		task     *storage.PausedTask
		wantErr  error
		wantCode codes.Code
	}{
		{name: "duplicate", task: newTask("task-1", 100), wantErr: storage.ErrPausedTaskAlreadyExists, wantCode: codes.AlreadyExists},
		{name: "missing_parent_work_unit", task: missingParent, wantErr: grpcerr.ErrNotFound, wantCode: codes.NotFound},
		{name: "nil_task", task: nil, wantErr: grpcerr.ErrInvalidArgument, wantCode: codes.InvalidArgument},
		{name: "nil_action", task: noAction, wantErr: grpcerr.ErrInvalidArgument, wantCode: codes.InvalidArgument},
		{name: "empty_task_id", task: newTask("", 100), wantErr: grpcerr.ErrInvalidArgument, wantCode: codes.InvalidArgument},
		{name: "zero_create_time", task: zeroCreateTime, wantErr: grpcerr.ErrInvalidArgument, wantCode: codes.InvalidArgument},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := r.createInTx(ctx, tt.task)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("Create() error = %v, want %v", err, tt.wantErr)
			}
			if status.Code(err) != tt.wantCode {
				t.Errorf("Create() error = %v (code %v), want code %v", err, status.Code(err), tt.wantCode)
			}
		})
	}
	// None of the failed transactions may have left a row behind.
	r.assertPendingIDs(ctx, t, []string{"task-1"})
}

func TestTxRepository_Create(t *testing.T) {
	errAbort := errors.New("abort")

	tests := []struct {
		name    string
		task    *storage.PausedTask
		fnErr   error
		wantErr error
		wantIDs []string
	}{
		{name: "committed", task: newTask("task-1", 100), wantIDs: []string{"task-1"}},
		{name: "rolled_back", task: newTask("task-1", 100), fnErr: errAbort, wantErr: errAbort},
		{name: "invalid_task", task: nil, wantErr: grpcerr.ErrInvalidArgument},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			r := newTestRepository(t)
			err := pgdb.RunInTx(ctx, r.pool, func(ctx context.Context, tx pgx.Tx) error {
				if err := r.repo.WithTx(tx).Create(ctx, tt.task); err != nil {
					return err
				}
				return tt.fnErr
			})
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("RunInTx() error = %v, want %v", err, tt.wantErr)
			}
			r.assertPendingIDs(ctx, t, tt.wantIDs)
		})
	}
}

// Concurrent transactions that each create a distinct task for the same work unit must all
// succeed: the insert takes no lock that would make them conflict.
func TestTxRepository_Create_ConcurrentDistinctTasks(t *testing.T) {
	const workers = 10
	ctx := t.Context()
	r := newTestRepository(t)

	errs := make([]error, workers)
	var wg sync.WaitGroup
	for i := range workers {
		wg.Go(func() {
			task := newTask(fmt.Sprintf("task-%02d", i), 100)
			task.CreateTime = baseTime.Add(time.Duration(i) * time.Second)
			errs[i] = r.createInTx(ctx, task)
		})
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}

	wantIDs := make([]string, workers)
	for i := range wantIDs {
		wantIDs[i] = fmt.Sprintf("task-%02d", i)
	}
	r.assertPendingIDs(ctx, t, wantIDs)
}

func TestRepository_ListPending_RoundTrip(t *testing.T) {
	ctx := t.Context()
	r := newTestRepository(t)
	want := newTask("task-1", 100)
	want.Target = "experiments/100/workUnits/200/executions/1"
	want.OperationName = "experiments/100/workUnits/200/operations/op-1"
	want.TraceID = "trace-1"
	want.Action = &actuatortaskpb.Action{
		Action: &actuatortaskpb.Action_Restart{
			Restart: &actuatortaskpb.RestartAction{Author: proto.String("author@example.com")},
		},
	}
	r.mustCreate(ctx, t, want)

	got, err := r.repo.ListPending(ctx, 0)
	if err != nil || len(got) != 1 {
		t.Fatalf("ListPending() = (%v, %v), want 1 task and nil error", got, err)
	}
	if diff := cmp.Diff(want, got[0], protocmp.Transform(), cmpopts.IgnoreFields(storage.PausedTask{}, "WorkUnit")); diff != "" {
		t.Errorf("ListPending() mismatch (-want +got):\n%s", diff)
	}
	if loc := got[0].CreateTime.Location(); loc != time.UTC {
		t.Errorf("ListPending() CreateTime location = %v, want UTC like the Spanner implementation", loc)
	}
	// The parent WorkUnit is decoded from the joined WorkUnits columns exactly like a Spanner row.
	if diff := cmp.Diff(r.workUnit, got[0].WorkUnit, protocmp.Transform()); diff != "" {
		t.Errorf("ListPending() WorkUnit mismatch (-want +got):\n%s", diff)
	}
}

func TestRepository_ListPending_Ordering(t *testing.T) {
	ctx := t.Context()
	r := newTestRepository(t)
	highNew := newTask("high-new", 200)
	highNew.CreateTime = baseTime.Add(time.Minute)
	r.mustCreate(ctx, t, newTask("low", 50), newTask("high-old", 200), highNew, newTask("top", 300), newTask("done", 400))
	if err := r.markProcessedInTx(ctx, []storage.PausedTaskResolution{{XID: testXID, WID: testWID, TaskID: "done", ProcessedTime: baseTime.Add(time.Hour)}}); err != nil {
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
		{name: "limit_above_count", limit: 10, wantIDs: []string{"top", "high-old", "high-new", "low"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := r.repo.ListPending(ctx, tt.limit)
			if err != nil {
				t.Fatalf("ListPending() unexpected error: %v", err)
			}
			if diff := cmp.Diff(tt.wantIDs, pendingIDs(got)); diff != "" {
				t.Errorf("ListPending() order mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// Tasks with equal priority and creation time are returned in a deterministic primary-key order.
func TestRepository_ListPending_TieBreaksByTaskID(t *testing.T) {
	ctx := t.Context()
	r := newTestRepository(t)
	r.mustCreate(ctx, t, newTask("c", 10), newTask("a", 10), newTask("b", 10))

	got, err := r.repo.ListPending(ctx, 0)
	if err != nil {
		t.Fatalf("ListPending() unexpected error: %v", err)
	}
	if diff := cmp.Diff([]string{"a", "b", "c"}, pendingIDs(got)); diff != "" {
		t.Errorf("ListPending() order mismatch (-want +got):\n%s", diff)
	}
}

// The resumption worker pages through pending tasks by listing a batch, marking it processed and
// listing again. Batches must drain the backlog in the global replay order, with no task skipped
// or repeated across batch boundaries.
func TestRepository_ListPending_LimitDrainsInOrder(t *testing.T) {
	const (
		tasks     = 7
		batchSize = 3
	)
	ctx := t.Context()
	r := newTestRepository(t)
	wantIDs := make([]string, 0, tasks)
	for i := range tasks {
		// Descending priorities and ascending creation times give a known replay order.
		task := newTask(fmt.Sprintf("task-%d", i), int64(tasks-i))
		task.CreateTime = baseTime.Add(time.Duration(i) * time.Minute)
		r.mustCreate(ctx, t, task)
		wantIDs = append(wantIDs, task.TaskID)
	}

	var gotIDs []string
	for batches := 0; ; batches++ {
		if batches > tasks {
			t.Fatalf("ListPending(%d) did not drain %d tasks within %d batches", batchSize, tasks, batches)
		}
		batch, err := r.repo.ListPending(ctx, batchSize)
		if err != nil {
			t.Fatalf("ListPending(%d) unexpected error: %v", batchSize, err)
		}
		if len(batch) == 0 {
			break
		}
		if len(batch) > batchSize {
			t.Fatalf("ListPending(%d) returned %d tasks, want at most %d", batchSize, len(batch), batchSize)
		}
		resolutions := make([]storage.PausedTaskResolution, len(batch))
		for i, task := range batch {
			gotIDs = append(gotIDs, task.TaskID)
			resolutions[i] = storage.PausedTaskResolution{XID: task.XID, WID: task.WID, TaskID: task.TaskID, ResolutionStatus: storage.ResolutionStatusReplayed, ProcessedTime: baseTime.Add(time.Hour)}
		}
		if err := r.markProcessedInTx(ctx, resolutions); err != nil {
			t.Fatalf("MarkProcessed() unexpected error: %v", err)
		}
	}
	if diff := cmp.Diff(wantIDs, gotIDs); diff != "" {
		t.Errorf("tasks drained across batches mismatch (-want +got):\n%s", diff)
	}
}

func TestRepository_ListPending_Empty(t *testing.T) {
	r := newTestRepository(t)

	got, err := r.repo.ListPending(t.Context(), 0)
	if err != nil {
		t.Fatalf("ListPending() unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("ListPending() = %v, want nil", got)
	}
}

func TestTxRepository_MarkProcessed_PersistsResolution(t *testing.T) {
	ctx := t.Context()
	r := newTestRepository(t)
	r.mustCreate(ctx, t, newTask("t1", 10), newTask("t2", 20))
	processedTime := baseTime.Add(time.Hour)

	err := r.markProcessedInTx(ctx, []storage.PausedTaskResolution{{
		XID:              testXID,
		WID:              testWID,
		TaskID:           "t1",
		ResolutionStatus: storage.ResolutionStatusReplayed,
		ResumptionRunID:  "run-1",
		ProcessedTime:    processedTime,
	}})
	if err != nil {
		t.Fatalf("MarkProcessed() unexpected error: %v", err)
	}

	got := r.readResolution(ctx, t, "t1")
	want := storage.PausedTaskResolution{XID: testXID, WID: testWID, TaskID: "t1", ResolutionStatus: storage.ResolutionStatusReplayed, ResumptionRunID: "run-1", ProcessedTime: processedTime}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("persisted resolution mismatch (-want +got):\n%s", diff)
	}
	r.assertPendingIDs(ctx, t, []string{"t2"})
}

// Empty resolution fields are stored as SQL NULL, like the Spanner implementation's NullString.
func TestTxRepository_MarkProcessed_EmptyFieldsAreNull(t *testing.T) {
	ctx := t.Context()
	r := newTestRepository(t)
	r.mustCreate(ctx, t, newTask("t1", 10))
	if err := r.markProcessedInTx(ctx, []storage.PausedTaskResolution{{XID: testXID, WID: testWID, TaskID: "t1", ProcessedTime: baseTime.Add(time.Hour)}}); err != nil {
		t.Fatalf("MarkProcessed() unexpected error: %v", err)
	}

	var statusNull, runNull bool
	const sql = "SELECT ResolutionStatus IS NULL, ResumptionRunId IS NULL FROM PausedTasks WHERE Xid = $1 AND WorkUnitId = $2 AND TaskId = $3"
	if err := r.pool.QueryRow(ctx, sql, testXID, testWID, "t1").Scan(&statusNull, &runNull); err != nil {
		t.Fatalf("QueryRow(%q) unexpected error: %v", sql, err)
	}
	if !statusNull || !runNull {
		t.Errorf("stored (ResolutionStatus IS NULL, ResumptionRunId IS NULL) = (%v, %v), want (true, true)", statusNull, runNull)
	}
	r.assertPendingIDs(ctx, t, nil)
}

func TestTxRepository_MarkProcessed_LastWriteWins(t *testing.T) {
	ctx := t.Context()
	r := newTestRepository(t)
	r.mustCreate(ctx, t, newTask("t1", 10))
	first := storage.PausedTaskResolution{XID: testXID, WID: testWID, TaskID: "t1", ResolutionStatus: storage.ResolutionStatusReplayed, ResumptionRunID: "run-1", ProcessedTime: baseTime.Add(time.Hour)}
	second := storage.PausedTaskResolution{XID: testXID, WID: testWID, TaskID: "t1", ResolutionStatus: storage.ResolutionStatusSkipped, ResumptionRunID: "run-2", ProcessedTime: baseTime.Add(2 * time.Hour)}

	for _, res := range []storage.PausedTaskResolution{first, second} {
		if err := r.markProcessedInTx(ctx, []storage.PausedTaskResolution{res}); err != nil {
			t.Fatalf("MarkProcessed(%q) unexpected error: %v", res.ResumptionRunID, err)
		}
	}

	if diff := cmp.Diff(second, r.readResolution(ctx, t, "t1")); diff != "" {
		t.Errorf("persisted resolution mismatch (-want +got):\n%s", diff)
	}
}

// Duplicate keys within one batch keep their input order, so the later resolution wins.
func TestTxRepository_MarkProcessed_DuplicateKeysInBatchLastWins(t *testing.T) {
	ctx := t.Context()
	r := newTestRepository(t)
	r.mustCreate(ctx, t, newTask("t1", 10))
	first := storage.PausedTaskResolution{XID: testXID, WID: testWID, TaskID: "t1", ResolutionStatus: storage.ResolutionStatusReplayed, ResumptionRunID: "run-1", ProcessedTime: baseTime.Add(time.Hour)}
	second := storage.PausedTaskResolution{XID: testXID, WID: testWID, TaskID: "t1", ResolutionStatus: storage.ResolutionStatusSkipped, ResumptionRunID: "run-2", ProcessedTime: baseTime.Add(2 * time.Hour)}

	if err := r.markProcessedInTx(ctx, []storage.PausedTaskResolution{first, second}); err != nil {
		t.Fatalf("MarkProcessed() unexpected error: %v", err)
	}
	if diff := cmp.Diff(second, r.readResolution(ctx, t, "t1")); diff != "" {
		t.Errorf("persisted resolution mismatch (-want +got):\n%s", diff)
	}
}

func TestTxRepository_MarkProcessed_UnknownTaskIsAtomic(t *testing.T) {
	ctx := t.Context()
	r := newTestRepository(t)
	r.mustCreate(ctx, t, newTask("t1", 10))
	processedTime := baseTime.Add(time.Hour)

	err := r.markProcessedInTx(ctx, []storage.PausedTaskResolution{
		{XID: testXID, WID: testWID, TaskID: "t1", ResolutionStatus: storage.ResolutionStatusSkipped, ProcessedTime: processedTime},
		{XID: testXID, WID: testWID, TaskID: "missing", ResolutionStatus: storage.ResolutionStatusSkipped, ProcessedTime: processedTime},
	})
	if status.Code(err) != codes.NotFound || !errors.Is(err, storage.ErrPausedTaskNotFound) {
		t.Errorf("MarkProcessed() error = %v (code %v), want %v (code %v)", err, status.Code(err), storage.ErrPausedTaskNotFound, codes.NotFound)
	}
	r.assertPendingIDs(ctx, t, []string{"t1"})
}

func TestTxRepository_MarkProcessed(t *testing.T) {
	errAbort := errors.New("abort")
	processedTime := baseTime.Add(time.Hour)
	resolutions := []storage.PausedTaskResolution{{
		XID:              testXID,
		WID:              testWID,
		TaskID:           "t1",
		ResolutionStatus: storage.ResolutionStatusReplayed,
		ResumptionRunID:  "run-1",
		ProcessedTime:    processedTime,
	}}

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
			r := newTestRepository(t)
			r.mustCreate(ctx, t, newTask("t1", 10), newTask("t2", 20))

			err := pgdb.RunInTx(ctx, r.pool, func(ctx context.Context, tx pgx.Tx) error {
				if err := r.repo.WithTx(tx).MarkProcessed(ctx, tt.resolutions); err != nil {
					return err
				}
				return tt.fnErr
			})
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("RunInTx() error = %v, want %v", err, tt.wantErr)
			}
			r.assertPendingIDs(ctx, t, tt.wantIDs)
		})
	}
}

// Concurrent batches that resolve overlapping tasks in different input orders must all commit:
// MarkProcessed updates rows in primary-key order, so the batches cannot deadlock, and every task
// ends up resolved by one of the runs.
func TestTxRepository_MarkProcessed_ConcurrentOverlappingBatches(t *testing.T) {
	const (
		tasks   = 6
		workers = 8
	)
	ctx := t.Context()
	r := newTestRepository(t)
	taskIDs := make([]string, tasks)
	for i := range taskIDs {
		taskIDs[i] = fmt.Sprintf("task-%d", i)
		r.mustCreate(ctx, t, newTask(taskIDs[i], int64(i)))
	}

	errs := make([]error, workers)
	var wg sync.WaitGroup
	for w := range workers {
		wg.Go(func() {
			// Each worker resolves every task, rotating the batch so that workers lock rows in
			// different input orders.
			resolutions := make([]storage.PausedTaskResolution, 0, tasks)
			for i := range tasks {
				id := taskIDs[(i+w)%tasks]
				if w%2 == 1 {
					id = taskIDs[(tasks-1-i+w)%tasks]
				}
				resolutions = append(resolutions, storage.PausedTaskResolution{
					XID: testXID, WID: testWID, TaskID: id,
					ResolutionStatus: storage.ResolutionStatusReplayed,
					ResumptionRunID:  fmt.Sprintf("run-%d", w),
					ProcessedTime:    baseTime.Add(time.Hour),
				})
			}
			errs[w] = r.markProcessedInTx(ctx, resolutions)
		})
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		t.Fatalf("MarkProcessed() unexpected error: %v", err)
	}

	r.assertPendingIDs(ctx, t, nil)
	runIDs := make([]string, 0, tasks)
	for _, id := range taskIDs {
		res := r.readResolution(ctx, t, id)
		if res.ResolutionStatus != storage.ResolutionStatusReplayed || res.ResumptionRunID == "" {
			t.Errorf("readResolution(%q) = %+v, want a replayed resolution with a run ID", id, res)
		}
		runIDs = append(runIDs, res.ResumptionRunID)
	}
	sort.Strings(runIDs)
	t.Logf("tasks were resolved by runs %v", runIDs)
}
