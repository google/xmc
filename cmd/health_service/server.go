// Copyright 2026 Google LLC
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

package main

import (
	"context"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
	spb "xmanager_cloud/health_service/proto/api_go_grpc_proto"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/pkg/auth/oauth2"
)

type server struct {
	spb.UnimplementedHealthServiceServer
	cfg           *Config
	authenticator *oauth2.Authenticator
}

func newServer(cfg *Config, authenticator *oauth2.Authenticator) *server {
	return &server{cfg: cfg, authenticator: authenticator}
}

func (s *server) GetHealthStatus(ctx context.Context, req *spb.GetHealthStatusRequest) (*spb.HealthStatusResponse, error) {
	if err := s.checkAdminGuard(ctx); err != nil {
		return nil, err
	}
	return &spb.HealthStatusResponse{
		HealthState: spb.HealthState_HEALTH_STATE_HEALTHY,
		SystemInfo: &spb.SystemInfo{
			AppVersion:         "mock-app-version-1.0.0",
			EssDbSchemaVersion: "6",
			XidDbSchemaVersion: "1",
			ObservabilityUrl:   "http://mock-observability-url",
			IsSchedulingPaused: false,
		},
		Components: []*spb.ComponentHealth{
			{
				Name:        "ESS",
				Type:        spb.ComponentType_COMPONENT_TYPE_CORE_SERVICE,
				HealthState: spb.HealthState_HEALTH_STATE_HEALTHY,
			},
			{
				Name:        "XID",
				Type:        spb.ComponentType_COMPONENT_TYPE_CORE_SERVICE,
				HealthState: spb.HealthState_HEALTH_STATE_HEALTHY,
			},
			{
				Name:        "Spanner",
				Type:        spb.ComponentType_COMPONENT_TYPE_EXTERNAL_DEPENDENCY,
				HealthState: spb.HealthState_HEALTH_STATE_HEALTHY,
			},
			{
				Name:        "Redis",
				Type:        spb.ComponentType_COMPONENT_TYPE_EXTERNAL_DEPENDENCY,
				HealthState: spb.HealthState_HEALTH_STATE_HEALTHY,
			},
		},
		HealthCheckTime: timestamppb.Now(),
	}, nil
}

func (s *server) ListConfigEntries(ctx context.Context, req *spb.ListConfigEntriesRequest) (*spb.ListConfigEntriesResponse, error) {
	if err := s.checkAdminGuard(ctx); err != nil {
		return nil, err
	}
	return &spb.ListConfigEntriesResponse{
		ConfigGroups: []*spb.ConfigGroup{
			{
				DisplayName: "Health Service Config",
				Configs: []*spb.ConfigInfo{
					{Key: "Port", Value: s.cfg.Port},
					{Key: "GRPCPort", Value: s.cfg.GRPCPort},
					{Key: "ProjectID", Value: s.cfg.ProjectID},
				},
			},
		},
	}, nil
}

func (s *server) GetUserInfo(ctx context.Context, req *spb.GetUserInfoRequest) (*spb.UserInfoResponse, error) {
	email, _ := s.getUser(ctx)

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

	email, ok := s.getUser(ctx)
	if !ok {
		return status.Errorf(codes.Unauthenticated, "missing user identity")
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

func (s *server) getUser(ctx context.Context) (string, bool) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return "", false
	}
	if emails := md.Get("x-goog-user-email"); len(emails) > 0 && emails[0] != "" {
		return emails[0], true
	}
	if emails := md.Get("x-goog-authenticated-user-email"); len(emails) > 0 && emails[0] != "" {
		return strings.TrimPrefix(emails[0], "accounts.google.com:"), true
	}
	return "", false
}
