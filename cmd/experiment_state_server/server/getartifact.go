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
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	msgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/validator"
)

// GetArtifact orchestrates the retrieval of a single artifact by its ID.
func (h *ArtifactHandler) GetArtifact(ctx context.Context, req *msgpb.GetArtifactRequest) (*msgpb.Artifact, error) {
	ctx = logging.InjectOperation(ctx, "")
	logging.Info(ctx, "Received GetArtifact request", "request", req)

	_, _, err := h.auth.AuthenticateUserFromContext(ctx)
	if err != nil {
		return nil, err
	}

	// 2. Validate the request payload.
	name := req.GetName()
	aid, err := validator.ValidateArtifactName(name)
	if err != nil {
		logging.Warn(ctx, "GetArtifact validation failed", "name", name, "error", err)
		return nil, fmt.Errorf("GetArtifact validation failed: %w (%v)", grpcerr.ErrInvalidArgument, err)
	}
	ctx = logging.WithArtifactName(ctx, name)

	// 3. Retrieve the XID entry from the XID service. This is for authorization purposes and to get
	// the ACLs.
	xidEntry, err := h.getXid(ctx, validator.ConstructXIDName(aid))
	if err != nil {
		switch status.Code(err) {
		case codes.PermissionDenied:
			logging.Warn(ctx, "User not authorized to get artifact", "error", err)
			return nil, fmt.Errorf("user not authorized to get artifact %s: %w (%v)", name, grpcerr.ErrPermissionDenied, err)
		case codes.NotFound:
			logging.Warn(ctx, "XID for artifact not found", "error", err)
			return nil, fmt.Errorf("artifact with ID %s not found: %w (%v)", name, grpcerr.ErrNotFound, err)
		default:
			logging.Error(ctx, "XID lookup failed", "error", err)
			return nil, fmt.Errorf("XID lookup failed for XID %d: %w (%v)", aid, grpcerr.ErrInternal, err)
		}
	}

	// 4. Retrieve the artifact from the storage layer.
	logging.Info(ctx, "Fetching artifact from storage")
	artifact, err := h.storage.FetchArtifact(ctx, aid)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			logging.Warn(ctx, "Artifact not found in storage")
			return nil, fmt.Errorf("artifact with ID %s not found: %w (%v)", name, grpcerr.ErrNotFound, err)
		}

		logging.Error(ctx, "Failed to retrieve artifact from storage", "error", err)
		return nil, fmt.Errorf("failed to retrieve artifact %s from storage: %w (%v)", name, grpcerr.ErrInternal, err)
	}
	ctx = logging.WithArtifactParent(ctx, artifact.GetParent())

	h.enrichArtifactOutputFields(ctx, artifact)

	// 4.1. Attach the ACLs from the XID entry to the artifact.
	artifact.Acls = xidEntry.GetAcls()

	// 5. Return the successful response.
	logging.Info(ctx, "Successfully fetched artifact", "artifact", artifact)
	return artifact, nil
}

// enrichArtifactOutputFields enriches output fields of an artifact if it is in the EXECUTION or OUTPUT phase.
func (h *ArtifactHandler) enrichArtifactOutputFields(ctx context.Context, artifact *msgpb.Artifact) {
	if artifact == nil {
		return
	}
	if phase := artifact.GetLifecyclePhase(); phase != msgpb.Artifact_EXECUTION && phase != msgpb.Artifact_OUTPUT {
		return
	}
	h.enrichManagedXprof(ctx, artifact)
}

// enrichManagedXprof enriches the artifact with MLRun URL if it is a ManagedXprof payload.
func (h *ArtifactHandler) enrichManagedXprof(ctx context.Context, artifact *msgpb.Artifact) {
	xprof := artifact.GetPayload().GetManagedXprof()
	if xprof == nil || xprof.GetXprof().GetXprofUrl() != "" {
		return
	}

	// Enrichment is best-effort: the parent may be an Experiment rather than a
	// WorkUnit, or its execution may not exist yet. These are expected states,
	// so log at Warn to avoid triggering error-rate alerts.
	xid, wid, err := validator.ValidateWorkUnitName(artifact.GetParent())
	if err != nil {
		logging.Warn(ctx, "Failed to validate work unit name for xprof artifact", "error", err)
		return
	}

	execution, err := h.storage.FetchCurrentWorkUnitExecution(ctx, xid, wid)
	if err != nil {
		// A missing execution is an expected pre-execution state: the artifact can
		// be read during work unit creation, before any execution exists. Skip
		// enrichment quietly instead of logging a spurious error on every work unit
		// creation.
		if isWorkUnitExecutionNotFound(err) {
			return
		}
		logging.Error(ctx, "Failed to fetch current WorkUnitExecution for xprof artifact", "error", err)
		return
	}

	if execution == nil {
		return
	}

	// Surface the MLRun URL both on the nested xprof payload and the top-level
	// artifact URL so clients that read either field link directly to the run.
	mlrunURL := execution.GetMetadata().GetMlrunUrl()
	updateXprofURL(xprof, mlrunURL)
	if mlrunURL != "" {
		if artifact.Url == nil {
			artifact.Url = &msgpb.Url{}
		}
		artifact.Url.Url = mlrunURL
	}
}

// isWorkUnitExecutionNotFound reports whether err indicates the work unit
// execution does not exist yet. This is an expected state (an artifact can be
// read before its work unit has an execution), so callers skip enrichment
// rather than treating it as a failure.
func isWorkUnitExecutionNotFound(err error) bool {
	return errors.Is(err, grpcerr.ErrNotFound)
}

// updateXprofURL applies the MLRun URL to the ManagedXProf artifact payload if it is not empty.
func updateXprofURL(xprof *msgpb.ManagedXprofArtifact, mlrunURL string) {
	if xprof == nil || mlrunURL == "" {
		return
	}
	if xprof.Xprof == nil {
		xprof.Xprof = &msgpb.XProfUIInstance{}
	}
	xprof.Xprof.XprofUrl = mlrunURL
}
