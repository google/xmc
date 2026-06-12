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
	msgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/validator/validator"
)

// GetArtifact orchestrates the retrieval of a single artifact by its ID.
func (s *Server) GetArtifact(ctx context.Context, req *apipb.GetArtifactRequest) (*msgpb.Artifact, error) {
	ctx = logging.InjectOperation(ctx, "")
	logging.Info(ctx, "Received GetArtifact request", "request", req)

	_, _, err := s.auth.AuthenticateUserFromContext(ctx)
	if err != nil {
		return nil, err
	}

	// 2. Validate the request payload.
	name := req.GetName()
	aid, err := validator.ValidateArtifactName(name)
	if err != nil {
		logging.Warn(ctx, "GetArtifact validation failed", "name", name, "error", err)
		return nil, fmt.Errorf("GetArtifact validation failed: %w (%v)", status.ErrInvalidArgument, err)
	}
	ctx = logging.WithArtifactName(ctx, name)

	// 3. Retrieve the XID entry from the XID service. This is for authorization purposes and to get
	// the ACLs.
	xidEntry, err := s.getXid(ctx, validator.ConstructXIDName(aid))
	if err != nil {
		switch status.ExtractCanonicalCode(err) {
		case status.PermissionDenied:
			logging.Warn(ctx, "User not authorized to get artifact", "error", err)
			return nil, fmt.Errorf("user not authorized to get artifact %s: %w (%v)", name, status.ErrPermissionDenied, err)
		case status.NotFound:
			logging.Warn(ctx, "XID for artifact not found", "error", err)
			return nil, fmt.Errorf("artifact with ID %s not found: %w (%v)", name, status.ErrNotFound, err)
		default:
			logging.Error(ctx, "XID lookup failed", "error", err)
			return nil, fmt.Errorf("XID lookup failed for XID %d: %w (%v)", aid, status.ErrInternal, err)
		}
	}

	// 4. Retrieve the artifact from the storage layer.
	logging.Info(ctx, "Fetching artifact from storage")
	artifact, err := s.storage.FetchArtifact(ctx, aid)
	if err != nil {
		if status.ExtractCanonicalCode(err) == status.NotFound {
			logging.Warn(ctx, "Artifact not found in storage")
			return nil, fmt.Errorf("artifact with ID %s not found: %w (%v)", name, status.ErrNotFound, err)
		}

		logging.Error(ctx, "Failed to retrieve artifact from storage", "error", err)
		return nil, fmt.Errorf("failed to retrieve artifact %s from storage: %w (%v)", name, status.ErrInternal, err)
	}
	ctx = logging.WithArtifactParent(ctx, artifact.GetParent())

	// 4.1. Attach the ACLs from the XID entry to the artifact.
	artifact.Acls = xidEntry.GetAcls()

	// 5. Return the successful response.
	logging.Info(ctx, "Successfully fetched artifact", "artifact", artifact)
	return artifact, nil
}
