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

	apipb "xmanager_cloud/api/protos/experiment"
	actuatortaskpb "xmanager_cloud/api/protos/task_queue"
	xidgrpcpb "xmanager_cloud/api/protos/xid"
	"xmanager_cloud/cmd/experiment_state_server/artifactlifecycle"
	"xmanager_cloud/cmd/experiment_state_server/config"
	"xmanager_cloud/internal/auth"
	"xmanager_cloud/internal/experiment"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/workunit"
)

var _ apipb.ExperimentStateServiceServer = (*Server)(nil)

// TODO(xmc): Refactor out of in-memory observer pattern to a durable Pub/Sub
// worker service. Relying on the gRPC server to orchestrate side-effects like
// Dashboard charts and Task Queue pushing violates SRP and risks dropped events
// during pod restarts.
type ArtifactObserver interface {
	OnArtifactCreated(ctx context.Context, artifact *apipb.Artifact)
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
	EnqueueWorkUnitTask(ctx context.Context, target string, workUnit *apipb.WorkUnit, action *actuatortaskpb.Action, operationName string) error
}

// baseHandler encapsulates common infrastructure dependencies shared across domain handlers.
type baseHandler struct {
	cfg               *config.Config
	storage           storage.Storage
	auth              auth.AuthorizerInterface
	xidClient         xidgrpcpb.XidServiceClient
	artifactLifecycle *artifactlifecycle.Registry
}

// ExperimentHandler handles experiment lifecycle operations.
type ExperimentHandler struct {
	baseHandler
	driftEvaluator *experiment.DriftEvaluator
	server         apipb.ExperimentStateServiceServer
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
	workUnitUpdater *workunit.Updater
	actuator        WorkUnitActuator
	server          apipb.ExperimentStateServiceServer
}

func newExperimentHandler(base baseHandler, server apipb.ExperimentStateServiceServer) *ExperimentHandler {
	var enableComponents bool
	if base.cfg != nil {
		enableComponents = base.cfg.EnableComponents
	}
	return &ExperimentHandler{
		baseHandler:    base,
		driftEvaluator: experiment.NewDriftEvaluator(base.storage, enableComponents),
		server:         server,
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

func newWorkUnitHandler(base baseHandler, actuator WorkUnitActuator, server apipb.ExperimentStateServiceServer) *WorkUnitHandler {
	return &WorkUnitHandler{
		baseHandler:     base,
		workUnitUpdater: workunit.NewUpdater(base.storage),
		actuator:        actuator,
		server:          server,
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
	ArtifactLifecycle *artifactlifecycle.Registry
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

	lifecycle := p.ArtifactLifecycle
	if lifecycle == nil {
		lifecycle = artifactlifecycle.NewRegistry()
	}

	base := baseHandler{
		cfg:               p.Config,
		storage:           p.Storage,
		auth:              p.Authorizer,
		xidClient:         p.XidClient,
		artifactLifecycle: lifecycle,
	}

	s := &Server{}
	s.ExperimentHandler = newExperimentHandler(base, s)
	s.WorkUnitHandler = newWorkUnitHandler(base, p.WorkUnitActuator, s)
	s.ArtifactHandler = newArtifactHandler(base, p.ArtifactObservers...)
	s.StatusMessageHandler = newStatusMessageHandler(base)
	return s, nil
}

// ArtifactLifecycle returns the registered artifact lifecycle registry.
func (s *Server) ArtifactLifecycle() *artifactlifecycle.Registry {
	if s == nil || s.WorkUnitHandler == nil {
		return nil
	}
	return s.WorkUnitHandler.artifactLifecycle
}
