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
	"strings"

	"google.golang.org/protobuf/proto"
	apipb "xmanager_cloud/experiment_state_server/proto/api_go_proto"
	msgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/validator"
	xidmsgpb "xmanager_cloud/xid_service/proto/messages_go_proto"
)

func (h *ArtifactHandler) CreateArtifact(ctx context.Context, req *apipb.CreateArtifactRequest) (*msgpb.Artifact, error) {
	ctx = logging.InjectOperation(ctx, "")
	logging.Info(ctx, "Create artifact", "request", req)

	// 1. Authenticate the user.
	creator, _, err := h.auth.AuthenticateUserFromContext(ctx)
	if err != nil {
		return nil, err
	}

	// 2. Validate the parent experiment or work unit format and enrich logging context.
	parentXID, parentWID, err := validator.ValidateArtifactParent(req.GetParent())
	if err != nil {
		logging.Error(ctx, "Failed to validate parent", "parent", req.GetParent(), "error", err)
		return nil, fmt.Errorf("failed to validate parent %s: %w (%v)", req.GetParent(), grpcerr.ErrInvalidArgument, err)
	}
	ctx = logging.WithArtifactParent(ctx, req.GetParent())

	// 3. Authorize the user to create artifacts for the parent experiment or work unit.
	authorized, err := h.checkUserAuthorization(ctx, validator.ConstructXIDName(parentXID), creator, xidmsgpb.XidAcls_READER.Enum())
	if err != nil {
		return nil, err
	}
	if !authorized {
		logging.Warn(ctx, "User not authorized to create artifacts for XID", "user", creator, "xid", parentXID)
		return nil, fmt.Errorf("user %s is not authorized to create artifacts for XID %d: %w", creator, parentXID, grpcerr.ErrPermissionDenied)
	}

	// 4. Validate the request payload.
	if err := validator.ValidateCreateArtifactRequest(req); err != nil {
		logging.Warn(ctx, "CreateArtifact validation failed", "error", err)
		return nil, fmt.Errorf("CreateArtifact validation failed: %w (%v)", grpcerr.ErrInvalidArgument, err)
	}

	// 4.1. Fill in any null values in the request payload.
	artifact := validator.FillArtifactNullValues(req.GetArtifact())

	// 4.2. Populate default metadata for ManagedXprof artifacts if unset.
	if err := h.fillManagedXprofDefaults(ctx, artifact, parentXID, parentWID); err != nil {
		return nil, err
	}

	// 4.3. Validate ChartArtifact payload.
	if err := h.validateChartArtifactPayload(ctx, artifact, req.GetParent()); err != nil {
		return nil, err
	}

	// 5. Provision an XIDEntry for the artifact.
	logging.Debug(ctx, "Calling XID service to provision ID", "creator", creator)
	artifactXIDEntry, err := h.createXid(ctx, creator, h.cfg.EndpointURL, artifact.Acls, validator.ConstructXIDName(parentXID))
	if err != nil {
		logging.Error(ctx, "Failed to provision XID", "error", err)
		return nil, fmt.Errorf("failed to provision XID: %w", err)
	}
	logging.Debug(ctx, "Successfully provisioned XID")

	// 4.1. Get the XID from the XIDEntry.
	artifactXID, err := validator.ValidateXIDName(artifactXIDEntry.GetName())
	if err != nil {
		logging.Error(ctx, "Failed to validate XID name", "xid_name", artifactXIDEntry.GetName(), "error", err)
		return nil, fmt.Errorf("failed to validate XID name %s: %w (%v)", artifactXIDEntry.GetName(), grpcerr.ErrInternal, err)
	}

	// 5. Assemble the artifact object.
	logging.Debug(ctx, "Assembling artifact object")
	name := validator.ConstructArtifactName(artifactXID)
	ctx = logging.WithArtifactName(ctx, name)
	newArtifact := &msgpb.Artifact{
		Name:           name,
		Parent:         req.GetParent(),
		Url:            artifact.Url,
		Priority:       artifact.Priority,
		Tags:           artifact.Tags,
		Notes:          artifact.Notes,
		LifecyclePhase: artifact.LifecyclePhase,
		DataType:       artifact.DataType,
		MimeType:       artifact.MimeType,
		AdditionalInfo: artifact.AdditionalInfo,
		Payload:        artifact.Payload,
	}

	// 6. Persist the artifact to the storage layer.
	logging.Debug(ctx, "Persisting artifact")
	retArtifact, err := h.storage.CreateArtifact(ctx, newArtifact)
	if err != nil {
		logging.Error(ctx, "Failed to save artifact", "artifact_name", newArtifact.GetName(), "error", err)
		return nil, fmt.Errorf("failed to save artifact %s: %w (%v)", newArtifact.GetName(), grpcerr.ErrInternal, err)
	}

	// 6.1. Set the artifact's ACLs to the XIDEntry's ACLs.
	retArtifact.Acls = artifactXIDEntry.Acls

	// 6.2. Trigger post-creation hooks asynchronously (Observers).
	if len(h.artifactObservers) > 0 {
		for _, obs := range h.artifactObservers {
			artifactCopy, ok := proto.Clone(retArtifact).(*msgpb.Artifact)
			if !ok {
				logging.Error(ctx, "Failed to clone retArtifact for async provisioning")
				return nil, fmt.Errorf("failed to clone artifact: %w", grpcerr.ErrInternal)
			}
			obs.OnArtifactCreated(context.WithoutCancel(ctx), artifactCopy)
		}
	}

	// 7. Return the successful response.
	logging.Info(ctx, "Created artifact")
	return retArtifact, nil
}

func (h *ArtifactHandler) validateChartArtifactPayload(ctx context.Context, artifact *msgpb.Artifact, parent string) error {
	if chartArtifact := artifact.GetPayload().GetChart(); chartArtifact != nil {
		if _, _, err := validator.ValidateWorkUnitName(parent); err != nil {
			logging.Warn(ctx, "ChartArtifact requires a Work Unit parent", "parent", parent, "error", err)
			return fmt.Errorf("ChartArtifact requires a Work Unit parent: %w (%v)", grpcerr.ErrInvalidArgument, err)
		}
		if chartArtifact.GetStatus() != msgpb.ChartArtifact_STATUS_UNSPECIFIED || chartArtifact.GetChart() != nil || chartArtifact.GetErrorMessage() != "" {
			logging.Warn(ctx, "ChartArtifact client-supplied status, chart, or error_message values are not allowed")
			return fmt.Errorf("ChartArtifact client-supplied status, chart, or error_message values are not allowed: %w", grpcerr.ErrInvalidArgument)
		}
		chartArtifact.Status = msgpb.ChartArtifact_PROVISIONING
	}
	return nil
}

// fillManagedXprofDefaults populates fallback DisplayName and RunGroup values
// for ManagedXprof artifacts when either field is empty. It retrieves the parent
// experiment title from storage to synthesize consistent, identifiable names.
func (h *ArtifactHandler) fillManagedXprofDefaults(ctx context.Context, artifact *msgpb.Artifact, xid, wid int64) error {
	mx := artifact.GetPayload().GetManagedXprof()
	if mx == nil {
		return nil
	}
	if strings.TrimSpace(mx.GetDisplayName()) != "" && strings.TrimSpace(mx.GetRunGroup()) != "" {
		return nil
	}
	exp, err := h.storage.FetchExperiment(ctx, xid)
	if err != nil {
		logging.Error(ctx, "Failed to fetch experiment for default ManagedXprof metadata", "xid", xid, "error", err)
		return fmt.Errorf("failed to fetch experiment %d to populate ManagedXprof defaults: %w", xid, err)
	}
	validator.DefaultManagedXprofMetadata(mx, exp.GetTitle(), wid)
	return nil
}
