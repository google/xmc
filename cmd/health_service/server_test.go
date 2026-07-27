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
	"testing"

	spb "xmanager_cloud/health_service/proto/api_go_grpc_proto"
)

func TestGetHealthStatus(t *testing.T) {
	cfg := &Config{Port: "8080", GRPCPort: "50051", ProjectID: "test-project"}
	s := newServer(cfg)

	resp, err := s.GetHealthStatus(context.Background(), &spb.GetHealthStatusRequest{})
	if err != nil {
		t.Fatalf("GetHealthStatus failed: %v", err)
	}

	if resp.HealthState != spb.HealthState_HEALTH_STATE_HEALTHY {
		t.Errorf("Expected status 'HEALTH_STATE_HEALTHY', got %v", resp.HealthState)
	}
}

func TestListConfigEntries(t *testing.T) {
	cfg := &Config{Port: "8080", GRPCPort: "50051", ProjectID: "test-project"}
	s := newServer(cfg)

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
	cfg := &Config{Port: "8080", GRPCPort: "50051", ProjectID: "test-project"}
	s := newServer(cfg)

	resp, err := s.GetUserInfo(context.Background(), &spb.GetUserInfoRequest{})
	if err != nil {
		t.Fatalf("GetUserInfo failed: %v", err)
	}

	if resp.UserEmail != "mock-user@google.com" {
		t.Errorf("Expected UserEmail 'mock-user@google.com', got %q", resp.UserEmail)
	}

	if len(resp.Privileges) != 1 || resp.Privileges[0] != spb.Privilege_PRIVILEGE_ACCESS_ADMIN_PANEL {
		t.Errorf("Expected privileges [PRIVILEGE_ACCESS_ADMIN_PANEL], got %v", resp.Privileges)
	}
}
