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

	emptypb "protobuf/empty_go_proto"
	"task/go/status"
	apipb "xmanager_cloud/experiment_state_server/proto/api_go_proto"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/validator"
	xidmsgpb "xmanager_cloud/xid_service/proto/messages_go_proto"
)

// DeleteArtifact deletes an artifact.
func (s *Server) DeleteArtifact(ctx context.Context, req *apipb.DeleteArtifactRequest) (*emptypb.Empty, error) {
	ctx = logging.InjectOperation(ctx, "")
	logging.Info(ctx, "Delete artifact", "request", req)

	// 1. Authenticate the user.
	email, _, err := s.auth.AuthenticateUserFromContext(ctx)
	if err != nil {
		return nil, err
	}

	// 2. Validate the request payload.
	name := req.GetName()
	aid, err := validator.ValidateArtifactName(name)
	if err != nil {
		logging.Warn(ctx, "DeleteArtifact validation failed", "error", err)
		return nil, fmt.Errorf("DeleteArtifact validation failed: %w (%v)", status.ErrInvalidArgument, err)
	}
	ctx = logging.WithArtifactName(ctx, name)

	// 3. Check if artifact exists before authorizing.
	artifact, err := s.storage.FetchArtifact(ctx, aid)
	if err != nil {
		if status.ExtractCanonicalCode(err) == status.NotFound {
			logging.Warn(ctx, "Artifact not found", "error", err)
			return nil, fmt.Errorf("artifact %s not found: %w (%v)", name, status.ErrNotFound, err)
		}
		logging.Error(ctx, "Failed to get artifact", "error", err)
		return nil, fmt.Errorf("failed to get artifact %s: %w (%v)", name, status.ErrInternal, err)
	}
	ctx = logging.WithArtifactParent(ctx, artifact.GetParent())

	// 4. Authorize the user to delete the artifact.
	isAuthorized, err := s.checkUserAuthorization(ctx, validator.ConstructXIDName(aid), email, xidmsgpb.XidAcls_OWNER.Enum())
	if err != nil {
		return nil, err
	}
	if !isAuthorized {
		logging.Warn(ctx, "User not authorized to delete artifact", "error", err)
		return nil, fmt.Errorf("user not authorized to delete artifact %s: %w", name, status.ErrPermissionDenied)
	}

	// 5. Delete the artifact from the storage layer.
	logging.Debug(ctx, "Deleting artifact", "name", req.GetName())
	err = s.storage.DeleteArtifact(ctx, aid)
	if err != nil {
		logging.Error(ctx, "Failed to delete artifact", "error", err)
		return nil, fmt.Errorf("failed to delete artifact %s: %w (%v)", name, status.ErrInternal, err)
	}

	// 6. Return the successful response.
	logging.Info(ctx, "Deleted artifact", "name", name)
	return &emptypb.Empty{}, nil
}
