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

	lropb "cloud.google.com/go/longrunning/autogen/longrunningpb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	xidmsgpb "xmanager_cloud/api/protos/xid"
	apipb "xmanager_cloud/experiment_state_server/proto/api_go_proto"
	msgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/validator"
)

// StopWorkUnit sends a request to stop a work unit and updates its state to Stopping.
func (h *WorkUnitHandler) StopWorkUnit(ctx context.Context, req *apipb.StopWorkUnitRequest) (*lropb.Operation, error) {
	ctx = logging.InjectOperation(ctx, "")
	logging.Info(ctx, "Enqueue stop work unit", "request", req)

	// 1. Authenticate the user.
	logging.Debug(ctx, "Extracting user identity from context")
	email, _, err := h.auth.AuthenticateUserFromContext(ctx)
	if err != nil {
		return nil, err
	}

	// 2. Validate the request payload.
	xid, wid, err := validator.ValidateWorkUnitName(req.GetName())
	if err != nil {
		logging.Warn(ctx, "Failed to validate work unit name", "name", req.GetName(), "error", err)
		return nil, fmt.Errorf("failed to validate work unit name: %w (%v)", grpcerr.ErrInvalidArgument, err)
	}
	ctx = logging.WithWorkUnitName(ctx, req.GetName())

	// 3. Authorize the caller to get the current work unit execution for this experiment.
	authorized, err := h.checkUserAuthorization(ctx, validator.ConstructXIDName(xid), email, xidmsgpb.XidAcls_WRITER.Enum())
	if err != nil {
		return nil, err
	}
	if !authorized {
		logging.Warn(ctx, "User not authorized to stop work units", "user", email)
		return nil, fmt.Errorf("user %s is not authorized to stop work units for XID %d: %w", email, xid, grpcerr.ErrPermissionDenied)
	}

	// 4. Retrieve the work unit from the storage layer.
	workUnit, err := h.storage.FetchWorkUnit(ctx, xid, wid)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			logging.Warn(ctx, "Work unit not found in storage")
			return nil, fmt.Errorf("StopWorkUnit with ID %s not found: %w (%v)", req.GetName(), grpcerr.ErrNotFound, err)
		}

		logging.Error(ctx, "Failed to get work unit", "error", err)
		return nil, fmt.Errorf("failed to get work unit: %w (%v)", grpcerr.ErrInternal, err)
	}

	// 5. Enqueue the stop request for the work unit.
	res, err := h.enqueueWorkUnitStop(ctx, workUnit, email)
	if err != nil {
		return nil, h.handleEnqueueActionError(ctx, "stop", workUnit, xid, wid, msgpb.StructuredMessage_ERROR, err)
	}
	operation, execUpdate := res.Operation, res.ExecutionUpdate

	// 6. Update the work unit execution status to STOPPING.
	_, err = h.workUnitUpdater.UpdateWorkUnitExecutions(ctx, xid, []*storage.WorkUnitExecutionUpdate{execUpdate})
	if err != nil {
		logging.Error(ctx, "Failed to update work unit execution statuses", "error", err)
		return nil, fmt.Errorf("failed to update work unit execution statuses: %w (%v)", grpcerr.ErrInternal, err)
	}

	// 7. Return the successful response.
	logging.Info(ctx, "Enqueued stop work unit", "name", req.GetName())
	return operation, nil
}
