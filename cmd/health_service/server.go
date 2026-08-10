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

	health "xmanager_cloud/common/health"
	spb "xmanager_cloud/health_service/proto/api_go_grpc_proto"

	run "google.golang.org/api/run/v2"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type server struct {
	spb.UnimplementedHealthServiceServer
	cfg     *Config
	checker *HealthChecker
}

func newServer(cfg *Config, essHealth, xidHealth health.SpannerHealthClient, redisClient RedisPingClient, cloudRunService *run.Service) (*server, error) {
	checker, err := newHealthChecker(HealthCheckerParams{
		Config:          cfg,
		EssHealthClient: essHealth,
		XidHealthClient: xidHealth,
		RedisClient:     redisClient,
		CloudRunService: cloudRunService,
	})
	if err != nil {
		return nil, err
	}
	return &server{
		cfg:     cfg,
		checker: checker,
	}, nil
}

func (s *server) Close() error {
	return s.checker.Close()
}

func (s *server) GetHealthStatus(ctx context.Context, req *spb.GetHealthStatusRequest) (*spb.HealthStatusResponse, error) {
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

	return &spb.HealthStatusResponse{
		HealthState: overallHealthy,
		SystemInfo: &spb.SystemInfo{
			AppVersion:         "app-version-1.0.0",
			EssDbSchemaVersion: essDbSchemaVersionStr,
			XidDbSchemaVersion: xidDbSchemaVersionStr,
			ObservabilityUrl:   "http://mock-observability-url",
			IsSchedulingPaused: false,
		},
		Components:      res.Components,
		HealthCheckTime: timestamppb.Now(),
	}, nil
}

func (s *server) ListConfigEntries(ctx context.Context, req *spb.ListConfigEntriesRequest) (*spb.ListConfigEntriesResponse, error) {
	groups, err := listConfigs(ctx, os.DirFS(s.cfg.ConfigDir))
	if err != nil {
		return nil, err
	}
	return &spb.ListConfigEntriesResponse{
		ConfigGroups: groups,
	}, nil
}

func (s *server) GetUserInfo(ctx context.Context, req *spb.GetUserInfoRequest) (*spb.UserInfoResponse, error) {
	return &spb.UserInfoResponse{
		UserEmail: "mock-user@google.com",
		Privileges: []spb.Privilege{
			spb.Privilege_PRIVILEGE_ACCESS_ADMIN_PANEL,
		},
	}, nil
}
