package server

import (
	"context"
	"fmt"

	fmpb "protobuf/field_mask_go_proto"
	apipb "xmanager_cloud/experiment_state_server/proto/api_go_proto"
	msgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/storage/storage"
	tpb "xmanager_cloud/task_queue/proto/task_go_proto"
	"xmanager_cloud/validator/validator"
	xidmsgpb "xmanager_cloud/xid_service/proto/messages_go_proto"
	"task/go/status"
)

// UpdateWorkUnit updates a work unit's data.
func (s *Server) UpdateWorkUnit(ctx context.Context, req *apipb.UpdateWorkUnitRequest) (*msgpb.WorkUnit, error) {
	ctx = logging.InjectOperation(ctx, "")
	logging.Info(ctx, "Update work unit", "request", req)

	// 1. Authenticate the user.
	logging.Debug(ctx, "Extracting user identity from context")
	email, _, err := s.auth.AuthenticateUserFromContext(ctx)
	if err != nil {
		return nil, err
	}

	// 2. Validate the request payload.
	name := req.GetWorkUnit().GetName()
	xid, wid, err := validator.ValidateWorkUnitName(name)
	if err != nil {
		logging.Warn(ctx, "UpdateWorkUnit validation failed", "name", name, "error", err)
		return nil, fmt.Errorf("UpdateWorkUnit validation failed: %w (%v)", status.ErrInvalidArgument, err)
	}
	ctx = logging.WithWorkUnitName(ctx, name)

	// 3. Authorize the caller to update work units for this experiment.
	authorized, err := s.checkUserAuthorization(ctx, validator.ConstructXIDName(xid), email, xidmsgpb.XidAcls_WRITER.Enum())
	if err != nil {
		return nil, err
	}
	if !authorized {
		logging.Warn(ctx, "User not authorized to update work units", "user", email, "xid", xid)
		return nil, fmt.Errorf("user %s is not authorized to update work units for XID %d: %w", email, xid, status.ErrPermissionDenied)
	}

	// 4. Update the work unit in the storage layer.
	logging.Debug(ctx, "Updating work unit in storage")
	update := &storage.WorkUnitUpdate{
		XID:        xid,
		WID:        wid,
		WorkUnit:   req.GetWorkUnit(),
		UpdateMask: req.GetUpdateMask(),
	}
	originalWu, returnedWu, err := s.storage.UpdateWorkUnit(ctx, update)
	if err != nil {
		logging.Error(ctx, "Failed to update work unit", "error", err)
		return nil, fmt.Errorf("failed to update work unit %s: %w (%v)", name, status.ErrInternal, err)
	}

	// 5. Check if we need to enqueue a task to update the work unit execution.
	updateAction, err := s.shouldEnqueueWorkUnitTask(ctx, xid, originalWu, returnedWu)
	if err != nil {
		logging.Error(ctx, "Failed to check if we need to enqueue a task to update the work unit execution", "error", err)
		return nil, fmt.Errorf("failed to check if we need to enqueue a task to update the work unit execution: %w (%v)", status.ErrInternal, err)
	}

	// 6. Enqueue the work unit for updating the corresponding work unit execution in the adapter.
	if len(updateAction.GetUpdateMask().GetPaths()) > 0 {
		ctx := logging.WithWorkUnitExecutionName(ctx, returnedWu.GetCurrentWorkUnitExecution().GetName())
		logging.Debug(ctx, "Work unit update requires enqueuing a task to update the corresponding work unit execution")

		action := &tpb.Action{
			Action: &tpb.Action_Update{
				Update: updateAction,
			},
		}

		err = s.taskQueueClient.EnqueueWorkUnitTask(ctx, returnedWu.GetCurrentWorkUnitExecution().GetName(), returnedWu, action, "")
		if err != nil {
			logging.Error(ctx, "Failed to enqueue work unit for updating the corresponding work unit execution", "error", err)
			return nil, fmt.Errorf("failed to enqueue work unit for updating the corresponding work unit execution %s: %w (%v)", returnedWu.GetName(), status.ErrInternal, err)
		}
	}

	// 7. Return the successful response.
	logging.Info(ctx, "Updated work unit", "work_unit", returnedWu)
	return returnedWu, nil
}

func (s *Server) shouldEnqueueWorkUnitTask(ctx context.Context, xid int64, originalWu, returnedWu *msgpb.WorkUnit) (*tpb.UpdateAction, error) {
	updateAction := &tpb.UpdateAction{
		UpdateMask: &fmpb.FieldMask{},
	}

	if originalWu.GetPriority() != returnedWu.GetPriority() {
		updateAction.UpdateMask.Paths = append(updateAction.UpdateMask.Paths, "priority")

		exp, err := s.storage.FetchExperiment(ctx, xid)
		if err != nil {
			logging.Error(ctx, "Failed to fetch experiment", "error", err)
			return nil, fmt.Errorf("failed to fetch experiment: %w (%v)", status.ErrInternal, err)
		}

		updateAction.ExperimentPriority = exp.GetPriority().Enum()
	}

	return updateAction, nil
}
