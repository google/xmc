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
	xidmsgpb "xmanager_cloud/api/protos/xid"
	apipb "xmanager_cloud/experiment_state_server/proto/api_go_proto"
	msgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/data"
	"xmanager_cloud/internal/validator"
)

// UpdateArtifact updates an artifact's data.
func (h *ArtifactHandler) UpdateArtifact(ctx context.Context, req *apipb.UpdateArtifactRequest) (*msgpb.Artifact, error) {
	ctx = logging.InjectOperation(ctx, "")
	logging.Info(ctx, "Received UpdateArtifact request", "request", req)

	// 1. Authenticate the user.
	email, _, err := h.auth.AuthenticateUserFromContext(ctx)
	if err != nil {
		return nil, err
	}

	// 2. Validate the request payload.
	name := req.GetArtifact().GetName()
	aid, err := validator.ValidateArtifactName(name)
	if err != nil {
		logging.Warn(ctx, "UpdateArtifact validation failed", "name", name, "error", err)
		return nil, fmt.Errorf("UpdateArtifact validation failed: %w (%v)", grpcerr.ErrInvalidArgument, err)
	}
	ctx = logging.WithArtifactName(ctx, name)

	if req.GetUpdateMask() == nil || len(req.GetUpdateMask().GetPaths()) == 0 {
		logging.Warn(ctx, "UpdateArtifact validation failed: update mask is nil or empty")
		return nil, fmt.Errorf("UpdateArtifact validation failed: %w", grpcerr.ErrInvalidArgument)
	}

	// 3. Authorize the user to update the artifact.
	isAuthorized, err := h.checkUserAuthorization(ctx, validator.ConstructXIDName(aid), email, xidmsgpb.XidAcls_WRITER.Enum())
	if err != nil {
		return nil, err
	}
	if !isAuthorized {
		logging.Warn(ctx, "User not authorized to update artifact", "error", err)
		return nil, fmt.Errorf("user not authorized to update artifact %s: %w", name, grpcerr.ErrPermissionDenied)
	}

	// 4. Check if the XID entry needs to be updated.
	xidEntryForUpdate, updateMask, err := data.ArtifactRequiresXIDUpdate(req.GetArtifact(), req.GetUpdateMask())
	if err != nil {
		logging.Error(ctx, "Failed to check if XID entry needs to be updated", "error", err)
		return nil, fmt.Errorf("failed to check if XID entry needs to be updated: %w (%v)", grpcerr.ErrInternal, err)
	}

	// 5. Fetch the XID entry from the XID service if no XID update is required.
	// This is required to populate the ACLs in the response.
	// We do this before updating the artifact to avoid partial updates.
	var xidEntry *xidmsgpb.XidEntry
	if updateMask.GetPaths() == nil {
		xidEntry, err = h.getXid(ctx, validator.ConstructXIDName(aid))
		if err != nil {
			if status.Code(err) == codes.PermissionDenied {
				logging.Warn(ctx, "User not authorized to read artifact", "error", err)
				return nil, fmt.Errorf("user not authorized to read artifact %s: %w (%v)", name, grpcerr.ErrPermissionDenied, err)
			}

			logging.Error(ctx, "XID lookup failed", "error", err)
			return nil, fmt.Errorf("XID lookup failed for XID %d: %w (%v)", aid, grpcerr.ErrInternal, err)
		}
	}

	// 6. Update the artifact in the storage layer.
	logging.Info(ctx, "Updating artifact", "name", req.GetArtifact().GetName())
	update := &storage.ArtifactUpdate{
		AID:        aid,
		Artifact:   req.GetArtifact(),
		UpdateMask: req.GetUpdateMask(),
	}
	res, err := h.storage.UpdateArtifact(ctx, update)
	if err != nil {
		logging.Error(ctx, "Failed to update artifact", "error", err)
		return nil, fmt.Errorf("failed to update artifact %s: %w (%v)", name, grpcerr.ErrInternal, err)
	}
	returnedArtifact := res.Updated

	// 7. Update the XID entry in the XID service if required.
	if updateMask.GetPaths() != nil {
		ctx := logging.WithXIDName(ctx, validator.ConstructXIDName(aid))
		logging.Info(ctx, "Updating XID entry", "xid_entry", xidEntryForUpdate)

		request := &xidmsgpb.UpdateXidEntryRequest{
			XidEntry:   xidEntryForUpdate,
			UpdateMask: updateMask,
		}

		if _, err := h.xidClient.UpdateXidEntry(propagateIAPJWT(ctx), request); err != nil {
			logging.Error(ctx, "Failed to update XID entry", "error", err)
			return nil, fmt.Errorf("failed to update XID entry: %w (%v)", grpcerr.ErrInternal, err)
		}
		returnedArtifact.Acls = xidEntryForUpdate.GetAcls()
	} else {
		returnedArtifact.Acls = xidEntry.GetAcls()
	}

	// 8. Return the successful response.
	logging.Info(ctx, "Successfully updated artifact", "artifact", returnedArtifact)
	return returnedArtifact, nil
}
