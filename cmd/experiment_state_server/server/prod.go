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
	"sync"
	"time"

	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	"golang/google_api/cloudidentity/v1beta1/cloudidentity"
	"golang/google_api/option/option"
	"golang/grpc/credentials/insecure/insecure"
	"golang/grpc/grpc"
	authz "xmanager_cloud/auth/auth"
	"xmanager_cloud/cmd/experiment_state_server/toollibs"
	commonhealth "xmanager_cloud/common/health"
	dashboard_pb "xmanager_cloud/dashboard_service/proto/service_go_proto"
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
	bgTaskWG        *sync.WaitGroup
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
	if p == nil {
		return nil
	}
	if p.bgTaskWG != nil {
		p.bgTaskWG.Wait()
	}
	if p.cleanup == nil {
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

func initDashboardClient(ctx context.Context, cfg *config.Config) (*toollibs.DashboardToolClient, *grpc.ClientConn, error) {
	if cfg.DashboardServiceAddr == "" {
		return nil, nil, nil
	}
	logging.Info(ctx, "Connecting to Dashboard service", "addr", cfg.DashboardServiceAddr)
	conn, err := grpc.NewClient(cfg.DashboardServiceAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create gRPC client for Dashboard service: %w", err)
	}
	return toollibs.NewDashboardToolClient(dashboard_pb.NewDashboardServiceClient(conn)), conn, nil
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

type storageHandle struct {
	storage storage.Storage
	client  *spanner.Client
	cleanup func() error
}

func initStorage(ctx context.Context, cfg *config.Config) (*storageHandle, error) {
	spClient, err := initSpannerClient(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create spanner client: %w", err)
	}
	cleanup := func() error {
		spClient.Close()
		return nil
	}
	st, err := ss.NewStorageWithConfig(spClient, ss.StorageConfig{StatusMessageHistoryEnabled: cfg.StatusMessageHistoryEnabled})
	if err != nil {
		_ = cleanup()
		return nil, fmt.Errorf("failed to create storage: %w", err)
	}
	return &storageHandle{
		storage: st,
		client:  spClient,
		cleanup: cleanup,
	}, nil
}

func initObservers(ctx context.Context, cfg *config.Config, st storage.Storage, wg *sync.WaitGroup) ([]ArtifactObserver, func() error, error) {
	dashboardClient, dashConn, err := initDashboardClient(ctx, cfg)
	if err != nil {
		return nil, nil, err
	}
	if dashboardClient == nil {
		return nil, func() error { return nil }, nil
	}
	cleanup := func() error {
		if err := dashConn.Close(); err != nil {
			return fmt.Errorf("failed to close Dashboard gRPC connection: %w", err)
		}
		return nil
	}
	return []ArtifactObserver{NewDashboardObserver(dashboardClient, st, wg)}, cleanup, nil
}

func runCleanups(cleanups []func() error) error {
	var errs []error
	for i := len(cleanups) - 1; i >= 0; i-- {
		if err := cleanups[i](); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
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
			_ = runCleanups(cleanups)
		}
	}()

	stHandle, err := initStorage(ctx, cfg)
	if err != nil {
		return nil, err
	}
	cleanups = append(cleanups, stHandle.cleanup)

	auth, err := initAuth(ctx, cfg, oauthCfg)
	if err != nil {
		return nil, err
	}

	xidClient, xidConn, err := initXidClient(ctx, cfg)
	if err != nil {
		return nil, err
	}
	cleanups = append(cleanups, func() error {
		if err := xidConn.Close(); err != nil {
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

	var bgTaskWG sync.WaitGroup
	observers, obsCleanup, err := initObservers(ctx, cfg, stHandle.storage, &bgTaskWG)
	if err != nil {
		return nil, err
	}
	cleanups = append(cleanups, obsCleanup)

	srv, err := New(Params{
		Config:            cfg,
		Storage:           stHandle.storage,
		Authorizer:        auth,
		XidClient:         xidClient,
		WorkUnitActuator:  taskQueueClient,
		ArtifactObservers: observers,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create server: %w", err)
	}

	return &ProdServer{
		Server:          srv,
		spannerClient:   stHandle.client,
		xidClient:       xidClient,
		taskQueueClient: taskQueueClient,
		bgTaskWG:        &bgTaskWG,
		cleanup:         func() error { return runCleanups(cleanups) },
	}, nil
}
