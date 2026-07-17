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

	lropb "longrunning/operations_go_proto"
	"task/go/status"
	apipb "xmanager_cloud/experiment_state_server/proto/api_go_proto"
	msgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/validator"
	xidmsgpb "xmanager_cloud/xid_service/proto/messages_go_proto"
)

// RestartWorkUnit sends a request to restart a work unit.
func (s *Server) RestartWorkUnit(ctx context.Context, req *apipb.RestartWorkUnitRequest) (*lropb.Operation, error) {
	ctx = logging.InjectOperation(ctx, "")
	logging.Info(ctx, "Enqueue restart work unit", "request", req)

	// 1. Authenticate the user.
	logging.Debug(ctx, "Extracting user identity from context")
	email, _, err := s.auth.AuthenticateUserFromContext(ctx)
	if err != nil {
		return nil, err
	}

	// 2. Validate the request payload.
	xid, wid, err := validator.ValidateWorkUnitName(req.GetName())
	if err != nil {
		logging.Warn(ctx, "Failed to validate work unit name", "name", req.GetName(), "error", err)
		return nil, fmt.Errorf("failed to validate work unit name: %w (%v)", status.ErrInvalidArgument, err)
	}
	ctx = logging.WithWorkUnitName(ctx, req.GetName())

	// 3. Authorize the caller to restart work units for this experiment.
	authorized, err := s.checkUserAuthorization(ctx, validator.ConstructXIDName(xid), email, xidmsgpb.XidAcls_WRITER.Enum())
	if err != nil {
		return nil, err
	}
	if !authorized {
		logging.Warn(ctx, "User not authorized to restart work units", "user", email)
		return nil, fmt.Errorf("user %s is not authorized to restart work units for XID %d: %w", email, xid, status.ErrPermissionDenied)
	}

	// 4. Retrieve the work unit from the storage layer. We need this to get the executable later.
	workUnit, err := s.storage.FetchWorkUnit(ctx, xid, wid)
	if err != nil {
		logging.Error(ctx, "Failed to get work unit", "error", err)
		return nil, fmt.Errorf("failed to get work unit: %w (%v)", status.ErrNotFound, err)
	}
	oldExecutionName := workUnit.GetCurrentWorkUnitExecution().GetName()

	// 5. Restart the work unit execution.
	restartedExecutions, err := s.storage.RestartWorkUnitExecutions(ctx, email, xid, []int64{wid})
	if err != nil {
		logging.Error(ctx, "Failed to restart work unit executions", "error", err)
		return nil, fmt.Errorf("failed to restart work unit executions: %w (%v)", status.ErrInternal, err)
	}

	// 5.1. Extract the restarted work unit execution and patch the work unit with it.
	restartedExecution := restartedExecutions[wid]
	if restartedExecution == nil {
		logging.Error(ctx, "No execution was restarted for work unit")
		return nil, fmt.Errorf("no execution was restarted for work unit %s: %w", req.GetName(), status.ErrInternal)
	}
	workUnit.CurrentWorkUnitExecution = restartedExecution

	// 6. Get the experiment priority for this experiment.
	experiment, err := s.storage.FetchExperiment(ctx, xid)
	if err != nil {
		logging.Error(ctx, "Failed to get experiment priority", "error", err)
		return nil, fmt.Errorf("failed to get experiment priority: %w (%v)", status.ErrInternal, err)
	}

	// 7. Enqueue the restart request for the work unit.
	operation, err := s.enqueueWorkUnitRestart(ctx, oldExecutionName, workUnit, email, experiment.GetPriority())
	if err != nil {
		logging.Error(ctx, "Failed to enqueue restart request for work unit", "error", err)
		_, _, eid, err := validator.ValidateWorkUnitExecutionName(workUnit.GetCurrentWorkUnitExecution().GetName())
		if err != nil {
			logging.Error(ctx, "Failed to validate work unit execution name", "error", err)
			return nil, fmt.Errorf("failed to validate work unit execution name: %w (%v)", status.ErrInternal, err)
		}

		if statusMsgErr := s.updateWorkUnitExecutionStatusMessage(ctx, xid, wid, eid, &msgpb.StructuredMessage{
			UserFacingText: "Failed to restart work unit. Please try again.",
			Severity:       msgpb.StructuredMessage_FATAL,
			Source:         msgpb.StructuredMessage_XMANAGER_BACKEND,
		}, false); statusMsgErr != nil {
			logging.Error(ctx, "Failed to update work unit execution status message after failed restart attempt", "error", statusMsgErr)
		}
		return nil, err
	}

	// 8. Return the successful response.
	logging.Info(ctx, "Enqueued restart request for work unit", "name", req.GetName())
	return operation, nil
}
