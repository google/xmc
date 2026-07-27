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

	"google.golang.org/protobuf/types/known/timestamppb"
	spb "xmanager_cloud/health_service/proto/api_go_grpc_proto"
)

type server struct {
	spb.UnimplementedHealthServiceServer
	cfg *Config
}

func newServer(cfg *Config) *server {
	return &server{cfg: cfg}
}

func (s *server) GetHealthStatus(ctx context.Context, req *spb.GetHealthStatusRequest) (*spb.HealthStatusResponse, error) {
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
	return &spb.UserInfoResponse{
		UserEmail: "mock-user@google.com",
		Privileges: []spb.Privilege{
			spb.Privilege_PRIVILEGE_ACCESS_ADMIN_PANEL,
		},
	}, nil
}
