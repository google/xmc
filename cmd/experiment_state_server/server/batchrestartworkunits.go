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
	"xmanager_cloud/task_queue/client"
	"xmanager_cloud/validator/validator"

	lropb "longrunning/operations_go_proto"
	apipb "xmanager_cloud/experiment_state_server/proto/api_go_proto"
	msgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	xidmsgpb "xmanager_cloud/xid_service/proto/messages_go_proto"
)

// BatchRestartWorkUnits sends a request to restart a work unit.
func (s *Server) BatchRestartWorkUnits(ctx context.Context, req *apipb.BatchRestartWorkUnitsRequest) (*lropb.Operation, error) {
	ctx = logging.InjectOperation(ctx, "")
	logging.Info(ctx, "Batch restart work units", "request", req)

	// 1-3. Authenticate, validate request payload, and authorize caller.
	xid, email, err := s.authenticateAndAuthorizeBatchRestart(ctx, req)
	if err != nil {
		return nil, err
	}
	ctx = logging.WithExperimentName(ctx, req.GetParent())

	// 4. Get the list of work units to restart.
	workUnits, _, err := s.storage.ListWorkUnits(ctx, req.GetParent(), math.MaxInt32, "", req.GetFilter(), "", 0)
	if err != nil {
		logging.Error(ctx, "Failed to list work units", "error", err, "filter", req.GetFilter())
		return nil, fmt.Errorf("failed to list work units: %w (%v)", status.ErrInternal, err)
	}

	if len(workUnits) == 0 {
		logging.Warn(ctx, "No work units found for batch restart")
		return &lropb.Operation{Done: true}, nil
	}

	// 5. Get a list of all of the work unit IDs that need to be restarted.
	wids, multiErr := extractWorkUnitIDs(ctx, workUnits)

	// 6. Restart all of the work unit executions in a single transaction.
	restartedExecutions, err := s.storage.RestartWorkUnitExecutions(ctx, email, xid, wids)
	if err != nil {
		logging.Error(ctx, "Failed to restart work unit executions", "error", err)
		multiErr = errors.Join(multiErr, fmt.Errorf("failed to restart work unit executions: %w (%v)", status.ErrInternal, err))
	}

	// 7. Get the experiment priority for this experiment.
	experiment, err := s.storage.FetchExperiment(ctx, xid)
	if err != nil {
		logging.Error(ctx, "Failed to get experiment priority", "error", err)
		multiErr = errors.Join(multiErr, fmt.Errorf("failed to get experiment priority: %w (%v)", status.ErrInternal, err))
	}

	// 8. Enqueue a task and operation for each work unit.
	var successfulOperations []*lropb.Operation
	for _, workUnit := range workUnits {
		_, wid, err := validator.ValidateWorkUnitName(workUnit.GetName())
		if err != nil {
			logging.Warn(ctx, "Failed to parse work unit name", "work_unit_name", workUnit.GetName(), "error", err)
			multiErr = errors.Join(multiErr, fmt.Errorf("failed to parse work unit name: %w (%v)", status.ErrInvalidArgument, err))
			continue
		}
		ctx := logging.WithWorkUnitName(ctx, workUnit.GetName())

		// 8.1. Get the execution that was restarted for this work unit. If there is none, then we fail
		// for this work unit and continue processing the others.
		_, ok := restartedExecutions[wid]
		if !ok {
			logging.Error(ctx, "No execution was restarted for work unit", "work_unit_name", workUnit.GetName())
			multiErr = errors.Join(multiErr, fmt.Errorf("no execution was restarted for work unit %s: %w", workUnit.GetName(), status.ErrInternal))
			continue
		}

		// 8.2. Patch the work unit with the restarted execution.
		oldExecutionName := workUnit.GetCurrentWorkUnitExecution().GetName()
		workUnit.CurrentWorkUnitExecution = restartedExecutions[wid]

		// 8.3. Enqueue a task and operation for this work unit.
		operation, enqueueErr := s.enqueueWorkUnitRestart(ctx, oldExecutionName, workUnit, email, experiment.GetPriority())
		if enqueueErr != nil {
			logging.Error(ctx, "Failed to enqueue restart request for work unit", "error", enqueueErr)
			_, _, eid, err := validator.ValidateWorkUnitExecutionName(workUnit.GetCurrentWorkUnitExecution().GetName())
			if err != nil {
				logging.Error(ctx, "Failed to validate work unit execution name", "error", err)
				multiErr = errors.Join(multiErr, fmt.Errorf("failed to validate work unit execution name: %w (%v)", status.ErrInternal, err))
				continue
			}

			if statusMsgErr := s.updateWorkUnitExecutionStatusMessage(ctx, xid, wid, eid, &msgpb.StructuredMessage{
				UserFacingText: "Failed to restart work unit. Please try again.",
				Severity:       msgpb.StructuredMessage_FATAL,
				Source:         msgpb.StructuredMessage_XMANAGER_BACKEND,
			}, false); statusMsgErr != nil {
				logging.Error(ctx, "Failed to update work unit execution status message after failed restart attempt", "error", statusMsgErr)
			}
			multiErr = errors.Join(multiErr, enqueueErr)
			continue
		}
		successfulOperations = append(successfulOperations, operation)
	}

	// 9. If there were any errors, return them.
	if multiErr != nil {
		return nil, multiErr
	}

	// 10. If there were no successful operations, return an error.
	if len(successfulOperations) == 0 {
		return nil, fmt.Errorf("no work units were successfully processed: %w", status.ErrNotFound)
	}

	// for experiment-level operations.
	logging.Info(ctx, "Batch restarted work units", "operations", successfulOperations)
	return successfulOperations[len(successfulOperations)-1], nil
}

// enqueueWorkUnitRestart handles the restarting process for a single work unit.
func (s *Server) enqueueWorkUnitRestart(ctx context.Context, oldExecutionName string, newWU *msgpb.WorkUnit, email string, experimentPriority msgpb.Priority) (*lropb.Operation, error) {
	xid, wid, err := validator.ValidateWorkUnitName(newWU.GetName())
	if err != nil {
		logging.Warn(ctx, "Failed to validate work unit name", "name", newWU.GetName(), "error", err)
		return nil, fmt.Errorf("failed to validate work unit name: %w (%v)", status.ErrInvalidArgument, err)
	}
	ctx = logging.WithWorkUnitName(ctx, newWU.GetName())

	// 1. Create a long-running operation for the work unit restarting.
	metadata := &msgpb.OperationMetadata{
		Target:            newWU.GetName(),
		ActionType:        msgpb.OperationMetadata_RESTART,
		OriginalRequester: email,
	}
	operation, err := s.storage.AddWorkUnitOperation(ctx, xid, wid, metadata)
	if err != nil {
		logging.Error(ctx, "Failed to add work unit operation", "error", err)
		return nil, fmt.Errorf("failed to add work unit operation: %w (%v)", status.ErrInternal, err)
	}

	// 1.1. Ensure that the operation is marked as done if an error is returned from now on.
	defer func() {
		s.stopOperationOnError(ctx, operation, err)
	}()

	// 2. Enqueue the work unit for restarting.
	logging.Info(ctx, "Add restart request for work unit to the queue")
	err = s.taskQueueClient.EnqueueWorkUnitTask(ctx, oldExecutionName, newWU, client.RestartAction(experimentPriority, email), operation.GetName())
	if err != nil {
		logging.Error(ctx, "Failed to enqueue restart request for work unit", "error", err)
		return nil, fmt.Errorf("failed to enqueue restart request for work unit %s: %w (%v)", newWU.GetName(), status.ErrInternal, err)
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
			multiErr = errors.Join(multiErr, fmt.Errorf("failed to parse work unit name: %w (%v)", status.ErrInvalidArgument, err))
			continue
		}
		wids = append(wids, wid)
	}
	return wids, multiErr
}

func (s *Server) authenticateAndAuthorizeBatchRestart(ctx context.Context, req *apipb.BatchRestartWorkUnitsRequest) (int64, string, error) {
	logging.Debug(ctx, "Extracting user identity from context")
	email, _, err := s.auth.AuthenticateUserFromContext(ctx)
	if err != nil {
		return 0, "", err
	}
	xid, err := validator.ValidateExperimentName(req.GetParent())
	if err != nil {
		logging.Warn(ctx, "Failed to validate experiment name", "name", req.GetParent(), "error", err)
		return 0, "", fmt.Errorf("failed to validate work unit name: %w (%v)", status.ErrInvalidArgument, err)
	}
	authorized, err := s.checkUserAuthorization(ctx, validator.ConstructXIDName(xid), email, xidmsgpb.XidAcls_WRITER.Enum())
	if err != nil {
		return 0, "", err
	}
	if !authorized {
		logging.Error(ctx, "User not authorized to restart work units", "user", email)
		return 0, "", fmt.Errorf("user %s is not authorized to restart work units for XID %d: %w", email, xid, status.ErrPermissionDenied)
	}
	return xid, email, nil
}
