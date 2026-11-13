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
	msgpb "xmanager_cloud/api/protos/experiment"
	xidmsgpb "xmanager_cloud/api/protos/xid"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/validator"
)

type createArtifactContext struct {
	creator   string
	parentXID int64
}

func (h *ArtifactHandler) validateAndPrepareArtifactCreation(ctx context.Context, req *msgpb.CreateArtifactRequest) (createArtifactContext, *msgpb.Artifact, error) {
	creator, _, err := h.auth.AuthenticateUserFromContext(ctx)
	if err != nil {
		return createArtifactContext{}, nil, err
	}
	parentXID, parentWID, err := validator.ValidateArtifactParent(req.GetParent())
	if err != nil {
		logging.Error(ctx, "Failed to validate parent", "parent", req.GetParent(), "error", err)
		return createArtifactContext{}, nil, fmt.Errorf("failed to validate parent %s: %w (%v)", req.GetParent(), grpcerr.ErrInvalidArgument, err)
	}
	ctx = logging.WithArtifactParent(ctx, req.GetParent())
	authorized, err := h.checkUserAuthorization(ctx, validator.ConstructXIDName(parentXID), creator, xidmsgpb.XidAcls_READER.Enum())
	if err != nil {
		return createArtifactContext{}, nil, err
	}
	if !authorized {
		logging.Warn(ctx, "User not authorized to create artifacts for XID", "user", creator, "xid", parentXID)
		return createArtifactContext{}, nil, fmt.Errorf("user %s is not authorized to create artifacts for XID %d: %w", creator, parentXID, grpcerr.ErrPermissionDenied)
	}
	if err := validator.ValidateCreateArtifactRequest(req); err != nil {
		logging.Warn(ctx, "CreateArtifact validation failed", "error", err)
		return createArtifactContext{}, nil, fmt.Errorf("CreateArtifact validation failed: %w (%v)", grpcerr.ErrInvalidArgument, err)
	}

	artifact := validator.FillArtifactNullValues(req.GetArtifact())
	if err := h.fillManagedXprofDefaults(ctx, artifact, parentXID, parentWID); err != nil {
		return createArtifactContext{}, nil, err
	}
	if err := h.validateChartArtifactPayload(ctx, artifact, req.GetParent()); err != nil {
		return createArtifactContext{}, nil, err
	}
	return createArtifactContext{creator: creator, parentXID: parentXID}, artifact, nil
}

func (h *ArtifactHandler) notifyArtifactObservers(ctx context.Context, artifact *msgpb.Artifact) error {
	for _, obs := range h.artifactObservers {
		artifactCopy, ok := proto.Clone(artifact).(*msgpb.Artifact)
		if !ok {
			logging.Error(ctx, "Failed to clone retArtifact for async provisioning")
			return fmt.Errorf("failed to clone artifact: %w", grpcerr.ErrInternal)
		}
		obs.OnArtifactCreated(context.WithoutCancel(ctx), artifactCopy)
	}
	return nil
}

func (h *ArtifactHandler) CreateArtifact(ctx context.Context, req *msgpb.CreateArtifactRequest) (*msgpb.Artifact, error) {
	ctx = logging.InjectOperation(ctx, "")
	logging.Info(ctx, "Create artifact", "request", req)

	idCtx, artifact, err := h.validateAndPrepareArtifactCreation(ctx, req)
	if err != nil {
		return nil, err
	}
	ctx = logging.WithArtifactParent(ctx, req.GetParent())

	logging.Debug(ctx, "Calling XID service to provision ID", "creator", idCtx.creator)
	artifactXIDEntry, err := h.createXid(ctx, idCtx.creator, h.cfg.EndpointURL, artifact.Acls, validator.ConstructXIDName(idCtx.parentXID))
	if err != nil {
		logging.Error(ctx, "Failed to provision XID", "error", err)
		return nil, fmt.Errorf("failed to provision XID: %w", err)
	}
	artifactXID, err := validator.ValidateXIDName(artifactXIDEntry.GetName())
	if err != nil {
		logging.Error(ctx, "Failed to validate XID name", "xid_name", artifactXIDEntry.GetName(), "error", err)
		return nil, fmt.Errorf("failed to validate XID name %s: %w (%v)", artifactXIDEntry.GetName(), grpcerr.ErrInternal, err)
	}

	name := validator.ConstructArtifactName(artifactXID)
	ctx = logging.WithArtifactName(ctx, name)
	newArtifact := &msgpb.Artifact{
		Name: name, Parent: req.GetParent(), Url: artifact.Url, Priority: artifact.Priority,
		Tags: artifact.Tags, Notes: artifact.Notes, LifecyclePhase: artifact.LifecyclePhase,
		DataType: artifact.DataType, MimeType: artifact.MimeType, AdditionalInfo: artifact.AdditionalInfo, Payload: artifact.Payload,
	}

	logging.Debug(ctx, "Persisting artifact")
	retArtifact, err := h.storage.CreateArtifact(ctx, newArtifact)
	if err != nil {
		logging.Error(ctx, "Failed to save artifact", "artifact_name", name, "error", err)
		return nil, fmt.Errorf("failed to save artifact %s: %w (%v)", name, grpcerr.ErrInternal, err)
	}
	retArtifact.Acls = artifactXIDEntry.Acls

	if err := h.notifyArtifactObservers(ctx, retArtifact); err != nil {
		return nil, err
	}
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
