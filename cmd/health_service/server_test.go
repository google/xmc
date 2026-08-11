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
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	spb "xmanager_cloud/health_service/proto/api_go_grpc_proto"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
)

func TestGetHealthStatus_AllHealthy(t *testing.T) {
	// 1. Setup mock HTTP servers
	essServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/readyz" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("OK"))
		}
	}))
	defer essServer.Close()

	actuatorServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/readyz" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("OK"))
		}
	}))
	defer actuatorServer.Close()

	reconcilerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/readyz" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("OK"))
		}
	}))
	defer reconcilerServer.Close()

	// 2. Setup mock gRPC health server
	lis, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatalf("failed to listen for gRPC server: %v", err)
	}
	grpcServer := grpc.NewServer()
	healthServer := health.NewServer()
	healthServer.SetServingStatus("xmc.XidService", grpc_health_v1.HealthCheckResponse_SERVING)
	grpc_health_v1.RegisterHealthServer(grpcServer, healthServer)
	go func() {
		_ = grpcServer.Serve(lis)
	}()
	defer grpcServer.Stop()

	// 3. Configure and create server
	cfg := &Config{
		Port:          "8080",
		GRPCPort:      "50051",
		ProjectID:     "test-project",
		EssURL:        essServer.URL,
		XidAddr:       lis.Addr().String(),
		ActuatorURL:   actuatorServer.URL,
		ReconcilerURL: reconcilerServer.URL,
	}
	mockEss := &mockSpannerHealthClient{version: 6, dirty: false}
	mockXid := &mockSpannerHealthClient{version: 1, dirty: false}
	s, err := newServer(cfg, mockEss, mockXid, &mockRedisClient{})
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	defer s.Close()

	// 4. Run health check
	resp, err := s.GetHealthStatus(context.Background(), &spb.GetHealthStatusRequest{})
	if err != nil {
		t.Fatalf("GetHealthStatus failed: %v", err)
	}

	// 5. Verify results
	if resp.HealthState != spb.HealthState_HEALTH_STATE_HEALTHY {
		t.Errorf("Expected overall status 'HEALTH_STATE_HEALTHY', got %v", resp.HealthState)
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

func TestGetHealthStatus_SomeUnhealthy(t *testing.T) {
	// ESS returns 500 Unhealthy
	essServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer essServer.Close()

	// Actuator is completely unreachable (mock invalid URL)
	actuatorURL := "http://localhost:9999" // Assuming nothing runs on port 9999

	reconcilerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer reconcilerServer.Close()

	// XID serving status is NOT_SERVING
	lis, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatalf("failed to listen for gRPC server: %v", err)
	}
	grpcServer := grpc.NewServer()
	healthServer := health.NewServer()
	healthServer.SetServingStatus("xmc.XidService", grpc_health_v1.HealthCheckResponse_NOT_SERVING)
	grpc_health_v1.RegisterHealthServer(grpcServer, healthServer)
	go func() {
		_ = grpcServer.Serve(lis)
	}()
	defer grpcServer.Stop()

	cfg := &Config{
		Port:          "8080",
		GRPCPort:      "50051",
		ProjectID:     "test-project",
		EssURL:        essServer.URL,
		XidAddr:       lis.Addr().String(),
		ActuatorURL:   actuatorURL,
		ReconcilerURL: reconcilerServer.URL,
	}
	s, err := newServer(cfg, nil, nil, &mockRedisClient{})
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
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
	s, err := newServer(cfg, nil, nil, nil)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
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
	s, err := newServer(cfg, nil, nil, nil)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
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

func TestGetHealthStatus_RedisUnhealthy(t *testing.T) {
	cfg := &Config{
		Port:          "8080",
		GRPCPort:      "50051",
		ProjectID:     "test-project",
		EssURL:        "http://invalid",
		XidAddr:       "invalid:50051",
		ActuatorURL:   "http://invalid",
		ReconcilerURL: "http://invalid",
	}
	mockRedis := &mockRedisClient{err: fmt.Errorf("connection refused")}
	s, err := newServer(cfg, nil, nil, mockRedis)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	defer s.Close()

	resp, err := s.GetHealthStatus(context.Background(), &spb.GetHealthStatusRequest{})
	if err != nil {
		t.Fatalf("GetHealthStatus failed: %v", err)
	}

	if resp.HealthState != spb.HealthState_HEALTH_STATE_UNHEALTHY {
		t.Errorf("Expected overall status 'HEALTH_STATE_UNHEALTHY', got %v", resp.HealthState)
	}

	var redisComp *spb.ComponentHealth
	for _, c := range resp.Components {
		if c.Name == "Redis" {
			redisComp = c
			break
		}
	}
	if redisComp == nil {
		t.Fatal("Redis component not found in response")
	}

	if redisComp.HealthState != spb.HealthState_HEALTH_STATE_UNHEALTHY {
		t.Errorf("Expected Redis component status 'HEALTH_STATE_UNHEALTHY', got %v", redisComp.HealthState)
	}
	if redisComp.ErrorMessage != "redis ping failed: connection refused" {
		t.Errorf("Expected error message 'redis ping failed: connection refused', got %q", redisComp.ErrorMessage)
	}
}
