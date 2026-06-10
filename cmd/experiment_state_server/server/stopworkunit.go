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
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/storage/storage"
	"xmanager_cloud/validator/validator"
	xidmsgpb "xmanager_cloud/xid_service/proto/messages_go_proto"
)

// StopWorkUnit sends a request to stop a work unit and updates its state to Stopping.
func (s *Server) StopWorkUnit(ctx context.Context, req *apipb.StopWorkUnitRequest) (*lropb.Operation, error) {
	ctx = logging.InjectOperation(ctx, "")
	logging.Info(ctx, "Enqueue stop work unit", "request", req)

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

	// 3. Authorize the caller to get the current work unit execution for this experiment.
	authorized, err := s.checkUserAuthorization(ctx, validator.ConstructXIDName(xid), email, xidmsgpb.XidAcls_WRITER.Enum())
	if err != nil {
		return nil, err
	}
	if !authorized {
		logging.Warn(ctx, "User not authorized to stop work units", "user", email)
		return nil, fmt.Errorf("user %s is not authorized to stop work units for XID %d: %w", email, xid, status.ErrPermissionDenied)
	}

	// 4. Retrieve the work unit from the storage layer.
	workUnit, err := s.storage.FetchWorkUnit(ctx, xid, wid)
	if err != nil {
		if status.ExtractCanonicalCode(err) == status.NotFound {
			logging.Warn(ctx, "Work unit not found in storage")
			return nil, fmt.Errorf("StopWorkUnit with ID %s not found: %w (%v)", req.GetName(), status.ErrNotFound, err)
		}

		logging.Error(ctx, "Failed to get work unit", "error", err)
		return nil, fmt.Errorf("failed to get work unit: %w (%v)", status.ErrInternal, err)
	}

	// 5. Enqueue the stop request for the work unit.
	operation, execUpdate, err := s.enqueueWorkUnitStop(ctx, workUnit, email)
	if err != nil {
		logging.Error(ctx, "Failed to enqueue stop request for work unit", "error", err)
		_, _, eid, err := validator.ValidateWorkUnitExecutionName(workUnit.GetCurrentWorkUnitExecution().GetName())
		if err != nil {
			logging.Error(ctx, "Failed to validate work unit execution name", "error", err)
			return nil, fmt.Errorf("failed to validate work unit execution name: %w (%v)", status.ErrInternal, err)
		}

		if statusMsgErr := s.updateWorkUnitExecutionStatusMessage(ctx, xid, wid, eid, &msgpb.StructuredMessage{
			UserFacingText: "Failed to stop work unit. Please try again.",
			Severity:       msgpb.StructuredMessage_ERROR,
			Source:         msgpb.StructuredMessage_XMANAGER_BACKEND,
		}, false); statusMsgErr != nil {
			logging.Error(ctx, "Failed to update work unit execution status message after failed stop attempt", "error", statusMsgErr)
		}
		return nil, err
	}

	// 6. Update the work unit execution status to STOPPING.
	_, err = s.storage.UpdateWorkUnitExecutions(ctx, xid, []*storage.WorkUnitExecutionUpdate{execUpdate})
	if err != nil {
		logging.Error(ctx, "Failed to update work unit execution statuses", "error", err)
		return nil, fmt.Errorf("failed to update work unit execution statuses: %w (%v)", status.ErrInternal, err)
	}

	// 7. Return the successful response.
	logging.Info(ctx, "Enqueued stop work unit", "name", req.GetName())
	return operation, nil
}
