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

	"golang/cmp/cmp"
	apierrors "golang/k8s_io/apimachinery/v/v0_23/pkg/api/errors/errors"
	"golang/protobuf/v2/proto/proto"
	timestamppb "protobuf/timestamp_go_proto"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/spanner/pausedtask"
	actuatortaskpb "xmanager_cloud/task_queue/proto/task_go_proto"
)

// mockPausedTaskRepo overrides Create; the embedded Repository is nil and must not be called.
type mockPausedTaskRepo struct {
	pausedtask.Repository
	err         error
	storedTasks []*pausedtask.PausedTask
}

func (m *mockPausedTaskRepo) Create(ctx context.Context, task *pausedtask.PausedTask) error {
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

	if h.IsPaused() {
		t.Errorf("expected IsPaused() = false initially, got true")
	}

	cache.Update(State{IsPaused: true, UpdatedAt: time.Now()})
	if !h.IsPaused() {
		t.Errorf("expected IsPaused() = true after update, got false")
	}

	cache.Update(State{IsPaused: false, UpdatedAt: time.Now()})
	if h.IsPaused() {
		t.Errorf("expected IsPaused() = false after unpause, got true")
	}

	var nilHandler *TaskPauseHandler
	if nilHandler.IsPaused() {
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
		expectedStatusEID    int64
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
			expectedStatusEID:      1,
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
			expectedStatusEID:      2,
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
			txRunner := &storage.MockStorage{
				UpdateWorkUnitExecutionsErr: tc.updateStatusErr,
				PausedTaskRepository:        repo,
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
				if tc.repoErr != nil && len(txRunner.GotWorkUnitExecutionUpdates) != 0 {
					t.Errorf("status should not be updated if repo fails, got %d updates", len(txRunner.GotWorkUnitExecutionUpdates))
				}
				return
			}

			if tc.expectedBufferedTarget != "" {
				if len(repo.storedTasks) != 1 {
					t.Fatalf("expected 1 stored task, got %d", len(repo.storedTasks))
				}
				task := repo.storedTasks[0]
				if task.Target != tc.expectedBufferedTarget {
					t.Errorf("buffered task target mismatch: got %q, want %q", task.Target, tc.expectedBufferedTarget)
				}
				if task.XID != 100 || task.WID != 200 {
					t.Errorf("buffered task XID/WID mismatch: got %d/%d, want 100/200", task.XID, task.WID)
				}
			}

			verifyStatusUpdate(t, txRunner, tc.expectedStatusTarget, tc.expectedStatusEID)
		})
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

func verifyStatusUpdate(t *testing.T, got *storage.MockStorage, wantTarget string, wantEID int64) {
	t.Helper()
	if wantTarget == "" {
		if len(got.GotWorkUnitExecutionUpdates) != 0 {
			t.Errorf("expected 0 status updates, got %d", len(got.GotWorkUnitExecutionUpdates))
		}
		return
	}
	if len(got.GotWorkUnitExecutionUpdates) != 1 {
		t.Fatalf("expected 1 status update, got %d", len(got.GotWorkUnitExecutionUpdates))
	}
	if got.GotWorkUnitExecutionsXID != 100 {
		t.Errorf("status update XID mismatch: got %d, want 100", got.GotWorkUnitExecutionsXID)
	}
	up := got.GotWorkUnitExecutionUpdates[0]
	if up.WID != 200 || up.EID != wantEID {
		t.Errorf("status update key mismatch: got WID/EID %d/%d, want 200/%d", up.WID, up.EID, wantEID)
	}
	if up.WorkUnitExecution.GetName() != wantTarget {
		t.Errorf("status update target mismatch: got %q, want %q", up.WorkUnitExecution.GetName(), wantTarget)
	}
	if up.WorkUnitExecution.GetStatus().GetState() != essmsgpb.WorkUnitStatus_SCHEDULING_PAUSED {
		t.Errorf("status state mismatch: got %v, want SCHEDULING_PAUSED", up.WorkUnitExecution.GetStatus().GetState())
	}
	if up.WorkUnitExecution.GetStatus().GetStateUpdateTime() == nil {
		t.Errorf("status state_update_time must be set when updating status.state")
	}
	expectedMask := []string{"status.state"}
	if diff := cmp.Diff(expectedMask, up.UpdateMask.GetPaths()); diff != "" {
		t.Errorf("update mask paths mismatch (-want +got):\n%s", diff)
	}
}
