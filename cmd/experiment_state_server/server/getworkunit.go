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

	"github.com/google/xmc/internal/grpcerr"
	"github.com/google/xmc/internal/logging"
	"github.com/google/xmc/internal/validator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	msgpb "github.com/google/xmc/api/protos/experiment"
	xidmsgpb "github.com/google/xmc/api/protos/xid"
)

// GetWorkUnit orchestrates the retrieval of a single work unit by its ID.
func (h *WorkUnitHandler) GetWorkUnit(ctx context.Context, req *msgpb.GetWorkUnitRequest) (*msgpb.WorkUnit, error) {
	ctx = logging.InjectOperation(ctx, "")
	logging.Info(ctx, "Fetch work unit", "request", req)

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
		logging.Warn(ctx, "GetWorkUnit validation failed", "name", name, "error", err)
		return nil, fmt.Errorf("GetWorkUnit validation failed: %w (%v)", grpcerr.ErrInvalidArgument, err)
	}
	ctx = logging.WithWorkUnitName(ctx, name)

	// 3. Authorize the caller to get the work unit from this experiment.
	authorized, err := h.checkUserAuthorization(ctx, validator.ConstructXIDName(xid), email, xidmsgpb.XidAcls_READER.Enum())
	if err != nil {
		return nil, err
	}
	if !authorized {
		logging.Warn(ctx, "User not authorized to get work unit", "user", email)
		return nil, fmt.Errorf("user %s is not authorized to get the current work unit for XID %d: %w", email, xid, grpcerr.ErrPermissionDenied)
	}

	// 4. Retrieve the work unit from the storage layer.
	logging.Debug(ctx, "Fetching work unit from storage")
	wu, err := h.storage.FetchWorkUnit(ctx, xid, wid)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			logging.Warn(ctx, "Work unit not found in storage")
			return nil, fmt.Errorf("GetWorkUnit with ID %s not found: %w (%v)", name, grpcerr.ErrNotFound, err)
		}

		logging.Error(ctx, "Failed to retrieve work unit from storage", "error", err)
		return nil, fmt.Errorf("failed to retrieve work unit %s from storage: %w (%v)", name, grpcerr.ErrInternal, err)
	}

	// 5. Return the successful response.
	logging.Info(ctx, "Fetched work unit", "work_unit", wu)
	return wu, nil
}
