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

// Server implements the generated pb.ExperimentStateServiceServer interface.
type Server struct {
	apipb.UnimplementedExperimentStateServiceServer

	cfg             *config.Config
	storage         storage.Storage
	auth            auth.AuthorizerInterface
	xidClient       xidgrpcpb.XidServiceClient
	taskQueueClient TaskQueueClient
}

// TaskQueueClient defines the interface for task queue operations.
type TaskQueueClient interface {
	EnqueueWorkUnitTask(ctx context.Context, target string, workUnit *essmsgpb.WorkUnit, action *actuatortaskpb.Action, operationName string) error
	Close() error
}

// Params holds dependencies for creating a Server.
type Params struct {
	Config          *config.Config
	Storage         storage.Storage
	Authorizer      auth.AuthorizerInterface
	XidClient       xidgrpcpb.XidServiceClient
	TaskQueueClient TaskQueueClient
}

// New creates a new instance of the Experiment State Server.
func New(p Params) *Server {
	return &Server{
		cfg:             p.Config,
		storage:         p.Storage,
		auth:            p.Authorizer,
		xidClient:       p.XidClient,
		taskQueueClient: p.TaskQueueClient,
	}
}
