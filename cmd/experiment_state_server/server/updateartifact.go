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
	msgpb "xmanager_cloud/api/protos/experiment"
	xidmsgpb "xmanager_cloud/api/protos/xid"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/data"
	"xmanager_cloud/internal/validator"
)

func (h *ArtifactHandler) validateAndAuthorizeArtifactUpdate(ctx context.Context, req *msgpb.UpdateArtifactRequest) (int64, string, error) {
	email, _, err := h.auth.AuthenticateUserFromContext(ctx)
	if err != nil {
		return 0, "", err
	}
	name := req.GetArtifact().GetName()
	aid, err := validator.ValidateArtifactName(name)
	if err != nil {
		logging.Warn(ctx, "UpdateArtifact validation failed", "name", name, "error", err)
		return 0, "", fmt.Errorf("UpdateArtifact validation failed: %w (%v)", grpcerr.ErrInvalidArgument, err)
	}
	ctx = logging.WithArtifactName(ctx, name)
	if req.GetUpdateMask() == nil || len(req.GetUpdateMask().GetPaths()) == 0 {
		logging.Warn(ctx, "UpdateArtifact validation failed: update mask is nil or empty")
		return 0, "", fmt.Errorf("UpdateArtifact validation failed: %w", grpcerr.ErrInvalidArgument)
	}
	isAuthorized, err := h.checkUserAuthorization(ctx, validator.ConstructXIDName(aid), email, xidmsgpb.XidAcls_WRITER.Enum())
	if err != nil {
		return 0, "", err
	}
	if !isAuthorized {
		logging.Warn(ctx, "User not authorized to update artifact", "error", err)
		return 0, "", fmt.Errorf("user not authorized to update artifact %s: %w", name, grpcerr.ErrPermissionDenied)
	}
	return aid, name, nil
}

func (h *ArtifactHandler) resolvePreUpdateArtifactXID(ctx context.Context, aid int64, name string) (*xidmsgpb.XidEntry, error) {
	// Fetch the XID entry before updating the artifact to avoid partial updates.
	xidEntry, err := h.getXid(ctx, validator.ConstructXIDName(aid))
	if err == nil {
		return xidEntry, nil
	}
	if status.Code(err) == codes.PermissionDenied {
		logging.Warn(ctx, "User not authorized to read artifact", "error", err)
		return nil, fmt.Errorf("user not authorized to read artifact %s: %w (%v)", name, grpcerr.ErrPermissionDenied, err)
	}
	logging.Error(ctx, "XID lookup failed", "error", err)
	return nil, fmt.Errorf("XID lookup failed for XID %d: %w (%v)", aid, grpcerr.ErrInternal, err)
}

func (h *ArtifactHandler) applyArtifactXIDACLs(ctx context.Context, aid int64, updateXIDReq *xidmsgpb.UpdateXidEntryRequest, prefetchedXID *xidmsgpb.XidEntry) (*xidmsgpb.XidAcls, error) {
	if updateXIDReq == nil {
		return prefetchedXID.GetAcls(), nil
	}
	ctx = logging.WithXIDName(ctx, validator.ConstructXIDName(aid))
	logging.Info(ctx, "Updating XID entry", "xid_entry", updateXIDReq.GetXidEntry())
	updatedXidEntry, err := h.xidClient.UpdateXidEntry(propagateIAPJWT(ctx), updateXIDReq)
	if err != nil {
		logging.Error(ctx, "Failed to update XID entry", "error", err)
		return nil, fmt.Errorf("failed to update XID entry: %w (%v)", grpcerr.ErrInternal, err)
	}
	// Return the ACLs as stored by the XID service, which normalizes principals.
	return updatedXidEntry.GetAcls(), nil
}

// UpdateArtifact updates an artifact's data.
func (h *ArtifactHandler) UpdateArtifact(ctx context.Context, req *msgpb.UpdateArtifactRequest) (*msgpb.Artifact, error) {
	ctx = logging.InjectOperation(ctx, "")
	logging.Info(ctx, "Received UpdateArtifact request", "request", req)

	aid, name, err := h.validateAndAuthorizeArtifactUpdate(ctx, req)
	if err != nil {
		return nil, err
	}
	ctx = logging.WithArtifactName(ctx, name)

	xidEntryForUpdate, updateMask, err := data.ArtifactRequiresXIDUpdate(req.GetArtifact(), req.GetUpdateMask())
	if err != nil {
		logging.Error(ctx, "Failed to check if XID entry needs to be updated", "error", err)
		return nil, fmt.Errorf("failed to check if XID entry needs to be updated: %w (%v)", grpcerr.ErrInternal, err)
	}

	var prefetchedXID *xidmsgpb.XidEntry
	var updateXIDReq *xidmsgpb.UpdateXidEntryRequest
	if updateMask.GetPaths() == nil {
		prefetchedXID, err = h.resolvePreUpdateArtifactXID(ctx, aid, name)
		if err != nil {
			return nil, err
		}
	} else {
		updateXIDReq = &xidmsgpb.UpdateXidEntryRequest{XidEntry: xidEntryForUpdate, UpdateMask: updateMask}
	}

	logging.Info(ctx, "Updating artifact", "name", req.GetArtifact().GetName())
	res, err := h.storage.UpdateArtifact(ctx, &storage.ArtifactUpdate{
		AID:        aid,
		Artifact:   req.GetArtifact(),
		UpdateMask: req.GetUpdateMask(),
	})
	if err != nil {
		logging.Error(ctx, "Failed to update artifact", "error", err)
		return nil, fmt.Errorf("failed to update artifact %s: %w (%v)", name, grpcerr.ErrInternal, err)
	}
	returnedArtifact := res.Updated

	returnedArtifact.Acls, err = h.applyArtifactXIDACLs(ctx, aid, updateXIDReq, prefetchedXID)
	if err != nil {
		return nil, err
	}

	logging.Info(ctx, "Successfully updated artifact", "artifact", returnedArtifact)
	return returnedArtifact, nil
}
