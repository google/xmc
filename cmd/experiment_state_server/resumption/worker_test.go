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

package resumption

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"golang/cmp/cmp"
	"golang/protobuf/v2/testing/protocmp/protocmp"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/storage/spanner/pausedtask"
	"xmanager_cloud/internal/storage/spanner/pausestate"
	"xmanager_cloud/logging/logging"
	actuatortaskpb "xmanager_cloud/task_queue/proto/task_go_proto"
)

type enqueuedTaskCall struct {
	Ctx           context.Context
	Target        string
	WorkUnit      *essmsgpb.WorkUnit
	Action        *actuatortaskpb.Action
	OperationName string
}

type mockPublisher struct {
	calls     []enqueuedTaskCall
	err       error
	failAfter int
}

func (m *mockPublisher) EnqueueWorkUnitTask(ctx context.Context, target string, workUnit *essmsgpb.WorkUnit, action *actuatortaskpb.Action, operationName string) error {
	if m.err != nil && len(m.calls) >= m.failAfter {
		return m.err
	}
	m.calls = append(m.calls, enqueuedTaskCall{
		Ctx:           ctx,
		Target:        target,
		WorkUnit:      workUnit,
		Action:        action,
		OperationName: operationName,
	})
	return nil
}

type errPausedTaskRepo struct {
	*pausedtask.Fake
	markProcessedErr error
	listPendingErr   error
	onMarkProcessed  func()
}

func (r *errPausedTaskRepo) ListPending(ctx context.Context, limit int) ([]*pausedtask.PausedTask, error) {
	if r.listPendingErr != nil {
		return nil, r.listPendingErr
	}
	return r.Fake.ListPending(ctx, limit)
}

func (r *errPausedTaskRepo) MarkProcessed(ctx context.Context, resolutions []pausedtask.TaskResolution) error {
	if r.markProcessedErr != nil {
		return r.markProcessedErr
	}
	err := r.Fake.MarkProcessed(ctx, resolutions)
	if r.onMarkProcessed != nil {
		r.onMarkProcessed()
	}
	return err
}

type errPauseStateRepo struct {
	*pausestate.Fake
	getErr error
	setErr error
}

func (r *errPauseStateRepo) Get(ctx context.Context) (*pausestate.PauseSchedulingState, error) {
	if r.getErr != nil {
		return nil, r.getErr
	}
	if r.Fake != nil {
		return r.Fake.Get(ctx)
	}
	return pausestate.DefaultPauseSchedulingState(), nil
}

func (r *errPauseStateRepo) Set(ctx context.Context, state *pausestate.PauseSchedulingState) error {
	if r.setErr != nil {
		return r.setErr
	}
	if r.Fake != nil {
		return r.Fake.Set(ctx, state)
	}
	return nil
}

func testValidTask(wid int64, taskID string, priority int64, createTime time.Time) *pausedtask.PausedTask {
	return &pausedtask.PausedTask{
		XID:        1,
		WID:        wid,
		TaskID:     taskID,
		Target:     fmt.Sprintf("experiments/1/workUnits/%d/executions/1", wid),
		Action:     &actuatortaskpb.Action{Action: &actuatortaskpb.Action_Create{Create: &actuatortaskpb.CreateAction{}}},
		Priority:   priority,
		CreateTime: createTime,
		WorkUnit: &essmsgpb.WorkUnit{
			Name:     fmt.Sprintf("experiments/1/workUnits/%d", wid),
			Priority: essmsgpb.Priority_NORMAL,
			CurrentWorkUnitExecution: &essmsgpb.WorkUnitExecution{
				Name: fmt.Sprintf("experiments/1/workUnits/%d/executions/1", wid),
				Status: &essmsgpb.WorkUnitStatus{
					State: essmsgpb.WorkUnitStatus_RUNNING,
				},
			},
		},
	}
}

func TestResumptionWorker_Registration(t *testing.T) {
	tests := []struct {
		name           string
		initialState   *pausestate.PauseSchedulingState
		triggerRunID   string
		wantRegistered bool
	}{
		{
			name: "exits gracefully when scheduling is paused",
			initialState: &pausestate.PauseSchedulingState{
				SchedulingPaused: true,
				ResumptionRunID:  "run-1",
			},
			triggerRunID:   "run-1",
			wantRegistered: false,
		},
		{
			name: "exits gracefully when run ID does not match",
			initialState: &pausestate.PauseSchedulingState{
				SchedulingPaused: false,
				ResumptionRunID:  "run-current",
			},
			triggerRunID:   "run-stale",
			wantRegistered: false,
		},
		{
			name: "overwrites previous worker ID when claiming run to fence out prior worker",
			initialState: &pausestate.PauseSchedulingState{
				SchedulingPaused:   false,
				ResumptionRunID:    "run-1",
				ResumptionWorkerID: "worker-previous",
			},
			triggerRunID:   "run-1",
			wantRegistered: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			pauseRepo := pausestate.NewFake()
			_ = pauseRepo.Set(ctx, tt.initialState)
			taskRepo := pausedtask.NewFake()
			publisher := &mockPublisher{}
			worker := NewResumptionWorker(pauseRepo, taskRepo, publisher, nil)

			if err := worker.StartResumption(ctx, Request{ResumptionRunID: tt.triggerRunID}); err != nil {
				t.Fatalf("StartResumption() unexpected error: %v", err)
			}

			state, _ := pauseRepo.Get(ctx)
			if tt.wantRegistered {
				if state.ResumptionWorkerID == "" || state.ResumptionWorkerID == tt.initialState.ResumptionWorkerID {
					t.Errorf("expected new ResumptionWorkerID, got %q", state.ResumptionWorkerID)
				}
			} else if state.ResumptionWorkerID != tt.initialState.ResumptionWorkerID {
				t.Errorf("ResumptionWorkerID = %q, want unchanged %q", state.ResumptionWorkerID, tt.initialState.ResumptionWorkerID)
			}
		})
	}
}

func TestResumptionWorker_HappyPathReplay(t *testing.T) {
	ctx := context.Background()
	pauseRepo := pausestate.NewFake()
	_ = pauseRepo.Set(ctx, &pausestate.PauseSchedulingState{
		SchedulingPaused: false,
		ResumptionRunID:  "run-1",
	})
	taskRepo := pausedtask.NewFake()
	publisher := &mockPublisher{}

	wu1 := &essmsgpb.WorkUnit{
		Name:     "experiments/1/workUnits/1",
		Priority: essmsgpb.Priority_NORMAL,
		CurrentWorkUnitExecution: &essmsgpb.WorkUnitExecution{
			Name: "experiments/1/workUnits/1/executions/1",
			Status: &essmsgpb.WorkUnitStatus{
				State: essmsgpb.WorkUnitStatus_RUNNING,
			},
		},
	}
	wu2 := &essmsgpb.WorkUnit{
		Name:     "experiments/1/workUnits/2",
		Priority: essmsgpb.Priority_NORMAL,
		CurrentWorkUnitExecution: &essmsgpb.WorkUnitExecution{
			Name: "experiments/1/workUnits/2/executions/1",
			Status: &essmsgpb.WorkUnitStatus{
				State: essmsgpb.WorkUnitStatus_RUNNING,
			},
		},
	}

	_ = taskRepo.Create(ctx, &pausedtask.PausedTask{
		XID:           1,
		WID:           1,
		TaskID:        "task-1",
		TraceID:       "trace-op-1",
		Target:        "experiments/1/workUnits/1/executions/1",
		Action:        &actuatortaskpb.Action{Action: &actuatortaskpb.Action_Create{Create: &actuatortaskpb.CreateAction{}}},
		WorkUnit:      wu1,
		OperationName: "op-create",
		Priority:      100,
	})
	_ = taskRepo.Create(ctx, &pausedtask.PausedTask{
		XID:           1,
		WID:           2,
		TaskID:        "task-2",
		TraceID:       "trace-op-2",
		Target:        "experiments/1/workUnits/2/executions/1",
		Action:        &actuatortaskpb.Action{Action: &actuatortaskpb.Action_Delete{Delete: &actuatortaskpb.DeleteAction{}}},
		WorkUnit:      wu2,
		OperationName: "op-delete",
		Priority:      90,
	})

	worker := NewResumptionWorker(pauseRepo, taskRepo, publisher, nil)
	if err := worker.StartResumption(ctx, Request{ResumptionRunID: "run-1", BatchSize: 10}); err != nil {
		t.Fatalf("StartResumption() unexpected error: %v", err)
	}

	if len(publisher.calls) != 2 {
		t.Fatalf("expected 2 publisher calls, got %d", len(publisher.calls))
	}
	if publisher.calls[0].Target != "experiments/1/workUnits/1/executions/1" || publisher.calls[0].OperationName != "op-create" {
		t.Errorf("call 0 mismatch: %+v", publisher.calls[0])
	}
	if diff := cmp.Diff(wu1, publisher.calls[0].WorkUnit, protocmp.Transform()); diff != "" {
		t.Errorf("call 0 WorkUnit mismatch (-want +got):\n%s", diff)
	}
	if opID, ok := logging.OperationID(publisher.calls[0].Ctx); !ok || opID != "trace-op-1" {
		t.Errorf("call 0 trace ID = %q (ok=%v), want trace-op-1", opID, ok)
	}
	if publisher.calls[1].Target != "experiments/1/workUnits/2/executions/1" || publisher.calls[1].OperationName != "op-delete" {
		t.Errorf("call 1 mismatch: %+v", publisher.calls[1])
	}
	if diff := cmp.Diff(wu2, publisher.calls[1].WorkUnit, protocmp.Transform()); diff != "" {
		t.Errorf("call 1 WorkUnit mismatch (-want +got):\n%s", diff)
	}
	if opID, ok := logging.OperationID(publisher.calls[1].Ctx); !ok || opID != "trace-op-2" {
		t.Errorf("call 1 trace ID = %q (ok=%v), want trace-op-2", opID, ok)
	}

	pending, _ := taskRepo.ListPending(ctx, 0)
	if len(pending) != 0 {
		t.Errorf("expected 0 pending tasks, got %d", len(pending))
	}

	allTasks, _ := taskRepo.List(ctx)
	for _, task := range allTasks {
		if task.ProcessedTime == nil || task.ResolutionStatus != pausedtask.ResolutionStatusReplayed || task.ResumptionRunID != "run-1" {
			t.Errorf("unexpected processed task state: %+v", task)
		}
	}
}

func TestResumptionWorker_Filtering(t *testing.T) {
	ctx := context.Background()
	pauseRepo := pausestate.NewFake()
	_ = pauseRepo.Set(ctx, &pausestate.PauseSchedulingState{
		SchedulingPaused: false,
		ResumptionRunID:  "run-filter",
	})
	taskRepo := pausedtask.NewFake()
	publisher := &mockPublisher{}

	wuValid := &essmsgpb.WorkUnit{
		Name:     "experiments/1/workUnits/5",
		Priority: essmsgpb.Priority_NORMAL,
		CurrentWorkUnitExecution: &essmsgpb.WorkUnitExecution{
			Name: "experiments/1/workUnits/5/executions/1",
			Status: &essmsgpb.WorkUnitStatus{
				State: essmsgpb.WorkUnitStatus_RUNNING,
			},
		},
	}

	// Task 1: Rule 1 - Newer ExecutionId exists on WorkUnit.
	_ = taskRepo.Create(ctx, &pausedtask.PausedTask{
		XID:      1,
		WID:      1,
		TaskID:   "task-rule1-stale-exec",
		Target:   "experiments/1/workUnits/1/executions/1",
		Action:   &actuatortaskpb.Action{Action: &actuatortaskpb.Action_Create{Create: &actuatortaskpb.CreateAction{}}},
		Priority: int64(essmsgpb.Priority_NORMAL),
		WorkUnit: &essmsgpb.WorkUnit{
			Name:     "experiments/1/workUnits/1",
			Priority: essmsgpb.Priority_NORMAL,
			CurrentWorkUnitExecution: &essmsgpb.WorkUnitExecution{
				Name: "experiments/1/workUnits/1/executions/2",
				Status: &essmsgpb.WorkUnitStatus{
					State: essmsgpb.WorkUnitStatus_RUNNING,
				},
			},
		},
	})

	// Task 2: Rule 2 - Stale UpdateAction while WorkUnit is in SCHEDULING_PAUSED state.
	_ = taskRepo.Create(ctx, &pausedtask.PausedTask{
		XID:      1,
		WID:      2,
		TaskID:   "task-rule2-update-paused",
		Target:   "experiments/1/workUnits/2/executions/1",
		Action:   &actuatortaskpb.Action{Action: &actuatortaskpb.Action_Update{Update: &actuatortaskpb.UpdateAction{}}},
		Priority: int64(essmsgpb.Priority_NORMAL),
		WorkUnit: &essmsgpb.WorkUnit{
			Name:     "experiments/1/workUnits/2",
			Priority: essmsgpb.Priority_NORMAL,
			CurrentWorkUnitExecution: &essmsgpb.WorkUnitExecution{
				Name: "experiments/1/workUnits/2/executions/1",
				Status: &essmsgpb.WorkUnitStatus{
					State: essmsgpb.WorkUnitStatus_SCHEDULING_PAUSED,
				},
			},
		},
	})

	// Task 3: Rule 3 - Stale UpdateAction where task priority does not match WorkUnit priority.
	_ = taskRepo.Create(ctx, &pausedtask.PausedTask{
		XID:      1,
		WID:      3,
		TaskID:   "task-rule3-priority-mismatch",
		Target:   "experiments/1/workUnits/3/executions/1",
		Action:   &actuatortaskpb.Action{Action: &actuatortaskpb.Action_Update{Update: &actuatortaskpb.UpdateAction{}}},
		Priority: int64(essmsgpb.Priority_LOW),
		WorkUnit: &essmsgpb.WorkUnit{
			Name:     "experiments/1/workUnits/3",
			Priority: essmsgpb.Priority_HIGH,
			CurrentWorkUnitExecution: &essmsgpb.WorkUnitExecution{
				Name: "experiments/1/workUnits/3/executions/1",
				Status: &essmsgpb.WorkUnitStatus{
					State: essmsgpb.WorkUnitStatus_RUNNING,
				},
			},
		},
	})

	// Task 4: Rule 4 - Terminal WorkUnit execution state (STOPPED).
	_ = taskRepo.Create(ctx, &pausedtask.PausedTask{
		XID:      1,
		WID:      4,
		TaskID:   "task-rule4-terminal-stopped",
		Target:   "experiments/1/workUnits/4/executions/1",
		Action:   &actuatortaskpb.Action{Action: &actuatortaskpb.Action_Create{Create: &actuatortaskpb.CreateAction{}}},
		Priority: int64(essmsgpb.Priority_NORMAL),
		WorkUnit: &essmsgpb.WorkUnit{
			Name:     "experiments/1/workUnits/4",
			Priority: essmsgpb.Priority_NORMAL,
			CurrentWorkUnitExecution: &essmsgpb.WorkUnitExecution{
				Name: "experiments/1/workUnits/4/executions/1",
				Status: &essmsgpb.WorkUnitStatus{
					State: essmsgpb.WorkUnitStatus_STOPPED,
				},
			},
		},
	})

	// Task 5: Valid task that should be replayed.
	_ = taskRepo.Create(ctx, &pausedtask.PausedTask{
		XID:           1,
		WID:           5,
		TaskID:        "task-valid-replay",
		Target:        "experiments/1/workUnits/5/executions/1",
		Action:        &actuatortaskpb.Action{Action: &actuatortaskpb.Action_Create{Create: &actuatortaskpb.CreateAction{}}},
		OperationName: "op-create-5",
		Priority:      int64(essmsgpb.Priority_NORMAL),
		WorkUnit:      wuValid,
	})

	worker := NewResumptionWorker(pauseRepo, taskRepo, publisher, nil)
	if err := worker.StartResumption(ctx, Request{ResumptionRunID: "run-filter", BatchSize: 10}); err != nil {
		t.Fatalf("StartResumption() unexpected error: %v", err)
	}

	if len(publisher.calls) != 1 {
		t.Fatalf("expected 1 publisher call, got %d", len(publisher.calls))
	}
	if publisher.calls[0].Target != "experiments/1/workUnits/5/executions/1" {
		t.Errorf("publisher call target = %q, want %q", publisher.calls[0].Target, "experiments/1/workUnits/5/executions/1")
	}
	if diff := cmp.Diff(wuValid, publisher.calls[0].WorkUnit, protocmp.Transform()); diff != "" {
		t.Errorf("publisher call WorkUnit mismatch (-want +got):\n%s", diff)
	}

	pending, _ := taskRepo.ListPending(ctx, 0)
	if len(pending) != 0 {
		t.Errorf("expected 0 pending tasks remaining, got %d", len(pending))
	}

	allTasks, _ := taskRepo.List(ctx)
	statusByTaskID := make(map[string]pausedtask.ResolutionStatus, len(allTasks))
	for _, task := range allTasks {
		if task.ProcessedTime == nil || task.ResumptionRunID != "run-filter" {
			t.Errorf("task %s not properly marked processed: %+v", task.TaskID, task)
		}
		statusByTaskID[task.TaskID] = task.ResolutionStatus
	}

	wantStatuses := map[string]pausedtask.ResolutionStatus{
		"task-rule1-stale-exec":        pausedtask.ResolutionStatusSkipped,
		"task-rule2-update-paused":     pausedtask.ResolutionStatusSkipped,
		"task-rule3-priority-mismatch": pausedtask.ResolutionStatusSkipped,
		"task-rule4-terminal-stopped":  pausedtask.ResolutionStatusSkipped,
		"task-valid-replay":            pausedtask.ResolutionStatusReplayed,
	}

	if diff := cmp.Diff(wantStatuses, statusByTaskID); diff != "" {
		t.Errorf("resolution statuses mismatch (-want +got):\n%s", diff)
	}
}

func TestResumptionWorker_MultipleBatches(t *testing.T) {
	ctx := context.Background()
	pauseRepo := pausestate.NewFake()
	_ = pauseRepo.Set(ctx, &pausestate.PauseSchedulingState{ResumptionRunID: "run-multi"})
	taskRepo := pausedtask.NewFake()
	publisher := &mockPublisher{}

	for i := 1; i <= 5; i++ {
		_ = taskRepo.Create(ctx, testValidTask(
			int64(i),
			fmt.Sprintf("task-%d", i),
			int64(i*10),
			time.Now().Add(time.Duration(i)*time.Minute),
		))
	}

	worker := NewResumptionWorker(pauseRepo, taskRepo, publisher, nil)
	if err := worker.StartResumption(ctx, Request{ResumptionRunID: "run-multi", BatchSize: 2}); err != nil {
		t.Fatalf("StartResumption() unexpected error: %v", err)
	}

	if len(publisher.calls) != 5 {
		t.Fatalf("expected 5 publisher calls, got %d", len(publisher.calls))
	}
	if pending, _ := taskRepo.ListPending(ctx, 0); len(pending) != 0 {
		t.Errorf("expected 0 pending tasks remaining, got %d", len(pending))
	}
}

func TestResumptionWorker_Errors(t *testing.T) {
	testErr := errors.New("injected failure")

	tests := []struct {
		name       string
		cancelCtx  bool
		getErr     error
		setErr     error
		listErr    error
		publishErr error
		markErr    error
		wantErr    error
	}{
		{name: "pause state Get fails", getErr: testErr, wantErr: testErr},
		{name: "pause state Set fails", setErr: testErr, wantErr: testErr},
		{name: "context canceled before batch loop", cancelCtx: true, wantErr: context.Canceled},
		{name: "paused tasks ListPending fails", listErr: testErr, wantErr: testErr},
		{name: "publisher EnqueueWorkUnitTask fails", publishErr: testErr, wantErr: testErr},
		{name: "paused tasks MarkProcessed fails", markErr: testErr, wantErr: testErr},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tt.cancelCtx {
				cancel()
			}

			basePauseRepo := pausestate.NewFake()
			_ = basePauseRepo.Set(context.Background(), &pausestate.PauseSchedulingState{ResumptionRunID: "run-1"})
			pauseRepo := &errPauseStateRepo{Fake: basePauseRepo, getErr: tt.getErr, setErr: tt.setErr}

			baseTaskRepo := pausedtask.NewFake()
			_ = baseTaskRepo.Create(context.Background(), testValidTask(1, "task-1", 100, time.Now()))
			taskRepo := &errPausedTaskRepo{Fake: baseTaskRepo, listPendingErr: tt.listErr, markProcessedErr: tt.markErr}
			publisher := &mockPublisher{err: tt.publishErr}

			worker := NewResumptionWorker(pauseRepo, taskRepo, publisher, nil)
			err := worker.StartResumption(ctx, Request{ResumptionRunID: "run-1", BatchSize: 10})
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("StartResumption() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestResumptionWorker_PartialBatchPublishFailure(t *testing.T) {
	ctx := context.Background()
	pauseRepo := pausestate.NewFake()
	_ = pauseRepo.Set(ctx, &pausestate.PauseSchedulingState{ResumptionRunID: "run-1"})

	taskRepo := pausedtask.NewFake()
	for i := 1; i <= 2; i++ {
		_ = taskRepo.Create(ctx, testValidTask(int64(i), fmt.Sprintf("task-%d", i), 100, time.Now()))
	}

	publishErr := errors.New("redis publish failure")
	publisher := &mockPublisher{err: publishErr, failAfter: 1}
	worker := NewResumptionWorker(pauseRepo, taskRepo, publisher, nil)

	err := worker.StartResumption(ctx, Request{ResumptionRunID: "run-1", BatchSize: 10})
	if !errors.Is(err, publishErr) {
		t.Fatalf("StartResumption() error = %v, want %v", err, publishErr)
	}

	pending, err := taskRepo.ListPending(ctx, 0)
	if err != nil {
		t.Fatalf("ListPending() error: %v", err)
	}
	if len(pending) != 1 || pending[0].TaskID != "task-2" {
		t.Fatalf("expected only task-2 to remain pending after task-1 succeeded, got %+v", pending)
	}
}

func TestResumptionWorker_MidLoopTermination(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(state *pausestate.PauseSchedulingState)
	}{
		{
			name: "halts after batch when scheduling is re-paused",
			mutate: func(state *pausestate.PauseSchedulingState) {
				state.SchedulingPaused = true
			},
		},
		{
			name: "halts after batch when resumption run ID changes",
			mutate: func(state *pausestate.PauseSchedulingState) {
				state.ResumptionRunID = "run-newer"
			},
		},
		{
			name: "halts after batch when another worker claims ResumptionWorkerID",
			mutate: func(state *pausestate.PauseSchedulingState) {
				state.ResumptionWorkerID = "worker-other"
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			pauseRepo := pausestate.NewFake()
			_ = pauseRepo.Set(ctx, &pausestate.PauseSchedulingState{ResumptionRunID: "run-1"})

			baseTaskRepo := pausedtask.NewFake()
			for i := 1; i <= 4; i++ {
				_ = baseTaskRepo.Create(ctx, testValidTask(int64(i), fmt.Sprintf("task-%d", i), 100, time.Now()))
			}

			taskRepo := &errPausedTaskRepo{
				Fake: baseTaskRepo,
				onMarkProcessed: func() {
					state, _ := pauseRepo.Get(ctx)
					tt.mutate(state)
					_ = pauseRepo.Set(ctx, state)
				},
			}
			publisher := &mockPublisher{}
			worker := NewResumptionWorker(pauseRepo, taskRepo, publisher, nil)

			if err := worker.StartResumption(ctx, Request{ResumptionRunID: "run-1", BatchSize: 2}); err != nil {
				t.Fatalf("StartResumption() unexpected error: %v", err)
			}

			if len(publisher.calls) != 2 {
				t.Errorf("expected 2 published tasks (1 batch) before halting, got %d", len(publisher.calls))
			}
			if pending, _ := baseTaskRepo.ListPending(ctx, 0); len(pending) != 2 {
				t.Errorf("expected 2 remaining pending tasks, got %d", len(pending))
			}
		})
	}
}

func TestResumptionWorker_MissingWorkUnitFailure(t *testing.T) {
	ctx := context.Background()
	pauseRepo := pausestate.NewFake()
	_ = pauseRepo.Set(ctx, &pausestate.PauseSchedulingState{ResumptionRunID: "run-invalid"})

	taskRepo := pausedtask.NewFake()
	// Task 1: Valid task
	_ = taskRepo.Create(ctx, testValidTask(1, "task-1", 100, time.Now()))
	// Task 2: Corrupted task (missing WorkUnit)
	_ = taskRepo.Create(ctx, &pausedtask.PausedTask{
		XID:      1,
		WID:      2,
		TaskID:   "task-corrupt",
		Target:   "experiments/1/workUnits/2/executions/1",
		Action:   &actuatortaskpb.Action{Action: &actuatortaskpb.Action_Create{Create: &actuatortaskpb.CreateAction{}}},
		WorkUnit: nil,
	})

	publisher := &mockPublisher{}
	worker := NewResumptionWorker(pauseRepo, taskRepo, publisher, nil)

	err := worker.StartResumption(ctx, Request{ResumptionRunID: "run-invalid", BatchSize: 10})
	if err == nil {
		t.Fatalf("StartResumption() expected error for missing WorkUnit, got nil")
	}
	if !strings.Contains(err.Error(), "task task-corrupt missing required parent work unit or execution") {
		t.Errorf("StartResumption() error = %v, want substring %q", err, "task task-corrupt missing required parent work unit or execution")
	}

	// Task 1 was valid and published before the corrupt task halted the batch.
	if len(publisher.calls) != 1 || publisher.calls[0].Target != "experiments/1/workUnits/1/executions/1" {
		t.Errorf("expected task-1 to be published before failure, got %d calls", len(publisher.calls))
	}

	// Task 1 should be marked processed, but the corrupt task-corrupt must remain pending.
	pending, err := taskRepo.ListPending(ctx, 0)
	if err != nil {
		t.Fatalf("ListPending() error: %v", err)
	}
	if len(pending) != 1 || pending[0].TaskID != "task-corrupt" {
		t.Fatalf("expected task-corrupt to remain pending in Spanner, got %+v", pending)
	}
}

type mockBacklogCalculator struct {
	mu            sync.Mutex
	backlogValues []int64
	calculateErr  error
	calls         int
	recordedMax   int
	onCalculate   func()
}

func (m *mockBacklogCalculator) CalculateBacklog(ctx context.Context, maxThreshold int) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	m.recordedMax = maxThreshold

	if m.onCalculate != nil {
		m.onCalculate()
	}
	if m.calculateErr != nil {
		return 0, m.calculateErr
	}
	if len(m.backlogValues) == 0 {
		return 0, nil
	}
	val := m.backlogValues[0]
	if len(m.backlogValues) > 1 {
		m.backlogValues = m.backlogValues[1:]
	}
	return val, nil
}

func TestResumptionWorker_BacklogThrottling(t *testing.T) {
	ctx := context.Background()
	pauseRepo := pausestate.NewFake()
	_ = pauseRepo.Set(ctx, &pausestate.PauseSchedulingState{ResumptionRunID: "run-throttle"})

	taskRepo := pausedtask.NewFake()
	_ = taskRepo.Create(ctx, testValidTask(1, "task-1", 100, time.Now()))

	publisher := &mockPublisher{}
	backlogCalc := &mockBacklogCalculator{
		backlogValues: []int64{10, 2}, // First check: busy (10 >= 5), second check: cleared (2 < 5).
	}

	worker := NewResumptionWorker(
		pauseRepo,
		taskRepo,
		publisher,
		backlogCalc,
	)
	worker.throttlePauseDuration = time.Millisecond

	err := worker.StartResumption(ctx, Request{
		ResumptionRunID:     "run-throttle",
		BatchSize:           10,
		MaxBacklogThreshold: 5,
	})
	if err != nil {
		t.Fatalf("StartResumption() unexpected error: %v", err)
	}

	if backlogCalc.calls != 3 {
		t.Errorf("backlogCalc.calls = %d, want 3 (2 for batch 1, 1 for subsequent batch check)", backlogCalc.calls)
	}
	if backlogCalc.recordedMax != 5 {
		t.Errorf("recordedMax = %d, want 5", backlogCalc.recordedMax)
	}
	if len(publisher.calls) != 1 {
		t.Errorf("expected 1 task published after throttling resolved, got %d", len(publisher.calls))
	}
}

func TestResumptionWorker_BacklogUnderThreshold(t *testing.T) {
	ctx := context.Background()
	pauseRepo := pausestate.NewFake()
	_ = pauseRepo.Set(ctx, &pausestate.PauseSchedulingState{ResumptionRunID: "run-unthrottled"})

	taskRepo := pausedtask.NewFake()
	_ = taskRepo.Create(ctx, testValidTask(1, "task-1", 100, time.Now()))

	publisher := &mockPublisher{}
	backlogCalc := &mockBacklogCalculator{
		backlogValues: []int64{2}, // 2 < 5: not busy.
	}

	worker := NewResumptionWorker(
		pauseRepo,
		taskRepo,
		publisher,
		backlogCalc,
	)
	worker.throttlePauseDuration = time.Millisecond

	err := worker.StartResumption(ctx, Request{
		ResumptionRunID:     "run-unthrottled",
		BatchSize:           10,
		MaxBacklogThreshold: 5,
	})
	if err != nil {
		t.Fatalf("StartResumption() unexpected error: %v", err)
	}

	if backlogCalc.calls != 2 {
		t.Errorf("backlogCalc.calls = %d, want 2 (1 for batch 1, 1 for subsequent batch check)", backlogCalc.calls)
	}
	if len(publisher.calls) != 1 {
		t.Errorf("expected 1 task published, got %d", len(publisher.calls))
	}
}

func TestResumptionWorker_BacklogThrottling_CalculatorError(t *testing.T) {
	ctx := context.Background()
	pauseRepo := pausestate.NewFake()
	_ = pauseRepo.Set(ctx, &pausestate.PauseSchedulingState{ResumptionRunID: "run-calc-err"})

	taskRepo := pausedtask.NewFake()
	_ = taskRepo.Create(ctx, testValidTask(1, "task-1", 100, time.Now()))

	calcErr := errors.New("redis connection refused")
	backlogCalc := &mockBacklogCalculator{calculateErr: calcErr}
	publisher := &mockPublisher{}

	worker := NewResumptionWorker(
		pauseRepo,
		taskRepo,
		publisher,
		backlogCalc,
	)
	worker.throttlePauseDuration = time.Millisecond

	err := worker.StartResumption(ctx, Request{
		ResumptionRunID:     "run-calc-err",
		BatchSize:           10,
		MaxBacklogThreshold: 5,
	})
	if !errors.Is(err, calcErr) {
		t.Fatalf("StartResumption() error = %v, want %v", err, calcErr)
	}
	if len(publisher.calls) != 0 {
		t.Errorf("expected 0 published tasks on error, got %d", len(publisher.calls))
	}
}

func TestResumptionWorker_BacklogThrottling_ContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	pauseRepo := pausestate.NewFake()
	_ = pauseRepo.Set(ctx, &pausestate.PauseSchedulingState{ResumptionRunID: "run-cancel"})

	taskRepo := pausedtask.NewFake()
	_ = taskRepo.Create(ctx, testValidTask(1, "task-1", 100, time.Now()))

	backlogCalc := &mockBacklogCalculator{
		backlogValues: []int64{10},
		onCalculate: func() {
			cancel()
		},
	}
	publisher := &mockPublisher{}

	worker := NewResumptionWorker(
		pauseRepo,
		taskRepo,
		publisher,
		backlogCalc,
	)
	worker.throttlePauseDuration = time.Hour

	err := worker.StartResumption(ctx, Request{
		ResumptionRunID:     "run-cancel",
		BatchSize:           10,
		MaxBacklogThreshold: 5,
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("StartResumption() error = %v, want %v", err, context.Canceled)
	}
	if len(publisher.calls) != 0 {
		t.Errorf("expected 0 published tasks on context cancellation, got %d", len(publisher.calls))
	}
}

func TestResumptionWorker_BacklogThrottling_SchedulingRepaused(t *testing.T) {
	ctx := context.Background()
	pauseRepo := pausestate.NewFake()
	_ = pauseRepo.Set(ctx, &pausestate.PauseSchedulingState{ResumptionRunID: "run-repause"})

	taskRepo := pausedtask.NewFake()
	_ = taskRepo.Create(ctx, testValidTask(1, "task-1", 100, time.Now()))

	backlogCalc := &mockBacklogCalculator{
		backlogValues: []int64{10},
		onCalculate: func() {
			_ = pauseRepo.Set(ctx, &pausestate.PauseSchedulingState{
				SchedulingPaused: true,
				ResumptionRunID:  "run-repause",
			})
		},
	}
	publisher := &mockPublisher{}

	worker := NewResumptionWorker(
		pauseRepo,
		taskRepo,
		publisher,
		backlogCalc,
	)
	worker.throttlePauseDuration = time.Millisecond

	err := worker.StartResumption(ctx, Request{
		ResumptionRunID:     "run-repause",
		BatchSize:           10,
		MaxBacklogThreshold: 5,
	})
	if err != nil {
		t.Fatalf("StartResumption() unexpected error: %v", err)
	}
	if len(publisher.calls) != 0 {
		t.Errorf("expected 0 published tasks when re-paused, got %d", len(publisher.calls))
	}
}

func TestResumptionWorker_BacklogThrottling_WorkerFenced(t *testing.T) {
	ctx := context.Background()
	pauseRepo := pausestate.NewFake()
	_ = pauseRepo.Set(ctx, &pausestate.PauseSchedulingState{ResumptionRunID: "run-fence"})

	taskRepo := pausedtask.NewFake()
	_ = taskRepo.Create(ctx, testValidTask(1, "task-1", 100, time.Now()))

	backlogCalc := &mockBacklogCalculator{
		backlogValues: []int64{10},
		onCalculate: func() {
			_ = pauseRepo.Set(ctx, &pausestate.PauseSchedulingState{
				SchedulingPaused:   false,
				ResumptionRunID:    "run-fence",
				ResumptionWorkerID: "worker-fenced-other",
			})
		},
	}
	publisher := &mockPublisher{}

	worker := NewResumptionWorker(
		pauseRepo,
		taskRepo,
		publisher,
		backlogCalc,
	)
	worker.throttlePauseDuration = time.Millisecond

	err := worker.StartResumption(ctx, Request{
		ResumptionRunID:     "run-fence",
		BatchSize:           10,
		MaxBacklogThreshold: 5,
	})
	if err != nil {
		t.Fatalf("StartResumption() unexpected error: %v", err)
	}
	if len(publisher.calls) != 0 {
		t.Errorf("expected 0 published tasks when fenced, got %d", len(publisher.calls))
	}
}

func TestWaitOrCancel_Elapses(t *testing.T) {
	worker := NewResumptionWorker(nil, nil, nil, nil)
	worker.throttlePauseDuration = time.Millisecond

	if err := worker.waitOrCancel(context.Background()); err != nil {
		t.Errorf("waitOrCancel() unexpected error: %v", err)
	}
}

func TestWaitOrCancel_ContextCancelled(t *testing.T) {
	worker := NewResumptionWorker(nil, nil, nil, nil)
	worker.throttlePauseDuration = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := worker.waitOrCancel(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("waitOrCancel() error = %v, want %v", err, context.Canceled)
	}
}
