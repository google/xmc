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
	commonhealth "xmanager_cloud/common/health"
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
	spannerClient   *spanner.Client
	xidClient       xidgrpcpb.XidServiceClient
	taskQueueClient *tq.Client
	cleanup         func() error
}

// HealthCheckers returns the health client interfaces required for active readiness checks.
func (p *ProdServer) HealthCheckers() (commonhealth.SpannerHealthClient, xidgrpcpb.XidServiceClient, *tq.Client) {
	if p == nil {
		return nil, nil, nil
	}
	return commonhealth.NewSpannerHealthClient(p.spannerClient), p.xidClient, p.taskQueueClient
}

// Close closes all underlying production client connections.
func (p *ProdServer) Close() error {
	if p == nil || p.cleanup == nil {
		return nil
	}
	return p.cleanup()
}

func initSpannerClient(ctx context.Context, cfg *config.Config) (*spanner.Client, error) {
	sessionPoolConfig := spanner.SessionPoolConfig{
		TrackSessionHandles: true,
		InactiveTransactionRemovalOptions: spanner.InactiveTransactionRemovalOptions{
			ActionOnInactiveTransaction: spanner.Warn,
		},
	}
	return spanner.NewClientWithConfig(
		ctx,
		cfg.SpannerDatabaseName,
		spanner.ClientConfig{SessionPoolConfig: sessionPoolConfig},
		option.WithQuotaProject(cfg.QuotaProjectID),
	)
}

func initXidClient(ctx context.Context, cfg *config.Config) (xidgrpcpb.XidServiceClient, *grpc.ClientConn, error) {
	logging.Info(ctx, "Connecting to XID service", "addr", cfg.XidServiceAddr)
	conn, err := grpc.NewClient(cfg.XidServiceAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create gRPC client for XID service: %w", err)
	}
	return xidgrpcpb.NewXidServiceClient(conn), conn, nil
}

func initTaskQueueClient(ctx context.Context, cfg *config.Config) (*tq.Client, error) {
	keyReloader, err := keyreloader.NewSignerReloader(cfg.SigningKeyFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load signing key: %w", err)
	}
	return tq.New(ctx, cfg.ProjectID, cfg.InvokerSA, cfg.ActuatorURL, cfg.ActuatorAudience, keyReloader.Signer, cfg.SignatureTTLSeconds, cfg.RedisAddr)
}

func initAuth(ctx context.Context, cfg *config.Config, oauthCfg *oauth.Config) (authz.AuthorizerInterface, error) {
	idService, err := cloudidentity.NewService(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create cloud identity service: %w", err)
	}
	auth, err := authz.NewAuthorizer(ctx, idService, 10*time.Minute, 1*time.Minute, []string{cfg.HTTPAudience, cfg.GRPCAudience, oauthCfg.ClientID}, cfg.IAPIssuer, false, []byte(oauthCfg.JWTSecret))
	if err != nil {
		return nil, fmt.Errorf("failed to create authorizer: %w", err)
	}
	return auth, nil
}

// NewProd creates and initializes a production Server instance along with its dependencies.
func NewProd(ctx context.Context, cfg *config.Config, oauthCfg *oauth.Config) (_ *ProdServer, retErr error) {
	if cfg == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}
	if oauthCfg == nil {
		return nil, fmt.Errorf("oauth config cannot be nil")
	}

	var cleanups []func() error
	defer func() {
		if retErr != nil {
			for i := len(cleanups) - 1; i >= 0; i-- {
				_ = cleanups[i]()
			}
		}
	}()

	spannerClient, err := initSpannerClient(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create spanner client: %w", err)
	}
	cleanups = append(cleanups, func() error {
		spannerClient.Close()
		return nil
	})

	storage, err := ss.NewStorageWithConfig(spannerClient, ss.StorageConfig{StatusMessageHistoryEnabled: cfg.StatusMessageHistoryEnabled})
	if err != nil {
		return nil, fmt.Errorf("failed to create storage: %w", err)
	}

	auth, err := initAuth(ctx, cfg, oauthCfg)
	if err != nil {
		return nil, err
	}

	xidClient, conn, err := initXidClient(ctx, cfg)
	if err != nil {
		return nil, err
	}
	cleanups = append(cleanups, func() error {
		if err := conn.Close(); err != nil {
			return fmt.Errorf("failed to close XID gRPC connection: %w", err)
		}
		return nil
	})

	taskQueueClient, err := initTaskQueueClient(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create task queue client: %w", err)
	}
	cleanups = append(cleanups, func() error {
		if err := taskQueueClient.Close(); err != nil {
			return fmt.Errorf("failed to close task queue client: %w", err)
		}
		return nil
	})

	srv, err := New(Params{
		Config:           cfg,
		Storage:          storage,
		Authorizer:       auth,
		XidClient:        xidClient,
		WorkUnitActuator: taskQueueClient,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create server: %w", err)
	}

	cleanup := func() error {
		var errs []error
		for i := len(cleanups) - 1; i >= 0; i-- {
			if err := cleanups[i](); err != nil {
				errs = append(errs, err)
			}
		}
		return errors.Join(errs...)
	}

	return &ProdServer{
		Server:          srv,
		spannerClient:   spannerClient,
		xidClient:       xidClient,
		taskQueueClient: taskQueueClient,
		cleanup:         cleanup,
	}, nil
}
