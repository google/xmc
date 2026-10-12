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

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/validator/validator"

	apipb "xmanager_cloud/experiment_state_server/proto/api_go_proto"
	msgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	xidmsgpb "xmanager_cloud/xid_service/proto/messages_go_proto"
)

// GetCurrentWorkUnitExecution returns the current execution that was run for a work unit.
func (h *WorkUnitHandler) GetCurrentWorkUnitExecution(ctx context.Context, req *apipb.GetCurrentWorkUnitExecutionRequest) (*msgpb.WorkUnitExecution, error) {
	ctx = logging.InjectOperation(ctx, "")
	logging.Info(ctx, "Fetch current work unit execution", "request", req)

	// 1. Authenticate the user.
	logging.Debug(ctx, "Extracting user identity from context")
	email, _, err := h.auth.AuthenticateUserFromContext(ctx)
	if err != nil {
		return nil, err
	}

	// 2. Validate the request payload.
	name := req.GetName()
	xid, wid, err := validator.ValidateWorkUnitName(name)
	if err != nil {
		logging.Warn(ctx, "GetCurrentWorkUnitExecution validation failed", "error", err)
		return nil, fmt.Errorf("GetCurrentWorkUnitExecution validation failed: %w (%v)", grpcerr.ErrInvalidArgument, err)
	}
	ctx = logging.WithWorkUnitName(ctx, name)

	// 3. Authorize the caller to get the current work unit execution for this experiment.
	authorized, err := h.checkUserAuthorization(ctx, validator.ConstructXIDName(xid), email, xidmsgpb.XidAcls_READER.Enum())
	if err != nil {
		return nil, err
	}
	if !authorized {
		logging.Warn(ctx, "User not authorized to get current work unit execution", "user", email)
		return nil, fmt.Errorf("user %s is not authorized to get the current work unit execution for XID %d: %w", email, xid, grpcerr.ErrPermissionDenied)
	}

	// 4. Retrieve the work unit from the storage layer.
	wue, err := h.getCurrentWorkUnitExecution(ctx, name, xid, wid)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			logging.Warn(ctx, "Current work unit execution not found in storage")
			return nil, fmt.Errorf("GetCurrentWorkUnitExecution with ID %s not found: %w (%v)", name, grpcerr.ErrNotFound, err)
		}
		logging.Error(ctx, "Failed to retrieve current work unit execution from storage", "error", err)
		return nil, fmt.Errorf("failed to retrieve current work unit execution %s from storage: %w (%v)", name, grpcerr.ErrInternal, err)
	}
	ctx = logging.WithWorkUnitExecutionName(ctx, wue.GetName())

	// 5. Return the successful response.
	logging.Info(ctx, "Fetched current work unit execution", "work_unit_execution", wue)
	return wue, nil
}

func (h *WorkUnitHandler) getCurrentWorkUnitExecution(ctx context.Context, name string, xid int64, wid int64) (*msgpb.WorkUnitExecution, error) {
	logging.Debug(ctx, "Fetching current work unit execution from storage")

	wue, err := h.storage.FetchCurrentWorkUnitExecution(ctx, xid, wid)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil, fmt.Errorf("current work unit execution with ID %s not found: %w (%v)", name, grpcerr.ErrNotFound, err)
		}

		logging.Error(ctx, "Failed to retrieve current work unit execution from storage", "error", err)
		return nil, fmt.Errorf("failed to retrieve current work unit execution %s from storage: %w (%v)", name, grpcerr.ErrInternal, err)
	}

	return wue, nil
}
