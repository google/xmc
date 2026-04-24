// Copyright 2025 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
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

	apipb "xmanager_cloud/experiment_state_server/proto/api_go_proto"
	msgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/validator/validator"
	xidmsgpb "xmanager_cloud/xid_service/proto/messages_go_proto"
	"task/go/status"
)

// UpdateExperimentLaunchState updates the launch state of an experiment. This should be called by
// the client when it's done with an experiment launch.
func (s *Server) UpdateExperimentLaunchState(ctx context.Context, req *apipb.UpdateExperimentLaunchStateRequest) (*msgpb.Experiment, error) {
	ctx = logging.InjectOperation(ctx, "")
	logging.Info(ctx, "Update experiment launch state", "request", req)

	// 1. Authenticate the user.
	logging.Debug(ctx, "Extracting user identity from context")
	email, _, err := s.auth.AuthenticateUserFromContext(ctx)
	if err != nil {
		return nil, err
	}

	// 2. Validate the request payload.
	name := req.GetName()
	xid, err := validator.ValidateExperimentName(name)
	if err != nil {
		logging.Warn(ctx, "UpdateExperimentLaunchState validation failed", "name", name, "error", err)
		return nil, fmt.Errorf("UpdateExperimentLaunchState validation failed: %w (%v)", status.ErrInvalidArgument, err)
	}
	ctx = logging.WithExperimentName(ctx, name)
	if req.GetLaunchState() != msgpb.ExperimentStatus_COMPLETED && req.GetLaunchState() != msgpb.ExperimentStatus_FAILED {
		logging.Warn(ctx, "Invalid launch state", "launch_state", req.GetLaunchState())
		return nil, fmt.Errorf("invalid launch state: %v, must be COMPLETED or FAILED: %w", req.GetLaunchState(), status.ErrInvalidArgument)
	}

	// 3. Authorize the user to update the experiment.
	isAuthorized, err := s.checkUserAuthorization(ctx, validator.ConstructXIDName(xid), email, xidmsgpb.XidAcls_WRITER.Enum())
	if err != nil {
		return nil, err
	}
	if !isAuthorized {
		logging.Warn(ctx, "User not authorized to update experiment", "error", err)
		return nil, fmt.Errorf("user not authorized to update experiment %s: %w", name, status.ErrPermissionDenied)
	}

	// 4. Update the experiment status in the storage layer.
	logging.Debug(ctx, "Updating experiment launch state in storage")
	exp, err := s.storage.UpdateExperimentLaunchState(ctx, xid, req.GetLaunchState())
	if err != nil {
		if status.ExtractCanonicalCode(err) == status.NotFound {
			logging.Warn(ctx, "Experiment not found in storage")
			return nil, fmt.Errorf("experiment with ID %s not found: %w (%v)", name, status.ErrNotFound, err)
		}

		logging.Error(ctx, "Failed to update experiment launch state in storage", "error", err)
		return nil, fmt.Errorf("failed to update experiment launch state for %s in storage: %w (%v)", name, status.ErrInternal, err)
	}

	// 5. Retrieve the XID entry from the XID service. This is required to populate the ACLs in the
	// response.
	xidEntry, err := s.getXid(ctx, validator.ConstructXIDName(xid))
	if err != nil {
		if status.ExtractCanonicalCode(err) == status.PermissionDenied {
			logging.Warn(ctx, "User not authorized to update experiment launch state", "xid", xid, "error", err)
			return nil, fmt.Errorf("user not authorized to update experiment launch state for XID %d: %w (%v)", xid, status.ErrPermissionDenied, err)
		}

		logging.Error(ctx, "XID lookup failed", "error", err)
		return nil, fmt.Errorf("XID lookup failed for XID %d: %w (%v)", xid, status.ErrInternal, err)
	}

	exp.Acls = xidEntry.GetAcls()

	// 6. Return the successful response.
	logging.Info(ctx, "Updated experiment launch state", "experiment", exp)
	return exp, nil
}
