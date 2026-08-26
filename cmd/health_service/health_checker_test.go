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
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	commonhealth "xmanager_cloud/common/health"
	spb "xmanager_cloud/health_service/proto/api_go_grpc_proto"

	"google.golang.org/api/option"
	run "google.golang.org/api/run/v2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
)

func TestNewHealthChecker_Success(t *testing.T) {
	cfg := &Config{
		XidAddr: "localhost:50051",
	}
	checker, err := NewHealthChecker(HealthCheckerParams{Config: cfg})
	if err != nil {
		t.Fatalf("NewHealthChecker failed: %v", err)
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
	_, err := NewHealthChecker(HealthCheckerParams{Config: cfg})
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

	checker, _ := NewHealthChecker(HealthCheckerParams{Config: &Config{XidAddr: "localhost:50051"}})
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

	checker, _ := NewHealthChecker(HealthCheckerParams{Config: &Config{XidAddr: "localhost:50051"}})
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

	checker, _ := NewHealthChecker(HealthCheckerParams{Config: &Config{XidAddr: "localhost:50051"}})
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

	checker, _ := NewHealthChecker(HealthCheckerParams{Config: &Config{XidAddr: lis.Addr().String()}})
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

	checker, _ := NewHealthChecker(HealthCheckerParams{Config: &Config{XidAddr: lis.Addr().String()}})
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
	checker, _ := NewHealthChecker(HealthCheckerParams{Config: &Config{XidAddr: "localhost:50051"}})
	checker.Close()

	res := checker.checkGRPCComponent(context.Background(), "XID", nil, "xmc.XidService")
	if res.HealthState != spb.HealthState_HEALTH_STATE_UNHEALTHY {
		t.Errorf("expected UNHEALTHY for nil conn, got %v", res.HealthState)
	}
	if res.ErrorMessage != "grpc client connection is nil" {
		t.Errorf("expected 'grpc client connection is nil', got %q", res.ErrorMessage)
	}
}

type mockSpannerHealthClient struct {
	version int64
	dirty   bool
	err     error
}

//nolint:revive // mock implementing interface with 3 returns
func (m *mockSpannerHealthClient) QuerySchemaVersion(ctx context.Context) (int64, bool, error) {
	return m.version, m.dirty, m.err
}

func TestCheckSpannerComponent(t *testing.T) {
	tests := []struct {
		name                 string
		client               commonhealth.SpannerHealthClient
		expectedVersionInput int64
		wantHealthState      spb.HealthState
		wantVersionStr       string
		expectErr            bool
		wantErrMsg           string
	}{
		{
			name:                 "Success",
			client:               &mockSpannerHealthClient{version: 42, dirty: false},
			expectedVersionInput: 42,
			wantHealthState:      spb.HealthState_HEALTH_STATE_HEALTHY,
			wantVersionStr:       "42",
		},
		{
			name:                 "Dirty",
			client:               &mockSpannerHealthClient{version: 42, dirty: true},
			expectedVersionInput: 42,
			wantHealthState:      spb.HealthState_HEALTH_STATE_UNHEALTHY,
			wantVersionStr:       "42 (dirty)",
			expectErr:            true,
			wantErrMsg:           "schema migration is in a dirty state at version 42",
		},
		{
			name:                 "Version Mismatch",
			client:               &mockSpannerHealthClient{version: 41, dirty: false},
			expectedVersionInput: 42,
			wantHealthState:      spb.HealthState_HEALTH_STATE_UNHEALTHY,
			wantVersionStr:       "41",
			expectErr:            true,
			wantErrMsg:           "schema version 41 does not match expected version 42",
		},
		{
			name:                 "Error",
			client:               &mockSpannerHealthClient{err: fmt.Errorf("connection failed")},
			expectedVersionInput: 42,
			wantHealthState:      spb.HealthState_HEALTH_STATE_UNHEALTHY,
			expectErr:            true,
			wantErrMsg:           "database check failed: connection failed",
		},
		{
			name:                 "Nil Client",
			client:               nil,
			expectedVersionInput: 42,
			wantHealthState:      spb.HealthState_HEALTH_STATE_UNHEALTHY,
			wantVersionStr:       "",
			expectErr:            true,
			wantErrMsg:           "spanner client not initialized",
		},
	}

	checker := &HealthChecker{}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			comp, versionStr := checker.checkSpannerComponent(context.Background(), "Spanner ESS", tt.client, tt.expectedVersionInput)
			if comp.HealthState != tt.wantHealthState {
				t.Errorf("expected state %v, got %v", tt.wantHealthState, comp.HealthState)
			}
			if versionStr != tt.wantVersionStr {
				t.Errorf("expected version %q, got %q", tt.wantVersionStr, versionStr)
			}
			if tt.expectErr && comp.ErrorMessage == "" {
				t.Error("expected non-empty error message, got empty")
			}
			if !tt.expectErr && comp.ErrorMessage != "" {
				t.Errorf("unexpected error message: %q", comp.ErrorMessage)
			}
			if tt.wantErrMsg != "" && comp.ErrorMessage != tt.wantErrMsg {
				t.Errorf("expected error message %q, got %q", tt.wantErrMsg, comp.ErrorMessage)
			}
		})
	}
}

const (
	mockEssExpectedVersion = int64(5)
	mockXidExpectedVersion = int64(1)
)

func TestRunChecks_SpannerMetadata(t *testing.T) {
	checker := &HealthChecker{
		cfg: &Config{
			EssURL:                 "http://invalid",
			XidAddr:                "invalid:50051",
			ActuatorURL:            "http://invalid",
			ReconcilerURL:          "http://invalid",
			LogMonitorResourceName: "projects/test-project/locations/us-central1/services/log-monitor",
		},
		httpClient: &http.Client{
			Timeout: 10 * time.Millisecond,
		},
		essHealthClient:          &mockSpannerHealthClient{version: 6, dirty: false},
		xidHealthClient:          &mockSpannerHealthClient{version: 1, dirty: true},
		redisClient:              &mockRedisClient{},
		essExpectedSchemaVersion: 6,
		xidExpectedSchemaVersion: 1,
	}

	res := checker.RunChecks(context.Background())

	if len(res.Components) != 8 {
		t.Errorf("expected 8 components, got %d", len(res.Components))
	}

	if res.EssDbSchemaVersion != "6" {
		t.Errorf("expected ESS version '6', got %q", res.EssDbSchemaVersion)
	}
	if res.XidDbSchemaVersion != "1 (dirty)" {
		t.Errorf("expected XID version '1 (dirty)', got %q", res.XidDbSchemaVersion)
	}
}

var _ RedisPingClient = (*mockRedisClient)(nil)

type mockRedisClient struct {
	err error
}

func (m *mockRedisClient) Ping(ctx context.Context) error {
	return m.err
}

func TestCheckRedis(t *testing.T) {
	tests := []struct {
		name          string
		client        RedisPingClient
		expectedState spb.HealthState
		expectedErr   string
	}{
		{
			name:          "Healthy",
			client:        &mockRedisClient{},
			expectedState: spb.HealthState_HEALTH_STATE_HEALTHY,
		},
		{
			name:          "Unhealthy",
			client:        &mockRedisClient{err: fmt.Errorf("connection refused")},
			expectedState: spb.HealthState_HEALTH_STATE_UNHEALTHY,
			expectedErr:   "redis ping failed: connection refused",
		},
		{
			name:          "Nil Client",
			client:        nil,
			expectedState: spb.HealthState_HEALTH_STATE_UNHEALTHY,
			expectedErr:   "redis client not initialized",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checker := &HealthChecker{
				cfg:         &Config{},
				redisClient: tt.client,
			}

			var redisTarget checkTarget
			for _, target := range checker.getCheckTargets() {
				if target.name == "Redis" {
					redisTarget = target
					break
				}
			}
			if redisTarget.name == "" {
				t.Fatal("Redis target not found")
			}

			res := redisTarget.checkFn(context.Background())
			if res.HealthState != tt.expectedState {
				t.Errorf("expected state %v, got %v", tt.expectedState, res.HealthState)
			}
			if tt.expectedErr != "" && res.ErrorMessage != tt.expectedErr {
				t.Errorf("expected error %q, got %q", tt.expectedErr, res.ErrorMessage)
			}
			if tt.expectedErr == "" && res.ErrorMessage != "" {
				t.Errorf("unexpected error: %q", res.ErrorMessage)
			}
		})
	}
}

func TestCheckCloudRunComponent(t *testing.T) {
	tests := []struct {
		name          string
		mockState     string
		mockReason    string
		mockMessage   string
		nilClient     bool
		serviceName   string
		expectedState spb.HealthState
		expectedErr   string
	}{
		{
			name:          "Healthy",
			mockState:     "CONDITION_SUCCEEDED",
			serviceName:   "projects/test-project/locations/us-central1/services/log-monitor",
			expectedState: spb.HealthState_HEALTH_STATE_HEALTHY,
		},
		{
			name:          "Unhealthy",
			mockState:     "CONDITION_FAILED",
			mockReason:    "UNHEALTHY",
			mockMessage:   "test error",
			serviceName:   "projects/test-project/locations/us-central1/services/log-monitor",
			expectedState: spb.HealthState_HEALTH_STATE_UNHEALTHY,
			expectedErr:   "service not ready: CONDITION_FAILED (reason: UNHEALTHY, message: test error)",
		},
		{
			name:          "Nil Client",
			nilClient:     true,
			serviceName:   "projects/test-project/locations/us-central1/services/log-monitor",
			expectedState: spb.HealthState_HEALTH_STATE_UNSPECIFIED,
			expectedErr:   "cloud run client not initialized",
		},
		{
			name:          "Missing Service Name",
			serviceName:   "",
			expectedState: spb.HealthState_HEALTH_STATE_UNSPECIFIED,
			expectedErr:   "log monitor resource name is not configured",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cloudRunService *run.Service
			if !tt.nilClient && tt.serviceName != "" {
				server := newMockCloudRunServer(t, tt.mockState, tt.mockReason, tt.mockMessage)
				defer server.Close()

				var err error
				cloudRunService, err = run.NewService(context.Background(),
					option.WithHTTPClient(server.Client()),
					option.WithEndpoint(server.URL),
					option.WithoutAuthentication(),
				)
				if err != nil {
					t.Fatalf("failed to create run service: %v", err)
				}
			}

			checker := &HealthChecker{
				cloudRunService: cloudRunService,
			}

			res := checker.checkCloudRunComponent(context.Background(), "Log Monitor", tt.serviceName)
			if res.HealthState != tt.expectedState {
				t.Errorf("expected state %v, got %v", tt.expectedState, res.HealthState)
			}
			if res.ErrorMessage != tt.expectedErr {
				t.Errorf("expected error %q, got %q", tt.expectedErr, res.ErrorMessage)
			}
		})
	}
}

func TestRunChecks_LogMonitorMissingConfig(t *testing.T) {
	checker := &HealthChecker{
		cfg: &Config{
			EssURL:                 "http://invalid",
			XidAddr:                "invalid:50051",
			ActuatorURL:            "http://invalid",
			ReconcilerURL:          "http://invalid",
			LogMonitorResourceName: "", // Empty to trigger config error
		},
		httpClient: &http.Client{
			Timeout: 10 * time.Millisecond,
		},
		essHealthClient:          &mockSpannerHealthClient{version: 6, dirty: false},
		xidHealthClient:          &mockSpannerHealthClient{version: 1, dirty: false},
		redisClient:              &mockRedisClient{},
		essExpectedSchemaVersion: 6,
		xidExpectedSchemaVersion: 1,
	}

	res := checker.RunChecks(context.Background())

	// Expect 8 components (including Log Monitor)
	if len(res.Components) != 8 {
		t.Errorf("expected 8 components, got %d", len(res.Components))
	}

	// Verify Log Monitor is in the list and reports unhealthy with config error
	var lmComp *spb.ComponentHealth
	for _, comp := range res.Components {
		if comp.Name == "Log Monitor" {
			lmComp = comp
			break
		}
	}

	if lmComp == nil {
		t.Fatal("Log Monitor component not found in results")
	}

	if lmComp.HealthState != spb.HealthState_HEALTH_STATE_UNSPECIFIED {
		t.Errorf("expected Log Monitor to be unspecified, got %v", lmComp.HealthState)
	}

	expectedErr := "log monitor resource name is not configured"
	if lmComp.ErrorMessage != expectedErr {
		t.Errorf("expected error message %q, got %q", expectedErr, lmComp.ErrorMessage)
	}
}

func newMockCloudRunServer(t *testing.T, state, reason, message string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{
			"terminalCondition": {
				"state": "%s",
				"reason": "%s",
				"message": "%s"
			}
		}`, state, reason, message)
	}))
}

func TestRunChecks(t *testing.T) {
	tests := []struct {
		name                   string
		httpStatus             int
		grpcStatus             grpc_health_v1.HealthCheckResponse_ServingStatus
		crState                string
		crReason               string
		crMessage              string
		essVersion             int64
		essSpannerErr          error
		essSpannerDirty        bool
		xidVersion             int64
		xidSpannerErr          error
		xidSpannerDirty        bool
		redisErr               error
		wantStates             map[string]spb.HealthState
		wantEssDbSchemaVersion string
		wantXidDbSchemaVersion string
	}{
		{
			name:       "AllHealthy",
			httpStatus: http.StatusOK,
			grpcStatus: grpc_health_v1.HealthCheckResponse_SERVING,
			crState:    "CONDITION_SUCCEEDED",
			essVersion: mockEssExpectedVersion,
			xidVersion: mockXidExpectedVersion,
			wantStates: map[string]spb.HealthState{
				"ESS":         spb.HealthState_HEALTH_STATE_HEALTHY,
				"XID":         spb.HealthState_HEALTH_STATE_HEALTHY,
				"Actuator":    spb.HealthState_HEALTH_STATE_HEALTHY,
				"Reconciler":  spb.HealthState_HEALTH_STATE_HEALTHY,
				"Log Monitor": spb.HealthState_HEALTH_STATE_HEALTHY,
				"Spanner ESS": spb.HealthState_HEALTH_STATE_HEALTHY,
				"Spanner XID": spb.HealthState_HEALTH_STATE_HEALTHY,
				"Redis":       spb.HealthState_HEALTH_STATE_HEALTHY,
			},
			wantEssDbSchemaVersion: "5",
			wantXidDbSchemaVersion: "1",
		},
		{
			name:          "AllUnhealthy",
			httpStatus:    http.StatusInternalServerError,
			grpcStatus:    grpc_health_v1.HealthCheckResponse_NOT_SERVING,
			crState:       "CONDITION_FAILED",
			crReason:      "UNHEALTHY",
			crMessage:     "test error",
			essVersion:    mockEssExpectedVersion,
			essSpannerErr: fmt.Errorf("spanner error"),
			xidVersion:    mockXidExpectedVersion,
			xidSpannerErr: fmt.Errorf("spanner error"),
			redisErr:      fmt.Errorf("redis failed"),
			wantStates: map[string]spb.HealthState{
				"ESS":         spb.HealthState_HEALTH_STATE_UNHEALTHY,
				"XID":         spb.HealthState_HEALTH_STATE_UNHEALTHY,
				"Actuator":    spb.HealthState_HEALTH_STATE_UNHEALTHY,
				"Reconciler":  spb.HealthState_HEALTH_STATE_UNHEALTHY,
				"Log Monitor": spb.HealthState_HEALTH_STATE_UNHEALTHY,
				"Spanner ESS": spb.HealthState_HEALTH_STATE_UNHEALTHY,
				"Spanner XID": spb.HealthState_HEALTH_STATE_UNHEALTHY,
				"Redis":       spb.HealthState_HEALTH_STATE_UNHEALTHY,
			},
			wantEssDbSchemaVersion: "",
			wantXidDbSchemaVersion: "",
		},
		{
			name:       "SpannerVersionMismatch",
			httpStatus: http.StatusOK,
			grpcStatus: grpc_health_v1.HealthCheckResponse_SERVING,
			crState:    "CONDITION_SUCCEEDED",
			essVersion: mockEssExpectedVersion - 1,
			xidVersion: mockXidExpectedVersion + 1,
			wantStates: map[string]spb.HealthState{
				"ESS":         spb.HealthState_HEALTH_STATE_HEALTHY,
				"XID":         spb.HealthState_HEALTH_STATE_HEALTHY,
				"Actuator":    spb.HealthState_HEALTH_STATE_HEALTHY,
				"Reconciler":  spb.HealthState_HEALTH_STATE_HEALTHY,
				"Log Monitor": spb.HealthState_HEALTH_STATE_HEALTHY,
				"Spanner ESS": spb.HealthState_HEALTH_STATE_UNHEALTHY,
				"Spanner XID": spb.HealthState_HEALTH_STATE_UNHEALTHY,
				"Redis":       spb.HealthState_HEALTH_STATE_HEALTHY,
			},
			wantEssDbSchemaVersion: "4",
			wantXidDbSchemaVersion: "2",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// 1. Mock HTTP server
			httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.httpStatus)
			}))
			defer httpServer.Close()

			// 2. Mock gRPC server
			lis, err := net.Listen("tcp", "localhost:0")
			if err != nil {
				t.Fatalf("failed to listen: %v", err)
			}
			grpcServer := grpc.NewServer()
			healthServer := health.NewServer()
			healthServer.SetServingStatus("xmc.XidService", tc.grpcStatus)
			grpc_health_v1.RegisterHealthServer(grpcServer, healthServer)
			go grpcServer.Serve(lis)
			defer grpcServer.Stop()

			// 3. Mock Cloud Run API server
			runServer := newMockCloudRunServer(t, tc.crState, tc.crReason, tc.crMessage)
			defer runServer.Close()
			cloudRunService, err := run.NewService(context.Background(),
				option.WithHTTPClient(runServer.Client()),
				option.WithEndpoint(runServer.URL),
				option.WithoutAuthentication(),
			)
			if err != nil {
				t.Fatalf("failed to create run service: %v", err)
			}

			// 4. Construct checker using NewHealthChecker
			checker, err := NewHealthChecker(HealthCheckerParams{
				Config: &Config{
					EssURL:                 httpServer.URL,
					XidAddr:                lis.Addr().String(),
					ActuatorURL:            httpServer.URL,
					ReconcilerURL:          httpServer.URL,
					LogMonitorResourceName: "projects/test-project/locations/us-central1/services/log-monitor",
				},
				EssHealthClient:          &mockSpannerHealthClient{version: tc.essVersion, dirty: tc.essSpannerDirty, err: tc.essSpannerErr},
				XidHealthClient:          &mockSpannerHealthClient{version: tc.xidVersion, dirty: tc.xidSpannerDirty, err: tc.xidSpannerErr},
				RedisClient:              &mockRedisClient{err: tc.redisErr},
				CloudRunService:          cloudRunService,
				EssExpectedSchemaVersion: mockEssExpectedVersion,
				XidExpectedSchemaVersion: mockXidExpectedVersion,
			})
			if err != nil {
				t.Fatalf("failed to create health checker: %v", err)
			}
			defer checker.Close()

			res := checker.RunChecks(context.Background())

			if len(res.Components) != 8 {
				t.Fatalf("expected 8 components, got %d", len(res.Components))
			}

			for _, comp := range res.Components {
				expected := tc.wantStates[comp.Name]
				if comp.HealthState != expected {
					t.Errorf("expected component %q to be %v, got %v (error: %q)", comp.Name, expected, comp.HealthState, comp.ErrorMessage)
				}
			}

			if res.EssDbSchemaVersion != tc.wantEssDbSchemaVersion {
				t.Errorf("expected ESS version %q, got %q", tc.wantEssDbSchemaVersion, res.EssDbSchemaVersion)
			}
			if res.XidDbSchemaVersion != tc.wantXidDbSchemaVersion {
				t.Errorf("expected XID version %q, got %q", tc.wantXidDbSchemaVersion, res.XidDbSchemaVersion)
			}
		})
	}
}
