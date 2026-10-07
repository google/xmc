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
	"xmanager_cloud/internal/storage"
	storagetest "xmanager_cloud/internal/storage/testing"
	"xmanager_cloud/kubernetes/common/constants"
	"xmanager_cloud/logging/logging"
	actuatortaskpb "xmanager_cloud/task_queue/proto/task_go_proto"
	"xmanager_cloud/task_queue_redis/redisclient"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

type enqueuedTaskCall struct {
	Ctx             context.Context
	Target          string
	WorkUnit        *essmsgpb.WorkUnit
	Action          *actuatortaskpb.Action
	OperationName   string
	ResumptionRunID string
	ResumptionTime  time.Time
}

type mockPublisher struct {
	calls     []enqueuedTaskCall
	err       error
	failAfter int
}

func (m *mockPublisher) EnqueueResumedWorkUnitTask(ctx context.Context, target string, workUnit *essmsgpb.WorkUnit, action *actuatortaskpb.Action, operationName string, runID string, resumptionTime time.Time) error {
	if m.err != nil && len(m.calls) >= m.failAfter {
		return m.err
	}
	m.calls = append(m.calls, enqueuedTaskCall{
		Ctx:             ctx,
		Target:          target,
		WorkUnit:        workUnit,
		Action:          action,
		OperationName:   operationName,
		ResumptionRunID: runID,
		ResumptionTime:  resumptionTime,
	})
	return nil
}

// mockTaskQueue combines the publisher and backlog mocks into a TaskQueue.
type mockTaskQueue struct {
	*mockPublisher
	*mockBacklogCalculator
}

// newMockTaskQueue builds a TaskQueue; a nil backlog calculator reports an empty queue.
func newMockTaskQueue(publisher *mockPublisher, backlogCalc *mockBacklogCalculator) *mockTaskQueue {
	if backlogCalc == nil {
		backlogCalc = &mockBacklogCalculator{}
	}
	return &mockTaskQueue{mockPublisher: publisher, mockBacklogCalculator: backlogCalc}
}

type errPausedTaskRepo struct {
	*storagetest.FakePausedTaskRepository
	markProcessedErr error
	listPendingErr   error
	onMarkProcessed  func()
}

func (r *errPausedTaskRepo) ListPending(ctx context.Context, limit int) ([]*storage.PausedTask, error) {
	if r.listPendingErr != nil {
		return nil, r.listPendingErr
	}
	return r.FakePausedTaskRepository.ListPending(ctx, limit)
}

func (r *errPausedTaskRepo) MarkProcessed(ctx context.Context, resolutions []storage.PausedTaskResolution) error {
	if r.markProcessedErr != nil {
		return r.markProcessedErr
	}
	err := r.FakePausedTaskRepository.MarkProcessed(ctx, resolutions)
	if r.onMarkProcessed != nil {
		r.onMarkProcessed()
	}
	return err
}

func testValidTask(wid int64, taskID string, priority int64, createTime time.Time) *storage.PausedTask {
	return &storage.PausedTask{
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
		initialState   *storage.PauseSchedulingState
		triggerRunID   string
		wantRegistered bool
	}{
		{
			name: "exits gracefully when scheduling is paused",
			initialState: &storage.PauseSchedulingState{
				SchedulingPaused: true,
				ResumptionRunID:  "run-1",
			},
			triggerRunID:   "run-1",
			wantRegistered: false,
		},
		{
			name: "exits gracefully when run ID does not match",
			initialState: &storage.PauseSchedulingState{
				SchedulingPaused: false,
				ResumptionRunID:  "run-current",
			},
			triggerRunID:   "run-stale",
			wantRegistered: false,
		},
		{
			name: "overwrites previous worker ID when claiming run to fence out prior worker",
			initialState: &storage.PauseSchedulingState{
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
			pauseRepo := storagetest.NewFakePauseStateRepositoryWithState(tt.initialState)
			taskRepo := storagetest.NewFakePausedTaskRepository()
			publisher := &mockPublisher{}
			worker := NewResumptionWorker(&storage.MockStorage{PauseStateRepository: pauseRepo, PausedTaskRepository: taskRepo}, newMockTaskQueue(publisher, nil), redisclient.NewFakeClient())

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
	pauseRepo := storagetest.NewFakePauseStateRepositoryWithState(&storage.PauseSchedulingState{
		SchedulingPaused: false,
		ResumptionRunID:  "run-1",
	})
	taskRepo := storagetest.NewFakePausedTaskRepository()
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

	_ = taskRepo.Create(ctx, &storage.PausedTask{
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
	_ = taskRepo.Create(ctx, &storage.PausedTask{
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

	worker := NewResumptionWorker(&storage.MockStorage{PauseStateRepository: pauseRepo, PausedTaskRepository: taskRepo}, newMockTaskQueue(publisher, nil), redisclient.NewFakeClient())
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

	state, _ := pauseRepo.Get(ctx)
	wantResumptionTime := state.UpdatedAt
	for i, call := range publisher.calls {
		if call.ResumptionRunID != "run-1" {
			t.Errorf("call %d ResumptionRunID = %q, want run-1", i, call.ResumptionRunID)
		}
		if !call.ResumptionTime.Equal(wantResumptionTime) {
			t.Errorf("call %d ResumptionTime = %v, want %v", i, call.ResumptionTime, wantResumptionTime)
		}
	}

	pending, _ := taskRepo.ListPending(ctx, 0)
	if len(pending) != 0 {
		t.Errorf("expected 0 pending tasks, got %d", len(pending))
	}

	allTasks, _ := taskRepo.List(ctx)
	for _, task := range allTasks {
		if task.ProcessedTime == nil || task.ResolutionStatus != storage.ResolutionStatusReplayed || task.ResumptionRunID != "run-1" {
			t.Errorf("unexpected processed task state: %+v", task)
		}
	}
}

func TestResumptionWorker_Filtering(t *testing.T) {
	ctx := context.Background()
	pauseRepo := storagetest.NewFakePauseStateRepositoryWithState(&storage.PauseSchedulingState{
		SchedulingPaused: false,
		ResumptionRunID:  "run-filter",
	})
	taskRepo := storagetest.NewFakePausedTaskRepository()
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
	_ = taskRepo.Create(ctx, &storage.PausedTask{
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
	_ = taskRepo.Create(ctx, &storage.PausedTask{
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
	_ = taskRepo.Create(ctx, &storage.PausedTask{
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
	_ = taskRepo.Create(ctx, &storage.PausedTask{
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
	_ = taskRepo.Create(ctx, &storage.PausedTask{
		XID:           1,
		WID:           5,
		TaskID:        "task-valid-replay",
		Target:        "experiments/1/workUnits/5/executions/1",
		Action:        &actuatortaskpb.Action{Action: &actuatortaskpb.Action_Create{Create: &actuatortaskpb.CreateAction{}}},
		OperationName: "op-create-5",
		Priority:      int64(essmsgpb.Priority_NORMAL),
		WorkUnit:      wuValid,
	})

	worker := NewResumptionWorker(&storage.MockStorage{PauseStateRepository: pauseRepo, PausedTaskRepository: taskRepo}, newMockTaskQueue(publisher, nil), redisclient.NewFakeClient())
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
	statusByTaskID := make(map[string]storage.ResolutionStatus, len(allTasks))
	for _, task := range allTasks {
		if task.ProcessedTime == nil || task.ResumptionRunID != "run-filter" {
			t.Errorf("task %s not properly marked processed: %+v", task.TaskID, task)
		}
		statusByTaskID[task.TaskID] = task.ResolutionStatus
	}

	wantStatuses := map[string]storage.ResolutionStatus{
		"task-rule1-stale-exec":        storage.ResolutionStatusSkipped,
		"task-rule2-update-paused":     storage.ResolutionStatusSkipped,
		"task-rule3-priority-mismatch": storage.ResolutionStatusSkipped,
		"task-rule4-terminal-stopped":  storage.ResolutionStatusSkipped,
		"task-valid-replay":            storage.ResolutionStatusReplayed,
	}

	if diff := cmp.Diff(wantStatuses, statusByTaskID); diff != "" {
		t.Errorf("resolution statuses mismatch (-want +got):\n%s", diff)
	}
}

func TestResumptionWorker_MultipleBatches(t *testing.T) {
	ctx := context.Background()
	pauseRepo := storagetest.NewFakePauseStateRepositoryWithState(&storage.PauseSchedulingState{ResumptionRunID: "run-multi"})
	taskRepo := storagetest.NewFakePausedTaskRepository()
	publisher := &mockPublisher{}

	for i := 1; i <= 5; i++ {
		_ = taskRepo.Create(ctx, testValidTask(
			int64(i),
			fmt.Sprintf("task-%d", i),
			int64(i*10),
			time.Now().Add(time.Duration(i)*time.Minute),
		))
	}

	worker := NewResumptionWorker(&storage.MockStorage{PauseStateRepository: pauseRepo, PausedTaskRepository: taskRepo}, newMockTaskQueue(publisher, nil), redisclient.NewFakeClient())
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

func TestResumptionWorker_ExtendsClaimAfterEachBatch(t *testing.T) {
	tests := []struct {
		name      string
		extendErr error
	}{
		{name: "extension succeeds"},
		{name: "extension failure is not fatal", extendErr: errors.New("redis unavailable")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			pauseRepo := storagetest.NewFakePauseStateRepositoryWithState(&storage.PauseSchedulingState{ResumptionRunID: "run-1"})
			taskRepo := storagetest.NewFakePausedTaskRepository()
			for i := 1; i <= 3; i++ {
				_ = taskRepo.Create(ctx, testValidTask(int64(i), fmt.Sprintf("task-%d", i), 100, time.Now()))
			}
			publisher := &mockPublisher{}
			claimExtender := redisclient.NewFakeClient()
			claimExtender.ExtendClaimError = tt.extendErr

			worker := NewResumptionWorker(&storage.MockStorage{PauseStateRepository: pauseRepo, PausedTaskRepository: taskRepo}, newMockTaskQueue(publisher, nil), claimExtender)
			if err := worker.StartResumption(ctx, Request{ResumptionRunID: "run-1", BatchSize: 1}); err != nil {
				t.Fatalf("StartResumption() unexpected error: %v", err)
			}

			if claimExtender.ExtendClaimCalls != 3 {
				t.Errorf("ExtendClaimCalls = %d, want 3", claimExtender.ExtendClaimCalls)
			}
			if len(publisher.calls) != 3 {
				t.Errorf("expected 3 publisher calls, got %d", len(publisher.calls))
			}
		})
	}
}

func TestResumptionWorker_Errors(t *testing.T) {
	testErr := errors.New("injected failure")

	tests := []struct {
		name       string
		cancelCtx  bool
		txErr      error
		getErr     error
		setErr     error
		listErr    error
		publishErr error
		markErr    error
		wantErr    error
	}{
		{name: "registration transaction fails", txErr: testErr, wantErr: testErr},
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

			pauseRepo := storagetest.NewFakePauseStateRepositoryWithState(&storage.PauseSchedulingState{ResumptionRunID: "run-1"})
			pauseRepo.GetErr = tt.getErr
			pauseRepo.SetErr = tt.setErr

			baseTaskRepo := storagetest.NewFakePausedTaskRepository()
			_ = baseTaskRepo.Create(context.Background(), testValidTask(1, "task-1", 100, time.Now()))
			taskRepo := &errPausedTaskRepo{FakePausedTaskRepository: baseTaskRepo, listPendingErr: tt.listErr, markProcessedErr: tt.markErr}
			publisher := &mockPublisher{err: tt.publishErr}

			store := &storage.MockStorage{RunInTransactionErr: tt.txErr, PauseStateRepository: pauseRepo, PausedTaskRepository: taskRepo}
			worker := NewResumptionWorker(store, newMockTaskQueue(publisher, nil), redisclient.NewFakeClient())
			err := worker.StartResumption(ctx, Request{ResumptionRunID: "run-1", BatchSize: 10})
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("StartResumption() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestResumptionWorker_PartialBatchPublishFailure(t *testing.T) {
	ctx := context.Background()
	pauseRepo := storagetest.NewFakePauseStateRepositoryWithState(&storage.PauseSchedulingState{ResumptionRunID: "run-1"})

	taskRepo := storagetest.NewFakePausedTaskRepository()
	for i := 1; i <= 2; i++ {
		_ = taskRepo.Create(ctx, testValidTask(int64(i), fmt.Sprintf("task-%d", i), 100, time.Now()))
	}

	publishErr := errors.New("redis publish failure")
	publisher := &mockPublisher{err: publishErr, failAfter: 1}
	worker := NewResumptionWorker(&storage.MockStorage{PauseStateRepository: pauseRepo, PausedTaskRepository: taskRepo}, newMockTaskQueue(publisher, nil), redisclient.NewFakeClient())

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
		mutate func(state *storage.PauseSchedulingState)
	}{
		{
			name: "halts after batch when scheduling is re-paused",
			mutate: func(state *storage.PauseSchedulingState) {
				state.SchedulingPaused = true
			},
		},
		{
			name: "halts after batch when resumption run ID changes",
			mutate: func(state *storage.PauseSchedulingState) {
				state.ResumptionRunID = "run-newer"
			},
		},
		{
			name: "halts after batch when another worker claims ResumptionWorkerID",
			mutate: func(state *storage.PauseSchedulingState) {
				state.ResumptionWorkerID = "worker-other"
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			pauseRepo := storagetest.NewFakePauseStateRepositoryWithState(&storage.PauseSchedulingState{ResumptionRunID: "run-1"})

			baseTaskRepo := storagetest.NewFakePausedTaskRepository()
			for i := 1; i <= 4; i++ {
				_ = baseTaskRepo.Create(ctx, testValidTask(int64(i), fmt.Sprintf("task-%d", i), 100, time.Now()))
			}

			taskRepo := &errPausedTaskRepo{
				FakePausedTaskRepository: baseTaskRepo,
				onMarkProcessed: func() {
					state, _ := pauseRepo.Get(ctx)
					tt.mutate(state)
					_ = pauseRepo.Set(ctx, state)
				},
			}
			publisher := &mockPublisher{}
			worker := NewResumptionWorker(&storage.MockStorage{PauseStateRepository: pauseRepo, PausedTaskRepository: taskRepo}, newMockTaskQueue(publisher, nil), redisclient.NewFakeClient())

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
	pauseRepo := storagetest.NewFakePauseStateRepositoryWithState(&storage.PauseSchedulingState{ResumptionRunID: "run-invalid"})

	taskRepo := storagetest.NewFakePausedTaskRepository()
	// Task 1: Valid task
	_ = taskRepo.Create(ctx, testValidTask(1, "task-1", 100, time.Now()))
	// Task 2: Corrupted task (missing WorkUnit)
	_ = taskRepo.Create(ctx, &storage.PausedTask{
		XID:      1,
		WID:      2,
		TaskID:   "task-corrupt",
		Target:   "experiments/1/workUnits/2/executions/1",
		Action:   &actuatortaskpb.Action{Action: &actuatortaskpb.Action_Create{Create: &actuatortaskpb.CreateAction{}}},
		WorkUnit: nil,
	})

	publisher := &mockPublisher{}
	worker := NewResumptionWorker(&storage.MockStorage{PauseStateRepository: pauseRepo, PausedTaskRepository: taskRepo}, newMockTaskQueue(publisher, nil), redisclient.NewFakeClient())

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
	recordedGroup string
	onCalculate   func()
}

func (m *mockBacklogCalculator) CalculateBacklogLength(ctx context.Context, group string, maxThreshold int) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	m.recordedMax = maxThreshold
	m.recordedGroup = group

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

const throttleTestRunID = "run-throttle"

// newThrottleTestWorker returns a worker with a single pending task and a 1ms throttle pause.
func newThrottleTestWorker(t *testing.T, backlogCalc *mockBacklogCalculator) (*ResumptionWorker, *mockPublisher, *storagetest.FakePauseStateRepository) {
	t.Helper()
	ctx := context.Background()

	pauseRepo := storagetest.NewFakePauseStateRepositoryWithState(&storage.PauseSchedulingState{ResumptionRunID: throttleTestRunID})

	taskRepo := storagetest.NewFakePausedTaskRepository()
	_ = taskRepo.Create(ctx, testValidTask(1, "task-1", 100, time.Now()))

	publisher := &mockPublisher{}
	worker := NewResumptionWorker(&storage.MockStorage{PauseStateRepository: pauseRepo, PausedTaskRepository: taskRepo}, newMockTaskQueue(publisher, backlogCalc), redisclient.NewFakeClient())
	worker.throttlePauseDuration = time.Millisecond
	return worker, publisher, pauseRepo
}

func throttleTestRequest(threshold int) Request {
	return Request{ResumptionRunID: throttleTestRunID, BatchSize: 10, MaxBacklogThreshold: threshold}
}

func TestResumptionWorker_BacklogThrottleDecision(t *testing.T) {
	// Checker calls: a throttled run checks once while busy, once before publishing,
	// and once before the final empty fetch (3); an unthrottled run skips the first (2).
	// Claim extensions: one per throttle pause plus one after the single batch.
	tests := []struct {
		name            string
		backlogValues   []int64
		threshold       int
		wantCalls       int
		wantMax         int
		wantExtendCalls int
	}{
		{name: "above threshold throttles once", backlogValues: []int64{10, 2}, threshold: 5, wantCalls: 3, wantMax: 5, wantExtendCalls: 2},
		{name: "equal to threshold throttles once", backlogValues: []int64{5, 0}, threshold: 5, wantCalls: 3, wantMax: 5, wantExtendCalls: 2},
		{name: "below threshold does not throttle", backlogValues: []int64{2}, threshold: 5, wantCalls: 2, wantMax: 5, wantExtendCalls: 1},
		{name: "zero threshold falls back to default", threshold: 0, wantCalls: 2, wantMax: defaultMaxBacklog, wantExtendCalls: 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			backlogCalc := &mockBacklogCalculator{backlogValues: tc.backlogValues}
			worker, publisher, _ := newThrottleTestWorker(t, backlogCalc)
			claimExtender := redisclient.NewFakeClient()
			worker.claimExtender = claimExtender

			if err := worker.StartResumption(context.Background(), throttleTestRequest(tc.threshold)); err != nil {
				t.Fatalf("StartResumption() unexpected error: %v", err)
			}
			if backlogCalc.calls != tc.wantCalls {
				t.Errorf("backlogCalc.calls = %d, want %d", backlogCalc.calls, tc.wantCalls)
			}
			if backlogCalc.recordedMax != tc.wantMax {
				t.Errorf("recordedMax = %d, want %d", backlogCalc.recordedMax, tc.wantMax)
			}
			if backlogCalc.recordedGroup != constants.RedisGroup {
				t.Errorf("recordedGroup = %q, want %q", backlogCalc.recordedGroup, constants.RedisGroup)
			}
			if len(publisher.calls) != 1 {
				t.Errorf("expected 1 published task, got %d", len(publisher.calls))
			}
			if claimExtender.ExtendClaimCalls != tc.wantExtendCalls {
				t.Errorf("ExtendClaimCalls = %d, want %d", claimExtender.ExtendClaimCalls, tc.wantExtendCalls)
			}
		})
	}
}

func TestResumptionWorker_BacklogThrottling_CalculatorError(t *testing.T) {
	calcErr := errors.New("redis connection refused")
	worker, publisher, _ := newThrottleTestWorker(t, &mockBacklogCalculator{calculateErr: calcErr})

	err := worker.StartResumption(context.Background(), throttleTestRequest(5))
	if !errors.Is(err, calcErr) {
		t.Fatalf("StartResumption() error = %v, want %v", err, calcErr)
	}
	if len(publisher.calls) != 0 {
		t.Errorf("expected 0 published tasks on error, got %d", len(publisher.calls))
	}
}

func TestResumptionWorker_BacklogThrottling_ContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	backlogCalc := &mockBacklogCalculator{backlogValues: []int64{10}, onCalculate: cancel}
	worker, publisher, _ := newThrottleTestWorker(t, backlogCalc)
	worker.throttlePauseDuration = time.Hour

	err := worker.StartResumption(ctx, throttleTestRequest(5))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("StartResumption() error = %v, want %v", err, context.Canceled)
	}
	if len(publisher.calls) != 0 {
		t.Errorf("expected 0 published tasks on context cancellation, got %d", len(publisher.calls))
	}
}

func TestResumptionWorker_BacklogThrottling_SchedulingRepaused(t *testing.T) {
	backlogCalc := &mockBacklogCalculator{backlogValues: []int64{10}}
	worker, publisher, pauseRepo := newThrottleTestWorker(t, backlogCalc)
	backlogCalc.onCalculate = func() {
		_ = pauseRepo.Set(context.Background(), &storage.PauseSchedulingState{
			SchedulingPaused: true,
			ResumptionRunID:  throttleTestRunID,
		})
	}

	if err := worker.StartResumption(context.Background(), throttleTestRequest(5)); err != nil {
		t.Fatalf("StartResumption() unexpected error: %v", err)
	}
	if len(publisher.calls) != 0 {
		t.Errorf("expected 0 published tasks when re-paused, got %d", len(publisher.calls))
	}
}

func TestResumptionWorker_BacklogThrottling_WorkerFenced(t *testing.T) {
	backlogCalc := &mockBacklogCalculator{backlogValues: []int64{10}}
	worker, publisher, pauseRepo := newThrottleTestWorker(t, backlogCalc)
	backlogCalc.onCalculate = func() {
		_ = pauseRepo.Set(context.Background(), &storage.PauseSchedulingState{
			ResumptionRunID:    throttleTestRunID,
			ResumptionWorkerID: "worker-fenced-other",
		})
	}

	if err := worker.StartResumption(context.Background(), throttleTestRequest(5)); err != nil {
		t.Fatalf("StartResumption() unexpected error: %v", err)
	}
	if len(publisher.calls) != 0 {
		t.Errorf("expected 0 published tasks when fenced, got %d", len(publisher.calls))
	}
}

func TestResumptionWorker_Metrics(t *testing.T) {
	ctx := context.Background()

	t.Run("records replayed, skipped_stopped, and skipped_obsolete task counters by action", func(t *testing.T) {
		resumptionTasksTotal.Reset()
		resumptionErrorsTotal.Reset()

		pauseRepo := storagetest.NewFakePauseStateRepositoryWithState(&storage.PauseSchedulingState{
			SchedulingPaused: false,
			ResumptionRunID:  "run-metrics",
		})
		taskRepo := storagetest.NewFakePausedTaskRepository()
		publisher := &mockPublisher{}

		// 1. Create -> replayed
		_ = taskRepo.Create(ctx, testValidTask(1, "task-create-replayed", int64(essmsgpb.Priority_NORMAL), time.Now()))

		// 2. Restart -> skipped_stopped (terminal state)
		_ = taskRepo.Create(ctx, &storage.PausedTask{
			XID:      1,
			WID:      2,
			TaskID:   "task-restart-stopped",
			Target:   "experiments/1/workUnits/2/executions/1",
			Action:   &actuatortaskpb.Action{Action: &actuatortaskpb.Action_Restart{Restart: &actuatortaskpb.RestartAction{}}},
			Priority: int64(essmsgpb.Priority_NORMAL),
			WorkUnit: &essmsgpb.WorkUnit{
				Name:     "experiments/1/workUnits/2",
				Priority: essmsgpb.Priority_NORMAL,
				CurrentWorkUnitExecution: &essmsgpb.WorkUnitExecution{
					Name: "experiments/1/workUnits/2/executions/1",
					Status: &essmsgpb.WorkUnitStatus{
						State: essmsgpb.WorkUnitStatus_STOPPED,
					},
				},
			},
		})

		// 3. Update -> skipped_obsolete (priority mismatch)
		_ = taskRepo.Create(ctx, &storage.PausedTask{
			XID:      1,
			WID:      3,
			TaskID:   "task-update-obsolete",
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

		worker := NewResumptionWorker(&storage.MockStorage{PauseStateRepository: pauseRepo, PausedTaskRepository: taskRepo}, newMockTaskQueue(publisher, nil), redisclient.NewFakeClient())
		if err := worker.StartResumption(ctx, Request{ResumptionRunID: "run-metrics", BatchSize: 10}); err != nil {
			t.Fatalf("StartResumption() unexpected error: %v", err)
		}

		if got := testutil.ToFloat64(resumptionTasksTotal.WithLabelValues("create", "replayed")); got != 1 {
			t.Errorf("xmc_resumption_tasks_total{action=create,result=replayed} = %v, want 1", got)
		}
		if got := testutil.ToFloat64(resumptionTasksTotal.WithLabelValues("restart", "skipped_stopped")); got != 1 {
			t.Errorf("xmc_resumption_tasks_total{action=restart,result=skipped_stopped} = %v, want 1", got)
		}
		if got := testutil.ToFloat64(resumptionTasksTotal.WithLabelValues("update", "skipped_obsolete")); got != 1 {
			t.Errorf("xmc_resumption_tasks_total{action=update,result=skipped_obsolete} = %v, want 1", got)
		}
	})

	t.Run("increments throttles counter when backlog meets or exceeds threshold", func(t *testing.T) {
		before := testutil.ToFloat64(resumptionThrottlesTotal)
		backlogCalc := &mockBacklogCalculator{backlogValues: []int64{10, 5, 1}}
		worker, _, _ := newThrottleTestWorker(t, backlogCalc)

		if err := worker.StartResumption(ctx, throttleTestRequest(5)); err != nil {
			t.Fatalf("StartResumption() unexpected error: %v", err)
		}

		after := testutil.ToFloat64(resumptionThrottlesTotal)
		if delta := after - before; delta != 2 {
			t.Errorf("xmc_resumption_throttles_total delta = %v, want 2", delta)
		}
	})

	t.Run("records fetch, publish, and commit stage errors", func(t *testing.T) {
		resumptionErrorsTotal.Reset()

		// 1. Fetch error
		pauseRepo := storagetest.NewFakePauseStateRepositoryWithState(&storage.PauseSchedulingState{ResumptionRunID: "run-err"})
		fetchErrRepo := &errPausedTaskRepo{FakePausedTaskRepository: storagetest.NewFakePausedTaskRepository(), listPendingErr: errors.New("fetch err")}
		workerFetch := NewResumptionWorker(&storage.MockStorage{PauseStateRepository: pauseRepo, PausedTaskRepository: fetchErrRepo}, newMockTaskQueue(&mockPublisher{}, nil), redisclient.NewFakeClient())
		_ = workerFetch.StartResumption(ctx, Request{ResumptionRunID: "run-err", BatchSize: 10})
		if got := testutil.ToFloat64(resumptionErrorsTotal.WithLabelValues("fetch")); got != 1 {
			t.Errorf("xmc_resumption_errors_total{stage=fetch} = %v, want 1", got)
		}

		// 2. Publish error
		pubTaskRepo := storagetest.NewFakePausedTaskRepository()
		_ = pubTaskRepo.Create(ctx, testValidTask(1, "task-1", 100, time.Now()))
		workerPublish := NewResumptionWorker(&storage.MockStorage{PauseStateRepository: pauseRepo, PausedTaskRepository: pubTaskRepo}, newMockTaskQueue(&mockPublisher{err: errors.New("publish err")}, nil), redisclient.NewFakeClient())
		_ = workerPublish.StartResumption(ctx, Request{ResumptionRunID: "run-err", BatchSize: 10})
		if got := testutil.ToFloat64(resumptionErrorsTotal.WithLabelValues("publish")); got != 1 {
			t.Errorf("xmc_resumption_errors_total{stage=publish} = %v, want 1", got)
		}

		// 3. Commit error
		commitTaskRepo := &errPausedTaskRepo{FakePausedTaskRepository: pubTaskRepo, markProcessedErr: errors.New("commit err")}
		workerCommit := NewResumptionWorker(&storage.MockStorage{PauseStateRepository: pauseRepo, PausedTaskRepository: commitTaskRepo}, newMockTaskQueue(&mockPublisher{}, nil), redisclient.NewFakeClient())
		_ = workerCommit.StartResumption(ctx, Request{ResumptionRunID: "run-err", BatchSize: 10})
		if got := testutil.ToFloat64(resumptionErrorsTotal.WithLabelValues("commit")); got != 1 {
			t.Errorf("xmc_resumption_errors_total{stage=commit} = %v, want 1", got)
		}
	})
}
