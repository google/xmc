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
	"errors"
	"fmt"
	"math"

	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/task_queue"
	"xmanager_cloud/internal/validator"

	lropb "cloud.google.com/go/longrunning/autogen/longrunningpb"
	msgpb "xmanager_cloud/api/protos/experiment"
	xidmsgpb "xmanager_cloud/api/protos/xid"
)

// BatchRestartWorkUnits sends a request to restart a work unit.
func (h *WorkUnitHandler) BatchRestartWorkUnits(ctx context.Context, req *msgpb.BatchRestartWorkUnitsRequest) (*lropb.Operation, error) {
	ctx = logging.InjectOperation(ctx, "")
	logging.Info(ctx, "Batch restart work units", "request", req)

	// 1. Authenticate the user.
	logging.Debug(ctx, "Extracting user identity from context")
	email, _, err := h.auth.AuthenticateUserFromContext(ctx)
	if err != nil {
		return nil, err
	}

	// 2. Validate the request payload.
	xid, err := validator.ValidateExperimentName(req.GetParent())
	if err != nil {
		logging.Warn(ctx, "Failed to validate experiment name", "name", req.GetParent(), "error", err)
		return nil, fmt.Errorf("failed to validate experiment name: %w: %w", grpcerr.ErrInvalidArgument, err)
	}
	ctx = logging.WithExperimentName(ctx, req.GetParent())

	// 3. Authorize the caller to restart work units for this experiment.
	if err := h.authorizeBatchRestart(ctx, xid, email); err != nil {
		return nil, err
	}

	// 4. Get the list of work units to restart.
	workUnits, _, err := h.storage.ListWorkUnits(ctx, req.GetParent(), math.MaxInt32, "", req.GetFilter(), "", 0)
	if err != nil {
		logging.Error(ctx, "Failed to list work units", "error", err, "filter", req.GetFilter())
		return nil, fmt.Errorf("failed to list work units: %w: %w", grpcerr.ErrInternal, err)
	}

	if len(workUnits) == 0 {
		logging.Warn(ctx, "No work units found for batch restart")
		return &lropb.Operation{Done: true}, nil
	}

	// 5. Get a list of all of the work unit IDs that need to be restarted.
	wids, multiErr := extractWorkUnitIDs(ctx, workUnits)

	// 6. Restart all of the work unit executions in a single transaction.
	restartedExecutions, err := restartWorkUnitExecutions(ctx, h.storage, email, xid, wids)
	if err != nil {
		logging.Error(ctx, "Failed to restart work unit executions", "error", err)
		multiErr = errors.Join(multiErr, fmt.Errorf("failed to restart work unit executions: %w: %w", grpcerr.ErrInternal, err))
	} else {
		h.finalizeSupersededExecutions(ctx, xid, workUnits)
	}

	// 7. Get the experiment priority for this experiment.
	experiment, err := h.storage.FetchExperiment(ctx, xid)
	if err != nil {
		logging.Error(ctx, "Failed to get experiment priority", "error", err)
		multiErr = errors.Join(multiErr, fmt.Errorf("failed to get experiment priority: %w: %w", grpcerr.ErrInternal, err))
	}

	// 8. Enqueue a task and operation for each work unit.
	var successfulOperations []*lropb.Operation
	for _, workUnit := range workUnits {
		op, err := h.processWorkUnitRestartItem(ctx, workUnit, restartedExecutions, email, experiment.GetPriority(), xid)
		if err != nil {
			multiErr = errors.Join(multiErr, err)
			continue
		}
		successfulOperations = append(successfulOperations, op)
	}

	// 9. If there were any errors, return them.
	if multiErr != nil {
		return nil, multiErr
	}

	// 10. If there were no successful operations, return an error.
	if len(successfulOperations) == 0 {
		return nil, fmt.Errorf("no work units were successfully processed: %w", grpcerr.ErrNotFound)
	}

	// for experiment-level operations.
	logging.Info(ctx, "Batch restarted work units", "operations", successfulOperations)
	return successfulOperations[len(successfulOperations)-1], nil
}

func (h *WorkUnitHandler) processWorkUnitRestartItem(ctx context.Context, workUnit *msgpb.WorkUnit, restartedExecutions map[int64]*msgpb.WorkUnitExecution, email string, priority msgpb.Priority, xid int64) (*lropb.Operation, error) {
	_, wid, err := validator.ValidateWorkUnitName(workUnit.GetName())
	if err != nil {
		logging.Warn(ctx, "Failed to parse work unit name", "work_unit_name", workUnit.GetName(), "error", err)
		return nil, fmt.Errorf("failed to parse work unit name: %w: %w", grpcerr.ErrInvalidArgument, err)
	}
	ctx = logging.WithWorkUnitName(ctx, workUnit.GetName())

	_, ok := restartedExecutions[wid]
	if !ok {
		logging.Error(ctx, "No execution was restarted for work unit", "work_unit_name", workUnit.GetName())
		return nil, fmt.Errorf("no execution was restarted for work unit %s: %w", workUnit.GetName(), grpcerr.ErrInternal)
	}

	if workUnit.GetCurrentWorkUnitExecution() == nil {
		logging.Error(ctx, "Work unit has no current execution", "work_unit_name", workUnit.GetName())
		return nil, fmt.Errorf("work unit %s has no current execution: %w", workUnit.GetName(), grpcerr.ErrInternal)
	}
	oldExecutionName := workUnit.GetCurrentWorkUnitExecution().GetName()
	workUnit.CurrentWorkUnitExecution = restartedExecutions[wid]

	operation, enqueueErr := h.enqueueWorkUnitRestart(ctx, oldExecutionName, workUnit, email, priority)
	if enqueueErr != nil {
		logging.Error(ctx, "Failed to enqueue restart request for work unit", "error", enqueueErr)
		_, _, eid, err := validator.ValidateWorkUnitExecutionName(workUnit.GetCurrentWorkUnitExecution().GetName())
		if err != nil {
			logging.Error(ctx, "Failed to validate work unit execution name", "error", err)
			return nil, fmt.Errorf("failed to validate work unit execution name: %w: %w", grpcerr.ErrInternal, err)
		}

		if statusMsgErr := h.updateWorkUnitExecutionStatusMessage(ctx, xid, wid, eid, &msgpb.StructuredMessage{
			UserFacingText: "Failed to restart work unit. Please try again.",
			Severity:       msgpb.StructuredMessage_FATAL,
			Source:         msgpb.StructuredMessage_XMANAGER_BACKEND,
		}, false); statusMsgErr != nil {
			logging.Error(ctx, "Failed to update work unit execution status message after failed restart attempt", "error", statusMsgErr)
		}
		return nil, enqueueErr
	}
	return operation, nil
}

// enqueueWorkUnitRestart handles the restarting process for a single work unit.
func (h *WorkUnitHandler) enqueueWorkUnitRestart(ctx context.Context, oldExecutionName string, newWU *msgpb.WorkUnit, email string, experimentPriority msgpb.Priority) (*lropb.Operation, error) {
	xid, wid, err := validator.ValidateWorkUnitName(newWU.GetName())
	if err != nil {
		logging.Warn(ctx, "Failed to validate work unit name", "name", newWU.GetName(), "error", err)
		return nil, fmt.Errorf("failed to validate work unit name: %w: %w", grpcerr.ErrInvalidArgument, err)
	}
	ctx = logging.WithWorkUnitName(ctx, newWU.GetName())

	// 1. Create a long-running operation for the work unit restarting.
	metadata := &msgpb.OperationMetadata{
		Target:            newWU.GetName(),
		ActionType:        msgpb.OperationMetadata_RESTART,
		OriginalRequester: email,
	}
	operation, err := h.storage.AddWorkUnitOperation(ctx, xid, wid, metadata)
	if err != nil {
		logging.Error(ctx, "Failed to add work unit operation", "error", err)
		return nil, fmt.Errorf("failed to add work unit operation: %w: %w", grpcerr.ErrInternal, err)
	}

	// 1.1. Ensure that the operation is marked as done if an error is returned from now on.
	defer func() {
		h.stopOperationOnError(ctx, operation, err)
	}()

	// 2. Enqueue the work unit for restarting.
	logging.Info(ctx, "Add restart request for work unit to the queue")
	var notifyWhen string
	if h.artifactLifecycle != nil {
		if arts, _, err := h.storage.ListArtifacts(ctx, newWU.GetName(), 100, "", "", "", 0); err == nil && len(arts) > 0 {
			notifyWhen = h.artifactLifecycle.RequiredNotifyWhenAnnotation(arts)
		}
	}
	err = h.actuator.EnqueueWorkUnitTask(ctx, oldExecutionName, newWU, client.RestartAction(experimentPriority, email, notifyWhen), operation.GetName())
	if err != nil {
		logging.Error(ctx, "Failed to enqueue restart request for work unit", "error", err)
		return nil, fmt.Errorf("failed to enqueue restart request for work unit %s: %w: %w", newWU.GetName(), grpcerr.ErrInternal, err)
	}

	// 3. Return the successful response.
	logging.Info(ctx, "Added restart request for work unit to the queue", "operation", operation.GetName())
	return operation, nil
}

func extractWorkUnitIDs(ctx context.Context, workUnits []*msgpb.WorkUnit) ([]int64, error) {
	var multiErr error
	var wids []int64
	for _, workUnit := range workUnits {
		_, wid, err := validator.ValidateWorkUnitName(workUnit.GetName())
		if err != nil {
			logging.Warn(ctx, "Failed to parse work unit name", "work_unit_name", workUnit.GetName(), "error", err)
			multiErr = errors.Join(multiErr, fmt.Errorf("failed to parse work unit name: %w: %w", grpcerr.ErrInvalidArgument, err))
			continue
		}
		wids = append(wids, wid)
	}
	return wids, multiErr
}

func (h *WorkUnitHandler) authorizeBatchRestart(ctx context.Context, xid int64, email string) error {
	authorized, err := h.checkUserAuthorization(ctx, validator.ConstructXIDName(xid), email, xidmsgpb.XidAcls_WRITER.Enum())
	if err != nil {
		return err
	}
	if !authorized {
		logging.Error(ctx, "User not authorized to restart work units", "user", email)
		return fmt.Errorf("user %s is not authorized to restart work units for XID %d: %w", email, xid, grpcerr.ErrPermissionDenied)
	}
	return nil
}

func (h *WorkUnitHandler) finalizeSupersededExecutions(ctx context.Context, xid int64, workUnits []*msgpb.WorkUnit) {
	if h.artifactLifecycle == nil {
		return
	}
	for _, wu := range workUnits {
		cur := wu.GetCurrentWorkUnitExecution()
		if cur == nil {
			continue
		}
		_, wid, oldEID, err := validator.ValidateWorkUnitExecutionName(cur.GetName())
		if err != nil {
			continue
		}
		if err := h.artifactLifecycle.HandleExecutionSuperseded(ctx, h.storage, xid, wid, oldEID); err != nil {
			logging.Warn(ctx, "Failed to finalize superseded execution artifact lifecycle", "wid", wid, "error", err)
		}
	}
}
