// Copyright 2025 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package server

import (
	"context"
	"errors"
	"fmt"
	"time"

	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	"golang/google_api/cloudidentity/v1beta1/cloudidentity"
	"golang/google_api/option/option"
	"golang/grpc/credentials/insecure/insecure"
	"golang/grpc/grpc"
	authz "xmanager_cloud/auth/auth"
	"xmanager_cloud/experiment_state_server/config"
	apipb "xmanager_cloud/experiment_state_server/proto/api_go_proto"
	"xmanager_cloud/logging/logging"
	oauth "xmanager_cloud/pkg/auth/oauth2"
	"xmanager_cloud/security/keyreloader"
	ss "xmanager_cloud/storage/spanner/spanner"
	tq "xmanager_cloud/task_queue/client"
	xidgrpcpb "xmanager_cloud/xid_service/proto/api_go_grpc_proto"
)

var _ apipb.ExperimentStateServiceServer = (*ProdServer)(nil)

// ProdServer encapsulates the production Server instance along with its underlying infrastructure dependencies and lifecycle.
type ProdServer struct {
	*Server
	SpannerClient   *spanner.Client
	XidClient       xidgrpcpb.XidServiceClient
	TaskQueueClient *tq.Client
	cleanup         func() error
}

// Close closes all underlying production client connections.
func (p *ProdServer) Close() error {
	if p == nil || p.cleanup == nil {
		return nil
	}
	return p.cleanup()
}

// NewProd creates and initializes a production Server instance along with its dependencies.
func NewProd(ctx context.Context, cfg *config.Config, oauthCfg *oauth.Config) (*ProdServer, error) {
	sessionPoolConfig := spanner.SessionPoolConfig{
		TrackSessionHandles: true,
		InactiveTransactionRemovalOptions: spanner.InactiveTransactionRemovalOptions{
			ActionOnInactiveTransaction: spanner.Warn,
		},
	}
	spannerClient, err := spanner.NewClientWithConfig(
		ctx,
		cfg.SpannerDatabaseName,
		spanner.ClientConfig{SessionPoolConfig: sessionPoolConfig},
		option.WithQuotaProject(cfg.QuotaProjectID),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create spanner client: %w", err)
	}

	idService, err := cloudidentity.NewService(ctx)
	if err != nil {
		spannerClient.Close()
		return nil, fmt.Errorf("failed to create cloud identity service: %w", err)
	}

	storage, err := ss.NewStorageWithConfig(spannerClient, ss.StorageConfig{StatusMessageHistoryEnabled: cfg.StatusMessageHistoryEnabled})
	if err != nil {
		spannerClient.Close()
		return nil, fmt.Errorf("failed to create storage: %w", err)
	}

	auth, err := authz.NewAuthorizer(ctx, idService, 10*time.Minute, 1*time.Minute, []string{cfg.HTTPAudience, cfg.GRPCAudience, oauthCfg.ClientID}, cfg.IAPIssuer, false, []byte(oauthCfg.JWTSecret))
	if err != nil {
		spannerClient.Close()
		return nil, fmt.Errorf("failed to create authorizer: %w", err)
	}

	logging.Info(ctx, "Connecting to XID service", "addr", cfg.XidServiceAddr)
	conn, err := grpc.NewClient(cfg.XidServiceAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		spannerClient.Close()
		return nil, fmt.Errorf("failed to create gRPC client for XID service: %w", err)
	}
	xidClient := xidgrpcpb.NewXidServiceClient(conn)

	keyReloader, err := keyreloader.NewSignerReloader(cfg.SigningKeyFile)
	if err != nil {
		_ = conn.Close()
		spannerClient.Close()
		return nil, fmt.Errorf("failed to load signing key: %w", err)
	}

	taskQueueClient, err := tq.New(ctx, cfg.ProjectID, cfg.InvokerSA, cfg.ActuatorURL, cfg.ActuatorAudience, keyReloader.Signer, cfg.SignatureTTLSeconds, cfg.RedisAddr)
	if err != nil {
		_ = conn.Close()
		spannerClient.Close()
		return nil, fmt.Errorf("failed to create task queue client: %w", err)
	}

	srv := New(Params{
		Config:           cfg,
		Storage:          storage,
		Authorizer:       auth,
		XidClient:        xidClient,
		WorkUnitActuator: taskQueueClient,
	})

	cleanup := func() error {
		var errs []error
		if err := taskQueueClient.Close(); err != nil {
			errs = append(errs, fmt.Errorf("failed to close task queue client: %w", err))
		}
		if err := conn.Close(); err != nil {
			errs = append(errs, fmt.Errorf("failed to close XID gRPC connection: %w", err))
		}
		spannerClient.Close()
		return errors.Join(errs...)
	}

	return &ProdServer{
		Server:          srv,
		SpannerClient:   spannerClient,
		XidClient:       xidClient,
		TaskQueueClient: taskQueueClient,
		cleanup:         cleanup,
	}, nil
}
