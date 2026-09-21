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
	"testing"
	"time"

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
	calls []enqueuedTaskCall
	err   error
}

func (m *mockPublisher) EnqueueWorkUnitTask(ctx context.Context, target string, workUnit *essmsgpb.WorkUnit, action *actuatortaskpb.Action, operationName string) error {
	if m.err != nil {
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
	return r.Fake.MarkProcessed(ctx, resolutions)
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
			worker := NewResumptionWorker(pauseRepo, taskRepo, publisher)

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

	_ = taskRepo.Create(ctx, &pausedtask.PausedTask{
		XID:           1,
		WID:           1,
		TaskID:        "task-1",
		TraceID:       "trace-op-1",
		Target:        "target-1",
		Action:        &actuatortaskpb.Action{Action: &actuatortaskpb.Action_Create{Create: &actuatortaskpb.CreateAction{}}},
		OperationName: "op-create",
		Priority:      100,
	})
	_ = taskRepo.Create(ctx, &pausedtask.PausedTask{
		XID:           1,
		WID:           2,
		TaskID:        "task-2",
		TraceID:       "trace-op-2",
		Target:        "target-2",
		Action:        &actuatortaskpb.Action{Action: &actuatortaskpb.Action_Delete{Delete: &actuatortaskpb.DeleteAction{}}},
		OperationName: "op-delete",
		Priority:      90,
	})

	worker := NewResumptionWorker(pauseRepo, taskRepo, publisher)
	if err := worker.StartResumption(ctx, Request{ResumptionRunID: "run-1", BatchSize: 10}); err != nil {
		t.Fatalf("StartResumption() unexpected error: %v", err)
	}

	if len(publisher.calls) != 2 {
		t.Fatalf("expected 2 publisher calls, got %d", len(publisher.calls))
	}
	if publisher.calls[0].Target != "target-1" || publisher.calls[0].OperationName != "op-create" {
		t.Errorf("call 0 mismatch: %+v", publisher.calls[0])
	}
	if opID, ok := logging.OperationID(publisher.calls[0].Ctx); !ok || opID != "trace-op-1" {
		t.Errorf("call 0 trace ID = %q (ok=%v), want trace-op-1", opID, ok)
	}
	if publisher.calls[1].Target != "target-2" || publisher.calls[1].OperationName != "op-delete" {
		t.Errorf("call 1 mismatch: %+v", publisher.calls[1])
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

func TestResumptionWorker_MultipleBatches(t *testing.T) {
	ctx := context.Background()
	pauseRepo := pausestate.NewFake()
	_ = pauseRepo.Set(ctx, &pausestate.PauseSchedulingState{ResumptionRunID: "run-multi"})
	taskRepo := pausedtask.NewFake()
	publisher := &mockPublisher{}

	for i := 1; i <= 5; i++ {
		_ = taskRepo.Create(ctx, &pausedtask.PausedTask{
			XID:        1,
			WID:        int64(i),
			TaskID:     "task-" + string(rune('0'+i)),
			Target:     "target-1",
			Action:     &actuatortaskpb.Action{},
			Priority:   int64(i * 10),
			CreateTime: time.Now().Add(time.Duration(i) * time.Minute),
		})
	}

	worker := NewResumptionWorker(pauseRepo, taskRepo, publisher)
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
			_ = baseTaskRepo.Create(context.Background(), &pausedtask.PausedTask{
				XID:    1,
				WID:    1,
				TaskID: "task-1",
				Action: &actuatortaskpb.Action{},
			})
			taskRepo := &errPausedTaskRepo{Fake: baseTaskRepo, listPendingErr: tt.listErr, markProcessedErr: tt.markErr}
			publisher := &mockPublisher{err: tt.publishErr}

			worker := NewResumptionWorker(pauseRepo, taskRepo, publisher)
			err := worker.StartResumption(ctx, Request{ResumptionRunID: "run-1", BatchSize: 10})
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("StartResumption() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}
