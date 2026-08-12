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
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	auth "xmanager_cloud/auth/auth"
	spb "xmanager_cloud/health_service/proto/api_go_grpc_proto"
	"xmanager_cloud/pkg/auth/oauth2"
	"xmanager_cloud/pkg/auth/oauth2/mock"
)

type mockAuthorizer struct {
	auth.MockAuth
}

func (m *mockAuthorizer) AuthenticateUserFromContext(ctx context.Context) (string, string, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return "", "", status.Errorf(codes.Unauthenticated, "missing metadata")
	}
	emails := md.Get("x-goog-user-email")
	if len(emails) == 0 || emails[0] == "" {
		emails = md.Get("x-goog-authenticated-user-email")
	}
	if len(emails) == 0 || emails[0] == "" {
		return "", "", status.Errorf(codes.Unauthenticated, "missing user identity")
	}
	email := strings.TrimPrefix(emails[0], "accounts.google.com:")
	return email, "mock-user-id", nil
}

func TestGetHealthStatus(t *testing.T) {
	cfg := &Config{Port: "8080", GRPCPort: "50051", ProjectID: "test-project"}
	s := newServer(cfg, nil, &mockAuthorizer{})

	resp, err := s.GetHealthStatus(context.Background(), &spb.GetHealthStatusRequest{})
	if err != nil {
		t.Fatalf("GetHealthStatus failed: %v", err)
	}

	if resp.HealthState != spb.HealthState_HEALTH_STATE_HEALTHY {
		t.Errorf("Expected status 'HEALTH_STATE_HEALTHY', got %v", resp.HealthState)
	}

	if resp.HealthCheckTime == nil {
		t.Error("Expected HealthCheckTime to be populated, got nil")
	}
}

func TestListConfigEntries(t *testing.T) {
	cfg := &Config{Port: "8080", GRPCPort: "50051", ProjectID: "test-project"}
	s := newServer(cfg, nil, &mockAuthorizer{})

	resp, err := s.ListConfigEntries(context.Background(), &spb.ListConfigEntriesRequest{})
	if err != nil {
		t.Fatalf("ListConfigEntries failed: %v", err)
	}

	if len(resp.ConfigGroups) != 1 {
		t.Fatalf("Expected 1 config group, got %d", len(resp.ConfigGroups))
	}

	group := resp.ConfigGroups[0]
	if group.DisplayName != "Health Service Config" {
		t.Errorf("Expected group display name 'Health Service Config', got %q", group.DisplayName)
	}

	configs := make(map[string]string)
	for _, c := range group.Configs {
		configs[c.Key] = c.Value
	}

	if configs["Port"] != cfg.Port {
		t.Errorf("Expected Port %q, got %q", cfg.Port, configs["Port"])
	}
	if configs["GRPCPort"] != cfg.GRPCPort {
		t.Errorf("Expected GRPCPort %q, got %q", cfg.GRPCPort, configs["GRPCPort"])
	}
	if configs["ProjectID"] != cfg.ProjectID {
		t.Errorf("Expected ProjectID %q, got %q", cfg.ProjectID, configs["ProjectID"])
	}
}

func TestGetUserInfo(t *testing.T) {
	oauthCfg := &oauth2.Config{
		DisableAuthentication: false,
		AdminIAMRole:          "roles/owner",
		TargetIAMRole:         "roles/viewer",
		GCPProjectID:          "test-project-id",
	}
	authenticator := oauth2.NewAuthenticator(oauthCfg)
	mock.ConfigureMockAuth(authenticator)

	s := newServer(&Config{}, authenticator, &mockAuthorizer{})

	tests := []struct {
		name           string
		email          string
		wantEmail      string
		wantPrivileges []spb.Privilege
	}{
		{
			name:      "admin user gets admin privilege",
			email:     "mock-admin@mycompany.com", // owner role in mock
			wantEmail: "mock-admin@mycompany.com",
			wantPrivileges: []spb.Privilege{
				spb.Privilege_PRIVILEGE_ACCESS_ADMIN_PANEL,
			},
		},
		{
			name:           "regular viewer user does not get admin privilege",
			email:          "mock-sa-user@mycompany.com", // viewer role in mock, not owner
			wantEmail:      "mock-sa-user@mycompany.com",
			wantPrivileges: nil,
		},
		{
			name:           "missing email returns empty response",
			email:          "",
			wantEmail:      "",
			wantPrivileges: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			if tt.email != "" {
				ctx = metadata.NewIncomingContext(ctx, metadata.Pairs("x-goog-user-email", tt.email))
			}
			resp, err := s.GetUserInfo(ctx, &spb.GetUserInfoRequest{})
			if err != nil {
				t.Fatalf("GetUserInfo failed: %v", err)
			}

			if resp.UserEmail != tt.wantEmail {
				t.Errorf("Expected UserEmail %q, got %q", tt.wantEmail, resp.UserEmail)
			}

			if len(resp.Privileges) != len(tt.wantPrivileges) {
				t.Fatalf("Expected %d privileges, got %d", len(tt.wantPrivileges), len(resp.Privileges))
			}
			for i, p := range resp.Privileges {
				if p != tt.wantPrivileges[i] {
					t.Errorf("Expected privilege at index %d to be %v, got %v", i, tt.wantPrivileges[i], p)
				}
			}
		})
	}
}
