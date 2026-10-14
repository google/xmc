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
	apipb "xmanager_cloud/api/protos/xid"
	"xmanager_cloud/cmd/xid_service/config"
	authz "xmanager_cloud/internal/auth"
	commonhealth "xmanager_cloud/internal/common/health"
	ss "xmanager_cloud/internal/storage/spanner"
	"xmanager_cloud/pkg/auth/jwks"
	oauth "xmanager_cloud/pkg/auth/oauth2"
)

var _ apipb.XidServiceServer = (*RuntimeServer)(nil)

// RuntimeServer encapsulates the live runtime XIDServer instance along with its real cloud infrastructure dependencies (Cloud Spanner, Cloud Identity, OAuth2) and lifecycle management. It serves as the runtime composition root across deployed environments, in contrast to unit-test environments which instantiate XIDServer directly with test doubles via New(Params).
type RuntimeServer struct {
	*XIDServer
	spannerClient *spanner.Client
	cleanup       func() error
	closeOnce     sync.Once
	closeErr      error
}

// HealthCheckers returns the health client interfaces required for active readiness checks.
func (r *RuntimeServer) HealthCheckers() commonhealth.SpannerHealthClient {
	if r == nil || r.spannerClient == nil {
		return nil
	}
	return commonhealth.NewSpannerHealthClient(r.spannerClient)
}

// Close closes all underlying runtime client connections in a thread-safe, idempotent manner.
func (r *RuntimeServer) Close() error {
	if r == nil {
		return nil
	}
	r.closeOnce.Do(func() {
		if r.cleanup != nil {
			r.closeErr = r.cleanup()
		}
	})
	return r.closeErr
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
	audiences := []string{cfg.Audience, cfg.HTTPAudience, cfg.GRPCAudience, oauthCfg.ClientID}
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

// NewRuntimeServer creates and initializes a live runtime XIDServer instance with real cloud infrastructure dependencies (Spanner, Cloud Identity, OAuth2) and automated LIFO rollback cleanup on error.
func NewRuntimeServer(ctx context.Context, cfg *config.Config, oauthCfg *oauth.Config) (_ *RuntimeServer, retErr error) {
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

	return &RuntimeServer{
		XIDServer:     srv,
		spannerClient: spannerClient,
		cleanup:       cleanup,
	}, nil
}
