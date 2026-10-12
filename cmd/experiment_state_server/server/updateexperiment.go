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

	xidmsgpb "xmanager_cloud/api/protos/xid"
	apipb "xmanager_cloud/experiment_state_server/proto/api_go_proto"
	msgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/data"
	"xmanager_cloud/internal/validator"
)

// UpdateExperiment updates an experiment's data.
func (h *ExperimentHandler) UpdateExperiment(ctx context.Context, req *apipb.UpdateExperimentRequest) (*msgpb.Experiment, error) {
	ctx = logging.InjectOperation(ctx, "")
	logging.Info(ctx, "Update experiment", "request", req)

	// 1. Authenticate the user.
	logging.Debug(ctx, "Extracting user identity from context")
	email, _, err := h.auth.AuthenticateUserFromContext(ctx)
	if err != nil {
		return nil, err
	}

	// 2. Validate the request payload.
	name := req.GetExperiment().GetName()
	xid, err := validator.ValidateExperimentName(name)
	if err != nil {
		logging.Warn(ctx, "UpdateExperiment validation failed", "name", name, "error", err)
		return nil, fmt.Errorf("UpdateExperiment validation failed: %w (%v)", grpcerr.ErrInvalidArgument, err)
	}
	ctx = logging.WithExperimentName(ctx, name)

	if req.GetUpdateMask() == nil {
		logging.Warn(ctx, "UpdateExperiment validation failed for empty update mask", "update_mask", req.GetUpdateMask())
		return nil, fmt.Errorf("UpdateExperiment validation failed: %w", grpcerr.ErrInvalidArgument)
	}

	if err := validator.ValidateUpdateExperimentRequest(req); err != nil {
		logging.Warn(ctx, "UpdateExperiment validation failed", "error", err)
		return nil, err
	}

	// 3. Authorize the user to update the experiment.
	isAuthorized, err := h.checkUserAuthorization(ctx, validator.ConstructXIDName(xid), email, xidmsgpb.XidAcls_WRITER.Enum())
	if err != nil {
		return nil, err
	}
	if !isAuthorized {
		logging.Warn(ctx, "User not authorized to update experiment", "error", err)
		return nil, fmt.Errorf("user not authorized to update experiment %s: %w", name, grpcerr.ErrPermissionDenied)
	}

	// 4. Update the experiment in the storage layer.
	logging.Debug(ctx, "Updating experiment")
	update := &storage.ExperimentUpdate{
		XID:        xid,
		Experiment: req.GetExperiment(),
		UpdateMask: req.GetUpdateMask(),
	}
	res, err := h.storage.UpdateExperiment(ctx, update)
	if err != nil {
		logging.Error(ctx, "Failed to update experiment", "error", err)
		return nil, fmt.Errorf("failed to update experiment %s: %w (%v)", name, grpcerr.ErrInternal, err)
	}
	returnedExp := res.Updated

	// 5. Check if the XID entry needs to be updated.
	xidEntryForUpdate, updateMask, err := data.ExperimentRequiresXIDUpdate(req.GetExperiment(), req.GetUpdateMask())
	if err != nil {
		logging.Error(ctx, "Failed to check if XID entry needs to be updated", "error", err)
		return nil, fmt.Errorf("failed to check if XID entry needs to be updated: %w (%v)", grpcerr.ErrInternal, err)
	}

	// 6. Update the XID entry in the XID service if required.
	if updateMask.GetPaths() != nil {
		ctx := logging.WithXIDName(ctx, validator.ConstructXIDName(xid))
		logging.Debug(ctx, "Updating XID entry")

		request := &xidmsgpb.UpdateXidEntryRequest{
			XidEntry:   xidEntryForUpdate,
			UpdateMask: updateMask,
		}

		if _, err := h.xidClient.UpdateXidEntry(propagateIAPJWT(ctx), request); err != nil {
			logging.Error(ctx, "Failed to update XID entry", "error", err)
			return nil, fmt.Errorf("failed to update XID entry, experiment resource may have been partially updated: %w (%v)", grpcerr.ErrInternal, err)
		}
		returnedExp.Acls = xidEntryForUpdate.GetAcls()
	} else {
		// 6.1. Fetch the XID entry from the XID service if required.
		// This is required to populate the ACLs in the response.
		currentXidEntry, err := h.getXid(ctx, validator.ConstructXIDName(xid))
		if err != nil {
			logging.Error(ctx, "XID lookup failed", "error", err)
			return nil, fmt.Errorf("XID lookup failed for XID %d: %w (%v)", xid, grpcerr.ErrInternal, err)
		}

		returnedExp.Acls = currentXidEntry.GetAcls()
	}

	logging.Info(ctx, "Updated experiment", "experiment", returnedExp)
	return returnedExp, nil
}
