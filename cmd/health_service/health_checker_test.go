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
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	spb "xmanager_cloud/health_service/proto/api_go_grpc_proto"
)

func TestNewHealthChecker_Success(t *testing.T) {
	cfg := &Config{
		XidAddr: "localhost:50051",
	}
	checker, err := newHealthChecker(cfg)
	if err != nil {
		t.Fatalf("newHealthChecker failed: %v", err)
	}
	if checker == nil {
		t.Fatal("expected non-nil checker")
	}
	defer checker.Close()
}

func TestNewHealthChecker_EmptyAddr(t *testing.T) {
	cfg := &Config{
		XidAddr: "",
	}
	_, err := newHealthChecker(cfg)
	if err == nil {
		t.Fatal("expected error with empty XidAddr")
	}
}

func TestCheckHTTPComponent_Healthy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/readyz" {
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer server.Close()

	checker, _ := newHealthChecker(&Config{XidAddr: "localhost:50051"})
	defer checker.Close()

	res := checker.checkHTTPComponent(context.Background(), "ESS", server.URL, "/readyz")
	if res.HealthState != spb.HealthState_HEALTH_STATE_HEALTHY {
		t.Errorf("expected HEALTHY, got %v (error: %q)", res.HealthState, res.ErrorMessage)
	}
}

func TestCheckHTTPComponent_UnhealthyStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	checker, _ := newHealthChecker(&Config{XidAddr: "localhost:50051"})
	defer checker.Close()

	res := checker.checkHTTPComponent(context.Background(), "ESS", server.URL, "/readyz")
	if res.HealthState != spb.HealthState_HEALTH_STATE_UNHEALTHY {
		t.Errorf("expected UNHEALTHY, got %v", res.HealthState)
	}
	if res.ErrorMessage == "" {
		t.Error("expected error message, got empty")
	}
}

func TestCheckHTTPComponent_Timeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	checker, _ := newHealthChecker(&Config{XidAddr: "localhost:50051"})
	// Shorten timeout for test
	checker.httpClient.Timeout = 10 * time.Millisecond
	defer checker.Close()

	res := checker.checkHTTPComponent(context.Background(), "ESS", server.URL, "/readyz")
	if res.HealthState != spb.HealthState_HEALTH_STATE_UNHEALTHY {
		t.Errorf("expected UNHEALTHY due to timeout, got %v", res.HealthState)
	}
}

func TestCheckGRPCComponent_Healthy(t *testing.T) {
	lis, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	grpcServer := grpc.NewServer()
	healthServer := health.NewServer()
	healthServer.SetServingStatus("xmc.XidService", grpc_health_v1.HealthCheckResponse_SERVING)
	grpc_health_v1.RegisterHealthServer(grpcServer, healthServer)
	go func() {
		_ = grpcServer.Serve(lis)
	}()
	defer grpcServer.Stop()

	checker, _ := newHealthChecker(&Config{XidAddr: lis.Addr().String()})
	defer checker.Close()

	res := checker.checkGRPCComponent(context.Background(), "XID", checker.xidConn, "xmc.XidService")
	if res.HealthState != spb.HealthState_HEALTH_STATE_HEALTHY {
		t.Errorf("expected HEALTHY, got %v (error: %q)", res.HealthState, res.ErrorMessage)
	}
}

func TestCheckGRPCComponent_NotServing(t *testing.T) {
	lis, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	grpcServer := grpc.NewServer()
	healthServer := health.NewServer()
	healthServer.SetServingStatus("xmc.XidService", grpc_health_v1.HealthCheckResponse_NOT_SERVING)
	grpc_health_v1.RegisterHealthServer(grpcServer, healthServer)
	go func() {
		_ = grpcServer.Serve(lis)
	}()
	defer grpcServer.Stop()

	checker, _ := newHealthChecker(&Config{XidAddr: lis.Addr().String()})
	defer checker.Close()

	res := checker.checkGRPCComponent(context.Background(), "XID", checker.xidConn, "xmc.XidService")
	if res.HealthState != spb.HealthState_HEALTH_STATE_UNHEALTHY {
		t.Errorf("expected UNHEALTHY, got %v", res.HealthState)
	}
	if res.ErrorMessage == "" {
		t.Error("expected error message, got empty")
	}
}

func TestCheckGRPCComponent_NilConnection(t *testing.T) {
	checker, _ := newHealthChecker(&Config{XidAddr: "localhost:50051"})
	checker.Close()

	res := checker.checkGRPCComponent(context.Background(), "XID", nil, "xmc.XidService")
	if res.HealthState != spb.HealthState_HEALTH_STATE_UNHEALTHY {
		t.Errorf("expected UNHEALTHY for nil conn, got %v", res.HealthState)
	}
	if res.ErrorMessage != "grpc client connection is nil" {
		t.Errorf("expected 'grpc client connection is nil', got %q", res.ErrorMessage)
	}
}
