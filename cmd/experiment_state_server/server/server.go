// Copyright 2025 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
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
	taskQueueClient taskQueueClientInterface
}

type taskQueueClientInterface interface {
	EnqueueWorkUnitTask(ctx context.Context, target string, workUnit *essmsgpb.WorkUnit, action *actuatortaskpb.Action, operationName string) error
	Close() error
}

// New creates a new instance of the Experiment State Server.
func New(storage storage.Storage, cfg *config.Config, auth auth.AuthorizerInterface, xidClient xidgrpcpb.XidServiceClient, taskQueueClient taskQueueClientInterface) *Server {
	return &Server{
		cfg:             cfg,
		storage:         storage,
		auth:            auth,
		xidClient:       xidClient,
		taskQueueClient: taskQueueClient,
	}
}
