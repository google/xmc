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
	"fmt"

	"xmanager_cloud/cmd/experiment_state_server/config"
	apipb "xmanager_cloud/experiment_state_server/proto/api_go_proto"
	spb "xmanager_cloud/experiment_state_server/proto/api_go_proto"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/auth"
	"xmanager_cloud/internal/storage"
	actuatortaskpb "xmanager_cloud/task_queue/proto/task_go_proto"
	xidgrpcpb "xmanager_cloud/xid_service/proto/api_go_proto"
)

var _ apipb.ExperimentStateServiceServer = (*Server)(nil)

// TODO(xmc): Refactor out of in-memory observer pattern to a durable Pub/Sub
// worker service. Relying on the gRPC server to orchestrate side-effects like
// Dashboard charts and Task Queue pushing violates SRP and risks dropped events
// during pod restarts.
type ArtifactObserver interface {
	OnArtifactCreated(ctx context.Context, artifact *essmsgpb.Artifact)
}

// Server implements the generated pb.ExperimentStateServiceServer interface.
type Server struct {
	*ExperimentHandler
	*WorkUnitHandler
	*ArtifactHandler
	*StatusMessageHandler
}

// WorkUnitActuator defines the interface for task actuation on work units.
type WorkUnitActuator interface {
	EnqueueWorkUnitTask(ctx context.Context, target string, workUnit *essmsgpb.WorkUnit, action *actuatortaskpb.Action, operationName string) error
}

// baseHandler encapsulates common infrastructure dependencies shared across domain handlers.
type baseHandler struct {
	cfg       *config.Config
	storage   storage.Storage
	auth      auth.AuthorizerInterface
	xidClient xidgrpcpb.XidServiceClient
}

// ExperimentHandler handles experiment lifecycle operations.
type ExperimentHandler struct {
	baseHandler
	server spb.ExperimentStateServiceServer
}

// ArtifactHandler handles artifact operations.
type ArtifactHandler struct {
	baseHandler
	artifactObservers []ArtifactObserver
}

// StatusMessageHandler handles status message operations.
type StatusMessageHandler struct {
	baseHandler
}

// WorkUnitHandler handles work unit lifecycle and actuation operations.
type WorkUnitHandler struct {
	baseHandler
	actuator WorkUnitActuator
	server   spb.ExperimentStateServiceServer
}

func newExperimentHandler(base baseHandler, server spb.ExperimentStateServiceServer) *ExperimentHandler {
	return &ExperimentHandler{
		baseHandler: base,
		server:      server,
	}
}

func newArtifactHandler(base baseHandler, observers ...ArtifactObserver) *ArtifactHandler {
	return &ArtifactHandler{
		baseHandler:       base,
		artifactObservers: observers,
	}
}

func newStatusMessageHandler(base baseHandler) *StatusMessageHandler {
	return &StatusMessageHandler{
		baseHandler: base,
	}
}

func newWorkUnitHandler(base baseHandler, actuator WorkUnitActuator, server spb.ExperimentStateServiceServer) *WorkUnitHandler {
	return &WorkUnitHandler{
		baseHandler: base,
		actuator:    actuator,
		server:      server,
	}
}

// Params holds dependencies for creating a Server.
type Params struct {
	Config            *config.Config
	Storage           storage.Storage
	Authorizer        auth.AuthorizerInterface
	XidClient         xidgrpcpb.XidServiceClient
	WorkUnitActuator  WorkUnitActuator
	ArtifactObservers []ArtifactObserver
}

// New creates a new instance of the Experiment State Server.
func New(p Params) (*Server, error) {
	if p.Config == nil {
		return nil, fmt.Errorf("server.New: Config is required")
	}
	if p.Storage == nil {
		return nil, fmt.Errorf("server.New: Storage is required")
	}
	if p.Authorizer == nil {
		return nil, fmt.Errorf("server.New: Authorizer is required")
	}
	if p.XidClient == nil {
		return nil, fmt.Errorf("server.New: XidClient is required")
	}
	if p.WorkUnitActuator == nil {
		return nil, fmt.Errorf("server.New: WorkUnitActuator is required")
	}

	base := baseHandler{
		cfg:       p.Config,
		storage:   p.Storage,
		auth:      p.Authorizer,
		xidClient: p.XidClient,
	}

	s := &Server{}
	s.ExperimentHandler = newExperimentHandler(base, s)
	s.WorkUnitHandler = newWorkUnitHandler(base, p.WorkUnitActuator, s)
	s.ArtifactHandler = newArtifactHandler(base, p.ArtifactObservers...)
	s.StatusMessageHandler = newStatusMessageHandler(base)
	return s, nil
}
