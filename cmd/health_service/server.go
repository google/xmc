// Copyright 2026 Google LLC
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

package main

import (
	"context"
	"log/slog"
	"os"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	authz "xmanager_cloud/auth/auth"
	spb "xmanager_cloud/health_service/proto/api_go_grpc_proto"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/pkg/auth/oauth2"
)

type healthChecker interface {
	RunChecks(ctx context.Context) RunChecksResult
	Close() error
}

var _ spb.HealthServiceServer = (*server)(nil)

type server struct {
	spb.UnimplementedHealthServiceServer
	cfg           *Config
	checker       healthChecker
	authenticator *oauth2.Authenticator
	auth          authz.AuthorizerInterface
}

func newServer(cfg *Config, checker healthChecker, authenticator *oauth2.Authenticator, auth authz.AuthorizerInterface) *server {
	return &server{
		cfg:           cfg,
		checker:       checker,
		authenticator: authenticator,
		auth:          auth,
	}
}

func (s *server) Close() error {
	return s.checker.Close()
}

func (s *server) GetHealthStatus(ctx context.Context, req *spb.GetHealthStatusRequest) (*spb.HealthStatusResponse, error) {
	if err := s.checkAdminGuard(ctx); err != nil {
		return nil, err
	}

	res := s.checker.RunChecks(ctx)

	overallHealthy := spb.HealthState_HEALTH_STATE_HEALTHY
	for _, c := range res.Components {
		if c.HealthState != spb.HealthState_HEALTH_STATE_HEALTHY {
			overallHealthy = spb.HealthState_HEALTH_STATE_UNHEALTHY
			slog.Warn("Component unhealthy", "name", c.Name, "error", c.ErrorMessage)
		}
	}

	essDbSchemaVersionStr := res.EssDbSchemaVersion
	if essDbSchemaVersionStr == "" {
		essDbSchemaVersionStr = "unknown"
	}

	xidDbSchemaVersionStr := res.XidDbSchemaVersion
	if xidDbSchemaVersionStr == "" {
		xidDbSchemaVersionStr = "unknown"
	}

	appVersionStr := s.cfg.AppVersion
	if appVersionStr == "" {
		appVersionStr = "unknown"
	}

	buildVersionStr := s.cfg.BuildVersion
	if buildVersionStr == "" {
		buildVersionStr = "unknown"
	}

	return &spb.HealthStatusResponse{
		HealthState: overallHealthy,
		SystemInfo: &spb.SystemInfo{
			AppVersion:         appVersionStr,
			BuildVersion:       buildVersionStr,
			EssDbSchemaVersion: essDbSchemaVersionStr,
			XidDbSchemaVersion: xidDbSchemaVersionStr,
			ObservabilityUrl:   s.cfg.ObservabilityURL,
			IsSchedulingPaused: false,
		},
		Components:      res.Components,
		HealthCheckTime: timestamppb.Now(),
	}, nil
}

func (s *server) ListConfigEntries(ctx context.Context, req *spb.ListConfigEntriesRequest) (*spb.ListConfigEntriesResponse, error) {
	if err := s.checkAdminGuard(ctx); err != nil {
		return nil, err
	}

	groups, err := listConfigs(ctx, os.DirFS(s.cfg.ConfigDir))
	if err != nil {
		return nil, err
	}
	return &spb.ListConfigEntriesResponse{
		ConfigGroups: groups,
	}, nil
}

func (s *server) GetUserInfo(ctx context.Context, req *spb.GetUserInfoRequest) (*spb.UserInfoResponse, error) {
	email, _, err := s.auth.AuthenticateUserFromContext(ctx)
	if err != nil {
		return &spb.UserInfoResponse{UserEmail: "", Privileges: nil}, nil
	}

	if err := s.checkAdminGuard(ctx); err != nil {
		return &spb.UserInfoResponse{UserEmail: email, Privileges: nil}, nil
	}

	return &spb.UserInfoResponse{
		UserEmail:  email,
		Privileges: []spb.Privilege{spb.Privilege_PRIVILEGE_ACCESS_ADMIN_PANEL},
	}, nil
}

func (s *server) checkAdminGuard(ctx context.Context) error {
	if s.authenticator == nil || s.authenticator.Config.DisableAuthentication {
		return nil
	}

	email, _, err := s.auth.AuthenticateUserFromContext(ctx)
	if err != nil {
		return err
	}

	authorized, err := s.authenticator.IsAdmin(ctx, email)
	if err != nil {
		logging.Error(ctx, "[Auth Guard] Admin check failed", "error", err, "user", email)
		return status.Errorf(codes.Internal, "internal authorization check failed")
	}
	if !authorized {
		return status.Errorf(codes.PermissionDenied, "forbidden: user lacks admin role")
	}

	return nil
}
