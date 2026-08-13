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
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	spb "xmanager_cloud/health_service/proto/api_go_grpc_proto"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type mockHealthChecker struct {
	result    RunChecksResult
	err       error
	callCount int64
	delay     time.Duration
}

func (m *mockHealthChecker) RunChecks(ctx context.Context) RunChecksResult {
	atomic.AddInt64(&m.callCount, 1)
	if m.delay > 0 {
		time.Sleep(m.delay)
	}
	return m.result
}

func (m *mockHealthChecker) Close() error {
	return m.err
}

type testFixture struct {
	checker *mockHealthChecker
	server  *server
	t       *testing.T
}

func newTestFixture(t *testing.T, cacheTTL time.Duration, maxRequests int, window time.Duration) *testFixture {
	cfg := &Config{
		CacheTTL:                cacheTTL,
		ForceRefreshMaxRequests: maxRequests,
		ForceRefreshWindow:      window,
	}
	mockResult := RunChecksResult{
		Components: []*spb.ComponentHealth{
			{Name: "ESS", HealthState: spb.HealthState_HEALTH_STATE_HEALTHY},
			{Name: "XID", HealthState: spb.HealthState_HEALTH_STATE_HEALTHY},
			{Name: "Log Monitor", HealthState: spb.HealthState_HEALTH_STATE_HEALTHY},
		},
	}
	checker := &mockHealthChecker{result: mockResult}
	s := newServer(cfg, checker)
	return &testFixture{
		checker: checker,
		server:  s,
		t:       t,
	}
}

func (f *testFixture) close() {
	f.server.Close()
}

func (f *testFixture) assertCallCount(expected int) {
	f.t.Helper()
	if actual := int(atomic.LoadInt64(&f.checker.callCount)); actual != expected {
		f.t.Errorf("expected call count %d, got %d", expected, actual)
	}
}

func (f *testFixture) assertCall(ctx context.Context, req *spb.GetHealthStatusRequest, expectedCalls int) *spb.HealthStatusResponse {
	f.t.Helper()
	resp, err := f.server.GetHealthStatus(ctx, req)
	if err != nil {
		f.t.Fatalf("call failed: %v", err)
	}
	f.assertCallCount(expectedCalls)
	return resp
}

func (f *testFixture) assertRateLimited(ctx context.Context, req *spb.GetHealthStatusRequest, expectedCalls int) {
	f.t.Helper()
	_, err := f.server.GetHealthStatus(ctx, req)
	if err == nil {
		f.t.Fatal("expected rate limit error, got nil")
	}
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.ResourceExhausted {
		f.t.Errorf("expected ResourceExhausted error, got %v", err)
	}
	f.assertCallCount(expectedCalls)
}

func TestGetHealthStatus(t *testing.T) {
	tests := []struct {
		name               string
		cfg                *Config
		mockResult         RunChecksResult
		expectedState      spb.HealthState
		expectedSystemInfo *spb.SystemInfo
		expectedComponents map[string]spb.HealthState
	}{
		{
			name: "AllHealthy",
			cfg: &Config{
				Port:             "8080",
				GRPCPort:         "50051",
				ProjectID:        "test-project",
				AppVersion:       "test-version-1.2.3",
				BuildVersion:     "test-build-version-4.5.6",
				ObservabilityURL: "http://test-observability-url",
			},
			mockResult: RunChecksResult{
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
			},
			expectedState: spb.HealthState_HEALTH_STATE_HEALTHY,
			expectedSystemInfo: &spb.SystemInfo{
				AppVersion:         "test-version-1.2.3",
				BuildVersion:       "test-build-version-4.5.6",
				ObservabilityUrl:   "http://test-observability-url",
				EssDbSchemaVersion: "6",
				XidDbSchemaVersion: "1",
			},
			expectedComponents: map[string]spb.HealthState{
				"ESS":         spb.HealthState_HEALTH_STATE_HEALTHY,
				"XID":         spb.HealthState_HEALTH_STATE_HEALTHY,
				"Actuator":    spb.HealthState_HEALTH_STATE_HEALTHY,
				"Reconciler":  spb.HealthState_HEALTH_STATE_HEALTHY,
				"Log Monitor": spb.HealthState_HEALTH_STATE_HEALTHY,
				"Spanner ESS": spb.HealthState_HEALTH_STATE_HEALTHY,
				"Spanner XID": spb.HealthState_HEALTH_STATE_HEALTHY,
				"Redis":       spb.HealthState_HEALTH_STATE_HEALTHY,
			},
		},
		{
			name: "UnhealthyWithFallbackDefaults",
			cfg: &Config{
				Port:      "8080",
				GRPCPort:  "50051",
				ProjectID: "test-project",
			},
			mockResult: RunChecksResult{
				EssDbSchemaVersion: "",
				XidDbSchemaVersion: "",
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
			},
			expectedState: spb.HealthState_HEALTH_STATE_UNHEALTHY,
			expectedSystemInfo: &spb.SystemInfo{
				AppVersion:         "unknown",
				BuildVersion:       "unknown",
				ObservabilityUrl:   "",
				EssDbSchemaVersion: "unknown",
				XidDbSchemaVersion: "unknown",
			},
			expectedComponents: map[string]spb.HealthState{
				"ESS":         spb.HealthState_HEALTH_STATE_UNHEALTHY,
				"XID":         spb.HealthState_HEALTH_STATE_UNHEALTHY,
				"Actuator":    spb.HealthState_HEALTH_STATE_UNHEALTHY,
				"Reconciler":  spb.HealthState_HEALTH_STATE_HEALTHY,
				"Log Monitor": spb.HealthState_HEALTH_STATE_HEALTHY,
				"Spanner ESS": spb.HealthState_HEALTH_STATE_UNHEALTHY,
				"Spanner XID": spb.HealthState_HEALTH_STATE_UNHEALTHY,
				"Redis":       spb.HealthState_HEALTH_STATE_HEALTHY,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checker := &mockHealthChecker{result: tt.mockResult}
			s := newServer(tt.cfg, checker)
			defer s.Close()

			resp, err := s.GetHealthStatus(context.Background(), &spb.GetHealthStatusRequest{})
			if err != nil {
				t.Fatalf("GetHealthStatus failed: %v", err)
			}

			if resp.HealthState != tt.expectedState {
				t.Errorf("Expected overall status %v, got %v", tt.expectedState, resp.HealthState)
			}

			if resp.SystemInfo.AppVersion != tt.expectedSystemInfo.AppVersion {
				t.Errorf("Expected AppVersion %q, got %q", tt.expectedSystemInfo.AppVersion, resp.SystemInfo.AppVersion)
			}
			if resp.SystemInfo.BuildVersion != tt.expectedSystemInfo.BuildVersion {
				t.Errorf("Expected BuildVersion %q, got %q", tt.expectedSystemInfo.BuildVersion, resp.SystemInfo.BuildVersion)
			}
			if resp.SystemInfo.ObservabilityUrl != tt.expectedSystemInfo.ObservabilityUrl {
				t.Errorf("Expected ObservabilityUrl %q, got %q", tt.expectedSystemInfo.ObservabilityUrl, resp.SystemInfo.ObservabilityUrl)
			}
			if resp.SystemInfo.EssDbSchemaVersion != tt.expectedSystemInfo.EssDbSchemaVersion {
				t.Errorf("Expected EssDbSchemaVersion %q, got %q", tt.expectedSystemInfo.EssDbSchemaVersion, resp.SystemInfo.EssDbSchemaVersion)
			}
			if resp.SystemInfo.XidDbSchemaVersion != tt.expectedSystemInfo.XidDbSchemaVersion {
				t.Errorf("Expected XidDbSchemaVersion %q, got %q", tt.expectedSystemInfo.XidDbSchemaVersion, resp.SystemInfo.XidDbSchemaVersion)
			}

			if len(resp.Components) != len(tt.expectedComponents) {
				t.Fatalf("Expected %d components, got %d", len(tt.expectedComponents), len(resp.Components))
			}

			for _, c := range resp.Components {
				expectedState, found := tt.expectedComponents[c.Name]
				if !found {
					t.Errorf("Unexpected component in response: %q", c.Name)
					continue
				}
				if c.HealthState != expectedState {
					t.Errorf("Expected component %q to be %v, got %v (error: %q)", c.Name, expectedState, c.HealthState, c.ErrorMessage)
				}
			}
		})
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

func TestGetHealthStatus_CachingAndRateLimiting(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// Use production-like values (15s cache, 5 requests per 5s rate limit)
		tf := newTestFixture(t, 15*time.Second, 5, 5*time.Second)
		defer tf.close()

		ctx := context.Background()

		// 1. First call - Cache Miss
		resp1 := tf.assertCall(ctx, &spb.GetHealthStatusRequest{}, 1)

		// 2. Second call immediately - Cache Hit
		resp2 := tf.assertCall(ctx, &spb.GetHealthStatusRequest{}, 1)
		if resp1.HealthCheckTime.AsTime() != resp2.HealthCheckTime.AsTime() {
			t.Error("expected cached response to have the same health check time")
		}

		// 3. Sleep past TTL (15s) to expire cache
		time.Sleep(16 * time.Second)

		// 4. Third call - Cache Miss
		tf.assertCall(ctx, &spb.GetHealthStatusRequest{}, 2)

		// 5. Force refresh calls - allowed up to 5 times (limit is 5)
		tf.assertCall(ctx, &spb.GetHealthStatusRequest{ForceRefresh: true}, 3)
		tf.assertCall(ctx, &spb.GetHealthStatusRequest{ForceRefresh: true}, 4)
		tf.assertCall(ctx, &spb.GetHealthStatusRequest{ForceRefresh: true}, 5)
		tf.assertCall(ctx, &spb.GetHealthStatusRequest{ForceRefresh: true}, 6)
		tf.assertCall(ctx, &spb.GetHealthStatusRequest{ForceRefresh: true}, 7)

		// 6. 6th Force refresh immediately - Rate Limited
		tf.assertRateLimited(ctx, &spb.GetHealthStatusRequest{ForceRefresh: true}, 7)

		// 7. Sleep past rate limit window (5s) to reset rate limit
		time.Sleep(6 * time.Second)

		// 8. Force refresh succeeds again
		tf.assertCall(ctx, &spb.GetHealthStatusRequest{ForceRefresh: true}, 8)
	})
}

func TestGetHealthStatus_RateLimitingBypassed(t *testing.T) {
	tf := newTestFixture(t, 15*time.Second, 0, 0)
	defer tf.close()

	ctx := context.Background()

	// 10 force refresh requests immediately should all succeed without rate limit
	for i := 1; i <= 10; i++ {
		tf.assertCall(ctx, &spb.GetHealthStatusRequest{ForceRefresh: true}, i)
	}
}

func TestGetHealthStatus_Singleflight(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tf := newTestFixture(t, 15*time.Second, 5, 5*time.Second)
		defer tf.close()

		// Simulate a slow backend check (1 second)
		tf.checker.delay = 1 * time.Second

		// Launch 10 concurrent requests
		var wg sync.WaitGroup
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, _ = tf.server.GetHealthStatus(context.Background(), &spb.GetHealthStatusRequest{})
			}()
		}
		wg.Wait()

		// Verify only 1 backend call was made (others collapsed via singleflight)
		tf.assertCallCount(1)
	})
}
