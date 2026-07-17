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

	"task/go/status"
	apipb "xmanager_cloud/experiment_state_server/proto/api_go_proto"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/validator"
)

// ListWorkUnitExecutions returns all executions that were run for a work unit. This includes the
// current execution, and past executions that have been run for this work unit.
func (s *Server) ListWorkUnitExecutions(ctx context.Context, req *apipb.ListWorkUnitExecutionsRequest) (*apipb.ListWorkUnitExecutionsResponse, error) {
	ctx = logging.InjectOperation(ctx, "")
	logging.Info(ctx, "Fetch work unit executions", "request", req)

	// 1. Authenticate the user.
	logging.Debug(ctx, "Extracting user identity from context")
	_, _, err := s.auth.AuthenticateUserFromContext(ctx)
	if err != nil {
		return nil, err
	}

	// 2. Validate the request payload.
	name := req.GetParent()
	xid, wid, err := validator.ValidateWorkUnitName(name)
	if err != nil {
		logging.Warn(ctx, "ListWorkUnitExecutions validation failed", "name", name, "error", err)
		return nil, fmt.Errorf("ListWorkUnitExecutions validation failed: %w (%v)", status.ErrInvalidArgument, err)
	}
	ctx = logging.WithWorkUnitName(ctx, name)

	// 3. Retrieve the XID entry from the XID service. This is purely for authorization purposes.
	_, err = s.getXid(ctx, validator.ConstructXIDName(xid))
	if err != nil {
		if status.ExtractCanonicalCode(err) == status.PermissionDenied {
			logging.Warn(ctx, "User not authorized to list work unit executions", "xid", xid, "error", err)
			return nil, fmt.Errorf("user not authorized to list work unit executions for XID %d: %w (%v)", xid, status.ErrPermissionDenied, err)
		}

		logging.Error(ctx, "XID lookup failed", "xid", xid, "error", err)
		return nil, fmt.Errorf("XID lookup failed for XID %d: %w (%v)", xid, status.ErrInternal, err)
	}

	// 4. Retrieve the work unit executions from the storage layer.
	wues, err := s.storage.ListWorkUnitExecutions(ctx, xid, wid)
	if err != nil {
		logging.Error(ctx, "Failed to retrieve work unit executions from storage", "name", name, "error", err)
		return nil, fmt.Errorf("failed to retrieve work unit executions %s from storage: %w (%v)", name, status.ErrInternal, err)
	}

	if len(wues) == 0 {
		logging.Warn(ctx, "No work unit executions found for request")
	}

	response := &apipb.ListWorkUnitExecutionsResponse{
		WorkUnitExecutions: wues,
	}

	// 5. Return the successful response.
	logging.Info(ctx, "Fetched work unit executions", "response", response)
	return response, nil
}
