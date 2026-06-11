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
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/validator/validator"

	apipb "xmanager_cloud/experiment_state_server/proto/api_go_proto"
	msgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	xidmsgpb "xmanager_cloud/xid_service/proto/messages_go_proto"
)

// GetExperimentStatus orchestrates the retrieval of a single experiment's status by its ID.
func (s *Server) GetExperimentStatus(ctx context.Context, req *apipb.GetExperimentStatusRequest) (*msgpb.ExperimentStatus, error) {
	ctx = logging.InjectOperation(ctx, "")
	logging.Info(ctx, "Fetch experiment status", "request", req)

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
		logging.Warn(ctx, "GetExperimentStatus validation failed", "name", name, "error", err)
		return nil, fmt.Errorf("GetExperimentStatus validation failed: %w (%v)", status.ErrInvalidArgument, err)
	}
	ctx = logging.WithExperimentName(ctx, name)

	// 3. Authorize the caller to get the experiment status for this experiment.
	authorized, err := s.checkUserAuthorization(ctx, validator.ConstructXIDName(xid), email, xidmsgpb.XidAcls_READER.Enum())
	if err != nil {
		if status.ExtractCanonicalCode(err) == status.NotFound {
			logging.Warn(ctx, "Experiment not found in XID service")
			return nil, fmt.Errorf("experiment with ID %s not found: %w (%v)", name, status.ErrNotFound, err)
		}

		return nil, err
	}
	if !authorized {
		logging.Warn(ctx, "User not authorized to get experiment status", "user", email)
		return nil, fmt.Errorf("user %s is not authorized to get the experiment status for XID %d: %w", email, xid, status.ErrPermissionDenied)
	}

	// 4. Retrieve the experiment from the storage layer.
	logging.Debug(ctx, "Fetching experiment status from storage")
	exp, err := s.storage.FetchExperimentStatus(ctx, xid)
	if err != nil {
		if status.ExtractCanonicalCode(err) == status.NotFound {
			logging.Warn(ctx, "Experiment not found in storage")
			return nil, fmt.Errorf("experiment with ID %s not found: %w (%v)", name, status.ErrNotFound, err)
		}

		logging.Error(ctx, "Failed to retrieve experiment from storage", "error", err)
		return nil, fmt.Errorf("failed to retrieve experiment %s from storage: %w (%v)", name, status.ErrInternal, err)
	}

	// 5. Return the successful response.
	logging.Info(ctx, "Fetched experiment status", "status", exp)
	return exp, nil
}
