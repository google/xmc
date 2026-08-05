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
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	spb "xmanager_cloud/health_service/proto/api_go_grpc_proto"
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
	s := newServer(cfg)

	// 4. Run health check
	resp, err := s.GetHealthStatus(context.Background(), &spb.GetHealthStatusRequest{})
	if err != nil {
		t.Fatalf("GetHealthStatus failed: %v", err)
	}

	// 5. Verify results
	if resp.HealthState != spb.HealthState_HEALTH_STATE_HEALTHY {
		t.Errorf("Expected overall status 'HEALTH_STATE_HEALTHY', got %v", resp.HealthState)
	}

	expectedComponents := map[string]spb.HealthState{
		"ESS":         spb.HealthState_HEALTH_STATE_HEALTHY,
		"XID":         spb.HealthState_HEALTH_STATE_HEALTHY,
		"Actuator":    spb.HealthState_HEALTH_STATE_HEALTHY,
		"Reconciler":  spb.HealthState_HEALTH_STATE_HEALTHY,
		"Log Monitor": spb.HealthState_HEALTH_STATE_HEALTHY,
		"Spanner":     spb.HealthState_HEALTH_STATE_HEALTHY,
		"Redis":       spb.HealthState_HEALTH_STATE_HEALTHY,
	}

	if len(resp.Components) != 7 {
		t.Fatalf("Expected 7 components, got %d", len(resp.Components))
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
	s := newServer(cfg)

	resp, err := s.GetHealthStatus(context.Background(), &spb.GetHealthStatusRequest{})
	if err != nil {
		t.Fatalf("GetHealthStatus failed: %v", err)
	}

	// Verify overall state is unhealthy
	if resp.HealthState != spb.HealthState_HEALTH_STATE_UNHEALTHY {
		t.Errorf("Expected overall status 'HEALTH_STATE_UNHEALTHY', got %v", resp.HealthState)
	}

	expectedComponents := map[string]spb.HealthState{
		"ESS":         spb.HealthState_HEALTH_STATE_UNHEALTHY,
		"XID":         spb.HealthState_HEALTH_STATE_UNHEALTHY,
		"Actuator":    spb.HealthState_HEALTH_STATE_UNHEALTHY,
		"Reconciler":  spb.HealthState_HEALTH_STATE_HEALTHY,
		"Log Monitor": spb.HealthState_HEALTH_STATE_HEALTHY,
		"Spanner":     spb.HealthState_HEALTH_STATE_HEALTHY,
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
