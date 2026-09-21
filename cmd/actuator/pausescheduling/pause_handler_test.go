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

type mockPausedTaskRepo struct {
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

type mockStatusUpdater struct {
	err             error
	callCount       int
	recordedUpdates []*storage.WorkUnitExecutionUpdate
}

func (m *mockStatusUpdater) UpdateWorkUnitExecutions(ctx context.Context, xid int64, updates []*storage.WorkUnitExecutionUpdate) (map[int64]*essmsgpb.WorkUnitExecution, error) {
	m.callCount++
	if m.err != nil {
		return nil, m.err
	}
	m.recordedUpdates = append(m.recordedUpdates, updates...)
	return make(map[int64]*essmsgpb.WorkUnitExecution), nil
}

func TestNewPauseHandler(t *testing.T) {
	cache := NewCache()
	repo := &mockPausedTaskRepo{}
	updater := &mockStatusUpdater{}

	t.Run("nil_cache", func(t *testing.T) {
		h, err := NewPauseHandler(nil, repo, updater)
		if err == nil || h != nil {
			t.Errorf("expected error for nil cache, got %v", err)
		}
	})

	t.Run("nil_repo", func(t *testing.T) {
		h, err := NewPauseHandler(cache, nil, updater)
		if err == nil || h != nil {
			t.Errorf("expected error for nil repo, got %v", err)
		}
	})

	t.Run("nil_updater", func(t *testing.T) {
		h, err := NewPauseHandler(cache, repo, nil)
		if err == nil || h != nil {
			t.Errorf("expected error for nil updater, got %v", err)
		}
	})

	t.Run("valid_construction", func(t *testing.T) {
		h, err := NewPauseHandler(cache, repo, updater)
		if err != nil || h == nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestTaskPauseHandler_IsPaused(t *testing.T) {
	cache := NewCache()
	repo := &mockPausedTaskRepo{}
	updater := &mockStatusUpdater{}

	h, err := NewPauseHandler(cache, repo, updater)
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
	cache := NewCache()
	repo := &mockPausedTaskRepo{}
	updater := &mockStatusUpdater{}

	h, err := NewPauseHandler(cache, repo, updater)
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
		updaterErr             error
		expectError            bool
		expectBadRequest       bool
		expectedBufferedTarget string
		expectedStatusTarget   string
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
			name: "repo_error",
			payload: &actuatortaskpb.ActuatorPayload{
				Target: proto.String("experiments/100/workUnits/200/executions/1"),
				Action: &actuatortaskpb.Action{
					Action: &actuatortaskpb.Action_Create{Create: &actuatortaskpb.CreateAction{}},
				},
				WorkUnit: &essmsgpb.WorkUnit{Name: "experiments/100/workUnits/200"},
			},
			repoErr:     errors.New("spanner repo error"),
			expectError: true,
		},
		{
			name: "status_updater_error",
			payload: &actuatortaskpb.ActuatorPayload{
				Target: proto.String("experiments/100/workUnits/200/executions/1"),
				Action: &actuatortaskpb.Action{
					Action: &actuatortaskpb.Action_Create{Create: &actuatortaskpb.CreateAction{}},
				},
				WorkUnit: &essmsgpb.WorkUnit{Name: "experiments/100/workUnits/200"},
			},
			updaterErr:  errors.New("spanner status update error"),
			expectError: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cache := NewCache()
			cache.Update(State{IsPaused: true, UpdatedAt: time.Now()})
			repo := &mockPausedTaskRepo{err: tc.repoErr}
			updater := &mockStatusUpdater{err: tc.updaterErr}

			h, err := NewPauseHandler(cache, repo, updater)
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
				if tc.repoErr != nil && updater.callCount != 0 {
					t.Errorf("updater should not be called if repo fails, got %d calls", updater.callCount)
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

			verifyStatusUpdate(t, updater, tc.expectedStatusTarget, tc.name)
		})
	}
}

func verifyStatusUpdate(t *testing.T, updater *mockStatusUpdater, expectedTarget, testName string) {
	t.Helper()
	if expectedTarget == "" {
		if updater.callCount != 0 {
			t.Errorf("expected 0 status updater calls for %s, got %d", testName, updater.callCount)
		}
		return
	}
	if updater.callCount != 1 {
		t.Fatalf("expected 1 status updater call for %s, got %d", testName, updater.callCount)
	}
	if len(updater.recordedUpdates) != 1 {
		t.Fatalf("expected 1 recorded update for %s, got %d", testName, len(updater.recordedUpdates))
	}
	up := updater.recordedUpdates[0]
	if up.WorkUnitExecution.GetName() != expectedTarget {
		t.Errorf("status update target mismatch: got %q, want %q", up.WorkUnitExecution.GetName(), expectedTarget)
	}
	if up.WorkUnitExecution.GetStatus().GetState() != essmsgpb.WorkUnitStatus_SCHEDULING_PAUSED {
		t.Errorf("status state mismatch: got %v, want SCHEDULING_PAUSED", up.WorkUnitExecution.GetStatus().GetState())
	}
	expectedMask := []string{"status.state", "status.state_update_time"}
	if diff := cmp.Diff(expectedMask, up.UpdateMask.GetPaths()); diff != "" {
		t.Errorf("update mask paths mismatch (-want +got):\n%s", diff)
	}
}
