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
	"xmanager_cloud/internal/auth"
	"xmanager_cloud/storage/storage"
	"xmanager_cloud/xid_service/config"
)

// XIDServer implements the XidServiceServer interface.
type XIDServer struct {
	storage storage.Storage
	config  config.Config
	auth    auth.AuthorizerInterface
}

// NewXIDServer creates a new XIDServer.
// The server uses the provided storage layer, configuration, and authorization.
func NewXIDServer(cfg config.Config, auth auth.AuthorizerInterface, storage storage.Storage) *XIDServer {
	return &XIDServer{
		storage: storage,
		config:  cfg,
		auth:    auth,
	}
}
