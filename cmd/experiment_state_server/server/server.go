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
// Package server implements the gRPC server for the Experiment State Service.
package server

import (
	"context"

	"xmanager_cloud/auth/auth"
	"xmanager_cloud/experiment_state_server/config"
	apipb "xmanager_cloud/experiment_state_server/proto/api_go_proto"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/storage/storage"
	actuatortaskpb "xmanager_cloud/task_queue/proto/task_go_proto"
	xidgrpcpb "xmanager_cloud/xid_service/proto/api_go_grpc_proto"
)

var _ apipb.ExperimentStateServiceServer = (*Server)(nil)

// Server implements the generated pb.ExperimentStateServiceServer interface.
type Server struct {
	storage storage.Storage

	*ExperimentHandler
	*WorkUnitHandler
	*ArtifactHandler
	*StatusMessageHandler
}

// WorkUnitActuator defines the interface for task actuation on work units.
type WorkUnitActuator interface {
	EnqueueWorkUnitTask(ctx context.Context, target string, workUnit *essmsgpb.WorkUnit, action *actuatortaskpb.Action, operationName string) error
}

// ExperimentHandler handles experiment lifecycle operations.
type ExperimentHandler struct {
	cfg       *config.Config
	storage   storage.Storage
	auth      auth.AuthorizerInterface
	xidClient xidgrpcpb.XidServiceClient
}

// ArtifactHandler handles artifact operations.
type ArtifactHandler struct {
	cfg       *config.Config
	storage   storage.Storage
	auth      auth.AuthorizerInterface
	xidClient xidgrpcpb.XidServiceClient
}

// StatusMessageHandler handles status message operations.
type StatusMessageHandler struct {
	storage   storage.Storage
	auth      auth.AuthorizerInterface
	xidClient xidgrpcpb.XidServiceClient
}

// WorkUnitHandler handles work unit lifecycle and actuation operations.
type WorkUnitHandler struct {
	cfg       *config.Config
	storage   storage.Storage
	auth      auth.AuthorizerInterface
	xidClient xidgrpcpb.XidServiceClient
	actuator  WorkUnitActuator
}

// NewExperimentHandler creates a new ExperimentHandler.
func NewExperimentHandler(cfg *config.Config, storage storage.Storage, auth auth.AuthorizerInterface, xidClient xidgrpcpb.XidServiceClient) *ExperimentHandler {
	return &ExperimentHandler{
		cfg:       cfg,
		storage:   storage,
		auth:      auth,
		xidClient: xidClient,
	}
}

// NewArtifactHandler creates a new ArtifactHandler.
func NewArtifactHandler(cfg *config.Config, storage storage.Storage, auth auth.AuthorizerInterface, xidClient xidgrpcpb.XidServiceClient) *ArtifactHandler {
	return &ArtifactHandler{
		cfg:       cfg,
		storage:   storage,
		auth:      auth,
		xidClient: xidClient,
	}
}

// NewStatusMessageHandler creates a new StatusMessageHandler.
func NewStatusMessageHandler(storage storage.Storage, auth auth.AuthorizerInterface, xidClient xidgrpcpb.XidServiceClient) *StatusMessageHandler {
	return &StatusMessageHandler{
		storage:   storage,
		auth:      auth,
		xidClient: xidClient,
	}
}

// NewWorkUnitHandler creates a new WorkUnitHandler.
func NewWorkUnitHandler(cfg *config.Config, storage storage.Storage, auth auth.AuthorizerInterface, xidClient xidgrpcpb.XidServiceClient, actuator WorkUnitActuator) *WorkUnitHandler {
	return &WorkUnitHandler{
		cfg:       cfg,
		storage:   storage,
		auth:      auth,
		xidClient: xidClient,
		actuator:  actuator,
	}
}

// Params holds dependencies for creating a Server.
type Params struct {
	Config           *config.Config
	Storage          storage.Storage
	Authorizer       auth.AuthorizerInterface
	XidClient        xidgrpcpb.XidServiceClient
	WorkUnitActuator WorkUnitActuator
}

// New creates a new instance of the Experiment State Server.
func New(p Params) *Server {
	return &Server{
		storage:              p.Storage,
		ExperimentHandler:    NewExperimentHandler(p.Config, p.Storage, p.Authorizer, p.XidClient),
		WorkUnitHandler:      NewWorkUnitHandler(p.Config, p.Storage, p.Authorizer, p.XidClient, p.WorkUnitActuator),
		ArtifactHandler:      NewArtifactHandler(p.Config, p.Storage, p.Authorizer, p.XidClient),
		StatusMessageHandler: NewStatusMessageHandler(p.Storage, p.Authorizer, p.XidClient),
	}
}
