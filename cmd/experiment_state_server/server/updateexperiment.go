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

	msgpb "xmanager_cloud/api/protos/experiment"
	xidmsgpb "xmanager_cloud/api/protos/xid"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/data"
	"xmanager_cloud/internal/validator"
)

func (h *ExperimentHandler) validateAndAuthorizeExperimentUpdate(ctx context.Context, req *msgpb.UpdateExperimentRequest) (int64, string, error) {
	logging.Debug(ctx, "Extracting user identity from context")
	email, _, err := h.auth.AuthenticateUserFromContext(ctx)
	if err != nil {
		return 0, "", err
	}

	name := req.GetExperiment().GetName()
	xid, err := validator.ValidateExperimentName(name)
	if err != nil {
		logging.Warn(ctx, "UpdateExperiment validation failed", "name", name, "error", err)
		return 0, "", fmt.Errorf("UpdateExperiment validation failed: %w (%v)", grpcerr.ErrInvalidArgument, err)
	}
	ctx = logging.WithExperimentName(ctx, name)
	if req.GetUpdateMask() == nil {
		logging.Warn(ctx, "UpdateExperiment validation failed for empty update mask", "update_mask", req.GetUpdateMask())
		return 0, "", fmt.Errorf("UpdateExperiment validation failed: %w", grpcerr.ErrInvalidArgument)
	}
	if err := validator.ValidateUpdateExperimentRequest(req); err != nil {
		logging.Warn(ctx, "UpdateExperiment validation failed", "error", err)
		return 0, "", err
	}

	isAuthorized, err := h.checkUserAuthorization(ctx, validator.ConstructXIDName(xid), email, xidmsgpb.XidAcls_WRITER.Enum())
	if err != nil {
		return 0, "", err
	}
	if !isAuthorized {
		logging.Warn(ctx, "User not authorized to update experiment", "error", err)
		return 0, "", fmt.Errorf("user not authorized to update experiment %s: %w", name, grpcerr.ErrPermissionDenied)
	}
	return xid, name, nil
}

func (h *ExperimentHandler) syncExperimentXIDACLs(ctx context.Context, xid int64, req *msgpb.UpdateExperimentRequest) (*xidmsgpb.XidAcls, error) {
	xidEntryForUpdate, updateMask, err := data.ExperimentRequiresXIDUpdate(req.GetExperiment(), req.GetUpdateMask())
	if err != nil {
		logging.Error(ctx, "Failed to check if XID entry needs to be updated", "error", err)
		return nil, fmt.Errorf("failed to check if XID entry needs to be updated: %w (%v)", grpcerr.ErrInternal, err)
	}

	if updateMask.GetPaths() == nil {
		// Populate the existing ACLs in the UpdateExperiment response even when ACLs are unchanged.
		currentXidEntry, err := h.getXid(ctx, validator.ConstructXIDName(xid))
		if err != nil {
			logging.Error(ctx, "XID lookup failed", "error", err)
			return nil, fmt.Errorf("XID lookup failed for XID %d: %w (%v)", xid, grpcerr.ErrInternal, err)
		}
		return currentXidEntry.GetAcls(), nil
	}

	ctx = logging.WithXIDName(ctx, validator.ConstructXIDName(xid))
	logging.Debug(ctx, "Updating XID entry")
	updatedXidEntry, err := h.xidClient.UpdateXidEntry(propagateIAPJWT(ctx), &xidmsgpb.UpdateXidEntryRequest{
		XidEntry:   xidEntryForUpdate,
		UpdateMask: updateMask,
	})
	if err != nil {
		logging.Error(ctx, "Failed to update XID entry", "error", err)
		return nil, fmt.Errorf("failed to update XID entry, experiment resource may have been partially updated: %w (%v)", grpcerr.ErrInternal, err)
	}
	// Return the ACLs as stored by the XID service, which normalizes principals.
	return updatedXidEntry.GetAcls(), nil
}

// UpdateExperiment updates an experiment's data.
func (h *ExperimentHandler) UpdateExperiment(ctx context.Context, req *msgpb.UpdateExperimentRequest) (*msgpb.Experiment, error) {
	ctx = logging.InjectOperation(ctx, "")
	logging.Info(ctx, "Update experiment", "request", req)

	xid, name, err := h.validateAndAuthorizeExperimentUpdate(ctx, req)
	if err != nil {
		return nil, err
	}
	ctx = logging.WithExperimentName(ctx, name)

	logging.Debug(ctx, "Updating experiment")
	res, err := h.storage.UpdateExperiment(ctx, &storage.ExperimentUpdate{
		XID:        xid,
		Experiment: req.GetExperiment(),
		UpdateMask: req.GetUpdateMask(),
	})
	if err != nil {
		logging.Error(ctx, "Failed to update experiment", "error", err)
		return nil, fmt.Errorf("failed to update experiment %s: %w (%v)", name, grpcerr.ErrInternal, err)
	}
	returnedExp := res.Updated

	returnedExp.Acls, err = h.syncExperimentXIDACLs(ctx, xid, req)
	if err != nil {
		return nil, err
	}

	logging.Info(ctx, "Updated experiment", "experiment", returnedExp)
	return returnedExp, nil
}
