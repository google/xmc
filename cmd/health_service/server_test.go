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
	"os"
	"path/filepath"
	"testing"

	spb "xmanager_cloud/health_service/proto/api_go_grpc_proto"
)

type mockHealthChecker struct {
	result RunChecksResult
	err    error
}

func (m *mockHealthChecker) RunChecks(ctx context.Context) RunChecksResult {
	return m.result
}

func (m *mockHealthChecker) Close() error {
	return m.err
}

func TestGetHealthStatus_AllHealthy(t *testing.T) {
	cfg := &Config{
		Port:             "8080",
		GRPCPort:         "50051",
		ProjectID:        "test-project",
		AppVersion:       "test-version-1.2.3",
		BuildVersion:     "test-build-version-4.5.6",
		ObservabilityURL: "http://test-observability-url",
	}
	mockResult := RunChecksResult{
		EssDbSchemaVersion: "6",
		XidDbSchemaVersion: "1",
		Components: []*spb.ComponentHealth{
			{Name: "ESS", HealthState: spb.HealthState_HEALTH_STATE_HEALTHY},
			{Name: "XID", HealthState: spb.HealthState_HEALTH_STATE_HEALTHY},
			{Name: "Actuator", HealthState: spb.HealthState_HEALTH_STATE_HEALTHY},
			{Name: "Reconciler", HealthState: spb.HealthState_HEALTH_STATE_HEALTHY},
			{Name: "Log Monitor", HealthState: spb.HealthState_HEALTH_STATE_HEALTHY},
			{Name: "Spanner ESS", HealthState: spb.HealthState_HEALTH_STATE_HEALTHY},
			{Name: "Spanner XID", HealthState: spb.HealthState_HEALTH_STATE_HEALTHY},
			{Name: "Redis", HealthState: spb.HealthState_HEALTH_STATE_HEALTHY},
		},
	}
	checker := &mockHealthChecker{result: mockResult}
	s := newServer(cfg, checker)
	defer s.Close()

	resp, err := s.GetHealthStatus(context.Background(), &spb.GetHealthStatusRequest{})
	if err != nil {
		t.Fatalf("GetHealthStatus failed: %v", err)
	}

	if resp.HealthState != spb.HealthState_HEALTH_STATE_HEALTHY {
		t.Errorf("Expected overall status 'HEALTH_STATE_HEALTHY', got %v", resp.HealthState)
	}

	if resp.SystemInfo.AppVersion != "test-version-1.2.3" {
		t.Errorf("Expected AppVersion 'test-version-1.2.3', got %q", resp.SystemInfo.AppVersion)
	}
	if resp.SystemInfo.BuildVersion != "test-build-version-4.5.6" {
		t.Errorf("Expected BuildVersion 'test-build-version-4.5.6', got %q", resp.SystemInfo.BuildVersion)
	}

	if resp.SystemInfo.ObservabilityUrl != "http://test-observability-url" {
		t.Errorf("Expected ObservabilityUrl 'http://test-observability-url', got %q", resp.SystemInfo.ObservabilityUrl)
	}

	if resp.SystemInfo.EssDbSchemaVersion != "6" {
		t.Errorf("Expected EssDbSchemaVersion '6', got %q", resp.SystemInfo.EssDbSchemaVersion)
	}
	if resp.SystemInfo.XidDbSchemaVersion != "1" {
		t.Errorf("Expected XidDbSchemaVersion '1', got %q", resp.SystemInfo.XidDbSchemaVersion)
	}

	expectedComponents := map[string]spb.HealthState{
		"ESS":         spb.HealthState_HEALTH_STATE_HEALTHY,
		"XID":         spb.HealthState_HEALTH_STATE_HEALTHY,
		"Actuator":    spb.HealthState_HEALTH_STATE_HEALTHY,
		"Reconciler":  spb.HealthState_HEALTH_STATE_HEALTHY,
		"Log Monitor": spb.HealthState_HEALTH_STATE_HEALTHY,
		"Spanner ESS": spb.HealthState_HEALTH_STATE_HEALTHY,
		"Spanner XID": spb.HealthState_HEALTH_STATE_HEALTHY,
		"Redis":       spb.HealthState_HEALTH_STATE_HEALTHY,
	}

	if len(resp.Components) != 8 {
		t.Fatalf("Expected 8 components, got %d", len(resp.Components))
	}

	for _, c := range resp.Components {
		expectedState, found := expectedComponents[c.Name]
		if !found {
			t.Errorf("Unexpected component in response: %q", c.Name)
			continue
		}
		if c.HealthState != expectedState {
			t.Errorf("Expected component %q to be %v, got %v (error: %q)", c.Name, expectedState, c.HealthState, c.ErrorMessage)
		}
	}
}

func TestGetHealthStatus_Unhealthy(t *testing.T) {
	cfg := &Config{
		Port:      "8080",
		GRPCPort:  "50051",
		ProjectID: "test-project",
	}
	mockResult := RunChecksResult{
		EssDbSchemaVersion: "", // triggers "unknown" in server.go
		XidDbSchemaVersion: "", // triggers "unknown" in server.go
		Components: []*spb.ComponentHealth{
			{Name: "ESS", HealthState: spb.HealthState_HEALTH_STATE_UNHEALTHY, ErrorMessage: "500 Internal Server Error"},
			{Name: "XID", HealthState: spb.HealthState_HEALTH_STATE_UNHEALTHY, ErrorMessage: "NOT_SERVING"},
			{Name: "Actuator", HealthState: spb.HealthState_HEALTH_STATE_UNHEALTHY, ErrorMessage: "connection refused"},
			{Name: "Reconciler", HealthState: spb.HealthState_HEALTH_STATE_HEALTHY},
			{Name: "Log Monitor", HealthState: spb.HealthState_HEALTH_STATE_HEALTHY},
			{Name: "Spanner ESS", HealthState: spb.HealthState_HEALTH_STATE_UNHEALTHY, ErrorMessage: "spanner client not initialized"},
			{Name: "Spanner XID", HealthState: spb.HealthState_HEALTH_STATE_UNHEALTHY, ErrorMessage: "spanner client not initialized"},
			{Name: "Redis", HealthState: spb.HealthState_HEALTH_STATE_HEALTHY},
		},
	}
	checker := &mockHealthChecker{result: mockResult}
	s := newServer(cfg, checker)
	defer s.Close()

	resp, err := s.GetHealthStatus(context.Background(), &spb.GetHealthStatusRequest{})
	if err != nil {
		t.Fatalf("GetHealthStatus failed: %v", err)
	}

	// Verify overall state is unhealthy
	if resp.HealthState != spb.HealthState_HEALTH_STATE_UNHEALTHY {
		t.Errorf("Expected overall status 'HEALTH_STATE_UNHEALTHY', got %v", resp.HealthState)
	}

	if resp.SystemInfo.EssDbSchemaVersion != "unknown" {
		t.Errorf("Expected EssDbSchemaVersion 'unknown', got %q", resp.SystemInfo.EssDbSchemaVersion)
	}
	if resp.SystemInfo.XidDbSchemaVersion != "unknown" {
		t.Errorf("Expected XidDbSchemaVersion 'unknown', got %q", resp.SystemInfo.XidDbSchemaVersion)
	}
	if resp.SystemInfo.AppVersion != "unknown" {
		t.Errorf("Expected AppVersion 'unknown', got %q", resp.SystemInfo.AppVersion)
	}
	if resp.SystemInfo.BuildVersion != "unknown" {
		t.Errorf("Expected BuildVersion 'unknown', got %q", resp.SystemInfo.BuildVersion)
	}

	expectedComponents := map[string]spb.HealthState{
		"ESS":         spb.HealthState_HEALTH_STATE_UNHEALTHY,
		"XID":         spb.HealthState_HEALTH_STATE_UNHEALTHY,
		"Actuator":    spb.HealthState_HEALTH_STATE_UNHEALTHY,
		"Reconciler":  spb.HealthState_HEALTH_STATE_HEALTHY,
		"Log Monitor": spb.HealthState_HEALTH_STATE_HEALTHY,
		"Spanner ESS": spb.HealthState_HEALTH_STATE_UNHEALTHY,
		"Spanner XID": spb.HealthState_HEALTH_STATE_UNHEALTHY,
		"Redis":       spb.HealthState_HEALTH_STATE_HEALTHY,
	}

	for _, c := range resp.Components {
		expectedState := expectedComponents[c.Name]
		if c.HealthState != expectedState {
			t.Errorf("Expected component %q to be %v, got %v (error: %q)", c.Name, expectedState, c.HealthState, c.ErrorMessage)
		}
	}
}

func TestListConfigEntries(t *testing.T) {
	tempDir := t.TempDir()

	// Mock one directory to verify integration
	essDir := filepath.Join(tempDir, "ess")
	if err := os.Mkdir(essDir, 0755); err != nil {
		t.Fatalf("failed to create ess dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(essDir, "ess_key"), []byte("ess_val"), 0644); err != nil {
		t.Fatalf("failed to write ess config: %v", err)
	}

	cfg := &Config{
		Port:      "8080",
		GRPCPort:  "50051",
		ProjectID: "test-project",
		XidAddr:   "localhost:50051",
		ConfigDir: tempDir,
	}
	checker, err := NewHealthChecker(HealthCheckerParams{
		Config:          cfg,
		EssHealthClient: nil,
		XidHealthClient: nil,
		RedisClient:     nil,
		CloudRunService: nil,
	})
	if err != nil {
		t.Fatalf("failed to create health checker: %v", err)
	}
	s := newServer(cfg, checker)
	defer s.Close()

	resp, err := s.ListConfigEntries(context.Background(), &spb.ListConfigEntriesRequest{})
	if err != nil {
		t.Fatalf("ListConfigEntries failed: %v", err)
	}

	// We expect 1 group (ESS)
	if len(resp.ConfigGroups) != 1 {
		t.Fatalf("Expected 1 config group, got %d", len(resp.ConfigGroups))
	}

	group := resp.ConfigGroups[0]
	if group.DisplayName != "Experiment State Server Config" {
		t.Errorf("Expected group display name 'Experiment State Server Config', got %q", group.DisplayName)
	}
	if len(group.Configs) != 1 {
		t.Fatalf("Expected 1 config in group, got %d", len(group.Configs))
	}
	if group.Configs[0].Key != "ess_key" || group.Configs[0].Value != "ess_val" {
		t.Errorf("Unexpected config: got key=%q, val=%q", group.Configs[0].Key, group.Configs[0].Value)
	}
}

func TestGetUserInfo(t *testing.T) {
	cfg := &Config{Port: "8080", GRPCPort: "50051", ProjectID: "test-project", XidAddr: "localhost:50051"}
	checker, err := NewHealthChecker(HealthCheckerParams{
		Config:          cfg,
		EssHealthClient: nil,
		XidHealthClient: nil,
		RedisClient:     nil,
		CloudRunService: nil,
	})
	if err != nil {
		t.Fatalf("failed to create health checker: %v", err)
	}
	s := newServer(cfg, checker)
	defer s.Close()

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
