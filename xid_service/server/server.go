// Package server implements the gRPC server for the XID Service.
// It handles incoming requests, performs validation and authorization,
// and orchestrates calls to the storage layer.
package server

import (
	"xmanager_cloud/auth/auth"
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
