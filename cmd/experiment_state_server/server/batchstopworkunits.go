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

	"task/go/status"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/storage/storage"
	"xmanager_cloud/task_queue/client"
	"xmanager_cloud/validator/validator"

	lropb "longrunning/operations_go_proto"
	fmpb "protobuf/field_mask_go_proto"
	timestamppb "protobuf/timestamp_go_proto"
	apipb "xmanager_cloud/experiment_state_server/proto/api_go_proto"
	msgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	xidmsgpb "xmanager_cloud/xid_service/proto/messages_go_proto"
)

// BatchStopWorkUnits sends a request to stop multiple work units based on a filter.
func (h *WorkUnitHandler) BatchStopWorkUnits(ctx context.Context, req *apipb.BatchStopWorkUnitsRequest) (*lropb.Operation, error) {
	ctx = logging.InjectOperation(ctx, "")
	logging.Info(ctx, "Batch stop work units", "request", req)

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
		return nil, fmt.Errorf("failed to validate work unit name: %w (%v)", status.ErrInvalidArgument, err)
	}
	ctx = logging.WithExperimentName(ctx, req.GetParent())

	// 3. Authorize the caller to stop work units for this experiment.
	authorized, err := h.checkUserAuthorization(ctx, validator.ConstructXIDName(xid), email, xidmsgpb.XidAcls_WRITER.Enum())
	if err != nil {
		return nil, err
	}
	if !authorized {
		logging.Error(ctx, "User not authorized to stop work units", "user", email)
		return nil, fmt.Errorf("user %s is not authorized to stop work units for XID %d: %w", email, xid, status.ErrPermissionDenied)
	}

	// 4. Retrieve the list of work units to stop from the storage layer.
	workUnits, _, err := h.storage.ListWorkUnits(ctx, req.Parent, math.MaxInt32, "", req.GetFilter(), "", 0)
	if err != nil {
		logging.Error(ctx, "Failed to list work units", "error", err, "filter", req.GetFilter())
		return nil, fmt.Errorf("failed to list work units: %w (%v)", status.ErrInternal, err)
	}

	if len(workUnits) == 0 {
		logging.Warn(ctx, "No work units to stop")
		return &lropb.Operation{Done: true}, nil
	}

	// 5. Enqueue the stop requests for the work units and collect any errors.
	var multiErr error
	var successfulOperations []*lropb.Operation
	var execUpdates []*storage.WorkUnitExecutionUpdate
	for _, workUnit := range workUnits {
		ctx := logging.WithWorkUnitName(ctx, workUnit.GetName())

		res, err := h.enqueueWorkUnitStop(ctx, workUnit, email)
		if err != nil {
			multiErr = errors.Join(multiErr, err)
			continue
		}

		successfulOperations = append(successfulOperations, res.Operation)
		execUpdates = append(execUpdates, res.ExecutionUpdate)
	}

	// 6. Update the work unit execution statuses to STOPPING for all work units that were
	// successfully processed.
	if len(execUpdates) > 0 {
		if _, err := h.storage.UpdateWorkUnitExecutions(ctx, xid, execUpdates); err != nil {
			logging.Error(ctx, "Failed to batch update work unit statuses", "error", err)
			multiErr = errors.Join(multiErr, fmt.Errorf("failed to batch update work unit statuses: %w (%v)", status.ErrInternal, err))
		}
	}

	// 7. Return all of the successful operations. If any errors occurred, also return them. This
	// breaks the error handling contract of Golang, but I think it's the best we can do in this case.
	if len(successfulOperations) == 0 {
		multiErr = errors.Join(multiErr, fmt.Errorf("no work units were successfully processed: %w", status.ErrNotFound))
		return nil, multiErr
	}
	// for experiment-level operations.
	logging.Info(ctx, "Batch stopped work units", "operations", successfulOperations)
	return successfulOperations[len(successfulOperations)-1], multiErr
}

// WorkUnitStopEnqueueResult contains the operation and execution update for an enqueued stop request.
type WorkUnitStopEnqueueResult struct {
	Operation       *lropb.Operation
	ExecutionUpdate *storage.WorkUnitExecutionUpdate
}

// enqueueWorkUnitStop handles the stopping process for a single work unit.
func (h *WorkUnitHandler) enqueueWorkUnitStop(ctx context.Context, workUnit *msgpb.WorkUnit, email string) (WorkUnitStopEnqueueResult, error) {
	xid, wid, eid, err := validator.ValidateWorkUnitExecutionName(workUnit.GetCurrentWorkUnitExecution().GetName())
	if err != nil {
		logging.Warn(ctx, "Failed to validate work unit name", "name", workUnit.GetName(), "error", err)
		return WorkUnitStopEnqueueResult{}, fmt.Errorf("failed to validate work unit name: %w (%v)", status.ErrInvalidArgument, err)
	}
	ctx = logging.WithWorkUnitName(ctx, workUnit.GetName())

	// 1. Create a long-running operation for the work unit stopping.
	metadata := &msgpb.OperationMetadata{
		Target:            workUnit.GetName(),
		ActionType:        msgpb.OperationMetadata_STOP,
		OriginalRequester: email,
	}
	operation, err := h.storage.AddWorkUnitOperation(ctx, xid, wid, metadata)
	if err != nil {
		logging.Error(ctx, "Failed to add work unit operation", "error", err)
		return WorkUnitStopEnqueueResult{}, fmt.Errorf("failed to add work unit operation: %w (%v)", status.ErrInternal, err)
	}

	// 1.1. Ensure that the operation is marked as done if an error is returned from now on.
	defer func() {
		h.stopOperationOnError(ctx, operation, err)
	}()

	// 2. Enqueue the work unit for stopping.
	logging.Info(ctx, "Add stop request for work unit to the queue")
	if err = h.actuator.EnqueueWorkUnitTask(ctx, workUnit.GetCurrentWorkUnitExecution().GetName(), workUnit, client.DeleteAction(), operation.GetName()); err != nil {
		return WorkUnitStopEnqueueResult{}, h.handleEnqueueActionError(ctx, "stop", workUnit, xid, wid, msgpb.StructuredMessage_ERROR, err)
	}

	// 3. Update the work unit's status to STOPPING.
	execUpdate := &storage.WorkUnitExecutionUpdate{
		WID: wid,
		EID: eid,
		WorkUnitExecution: &msgpb.WorkUnitExecution{
			Status: &msgpb.WorkUnitStatus{
				SystemMessage: &msgpb.StructuredMessage{
					UserFacingText: fmt.Sprintf("Work unit is being stopped by user %s", email),
					Severity:       msgpb.StructuredMessage_INFO,
					Source:         msgpb.StructuredMessage_XMANAGER_BACKEND,
				},
				StateUpdateTime: timestamppb.Now(),
				State:           msgpb.WorkUnitStatus_STOPPING,
			},
		},
		UpdateMask: &fmpb.FieldMask{Paths: []string{"status.state"}},
	}

	// 4. Return the successful response.
	logging.Info(ctx, "Added stop request for work unit to the queue", "operation", operation.GetName())
	return WorkUnitStopEnqueueResult{
		Operation:       operation,
		ExecutionUpdate: execUpdate,
	}, nil
}
