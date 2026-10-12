// Copyright 2025 Google LLC
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
package server

import (
	"context"
	"fmt"

	fmpb "google.golang.org/protobuf/types/known/fieldmaskpb"
	actuatortaskpb "xmanager_cloud/api/protos/task_queue"
	apipb "xmanager_cloud/experiment_state_server/proto/api_go_proto"
	msgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/validator"
	xidmsgpb "xmanager_cloud/xid_service/proto/messages_go_proto"
)

// UpdateWorkUnit updates a work unit's data.
func (h *WorkUnitHandler) UpdateWorkUnit(ctx context.Context, req *apipb.UpdateWorkUnitRequest) (*msgpb.WorkUnit, error) {
	ctx = logging.InjectOperation(ctx, "")
	logging.Info(ctx, "Update work unit", "request", req)

	// 1. Authenticate the user.
	logging.Debug(ctx, "Extracting user identity from context")
	email, _, err := h.auth.AuthenticateUserFromContext(ctx)
	if err != nil {
		return nil, err
	}

	// 2. Validate the request payload.
	name := req.GetWorkUnit().GetName()
	xid, wid, err := validator.ValidateWorkUnitName(name)
	if err != nil {
		logging.Warn(ctx, "UpdateWorkUnit validation failed", "name", name, "error", err)
		return nil, fmt.Errorf("UpdateWorkUnit validation failed: %w (%v)", grpcerr.ErrInvalidArgument, err)
	}
	ctx = logging.WithWorkUnitName(ctx, name)

	// 3. Authorize the caller to update work units for this experiment.
	authorized, err := h.checkUserAuthorization(ctx, validator.ConstructXIDName(xid), email, xidmsgpb.XidAcls_WRITER.Enum())
	if err != nil {
		return nil, err
	}
	if !authorized {
		logging.Warn(ctx, "User not authorized to update work units", "user", email, "xid", xid)
		return nil, fmt.Errorf("user %s is not authorized to update work units for XID %d: %w", email, xid, grpcerr.ErrPermissionDenied)
	}

	// 4. Update the work unit in the storage layer.
	logging.Debug(ctx, "Updating work unit in storage")
	update := &storage.WorkUnitUpdate{
		XID:        xid,
		WID:        wid,
		WorkUnit:   req.GetWorkUnit(),
		UpdateMask: req.GetUpdateMask(),
	}
	res, err := h.storage.UpdateWorkUnit(ctx, update)
	if err != nil {
		logging.Error(ctx, "Failed to update work unit", "error", err)
		return nil, fmt.Errorf("failed to update work unit %s: %w (%v)", name, grpcerr.ErrInternal, err)
	}
	originalWu, returnedWu := res.Original, res.Updated

	if originalWu == nil || returnedWu == nil {
		return nil, fmt.Errorf("storage returned nil work unit: %w", grpcerr.ErrInternal)
	}

	// 5. Check if we need to enqueue a task to update the work unit execution.
	updateAction, err := h.shouldEnqueueWorkUnitTask(ctx, xid, originalWu, returnedWu)
	if err != nil {
		logging.Error(ctx, "Failed to check if we need to enqueue a task to update the work unit execution", "error", err)
		return nil, fmt.Errorf("failed to check if we need to enqueue a task to update the work unit execution: %w (%v)", grpcerr.ErrInternal, err)
	}

	// 6. Enqueue the work unit for updating the corresponding work unit execution in the adapter.
	if len(updateAction.GetUpdateMask().GetPaths()) > 0 {
		if returnedWu.GetCurrentWorkUnitExecution() == nil {
			return nil, fmt.Errorf("updated work unit %s has no current execution to update: %w", returnedWu.GetName(), grpcerr.ErrInternal)
		}
		ctx := logging.WithWorkUnitExecutionName(ctx, returnedWu.GetCurrentWorkUnitExecution().GetName())
		logging.Debug(ctx, "Work unit update requires enqueuing a task to update the corresponding work unit execution")

		action := &actuatortaskpb.Action{
			Action: &actuatortaskpb.Action_Update{
				Update: updateAction,
			},
		}

		err = h.actuator.EnqueueWorkUnitTask(ctx, returnedWu.GetCurrentWorkUnitExecution().GetName(), returnedWu, action, "")
		if err != nil {
			logging.Error(ctx, "Failed to enqueue work unit for updating the corresponding work unit execution", "error", err)
			return nil, fmt.Errorf("failed to enqueue work unit for updating the corresponding work unit execution %s: %w (%v)", returnedWu.GetName(), grpcerr.ErrInternal, err)
		}
	}

	// 7. Return the successful response.
	logging.Info(ctx, "Updated work unit", "work_unit", returnedWu)
	return returnedWu, nil
}

func (h *WorkUnitHandler) shouldEnqueueWorkUnitTask(ctx context.Context, xid int64, originalWu, returnedWu *msgpb.WorkUnit) (*actuatortaskpb.UpdateAction, error) {
	updateAction := &actuatortaskpb.UpdateAction{
		UpdateMask: &fmpb.FieldMask{},
	}

	if originalWu.GetPriority() != returnedWu.GetPriority() {
		updateAction.UpdateMask.Paths = append(updateAction.UpdateMask.Paths, "priority")

		exp, err := h.storage.FetchExperiment(ctx, xid)
		if err != nil {
			logging.Error(ctx, "Failed to fetch experiment", "error", err)
			return nil, fmt.Errorf("failed to fetch experiment: %w (%v)", grpcerr.ErrInternal, err)
		}

		updateAction.ExperimentPriority = exp.GetPriority().Enum()
	}

	return updateAction, nil
}
