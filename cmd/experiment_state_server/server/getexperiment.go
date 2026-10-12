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
)

// GetExperiment orchestrates the retrieval of a single experiment by its ID.
func (h *ExperimentHandler) GetExperiment(ctx context.Context, req *msgpb.GetExperimentRequest) (*msgpb.Experiment, error) {
	ctx = logging.InjectOperation(ctx, "")
	logging.Info(ctx, "Fetch experiment", "request", req)

	// 1. Authenticate the user.
	logging.Debug(ctx, "Extracting user identity from context")
	_, _, err := h.auth.AuthenticateUserFromContext(ctx)
	if err != nil {
		return nil, err
	}

	// 2. Validate the request payload.
	name := req.GetName()
	xid, err := validator.ValidateExperimentName(name)
	if err != nil {
		logging.Warn(ctx, "GetExperiment validation failed", "name", name, "error", err)
		return nil, fmt.Errorf("GetExperiment validation failed: %w (%v)", grpcerr.ErrInvalidArgument, err)
	}
	ctx = logging.WithExperimentName(ctx, name)

	// 3. Retrieve the XID entry from the XID service. This is for authorization purposes and to get
	// the ACLs.
	xidEntry, err := h.getXid(ctx, validator.ConstructXIDName(xid))
	if err != nil {
		if status.Code(err) == codes.PermissionDenied {
			logging.Warn(ctx, "User not authorized to get experiment", "error", err)
			return nil, fmt.Errorf("user not authorized to get experiment %s: %w (%v)", name, grpcerr.ErrPermissionDenied, err)
		}
		if status.Code(err) == codes.NotFound {
			logging.Warn(ctx, "Experiment not found in XID service")
			return nil, fmt.Errorf("experiment with name %s not found: %w (%v)", name, grpcerr.ErrNotFound, err)
		}

		logging.Error(ctx, "XID lookup failed", "error", err)
		return nil, fmt.Errorf("XID lookup failed for XID %d: %w (%v)", xid, grpcerr.ErrInternal, err)
	}

	// 4. Retrieve the experiment from the storage layer.
	logging.Debug(ctx, "Fetching experiment from storage")
	exp, err := h.storage.FetchExperiment(ctx, xid)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			logging.Warn(ctx, "Experiment not found in storage")
			return nil, fmt.Errorf("experiment with name %s not found: %w (%v)", name, grpcerr.ErrNotFound, err)
		}

		logging.Error(ctx, "Failed to retrieve experiment from storage", "error", err)
		return nil, fmt.Errorf("failed to retrieve experiment %s from storage: %w (%v)", name, grpcerr.ErrInternal, err)
	}

	// 4.1. Attach the ACLs from the XID entry to the experiment.
	exp.Acls = xidEntry.GetAcls()

	// 5. Return the successful response.
	logging.Info(ctx, "Fetched experiment", "experiment", exp)
	return exp, nil
}
