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

	"cloud.google.com/go/spanner"
	"google.golang.org/api/cloudidentity/v1beta1"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	dashboard_pb "xmanager_cloud/api/protos/dashboard"
	apipb "xmanager_cloud/api/protos/experiment"
	xidgrpcpb "xmanager_cloud/api/protos/xid"
	"xmanager_cloud/cmd/experiment_state_server/config"
	"xmanager_cloud/cmd/experiment_state_server/resumption"
	"xmanager_cloud/cmd/experiment_state_server/toollibs"
	authz "xmanager_cloud/internal/auth"
	commonhealth "xmanager_cloud/internal/common/health"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/security/keyreloader"
	"xmanager_cloud/internal/storage"
	ss "xmanager_cloud/internal/storage/spanner"
	tq "xmanager_cloud/internal/task_queue"
	"xmanager_cloud/pkg/auth/jwks"
	oauth "xmanager_cloud/pkg/auth/oauth2"
)

var _ apipb.ExperimentStateServiceServer = (*ProdServer)(nil)

// ProdServer encapsulates the production Server instance along with its underlying infrastructure dependencies and lifecycle.
type ProdServer struct {
	*Server
	spannerClient   *spanner.Client
	authorizer      authz.AuthorizerInterface
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

// SpannerClient returns the underlying Spanner client.
func (p *ProdServer) SpannerClient() *spanner.Client {
	if p == nil {
		return nil
	}
	return p.spannerClient
}

// Authorizer returns the initialized AuthorizerInterface.
func (p *ProdServer) Authorizer() authz.AuthorizerInterface {
	if p == nil {
		return nil
	}
	return p.authorizer
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

func initTaskQueueClient(ctx context.Context, cfg *config.Config, oauthCfg *oauth.Config) (*tq.Client, error) {
	keyReloader, err := keyreloader.NewSignerReloader(cfg.SigningKeyFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load signing key: %w", err)
	}
	var opts []tq.Option
	if oauthCfg != nil && oauthCfg.IsNonProdAuth() && oauthCfg.TokenURL != "" && oauthCfg.ClientID != "" {
		opts = append(opts, tq.WithTokenProvider(tq.NewOidcTokenProvider(tq.OidcTokenProviderConfig{
			TokenURL:     oauthCfg.TokenURL,
			ClientID:     oauthCfg.ClientID,
			ClientSecret: oauthCfg.ClientSecret,
			Username:     oauthCfg.Username,
			Password:     oauthCfg.Password,
			Scope:        "openid email profile",
		})))
	}
	return tq.New(ctx, cfg.ProjectID, cfg.InvokerSA, cfg.ActuatorURL, cfg.ActuatorAudience, keyReloader.Signer, cfg.SignatureTTLSeconds, cfg.RedisAddr, opts...)
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
	// Match the XID service's Cloud Identity client so that group-based ACL grants resolve the same
	// way when CreateExperiment authorizes the creator locally before provisioning an XID.
	idService, err := cloudidentity.NewService(ctx, option.WithQuotaProject(cfg.QuotaProjectID))
	if err != nil {
		return nil, fmt.Errorf("failed to create cloud identity service: %w", err)
	}
	audiences := []string{cfg.HTTPAudience, cfg.GRPCAudience, oauthCfg.ClientID}
	auth, err := authz.NewAuthorizer(ctx, idService, 10*time.Minute, 1*time.Minute, audiences, cfg.IAPIssuer, []byte(oauthCfg.JWTSecret))
	if err != nil {
		return nil, fmt.Errorf("failed to create authorizer: %w", err)
	}
	if cfg.JWKSURL != "" {
		auth.SetJWKSValidator(jwks.NewValidator(jwks.ValidatorConfig{
			JWKSURL:          cfg.JWKSURL,
			AllowedAudiences: audiences,
			ExpectedIssuer:   cfg.IAPIssuer,
		}))
	}
	return auth, nil
}

func initObservers(ctx context.Context, cfg *config.Config, st storage.Storage, wg *sync.WaitGroup) ([]ArtifactObserver, func() error, error) {
	if cfg.DashboardServiceAddr == "" {
		return nil, func() error { return nil }, nil
	}
	dashboardClient, dashConn, err := initDashboardClient(ctx, cfg)
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() error {
		if err := dashConn.Close(); err != nil {
			return fmt.Errorf("failed to close Dashboard gRPC connection: %w", err)
		}
		return nil
	}
	return []ArtifactObserver{NewDashboardObserver(dashboardClient, st, wg)}, cleanup, nil
}

// runCleanups executes cleanup functions in reverse (LIFO) order to tear down
// dependencies in the reverse order of their initialization.
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

	spannerClient, err := initSpannerClient(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create spanner client: %w", err)
	}
	cleanups = append(cleanups, func() error {
		spannerClient.Close()
		return nil
	})

	storage, err := ss.NewStorage(spannerClient)
	if err != nil {
		return nil, fmt.Errorf("failed to create storage: %w", err)
	}

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

	taskQueueClient, err := initTaskQueueClient(ctx, cfg, oauthCfg)
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
	observers, obsCleanup, err := initObservers(ctx, cfg, storage, &bgTaskWG)
	if err != nil {
		return nil, err
	}
	cleanups = append(cleanups, obsCleanup)

	srv, err := New(Params{
		Config:            cfg,
		Storage:           storage,
		Authorizer:        auth,
		XidClient:         xidClient,
		WorkUnitActuator:  taskQueueClient,
		ArtifactObservers: observers,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create server: %w", err)
	}

	if cfg.FeatureFlags.PauseSchedulingEnabled {
		consumer, err := startResumptionConsumer(ctx, cfg, taskQueueClient, storage)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize resumption consumer: %w", err)
		}
		cleanups = append(cleanups, consumer.Close)
	}

	return &ProdServer{
		Server:          srv,
		spannerClient:   spannerClient,
		authorizer:      auth,
		xidClient:       xidClient,
		taskQueueClient: taskQueueClient,
		bgTaskWG:        &bgTaskWG,
		cleanup:         func() error { return runCleanups(cleanups) },
	}, nil
}

// startResumptionConsumer wires the resumption worker to the Redis trigger stream.
// The Redis consumer also serves as the worker's ClaimExtender.
func startResumptionConsumer(ctx context.Context, cfg *config.Config, taskQueue resumption.TaskQueue, storageClient storage.Storage) (*resumption.Consumer, error) {
	redisConsumer, err := resumption.NewRedisConsumer(cfg.RedisAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to create resumption redis consumer: %w", err)
	}
	worker := resumption.NewResumptionWorker(storageClient, taskQueue, redisConsumer)
	return resumption.StartConsumer(ctx, resumption.ConsumerConfig{Client: redisConsumer}, worker)
}
