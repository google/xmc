// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package pausescheduling

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/proto"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/storage"
	actuatortaskpb "xmanager_cloud/task_queue/proto/task_go_proto"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// mockPausedTaskRepo overrides Create; the embedded PausedTaskTxRepository is nil and must not be called.
type mockPausedTaskRepo struct {
	storage.PausedTaskTxRepository
	err         error
	storedTasks []*storage.PausedTask
}

func (m *mockPausedTaskRepo) Create(ctx context.Context, task *storage.PausedTask) error {
	if m.err != nil {
		return m.err
	}
	m.storedTasks = append(m.storedTasks, task)
	return nil
}

func TestNewPauseHandler(t *testing.T) {
	cache := NewCache()
	txRunner := &storage.MockStorage{}

	t.Run("nil_cache", func(t *testing.T) {
		h, err := NewPauseHandler(nil, txRunner)
		if err == nil || h != nil {
			t.Errorf("expected error for nil cache, got %v", err)
		}
	})

	t.Run("nil_tx_runner", func(t *testing.T) {
		h, err := NewPauseHandler(cache, nil)
		if err == nil || h != nil {
			t.Errorf("expected error for nil transaction runner, got %v", err)
		}
	})

	t.Run("valid_construction", func(t *testing.T) {
		h, err := NewPauseHandler(cache, txRunner)
		if err != nil || h == nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestTaskPauseHandler_IsPaused(t *testing.T) {
	cache := NewCache()

	h, err := NewPauseHandler(cache, &storage.MockStorage{})
	if err != nil {
		t.Fatalf("failed to create handler: %v", err)
	}

	if h.IsPaused(nil) {
		t.Errorf("expected IsPaused() = false initially, got true")
	}

	cache.Update(State{IsPaused: true, UpdatedAt: time.Now()})
	if !h.IsPaused(nil) {
		t.Errorf("expected IsPaused() = true after update, got false")
	}

	cache.Update(State{IsPaused: false, UpdatedAt: time.Now()})
	if h.IsPaused(nil) {
		t.Errorf("expected IsPaused() = false after unpause, got true")
	}

	var nilHandler *TaskPauseHandler
	if nilHandler.IsPaused(nil) {
		t.Errorf("expected nilHandler.IsPaused() = false, got true")
	}
}

func TestTaskPauseHandler_ShouldPause(t *testing.T) {
	h, err := NewPauseHandler(NewCache(), &storage.MockStorage{})
	if err != nil {
		t.Fatalf("failed to create handler: %v", err)
	}

	tests := []struct {
		name       string
		action     *actuatortaskpb.Action
		wantResult bool
	}{
		{
			name:       "nil_action",
			action:     nil,
			wantResult: false,
		},
		{
			name: "create_action",
			action: &actuatortaskpb.Action{
				Action: &actuatortaskpb.Action_Create{Create: &actuatortaskpb.CreateAction{}},
			},
			wantResult: true,
		},
		{
			name: "restart_action",
			action: &actuatortaskpb.Action{
				Action: &actuatortaskpb.Action_Restart{Restart: &actuatortaskpb.RestartAction{}},
			},
			wantResult: true,
		},
		{
			name: "update_action",
			action: &actuatortaskpb.Action{
				Action: &actuatortaskpb.Action_Update{Update: &actuatortaskpb.UpdateAction{}},
			},
			wantResult: true,
		},
		{
			name: "delete_action",
			action: &actuatortaskpb.Action{
				Action: &actuatortaskpb.Action_Delete{Delete: &actuatortaskpb.DeleteAction{}},
			},
			wantResult: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := h.ShouldPause(tc.action); got != tc.wantResult {
				t.Errorf("ShouldPause() = %v, want %v", got, tc.wantResult)
			}
		})
	}
}

func TestHandlePause(t *testing.T) {
	tests := []struct {
		name                   string
		payload                *actuatortaskpb.ActuatorPayload
		repoErr                error
		updateStatusErr        error
		expectError            bool
		expectBadRequest       bool
		expectedBufferedTarget string
		// expectedStatusTarget is the execution whose status is updated; empty means no status update.
		expectedStatusTarget string
	}{
		{
			name: "nil_action_error",
			payload: &actuatortaskpb.ActuatorPayload{
				Target: proto.String("experiments/100/workUnits/200/executions/1"),
			},
			expectError: true,
		},
		{
			name: "delete_action_rejected_by_handle_pause",
			payload: &actuatortaskpb.ActuatorPayload{
				Target: proto.String("experiments/100/workUnits/200/executions/1"),
				Action: &actuatortaskpb.Action{
					Action: &actuatortaskpb.Action_Delete{Delete: &actuatortaskpb.DeleteAction{}},
				},
				WorkUnit: &essmsgpb.WorkUnit{Name: "experiments/100/workUnits/200"},
			},
			expectError:      true,
			expectBadRequest: true,
		},
		{
			name: "create_action_buffered_and_status_updated",
			payload: &actuatortaskpb.ActuatorPayload{
				Target: proto.String("experiments/100/workUnits/200/executions/1"),
				Action: &actuatortaskpb.Action{
					Action: &actuatortaskpb.Action_Create{Create: &actuatortaskpb.CreateAction{
						ExperimentPriority: essmsgpb.Priority_NORMAL.Enum(),
						Author:             proto.String("user@google.com"),
					}},
				},
				WorkUnit:   &essmsgpb.WorkUnit{Name: "experiments/100/workUnits/200", Priority: 50},
				Metadata:   &actuatortaskpb.Metadata{IdempotencyKey: proto.String("create-task-123"), OperationName: proto.String("op-1")},
				CreateTime: timestamppb.Now(),
			},
			expectedBufferedTarget: "experiments/100/workUnits/200/executions/1",
			expectedStatusTarget:   "experiments/100/workUnits/200/executions/1",
		},
		{
			name: "restart_action_buffered_with_old_target_and_status_updated_with_new_target",
			payload: &actuatortaskpb.ActuatorPayload{
				Target: proto.String("experiments/100/workUnits/200/executions/1"),
				Action: &actuatortaskpb.Action{
					Action: &actuatortaskpb.Action_Restart{Restart: &actuatortaskpb.RestartAction{
						ExperimentPriority: essmsgpb.Priority_NORMAL.Enum(),
						Author:             proto.String("user@google.com"),
					}},
				},
				WorkUnit: &essmsgpb.WorkUnit{
					Name: "experiments/100/workUnits/200",
					CurrentWorkUnitExecution: &essmsgpb.WorkUnitExecution{
						Name: "experiments/100/workUnits/200/executions/2",
					},
				},
				Metadata:   &actuatortaskpb.Metadata{IdempotencyKey: proto.String("restart-task-456")},
				CreateTime: timestamppb.Now(),
			},
			expectedBufferedTarget: "experiments/100/workUnits/200/executions/1",
			expectedStatusTarget:   "experiments/100/workUnits/200/executions/2",
		},
		{
			name: "update_action_buffered_without_status_update",
			payload: &actuatortaskpb.ActuatorPayload{
				Target: proto.String("experiments/100/workUnits/200/executions/1"),
				Action: &actuatortaskpb.Action{
					Action: &actuatortaskpb.Action_Update{Update: &actuatortaskpb.UpdateAction{
						ExperimentPriority: essmsgpb.Priority_HIGH.Enum(),
					}},
				},
				WorkUnit:   &essmsgpb.WorkUnit{Name: "experiments/100/workUnits/200"},
				Metadata:   &actuatortaskpb.Metadata{IdempotencyKey: proto.String("update-task-789")},
				CreateTime: timestamppb.Now(),
			},
			expectedBufferedTarget: "experiments/100/workUnits/200/executions/1",
		},
		{
			name: "invalid_target_name_error",
			payload: &actuatortaskpb.ActuatorPayload{
				Target: proto.String("invalid-target-format"),
				Action: &actuatortaskpb.Action{
					Action: &actuatortaskpb.Action_Create{Create: &actuatortaskpb.CreateAction{}},
				},
				WorkUnit: &essmsgpb.WorkUnit{Name: "experiments/100/workUnits/200"},
			},
			expectError: true,
		},
		{
			name:        "repo_error",
			payload:     newCreatePayload(),
			repoErr:     errors.New("spanner repo error"),
			expectError: true,
		},
		{
			name:            "update_status_error",
			payload:         newCreatePayload(),
			updateStatusErr: errors.New("spanner status update error"),
			expectError:     true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cache := NewCache()
			cache.Update(State{IsPaused: true, UpdatedAt: time.Now()})
			repo := &mockPausedTaskRepo{err: tc.repoErr}
			execName := resolveExecutionTarget(tc.payload)
			txRunner := &storage.MockStorage{
				WorkUnit: &essmsgpb.WorkUnit{
					Name: "experiments/100/workUnits/200",
					CurrentWorkUnitExecution: &essmsgpb.WorkUnitExecution{
						Name: execName,
					},
				},
				Experiment:                        &essmsgpb.Experiment{Name: "experiments/100"},
				UpdateCurrentWorkUnitExecutionErr: tc.updateStatusErr,
				Transaction:                       &storage.Transaction{PausedTasks: repo},
			}

			h, err := NewPauseHandler(cache, txRunner)
			if err != nil {
				t.Fatalf("failed to create handler: %v", err)
			}

			err = h.HandlePause(context.Background(), tc.payload, "test-trace-id")
			if (err != nil) != tc.expectError {
				t.Fatalf("HandlePause() error = %v, expectError = %v", err, tc.expectError)
			}
			if tc.expectBadRequest && !apierrors.IsBadRequest(err) {
				t.Errorf("HandlePause() error = %v, want BadRequest error", err)
			}
			if tc.expectError {
				if tc.repoErr != nil && len(txRunner.UpdatedWorkUnitExecutions) != 0 {
					t.Errorf("status should not be updated if repo fails, got %d updates", len(txRunner.UpdatedWorkUnitExecutions))
				}
				return
			}

			verifyBufferedTask(t, repo.storedTasks, tc.expectedBufferedTarget)
			verifyStatusUpdate(t, txRunner, tc.expectedStatusTarget)
		})
	}
}

func verifyBufferedTask(t *testing.T, storedTasks []*storage.PausedTask, wantTarget string) {
	t.Helper()
	if wantTarget == "" {
		return
	}
	if len(storedTasks) != 1 {
		t.Fatalf("expected 1 stored task, got %d", len(storedTasks))
	}
	task := storedTasks[0]
	if task.TaskID == "" {
		t.Errorf("expected buffered task to have non-empty TaskID")
	}
	if task.CreateTime.IsZero() {
		t.Errorf("expected buffered task to have non-zero CreateTime")
	}
	if task.Target != wantTarget {
		t.Errorf("buffered task target mismatch: got %q, want %q", task.Target, wantTarget)
	}
	if task.XID != 100 || task.WID != 200 {
		t.Errorf("buffered task XID/WID mismatch: got %d/%d, want 100/200", task.XID, task.WID)
	}
}

func newCreatePayload() *actuatortaskpb.ActuatorPayload {
	return &actuatortaskpb.ActuatorPayload{
		Target: proto.String("experiments/100/workUnits/200/executions/1"),
		Action: &actuatortaskpb.Action{
			Action: &actuatortaskpb.Action_Create{Create: &actuatortaskpb.CreateAction{}},
		},
		WorkUnit: &essmsgpb.WorkUnit{Name: "experiments/100/workUnits/200"},
	}
}

func verifyStatusUpdate(t *testing.T, got *storage.MockStorage, wantTarget string) {
	t.Helper()
	if wantTarget == "" {
		if len(got.UpdatedWorkUnitExecutions) != 0 {
			t.Errorf("expected 0 status updates, got %d", len(got.UpdatedWorkUnitExecutions))
		}
		return
	}
	if len(got.UpdatedWorkUnitExecutions) != 1 {
		t.Fatalf("expected 1 status update, got %d", len(got.UpdatedWorkUnitExecutions))
	}
	exec, ok := got.UpdatedWorkUnitExecutions[200]
	if !ok {
		t.Fatalf("expected status update for WID 200, got %v", got.UpdatedWorkUnitExecutions)
	}
	if exec.GetName() != wantTarget {
		t.Errorf("status update target mismatch: got %q, want %q", exec.GetName(), wantTarget)
	}
	if exec.GetStatus().GetState() != essmsgpb.WorkUnitStatus_SCHEDULING_PAUSED {
		t.Errorf("status state mismatch: got %v, want SCHEDULING_PAUSED", exec.GetStatus().GetState())
	}
	if exec.GetStatus().GetStateUpdateTime() == nil {
		t.Errorf("status state_update_time must be set when updating status.state")
	}
}

func TestTaskPauseHandler_IsPaused_ResumptionTime(t *testing.T) {
	pauseTime := time.Date(2026, 9, 24, 14, 0, 0, 0, time.UTC)
	staleTime := time.Date(2026, 9, 24, 13, 0, 0, 0, time.UTC)
	newerTime := time.Date(2026, 9, 24, 14, 5, 0, 0, time.UTC)

	tests := []struct {
		name      string
		initState State
		meta      *actuatortaskpb.Metadata
		wantState State
	}{
		{
			name:      "nil_metadata_remains_paused",
			initState: State{IsPaused: true, UpdatedAt: pauseTime},
			meta:      nil,
			wantState: State{IsPaused: true, UpdatedAt: pauseTime},
		},
		{
			name:      "nil_resumption_time_remains_paused",
			initState: State{IsPaused: true, UpdatedAt: pauseTime},
			meta:      &actuatortaskpb.Metadata{OperationName: proto.String("op-1")},
			wantState: State{IsPaused: true, UpdatedAt: pauseTime},
		},
		{
			name:      "zero_resumption_time_remains_paused",
			initState: State{IsPaused: true, UpdatedAt: pauseTime},
			meta:      &actuatortaskpb.Metadata{ResumptionTime: &timestamppb.Timestamp{}},
			wantState: State{IsPaused: true, UpdatedAt: pauseTime},
		},
		{
			name:      "stale_resumption_time_remains_paused",
			initState: State{IsPaused: true, UpdatedAt: pauseTime},
			meta:      &actuatortaskpb.Metadata{ResumptionRunId: proto.String("run-stale"), ResumptionTime: timestamppb.New(staleTime)},
			wantState: State{IsPaused: true, UpdatedAt: pauseTime},
		},
		{
			name:      "newer_resumption_time_unpauses_and_advances",
			initState: State{IsPaused: true, UpdatedAt: pauseTime},
			meta:      &actuatortaskpb.Metadata{ResumptionRunId: proto.String("run-resumed"), ResumptionTime: timestamppb.New(newerTime)},
			wantState: State{IsPaused: false, UpdatedAt: newerTime},
		},
		{
			name:      "equal_resumption_time_unpauses",
			initState: State{IsPaused: true, UpdatedAt: pauseTime},
			meta:      &actuatortaskpb.Metadata{ResumptionRunId: proto.String("run-resumed"), ResumptionTime: timestamppb.New(pauseTime)},
			wantState: State{IsPaused: false, UpdatedAt: pauseTime},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cache := NewCache()
			cache.Update(tc.initState)
			h, err := NewPauseHandler(cache, &storage.MockStorage{})
			if err != nil {
				t.Fatalf("NewPauseHandler() unexpected error: %v", err)
			}
			if got := h.IsPaused(tc.meta); got != tc.wantState.IsPaused {
				t.Errorf("IsPaused() = %v, want %v", got, tc.wantState.IsPaused)
			}
			if diff := cmp.Diff(tc.wantState, cache.State()); diff != "" {
				t.Errorf("State() mismatch (-want +got):\n%s", diff)
			}
			wantGauge := 0.0
			if tc.wantState.IsPaused {
				wantGauge = 1.0
			}
			if got := testutil.ToFloat64(schedulingPausedGauge); got != wantGauge {
				t.Errorf("xmc_actuator_scheduling_paused = %v, want %v", got, wantGauge)
			}
		})
	}
}

func TestHandlePause_BufferedTasksMetrics(t *testing.T) {
	ctx := context.Background()
	pausedTasksBufferedTotal.Reset()

	cache := NewCache()
	cache.Update(State{IsPaused: true, UpdatedAt: time.Now()})
	repo := &mockPausedTaskRepo{}
	txRunner := &storage.MockStorage{
		Experiment:  &essmsgpb.Experiment{Name: "experiments/100"},
		Transaction: &storage.Transaction{PausedTasks: repo},
	}
	h, err := NewPauseHandler(cache, txRunner)
	if err != nil {
		t.Fatalf("failed to create handler: %v", err)
	}

	actions := []struct {
		label   string
		payload *actuatortaskpb.ActuatorPayload
	}{
		{
			label:   "create",
			payload: newCreatePayload(),
		},
		{
			label: "restart",
			payload: &actuatortaskpb.ActuatorPayload{
				Target: proto.String("experiments/100/workUnits/200/executions/1"),
				Action: &actuatortaskpb.Action{
					Action: &actuatortaskpb.Action_Restart{Restart: &actuatortaskpb.RestartAction{}},
				},
				WorkUnit: &essmsgpb.WorkUnit{
					Name: "experiments/100/workUnits/200",
					CurrentWorkUnitExecution: &essmsgpb.WorkUnitExecution{
						Name: "experiments/100/workUnits/200/executions/2",
					},
				},
			},
		},
		{
			label: "update",
			payload: &actuatortaskpb.ActuatorPayload{
				Target: proto.String("experiments/100/workUnits/200/executions/1"),
				Action: &actuatortaskpb.Action{
					Action: &actuatortaskpb.Action_Update{Update: &actuatortaskpb.UpdateAction{}},
				},
				WorkUnit: &essmsgpb.WorkUnit{Name: "experiments/100/workUnits/200"},
			},
		},
	}

	for _, a := range actions {
		txRunner.WorkUnit = &essmsgpb.WorkUnit{
			Name: "experiments/100/workUnits/200",
			CurrentWorkUnitExecution: &essmsgpb.WorkUnitExecution{
				Name: resolveExecutionTarget(a.payload),
			},
		}
		if err := h.HandlePause(ctx, a.payload, "trace-id"); err != nil {
			t.Fatalf("HandlePause(%s) unexpected error: %v", a.label, err)
		}
		if got := testutil.ToFloat64(pausedTasksBufferedTotal.WithLabelValues(a.label)); got != 1 {
			t.Errorf("xmc_actuator_paused_tasks_buffered_total{action=%s} = %v, want 1", a.label, got)
		}
	}

	// Verify failed transaction does not increment buffered counter
	txRunner.UpdateCurrentWorkUnitExecutionErr = errors.New("tx failure")
	if err := h.HandlePause(ctx, newCreatePayload(), "trace-id"); err == nil {
		t.Fatalf("expected error from HandlePause(), got nil")
	}
	if got := testutil.ToFloat64(pausedTasksBufferedTotal.WithLabelValues("create")); got != 1 {
		t.Errorf("xmc_actuator_paused_tasks_buffered_total{action=create} after failed tx = %v, want 1", got)
	}
}
