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

package storagetest

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/testing/protocmp"
	"task/go/status"

	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/prototest"
	"xmanager_cloud/internal/storage"
	actuatortaskpb "xmanager_cloud/task_queue/proto/task_go_proto"
)

var baseTime = time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)

func newTask(taskID string, priority int64) *storage.PausedTask {
	return &storage.PausedTask{
		XID:        100,
		WID:        200,
		TaskID:     taskID,
		Priority:   priority,
		CreateTime: baseTime,
		Action:     &actuatortaskpb.Action{Action: &actuatortaskpb.Action_Create{Create: &actuatortaskpb.CreateAction{}}},
	}
}

func taskIDs(tasks []*storage.PausedTask) []string {
	ids := make([]string, len(tasks))
	for i, task := range tasks {
		ids[i] = task.TaskID
	}
	return ids
}

func TestFakePausedTask_Create(t *testing.T) {
	ctx := context.Background()
	fake := NewFakePausedTaskRepository()
	task := newTask("task-1", 100)

	if err := fake.Create(ctx, task); err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}
	if err := fake.Create(ctx, task); !errors.Is(err, storage.ErrPausedTaskAlreadyExists) {
		t.Errorf("Create() duplicate error = %v, want %v", err, storage.ErrPausedTaskAlreadyExists)
	}
	if err := fake.Create(ctx, nil); !errors.Is(err, status.ErrInvalidArgument) {
		t.Errorf("Create(nil) error = %v, want %v", err, status.ErrInvalidArgument)
	}

	tasks, err := fake.List(ctx)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("List() = (%v, %v), want 1 task and nil error", tasks, err)
	}
	if diff := cmp.Diff(task, tasks[0], protocmp.Transform()); diff != "" {
		t.Errorf("List() mismatch (-want +got):\n%s", diff)
	}
}

func TestFakePausedTask_List(t *testing.T) {
	ctx, fake := context.Background(), NewFakePausedTaskRepository()
	for _, task := range []*storage.PausedTask{newTask("t1", 10), newTask("t2", 20), newTask("t3", 30)} {
		_ = fake.Create(ctx, task)
	}
	_ = fake.MarkProcessed(ctx, []storage.PausedTaskResolution{{XID: 100, WID: 200, TaskID: "t2", ResolutionStatus: storage.ResolutionStatusReplayed}})

	got, err := fake.List(ctx)
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}
	if diff := cmp.Diff([]string{"t1", "t2", "t3"}, taskIDs(got)); diff != "" {
		t.Errorf("List() order mismatch (-want +got):\n%s", diff)
	}
}

func TestFakePausedTask_ListPending_Ordering(t *testing.T) {
	ctx, fake := context.Background(), NewFakePausedTaskRepository()
	tNew := newTask("high-new", 200)
	tNew.CreateTime = baseTime.Add(time.Minute)
	for _, task := range []*storage.PausedTask{newTask("low", 50), newTask("high-old", 200), tNew, newTask("top", 300), newTask("done", 400)} {
		_ = fake.Create(ctx, task)
	}
	_ = fake.MarkProcessed(ctx, []storage.PausedTaskResolution{{XID: 100, WID: 200, TaskID: "done"}})

	tests := []struct {
		limit   int
		wantIDs []string
	}{
		{limit: 0, wantIDs: []string{"top", "high-old", "high-new", "low"}},
		{limit: 2, wantIDs: []string{"top", "high-old"}},
	}
	for _, tt := range tests {
		got, err := fake.ListPending(ctx, tt.limit)
		if err != nil {
			t.Fatalf("ListPending(%d) unexpected error: %v", tt.limit, err)
		}
		if diff := cmp.Diff(tt.wantIDs, taskIDs(got)); diff != "" {
			t.Errorf("ListPending(%d) IDs mismatch (-want +got):\n%s", tt.limit, diff)
		}
	}
}

func TestFakePausedTask_MarkProcessed(t *testing.T) {
	ctx, fake := context.Background(), NewFakePausedTaskRepository()
	t1, t2, t3 := newTask("t1", 10), newTask("t2", 20), newTask("t3", 30)
	for _, task := range []*storage.PausedTask{t1, t2, t3} {
		_ = fake.Create(ctx, task)
	}

	procTime := baseTime.Add(5 * time.Minute)
	res := []storage.PausedTaskResolution{
		{XID: 100, WID: 200, TaskID: "t1", ResolutionStatus: storage.ResolutionStatusReplayed, ResumptionRunID: "run-1", ProcessedTime: procTime},
		{XID: 100, WID: 200, TaskID: "t2", ResolutionStatus: storage.ResolutionStatusSkipped, ResumptionRunID: "run-1", ProcessedTime: procTime},
	}
	if err := fake.MarkProcessed(ctx, res); err != nil {
		t.Fatalf("MarkProcessed() error: %v", err)
	}

	tasks, _ := fake.List(ctx)
	t1.ResolutionStatus, t1.ResumptionRunID, t1.ProcessedTime = storage.ResolutionStatusReplayed, "run-1", &procTime
	t2.ResolutionStatus, t2.ResumptionRunID, t2.ProcessedTime = storage.ResolutionStatusSkipped, "run-1", &procTime
	if diff := cmp.Diff([]*storage.PausedTask{t1, t2, t3}, tasks, protocmp.Transform()); diff != "" {
		t.Errorf("List() mismatch after MarkProcessed (-want +got):\n%s", diff)
	}

	missingRes := []storage.PausedTaskResolution{
		{XID: 100, WID: 200, TaskID: "t3", ResolutionStatus: storage.ResolutionStatusReplayed},
		{XID: 999, WID: 999, TaskID: "ghost"},
	}
	if err := fake.MarkProcessed(ctx, missingRes); !errors.Is(err, storage.ErrPausedTaskNotFound) {
		t.Errorf("MarkProcessed() error = %v, want %v", err, storage.ErrPausedTaskNotFound)
	}
	pending, _ := fake.ListPending(ctx, 0)
	if diff := cmp.Diff([]string{"t3"}, taskIDs(pending)); diff != "" {
		t.Errorf("t3 was modified despite failure (-want +got):\n%s", diff)
	}
}

func TestFakePausedTask_Empty(t *testing.T) {
	ctx := context.Background()
	fake := NewFakePausedTaskRepository()
	if tasks, err := fake.List(ctx); err != nil || tasks != nil {
		t.Errorf("List() = (%v, %v), want (nil, nil)", tasks, err)
	}
	if pending, err := fake.ListPending(ctx, 0); err != nil || pending != nil {
		t.Errorf("ListPending() = (%v, %v), want (nil, nil)", pending, err)
	}
}

func TestFakePausedTask_Reset(t *testing.T) {
	ctx := context.Background()
	fake := NewFakePausedTaskRepository()
	_ = fake.Create(ctx, newTask("t1", 10))
	fake.Reset()
	if tasks, err := fake.List(ctx); err != nil || len(tasks) != 0 {
		t.Errorf("List() after Reset() = (%v, %v), want (nil/empty, nil)", tasks, err)
	}
}

func TestFakePausedTask_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	fake := NewFakePausedTaskRepository()

	if err := fake.Create(ctx, newTask("t1", 10)); !errors.Is(err, context.Canceled) {
		t.Errorf("Create() error = %v, want %v", err, context.Canceled)
	}
	if _, err := fake.List(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("List() error = %v, want %v", err, context.Canceled)
	}
	if _, err := fake.ListPending(ctx, 0); !errors.Is(err, context.Canceled) {
		t.Errorf("ListPending() error = %v, want %v", err, context.Canceled)
	}
	if err := fake.MarkProcessed(ctx, []storage.PausedTaskResolution{{}}); !errors.Is(err, context.Canceled) {
		t.Errorf("MarkProcessed() error = %v, want %v", err, context.Canceled)
	}
}

func TestFakePausedTask_MutationIsolation(t *testing.T) {
	ctx, fake := context.Background(), NewFakePausedTaskRepository()
	task := newTask("t1", 10)
	if err := fake.Create(ctx, task); err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	task.Action = prototest.Modify(task.Action, func(a *actuatortaskpb.Action) {
		a.Action = &actuatortaskpb.Action_Delete{Delete: &actuatortaskpb.DeleteAction{}}
	})

	tasks, err := fake.List(ctx)
	if err != nil || len(tasks) != 1 || tasks[0].Action.GetDelete() != nil {
		t.Errorf("Stored task Action was mutated via input reference")
	}
}

func TestFakePausedTask_MutationIsolation_WorkUnit(t *testing.T) {
	ctx, fake := context.Background(), NewFakePausedTaskRepository()
	task := newTask("t1", 10)
	task.WorkUnit = &essmsgpb.WorkUnit{
		Name:     "experiments/100/workUnits/200",
		Priority: essmsgpb.Priority_NORMAL,
	}
	if err := fake.Create(ctx, task); err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	task.WorkUnit = prototest.Modify(task.WorkUnit, func(wu *essmsgpb.WorkUnit) {
		wu.Priority = essmsgpb.Priority_HIGH
	})

	tasks, err := fake.List(ctx)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("List() error = %v, want 1 task", err)
	}
	if tasks[0].WorkUnit.GetPriority() != essmsgpb.Priority_NORMAL {
		t.Errorf("Stored task WorkUnit was mutated via input reference: got %v, want %v", tasks[0].WorkUnit.GetPriority(), essmsgpb.Priority_NORMAL)
	}

	tasks[0].WorkUnit = prototest.Modify(tasks[0].WorkUnit, func(wu *essmsgpb.WorkUnit) {
		wu.Priority = essmsgpb.Priority_LOW
	})
	tasksSecond, err := fake.ListPending(ctx, 0)
	if err != nil || len(tasksSecond) != 1 {
		t.Fatalf("ListPending() error = %v, want 1 task", err)
	}
	if tasksSecond[0].WorkUnit.GetPriority() != essmsgpb.Priority_NORMAL {
		t.Errorf("Stored task WorkUnit in Fake was mutated via output reference: got %v, want %v", tasksSecond[0].WorkUnit.GetPriority(), essmsgpb.Priority_NORMAL)
	}
}

func TestFakePausedTask_Concurrency(t *testing.T) {
	ctx := context.Background()
	fake := NewFakePausedTaskRepository()
	var wg sync.WaitGroup
	for g := 0; g < 10; g++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			for i := 0; i < 10; i++ {
				_ = fake.Create(ctx, newTask(fmt.Sprintf("t-%d-%d", gid, i), int64(i)))
				_, _ = fake.List(ctx)
			}
		}(g)
	}
	wg.Wait()

	tasks, err := fake.List(ctx)
	if err != nil || len(tasks) != 100 {
		t.Fatalf("List() = (%d tasks, %v), want (100, nil)", len(tasks), err)
	}
}

func TestFakePausedTask_Create_GeneratesTaskIDWhenEmpty(t *testing.T) {
	ctx := context.Background()
	fake := NewFakePausedTaskRepository()
	task := newTask("", 100)

	if err := fake.Create(ctx, task); err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}

	if task.TaskID == "" {
		t.Errorf("expected generated TaskID on task, got empty string")
	}

	list, err := fake.List(ctx)
	if err != nil {
		t.Fatalf("List() unexpected error: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 task, got %d", len(list))
	}
	if list[0].TaskID == "" {
		t.Errorf("expected stored task to have generated TaskID, got empty string")
	}
	if list[0].TaskID != task.TaskID {
		t.Errorf("stored TaskID mismatch: got %q, want %q", list[0].TaskID, task.TaskID)
	}
}
