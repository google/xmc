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

// Package pausescheduling manages pause scheduling state, broadcast listening, and task handling.
package pausescheduling

import (
	"context"
	"fmt"
	"time"

	apierrors "golang/k8s_io/apimachinery/v/v0_23/pkg/api/errors/errors"
	"golang/protobuf/v2/proto/proto"
	timestamppb "protobuf/timestamp_go_proto"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/spanner/pausedtask"
	"xmanager_cloud/logging/logging"
	actuatortaskpb "xmanager_cloud/task_queue/proto/task_go_proto"
	"xmanager_cloud/validator/validator"
)

// PausedTaskRepository defines operations for persisting paused tasks.
type PausedTaskRepository interface {
	Create(ctx context.Context, task *pausedtask.PausedTask) error
}

// PauseHandler defines the interface for handling actuation tasks when scheduling is paused.
type PauseHandler interface {
	// IsPaused returns whether scheduling is currently paused.
	IsPaused() bool
	// ShouldPause returns whether the given action is subject to pause scheduling (Create, Restart, Update).
	ShouldPause(action *actuatortaskpb.Action) bool
	// HandlePause buffers the eligible task and updates execution status if required.
	HandlePause(ctx context.Context, payload *actuatortaskpb.ActuatorPayload, traceID string) error
}

var _ PauseHandler = (*TaskPauseHandler)(nil)

// TaskPauseHandler handles task buffering and status updates during scheduling pause.
type TaskPauseHandler struct {
	cache          Cache
	pausedTaskRepo PausedTaskRepository
	txRunner       storage.TransactionRunner
}

// NewPauseHandler creates a new TaskPauseHandler instance.
func NewPauseHandler(cache Cache, repo PausedTaskRepository, txRunner storage.TransactionRunner) (*TaskPauseHandler, error) {
	if cache == nil {
		return nil, fmt.Errorf("pause cache cannot be nil")
	}
	if repo == nil {
		return nil, fmt.Errorf("paused task repository cannot be nil")
	}
	if txRunner == nil {
		return nil, fmt.Errorf("transaction runner cannot be nil")
	}

	return &TaskPauseHandler{
		cache:          cache,
		pausedTaskRepo: repo,
		txRunner:       txRunner,
	}, nil
}

// IsPaused returns whether scheduling is currently paused.
func (h *TaskPauseHandler) IsPaused() bool {
	if h == nil || h.cache == nil {
		return false
	}
	return h.cache.State().IsPaused
}

// ShouldPause returns whether the given action is subject to pause scheduling (Create, Restart, Update).
// Delete actions return false as they bypass pause scheduling and execute immediately.
func (h *TaskPauseHandler) ShouldPause(action *actuatortaskpb.Action) bool {
	if action == nil {
		return false
	}
	return action.GetCreate() != nil || action.GetRestart() != nil || action.GetUpdate() != nil
}

// HandlePause buffers eligible tasks and updates execution status when scheduling is paused.
func (h *TaskPauseHandler) HandlePause(ctx context.Context, payload *actuatortaskpb.ActuatorPayload, traceID string) error {
	action := payload.GetAction()
	if !h.ShouldPause(action) {
		return apierrors.NewBadRequest("action is not eligible for pause handling")
	}

	var actionLabel string
	var updateStatus bool

	switch {
	case action.GetCreate() != nil:
		actionLabel = "create"
		updateStatus = true
	case action.GetRestart() != nil:
		actionLabel = "restart"
		updateStatus = true
	case action.GetUpdate() != nil:
		actionLabel = "update"
		updateStatus = false
	default:
		return apierrors.NewBadRequest(fmt.Sprintf("unsupported action type for pause handling: %v", action))
	}

	task, err := buildPausedTask(payload, traceID)
	if err != nil {
		return err
	}

	err = h.txRunner.RunInTransaction(ctx, func(ctx context.Context, tx *storage.Transaction) error {
		// TODO(pausescheduling): Buffer the paused task through the transaction-scoped repository.
		if err := h.pausedTaskRepo.Create(ctx, task); err != nil {
			return fmt.Errorf("failed to buffer paused task: %w", err)
		}
		if !updateStatus {
			return nil
		}
		return updateExecutionStatus(ctx, tx.WorkUnits, payload)
	})
	if err != nil {
		return err
	}

	logging.Info(ctx, fmt.Sprintf("Buffered %s task during scheduling pause", actionLabel), "target", payload.GetTarget())
	return nil
}

func buildPausedTask(payload *actuatortaskpb.ActuatorPayload, traceID string) (*pausedtask.PausedTask, error) {
	target := payload.GetTarget()
	xid, wid, _, err := validator.ValidateWorkUnitExecutionName(target)
	if err != nil {
		return nil, fmt.Errorf("failed to validate target %q for paused task: %w", target, err)
	}

	var priority int64
	if wu := payload.GetWorkUnit(); wu != nil {
		priority = int64(wu.GetPriority())
	}

	createTime := time.Now()
	if ct := payload.GetCreateTime(); ct != nil {
		createTime = ct.AsTime()
	}

	return &pausedtask.PausedTask{
		XID:           xid,
		WID:           wid,
		Target:        target,
		Action:        payload.GetAction(),
		OperationName: payload.GetMetadata().GetOperationName(),
		TraceID:       traceID,
		Priority:      priority,
		CreateTime:    createTime,
	}, nil
}

func resolveExecutionTarget(payload *actuatortaskpb.ActuatorPayload) string {
	if payload.GetAction().GetRestart() != nil {
		if newTarget := payload.GetWorkUnit().GetCurrentWorkUnitExecution().GetName(); newTarget != "" {
			return newTarget
		}
	}
	return payload.GetTarget()
}

// updateExecutionStatus sets the current execution state to SCHEDULING_PAUSED, preserving other status fields.
func updateExecutionStatus(ctx context.Context, workUnits storage.WorkUnitTxRepository, payload *actuatortaskpb.ActuatorPayload) error {
	targetToUpdate := resolveExecutionTarget(payload)
	xid, wid, eid, err := validator.ValidateWorkUnitExecutionName(targetToUpdate)
	if err != nil {
		return fmt.Errorf("failed to validate execution target %q: %w", targetToUpdate, err)
	}

	wu, err := workUnits.FetchWorkUnit(ctx, xid, wid)
	if err != nil {
		return fmt.Errorf("failed to fetch work unit for %q: %w", targetToUpdate, err)
	}
	status := &essmsgpb.WorkUnitStatus{}
	if current := wu.GetCurrentWorkUnitExecution().GetStatus(); current != nil {
		status = proto.Clone(current).(*essmsgpb.WorkUnitStatus)
	}
	status.State = essmsgpb.WorkUnitStatus_SCHEDULING_PAUSED
	status.StateUpdateTime = timestamppb.Now()

	if err := workUnits.UpdateWorkUnitStatus(ctx, xid, wid, eid, status); err != nil {
		return fmt.Errorf("failed to update work unit execution status to SCHEDULING_PAUSED for %q: %w", targetToUpdate, err)
	}
	return nil
}
