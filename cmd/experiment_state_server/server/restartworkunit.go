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
	msgpb "xmanager_cloud/api/protos/experiment"
	xidmsgpb "xmanager_cloud/api/protos/xid"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/validator"
)

// RestartWorkUnit sends a request to restart a work unit.
func (h *WorkUnitHandler) RestartWorkUnit(ctx context.Context, req *msgpb.RestartWorkUnitRequest) (*lropb.Operation, error) {
	ctx = logging.InjectOperation(ctx, "")
	logging.Info(ctx, "Enqueue restart work unit", "request", req)

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
		return nil, fmt.Errorf("failed to validate work unit name: %w: %w", grpcerr.ErrInvalidArgument, err)
	}
	ctx = logging.WithWorkUnitName(ctx, req.GetName())

	// 3. Authorize the caller to restart work units for this experiment.
	authorized, err := h.checkUserAuthorization(ctx, validator.ConstructXIDName(xid), email, xidmsgpb.XidAcls_WRITER.Enum())
	if err != nil {
		return nil, err
	}
	if !authorized {
		logging.Warn(ctx, "User not authorized to restart work units", "user", email)
		return nil, fmt.Errorf("user %s is not authorized to restart work units for XID %d: %w", email, xid, grpcerr.ErrPermissionDenied)
	}

	// 4. Retrieve the work unit from the storage layer. We need this to get the executable later.
	workUnit, err := h.storage.FetchWorkUnit(ctx, xid, wid)
	if err != nil {
		logging.Error(ctx, "Failed to get work unit", "error", err)
		return nil, fmt.Errorf("failed to get work unit: %w: %w", grpcerr.ErrNotFound, err)
	}
	oldExecutionName := workUnit.GetCurrentWorkUnitExecution().GetName()

	// 5. Restart the work unit execution.
	restartedExecutions, err := restartWorkUnitExecutions(ctx, h.storage, email, xid, []int64{wid})
	if err != nil {
		logging.Error(ctx, "Failed to restart work unit executions", "error", err)
		return nil, fmt.Errorf("failed to restart work unit executions: %w: %w", grpcerr.ErrInternal, err)
	}

	// 5.1. Extract the restarted work unit execution and patch the work unit with it.
	restartedExecution := restartedExecutions[wid]
	if restartedExecution == nil {
		logging.Error(ctx, "No execution was restarted for work unit")
		return nil, fmt.Errorf("no execution was restarted for work unit %s: %w", req.GetName(), grpcerr.ErrInternal)
	}
	workUnit.CurrentWorkUnitExecution = restartedExecution

	// 6. Get the experiment priority for this experiment.
	experiment, err := h.storage.FetchExperiment(ctx, xid)
	if err != nil {
		logging.Error(ctx, "Failed to get experiment priority", "error", err)
		return nil, fmt.Errorf("failed to get experiment priority: %w: %w", grpcerr.ErrInternal, err)
	}

	// 7. Enqueue the restart request for the work unit.
	operation, err := h.enqueueWorkUnitRestart(ctx, oldExecutionName, workUnit, email, experiment.GetPriority())
	if err != nil {
		return nil, h.handleEnqueueActionError(ctx, "restart", workUnit, xid, wid, msgpb.StructuredMessage_FATAL, err)
	}

	// 8. Return the successful response.
	logging.Info(ctx, "Enqueued restart request for work unit", "name", req.GetName())
	return operation, nil
}
