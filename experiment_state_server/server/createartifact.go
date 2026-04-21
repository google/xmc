package server

import (
	"context"
	"fmt"

	apipb "xmanager_cloud/experiment_state_server/proto/api_go_proto"
	msgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/validator/validator"
	xidmsgpb "xmanager_cloud/xid_service/proto/messages_go_proto"
	"task/go/status"
)

func (s *Server) CreateArtifact(ctx context.Context, req *apipb.CreateArtifactRequest) (*msgpb.Artifact, error) {
	ctx = logging.InjectOperation(ctx, "")
	logging.Info(ctx, "Create artifact", "request", req)

	// 1. Authenticate the user.
	creator, _, err := s.auth.AuthenticateUserFromContext(ctx)
	if err != nil {
		return nil, err
	}

	// 2. Validate the request payload.
	if err := validator.ValidateCreateArtifactRequest(req); err != nil {
		logging.Warn(ctx, "CreateArtifact validation failed", "error", err)
		return nil, fmt.Errorf("CreateArtifact validation failed: %w (%v)", status.ErrInvalidArgument, err)
	}

	// 2.1. Fill in any null values in the request payload.
	artifact := validator.FillArtifactNullValues(req.GetArtifact())

	// 3. Get the parent experiment or work unit.
	parentXID, _, err := validator.ValidateArtifactParent(req.GetParent())
	if err != nil {
		logging.Error(ctx, "Failed to validate parent", "parent", req.GetParent(), "error", err)
		return nil, fmt.Errorf("failed to validate parent %s: %w (%v)", req.GetParent(), status.ErrInvalidArgument, err)
	}
	ctx = logging.WithArtifactParent(ctx, req.GetParent())

	// 3.1. Authorize the user to write against the parent experiment or work unit.
	authorized, err := s.checkUserAuthorization(ctx, validator.ConstructXIDName(parentXID), creator, xidmsgpb.XidAcls_READER.Enum())
	if err != nil {
		return nil, err
	}
	if !authorized {
		logging.Warn(ctx, "User not authorized to create artifacts for XID", "user", creator, "xid", parentXID)
		return nil, fmt.Errorf("user %s is not authorized to create artifacts for XID %d: %w", creator, parentXID, status.ErrPermissionDenied)
	}

	// 4. Provision an XIDEntry for the artifact.
	logging.Debug(ctx, "Calling XID service to provision ID", "creator", creator)
	artifactXIDEntry, err := s.createXid(ctx, creator, s.cfg.EndpointURL, artifact.Acls, validator.ConstructXIDName(parentXID))
	if err != nil {
		logging.Error(ctx, "Failed to provision XID", "error", err)
		return nil, fmt.Errorf("failed to provision XID: %w", err)
	}
	logging.Debug(ctx, "Successfully provisioned XID")

	// 4.1. Get the XID from the XIDEntry.
	artifactXID, err := validator.ValidateXIDName(artifactXIDEntry.GetName())
	if err != nil {
		logging.Error(ctx, "Failed to validate XID name", "xid_name", artifactXIDEntry.GetName(), "error", err)
		return nil, fmt.Errorf("failed to validate XID name %s: %w (%v)", artifactXIDEntry.GetName(), status.ErrInternal, err)
	}

	// 5. Assemble the artifact object.
	logging.Debug(ctx, "Assembling artifact object")
	name := validator.ConstructArtifactName(artifactXID)
	ctx = logging.WithArtifactName(ctx, name)
	newArtifact := &msgpb.Artifact{
		Name:           name,
		Parent:         req.GetParent(),
		Url:            artifact.Url,
		Tags:           artifact.Tags,
		Notes:          artifact.Notes,
		LifecyclePhase: artifact.LifecyclePhase,
		DataType:       artifact.DataType,
		MimeType:       artifact.MimeType,
		AdditionalInfo: artifact.AdditionalInfo,
	}

	// 6. Persist the artifact to the storage layer.
	logging.Debug(ctx, "Persisting artifact")
	retArtifact, err := s.storage.CreateArtifact(ctx, newArtifact)
	if err != nil {
		logging.Error(ctx, "Failed to save artifact", "artifact_name", newArtifact.GetName(), "error", err)
		return nil, fmt.Errorf("failed to save artifact %s: %w (%v)", newArtifact.GetName(), status.ErrInternal, err)
	}

	// 6.1. Set the artifact's ACLs to the XIDEntry's ACLs.
	retArtifact.Acls = artifactXIDEntry.Acls

	// 7. Return the successful response.
	logging.Info(ctx, "Created artifact")
	return retArtifact, nil
}
