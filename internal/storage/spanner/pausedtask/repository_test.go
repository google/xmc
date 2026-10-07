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

// External test package: the Spanner storage used to create parent rows imports pausedtask.
package pausedtask_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/data"
	ss "xmanager_cloud/internal/storage/spanner"
	"xmanager_cloud/internal/storage/spanner/emulator"
	"xmanager_cloud/internal/storage/spanner/pausedtask"
)

var spannerBaseTime = time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)

func setTaskActionText(task *storage.PausedTask, text string) {
	actionElemType := reflect.TypeOf(task.Action).Elem()
	msg := reflect.New(actionElemType).Interface().(proto.Message)
	if err := prototext.Unmarshal([]byte(text), msg); err != nil {
		panic(err)
	}
	reflect.ValueOf(&task.Action).Elem().Set(reflect.ValueOf(msg))
}

func newSpannerTask(taskID string, priority int64) *storage.PausedTask {
	task := &storage.PausedTask{
		XID:        100,
		WID:        200,
		TaskID:     taskID,
		Priority:   priority,
		CreateTime: spannerBaseTime,
	}
	setTaskActionText(task, `create: {}`)
	return task
}

func pendingIDs(tasks []*storage.PausedTask) []string {
	ids := make([]string, len(tasks))
	for i, task := range tasks {
		ids[i] = task.TaskID
	}
	return ids
}

// newTestRepository returns a repository and client backed by a fresh emulator database
// that contains the parent Experiment and WorkUnit referenced by newSpannerTask.
func newTestRepository(t *testing.T) (*pausedtask.Repository, *spanner.Client) {
	t.Helper()
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)

	store, err := ss.NewStorage(essClient)
	if err != nil {
		t.Fatalf("NewStorage() unexpected error: %v", err)
	}
	ss.CreateBlankExperiment(ctx, t, store, 100)
	// 12 corresponds to WorkUnitStatus_SCHEDULING_PAUSED.
	ss.CreateBlankWorkUnitWithState(ctx, t, store, 100, 200, 12)

	repo, err := pausedtask.NewRepository(essClient)
	if err != nil {
		t.Fatalf("NewRepository() unexpected error: %v", err)
	}
	return repo, essClient
}

func createInTx(ctx context.Context, client *spanner.Client, repo *pausedtask.Repository, task *storage.PausedTask) error {
	_, err := client.ReadWriteTransaction(ctx, func(ctx context.Context, tx *spanner.ReadWriteTransaction) error {
		return repo.WithTx(tx).Create(ctx, task)
	})
	return err
}

func markProcessedInTx(ctx context.Context, client *spanner.Client, repo *pausedtask.Repository, resolutions []storage.PausedTaskResolution) error {
	_, err := client.ReadWriteTransaction(ctx, func(ctx context.Context, tx *spanner.ReadWriteTransaction) error {
		return repo.WithTx(tx).MarkProcessed(ctx, resolutions)
	})
	return err
}

func mustCreate(ctx context.Context, t *testing.T, client *spanner.Client, repo *pausedtask.Repository, tasks ...*storage.PausedTask) {
	t.Helper()
	for _, task := range tasks {
		if err := createInTx(ctx, client, repo, task); err != nil {
			t.Fatalf("Create(%q) unexpected error: %v", task.TaskID, err)
		}
	}
}

func TestNewRepository_NilClient(t *testing.T) {
	if _, err := pausedtask.NewRepository(nil); err == nil {
		t.Error("NewRepository(nil) error = nil, want non-nil")
	}
}

func TestTxRepository_CreateErrors(t *testing.T) {
	ctx := context.Background()
	repo, client := newTestRepository(t)
	mustCreate(ctx, t, client, repo, newSpannerTask("task-1", 100))

	noAction := newSpannerTask("task-2", 100)
	noAction.Action = nil
	missingParent := newSpannerTask("task-3", 100)
	missingParent.WID = 999

	tests := []struct {
		name     string
		task     *storage.PausedTask
		wantErr  error
		wantCode codes.Code
	}{
		{name: "duplicate", task: newSpannerTask("task-1", 100), wantCode: codes.AlreadyExists},
		{name: "missing_parent_work_unit", task: missingParent, wantCode: codes.NotFound},
		{name: "nil_task", task: nil, wantErr: pausedtask.ErrInvalidArgument},
		{name: "nil_action", task: noAction, wantErr: pausedtask.ErrInvalidArgument},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := createInTx(ctx, client, repo, tt.task)
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Errorf("Create() error = %v, want %v", err, tt.wantErr)
			}
			if tt.wantCode != codes.OK && spanner.ErrCode(err) != tt.wantCode {
				t.Errorf("Create() error = %v (code %v), want code %v", err, spanner.ErrCode(err), tt.wantCode)
			}
		})
	}
}

func TestTxRepository_CreateGeneratesTaskIDAndCreateTime(t *testing.T) {
	ctx := context.Background()
	repo, client := newTestRepository(t)
	task := newSpannerTask("", 100)
	task.CreateTime = time.Time{}

	before := time.Now().Add(-time.Minute)
	mustCreate(ctx, t, client, repo, task)

	if task.TaskID == "" {
		t.Fatal("Create() did not populate TaskID")
	}
	if task.CreateTime.Before(before) {
		t.Fatalf("Create() did not populate task.CreateTime: got %v, want after %v", task.CreateTime, before)
	}
	got, err := repo.ListPending(ctx, 0)
	if err != nil || len(got) != 1 {
		t.Fatalf("ListPending() = (%v, %v), want 1 task and nil error", got, err)
	}
	if got[0].TaskID != task.TaskID {
		t.Errorf("TaskID = %q, want %q", got[0].TaskID, task.TaskID)
	}
	if !got[0].CreateTime.Equal(task.CreateTime) {
		t.Errorf("CreateTime = %v, want %v", got[0].CreateTime, task.CreateTime)
	}
}

func TestTxRepository_Create(t *testing.T) {
	ctx := context.Background()
	errAbort := errors.New("abort")

	tests := []struct {
		name    string
		task    *storage.PausedTask
		fnErr   error
		wantErr error
		wantIDs []string
	}{
		{name: "committed", task: newSpannerTask("task-1", 100), wantIDs: []string{"task-1"}},
		{name: "rolled_back", task: newSpannerTask("task-1", 100), fnErr: errAbort, wantErr: errAbort},
		{name: "invalid_task", task: nil, wantErr: pausedtask.ErrInvalidArgument},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, client := newTestRepository(t)
			_, err := client.ReadWriteTransaction(ctx, func(ctx context.Context, tx *spanner.ReadWriteTransaction) error {
				if err := repo.WithTx(tx).Create(ctx, tt.task); err != nil {
					return err
				}
				return tt.fnErr
			})
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("ReadWriteTransaction() error = %v, want %v", err, tt.wantErr)
			}
			assertPendingIDs(ctx, t, repo, tt.wantIDs)
		})
	}
}

func TestRepository_ListPending_RoundTrip(t *testing.T) {
	ctx := context.Background()
	repo, client := newTestRepository(t)
	want := newSpannerTask("task-1", 100)
	want.Target = "experiments/100/workUnits/200/executions/1"
	want.OperationName = "experiments/100/workUnits/200/operations/op-1"
	want.TraceID = "trace-1"
	setTaskActionText(want, `restart: { author: "author@example.com" }`)
	mustCreate(ctx, t, client, repo, want)

	got, err := repo.ListPending(ctx, 0)
	if err != nil || len(got) != 1 {
		t.Fatalf("ListPending() = (%v, %v), want 1 task and nil error", got, err)
	}
	if diff := cmp.Diff(want, got[0], protocmp.Transform(), cmpopts.IgnoreFields(storage.PausedTask{}, "WorkUnit")); diff != "" {
		t.Errorf("ListPending() mismatch (-want +got):\n%s", diff)
	}

	// Timestamps on the parent WorkUnit are non-deterministic, so only identity and state are checked.
	wantWorkUnit := []any{"experiments/100/workUnits/200", "SCHEDULING_PAUSED"}
	gotWorkUnit := []any{got[0].WorkUnit.GetName(), got[0].WorkUnit.GetCurrentWorkUnitExecution().GetStatus().GetState().String()}
	if diff := cmp.Diff(wantWorkUnit, gotWorkUnit); diff != "" {
		t.Errorf("ListPending() WorkUnit mismatch (-want +got):\n%s", diff)
	}
}

func TestRepository_ListPending_Ordering(t *testing.T) {
	ctx := context.Background()
	repo, client := newTestRepository(t)
	highNew := newSpannerTask("high-new", 200)
	highNew.CreateTime = spannerBaseTime.Add(time.Minute)
	mustCreate(ctx, t, client, repo, newSpannerTask("low", 50), newSpannerTask("high-old", 200), highNew, newSpannerTask("top", 300), newSpannerTask("done", 400))
	if err := markProcessedInTx(ctx, client, repo, []storage.PausedTaskResolution{{XID: 100, WID: 200, TaskID: "done"}}); err != nil {
		t.Fatalf("MarkProcessed() unexpected error: %v", err)
	}

	tests := []struct {
		name    string
		limit   int
		wantIDs []string
	}{
		{name: "unlimited", limit: 0, wantIDs: []string{"top", "high-old", "high-new", "low"}},
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

	got, err := repo.ListPending(context.Background(), 0)
	if err != nil {
		t.Fatalf("ListPending() unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("ListPending() = %v, want nil", got)
	}
}

func TestTxRepository_MarkProcessed_PersistsResolution(t *testing.T) {
	ctx := context.Background()
	repo, client := newTestRepository(t)
	mustCreate(ctx, t, client, repo, newSpannerTask("t1", 10), newSpannerTask("t2", 20))
	processedTime := spannerBaseTime.Add(time.Hour)

	err := markProcessedInTx(ctx, client, repo, []storage.PausedTaskResolution{{
		XID:              100,
		WID:              200,
		TaskID:           "t1",
		ResolutionStatus: storage.ResolutionStatusReplayed,
		ResumptionRunID:  "run-1",
		ProcessedTime:    processedTime,
	}})
	if err != nil {
		t.Fatalf("MarkProcessed() unexpected error: %v", err)
	}

	got := readResolution(ctx, t, client, "t1")
	want := storage.PausedTaskResolution{XID: 100, WID: 200, TaskID: "t1", ResolutionStatus: storage.ResolutionStatusReplayed, ResumptionRunID: "run-1", ProcessedTime: processedTime}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("persisted resolution mismatch (-want +got):\n%s", diff)
	}
	assertPendingIDs(ctx, t, repo, []string{"t2"})
}

func TestTxRepository_MarkProcessed_UnknownTaskIsAtomic(t *testing.T) {
	ctx := context.Background()
	repo, client := newTestRepository(t)
	mustCreate(ctx, t, client, repo, newSpannerTask("t1", 10))

	err := markProcessedInTx(ctx, client, repo, []storage.PausedTaskResolution{
		{XID: 100, WID: 200, TaskID: "t1", ResolutionStatus: storage.ResolutionStatusSkipped},
		{XID: 100, WID: 200, TaskID: "missing", ResolutionStatus: storage.ResolutionStatusSkipped},
	})
	if spanner.ErrCode(err) != codes.NotFound {
		t.Errorf("MarkProcessed() error = %v (code %v), want code %v", err, spanner.ErrCode(err), codes.NotFound)
	}
	assertPendingIDs(ctx, t, repo, []string{"t1"})
}

func TestTxRepository_MarkProcessed(t *testing.T) {
	ctx := context.Background()
	errAbort := errors.New("abort")
	processedTime := spannerBaseTime.Add(time.Hour)
	resolutions := []storage.PausedTaskResolution{{
		XID:              100,
		WID:              200,
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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, client := newTestRepository(t)
			mustCreate(ctx, t, client, repo, newSpannerTask("t1", 10), newSpannerTask("t2", 20))

			_, err := client.ReadWriteTransaction(ctx, func(ctx context.Context, tx *spanner.ReadWriteTransaction) error {
				if err := repo.WithTx(tx).MarkProcessed(ctx, tt.resolutions); err != nil {
					return err
				}
				return tt.fnErr
			})
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("ReadWriteTransaction() error = %v, want %v", err, tt.wantErr)
			}
			assertPendingIDs(ctx, t, repo, tt.wantIDs)
		})
	}
}

func assertPendingIDs(ctx context.Context, t *testing.T, repo *pausedtask.Repository, wantIDs []string) {
	t.Helper()
	got, err := repo.ListPending(ctx, 0)
	if err != nil {
		t.Fatalf("ListPending() unexpected error: %v", err)
	}
	if diff := cmp.Diff(wantIDs, pendingIDs(got), cmpopts.EquateEmpty()); diff != "" {
		t.Errorf("ListPending() IDs mismatch (-want +got):\n%s", diff)
	}
}

// readResolution reads the soft-delete columns directly, since the Repository interface only
// exposes pending tasks.
func readResolution(ctx context.Context, t *testing.T, client *spanner.Client, taskID string) storage.PausedTaskResolution {
	t.Helper()
	key := spanner.Key{data.ReverseBits(100), int64(200), taskID}
	cols := []string{pausedtask.ColXID, pausedtask.ColWorkUnitID, pausedtask.ColTaskID, pausedtask.ColResolutionStatus, pausedtask.ColResumptionRunID, pausedtask.ColProcessedTime}
	row, err := client.Single().ReadRow(ctx, pausedtask.TableName, key, cols)
	if err != nil {
		t.Fatalf("ReadRow(%q) unexpected error: %v", taskID, err)
	}
	var res storage.PausedTaskResolution
	var resolutionStatus, resumptionRunID spanner.NullString
	if err := row.Columns(&res.XID, &res.WID, &res.TaskID, &resolutionStatus, &resumptionRunID, &res.ProcessedTime); err != nil {
		t.Fatalf("row.Columns() unexpected error: %v", err)
	}
	res.ResolutionStatus = storage.ResolutionStatus(resolutionStatus.StringVal)
	res.ResumptionRunID = resumptionRunID.StringVal
	res.ProcessedTime = res.ProcessedTime.UTC()
	return res
}
