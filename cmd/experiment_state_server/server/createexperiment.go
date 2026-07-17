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
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/validator"
	xidmsgpb "xmanager_cloud/xid_service/proto/messages_go_proto"

	apipb "xmanager_cloud/experiment_state_server/proto/api_go_proto"
	msgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
)

// CreateExperiment orchestrates the creation of a new experiment.
func (s *Server) CreateExperiment(ctx context.Context, req *apipb.CreateExperimentRequest) (*msgpb.Experiment, error) {
	ctx = logging.InjectOperation(ctx, "")
	logging.Info(ctx, "Received CreateExperiment request", "request", req)

	// 1. Authenticate the user.
	logging.Info(ctx, "Extracting user identity from context")
	creator, _, err := s.auth.AuthenticateUserFromContext(ctx)
	if err != nil {
		return nil, err
	}

	// 2. Validate the request payload.
	if err := validator.ValidateCreateExperimentRequest(req); err != nil {
		logging.Warn(ctx, "CreateExperiment validation failed", "error", err)
		return nil, fmt.Errorf("CreateExperiment validation failed: %v: %w", err, status.ErrInvalidArgument)
	}

	// 2.1. Fill in any null values in the request payload.
	exp := validator.FillExperimentNullValues(req.GetExperiment())

	// 2.2. Inject the execution environment on experiment creation.
	executionEnv, err := s.defaultExecutionEnvironment()
	if err != nil {
		return nil, err
	}
	exp, err = executionEnv.InjectOnExperimentCreation(ctx, s, exp)
	if err != nil {
		logging.Error(ctx, "Failed to inject execution environment data into experiment", "error", err)
		return nil, status.Errorf(status.Internal, "failed to inject execution environment data into experiment: %v", err)
	}

	// 3. Provision an XIDEntry for the experiment.
	logging.Info(ctx, "Calling XID service to provision ID", "creator", creator)
	xidEntry, err := s.createXid(ctx, creator, s.cfg.EndpointURL, exp.Acls, "")
	if err != nil {
		logging.Error(ctx, "Failed to provision XID", "error", err)
		return nil, fmt.Errorf("failed to provision XID: %v", err)
	}

	// 3.1. Get the XID from the XIDEntry.
	xid, err := validator.ValidateXIDName(xidEntry.GetName())
	if err != nil {
		logging.Error(ctx, "Failed to validate XID name", "xid_name", xidEntry.GetName(), "error", err)
		return nil, fmt.Errorf("failed to validate XID name %s: %v: %w", xidEntry.GetName(), err, status.ErrInternal)
	}
	ctx = logging.WithXIDName(ctx, xidEntry.GetName())
	logging.Info(ctx, "Successfully provisioned XID")

	// 3.2. Check if the current user has write permissions on the XIDEntry.
	authorized, err := s.checkUserAuthorization(ctx, validator.ConstructXIDName(xid), creator, xidmsgpb.XidAcls_WRITER.Enum())
	if err != nil {
		return nil, err
	}
	if !authorized {
		logging.Warn(ctx, "Creator did not give themselves write permissions on the XID", "user", creator)
		return nil, fmt.Errorf("creator %s did not give themselves write permissions on the XID %d: %w", creator, xid, status.ErrPermissionDenied)
	}

	name := validator.ConstructExperimentName(xid)
	ctx = logging.WithExperimentName(ctx, name)

	// 4. Assemble the experiment object.
	logging.Info(ctx, "Assembling experiment object")
	newExp := &msgpb.Experiment{
		Name:      name,
		Title:     exp.Title,
		Creator:   creator,
		Status:    newExperimentStatus(name),
		Priority:  exp.Priority,
		Urls:      exp.GetUrls(),
		Tags:      exp.GetTags(),
		Notes:     exp.GetNotes(),
		Resources: []*msgpb.ResourceSet{},
	}

	// 5. Persist the experiment to the storage layer.
	logging.Info(ctx, "Persisting experiment", "experiment_name", newExp.Name)
	retExp, err := s.storage.CreateExperiment(ctx, newExp)
	if err != nil {
		logging.Error(ctx, "Failed to save experiment", "experiment_name", newExp.GetName(), "error", err)
		return nil, fmt.Errorf("failed to save experiment %s: %v: %w", newExp.GetName(), err, status.ErrInternal)
	}

	// 5.1. Set the experiment's ACLs to the XIDEntry's ACLs.
	retExp.Acls = xidEntry.Acls

	// 6. Return the successful response.
	logging.Info(ctx, "Successfully created experiment", "experiment_name", retExp.GetName())
	return retExp, nil
}
