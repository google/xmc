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

// Package server implements the gRPC server for the XID Service.
// It handles incoming requests, performs validation and authorization,
// and orchestrates calls to the storage layer.
package server

import (
	"fmt"

	"xmanager_cloud/auth/auth"
	"xmanager_cloud/storage/storage"
	"xmanager_cloud/xid_service/config"
	apipb "xmanager_cloud/xid_service/proto/api_go_proto"
)

var _ apipb.XidServiceServer = (*XIDServer)(nil)

// XIDServer implements the XidServiceServer interface.
type XIDServer struct {
	storage storage.Storage
	config  config.Config
	auth    auth.AuthorizerInterface
}

// Params holds dependencies for creating an XIDServer.
type Params struct {
	Config     config.Config
	Authorizer auth.AuthorizerInterface
	Storage    storage.Storage
}

// New creates a new instance of the XID Server.
func New(p Params) (*XIDServer, error) {
	if p.Storage == nil {
		return nil, fmt.Errorf("server.New: Storage is required")
	}
	if p.Authorizer == nil {
		return nil, fmt.Errorf("server.New: Authorizer is required")
	}
	return &XIDServer{
		storage: p.Storage,
		config:  p.Config,
		auth:    p.Authorizer,
	}, nil
}

// NewXIDServer creates a new instance of the XID Server.
func NewXIDServer(p Params) (*XIDServer, error) {
	return New(p)
}
