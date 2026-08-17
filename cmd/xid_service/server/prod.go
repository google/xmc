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
	authz "xmanager_cloud/auth/auth"
	commonhealth "xmanager_cloud/common/health"
	oauth "xmanager_cloud/pkg/auth/oauth2"
	ss "xmanager_cloud/storage/spanner/spanner"
	"xmanager_cloud/xid_service/config"
	apipb "xmanager_cloud/xid_service/proto/api_go_proto"
)

var _ apipb.XidServiceServer = (*ProdServer)(nil)

// ProdServer encapsulates the production XIDServer instance along with its underlying infrastructure dependencies and lifecycle.
type ProdServer struct {
	*XIDServer
	spannerClient *spanner.Client
	cleanup       func() error
	closeOnce     sync.Once
	closeErr      error
}

// HealthCheckers returns the health client interfaces required for active readiness checks.
func (p *ProdServer) HealthCheckers() commonhealth.SpannerHealthClient {
	if p == nil || p.spannerClient == nil {
		return nil
	}
	return commonhealth.NewSpannerHealthClient(p.spannerClient)
}

// Close closes all underlying production client connections in a thread-safe, idempotent manner.
func (p *ProdServer) Close() error {
	if p == nil {
		return nil
	}
	p.closeOnce.Do(func() {
		if p.cleanup != nil {
			p.closeErr = p.cleanup()
		}
	})
	return p.closeErr
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

func initAuth(ctx context.Context, cfg *config.Config, oauthCfg *oauth.Config) (authz.AuthorizerInterface, error) {
	idService, err := cloudidentity.NewService(ctx, option.WithQuotaProject(cfg.QuotaProjectID))
	if err != nil {
		return nil, fmt.Errorf("failed to create cloud identity service: %w", err)
	}
	auth, err := authz.NewAuthorizer(
		ctx,
		idService,
		10*time.Minute,
		1*time.Minute,
		[]string{cfg.Audience, cfg.HTTPAudience, cfg.GRPCAudience, oauthCfg.ClientID},
		cfg.IAPIssuer,
		cfg.DisableAuthentication,
		[]byte(oauthCfg.JWTSecret),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create authorizer: %w", err)
	}
	return auth, nil
}

// NewProd creates and initializes a production XIDServer instance along with its dependencies.
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

	storage, err := ss.NewStorage(spannerClient)
	if err != nil {
		return nil, fmt.Errorf("failed to create storage: %w", err)
	}

	auth, err := initAuth(ctx, cfg, oauthCfg)
	if err != nil {
		return nil, err
	}

	srv, err := New(Params{
		Config:     *cfg,
		Authorizer: auth,
		Storage:    storage,
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
		XIDServer:     srv,
		spannerClient: spannerClient,
		cleanup:       cleanup,
	}, nil
}
