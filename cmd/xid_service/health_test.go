// Copyright 2025 Google LLC
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
	"errors"
	"testing"
	"time"

	"golang/grpc/grpc"
	healthpb "grpc_proto/health_go_proto"
	"xmanager_cloud/storage/spanner/migrations/version"
)

type mockSpannerHealthClient struct {
	version int64
	dirty   bool
	err     error
}

func (m *mockSpannerHealthClient) QuerySchemaVersion(ctx context.Context) (int64, bool, error) {
	return m.version, m.dirty, m.err
}

func TestRegisterHealthServer(t *testing.T) {
	t.Parallel()

	s := grpc.NewServer()
	healthServer := RegisterHealthServer(s)
	if healthServer == nil {
		t.Fatal("RegisterHealthServer returned nil")
	}

	ctx := context.Background()
	resp, err := healthServer.Check(ctx, &healthpb.HealthCheckRequest{Service: ""})
	if err != nil {
		t.Fatalf("healthServer.Check(\"\") failed: %v", err)
	}
	if resp.Status != healthpb.HealthCheckResponse_SERVING {
		t.Errorf("status for \"\" = %v, want %v", resp.Status, healthpb.HealthCheckResponse_SERVING)
	}
}

func TestUpdateHealthStatus(t *testing.T) {
	ctx := context.Background()

	t.Run("SERVING when spanner ok", func(t *testing.T) {
		mockClient := &mockSpannerHealthClient{
			version: version.XidExpectedSchemaVersion(),
			dirty:   false,
		}
		healthServer := RegisterHealthServer(grpc.NewServer())

		updateHealthStatus(ctx, mockClient, healthServer)

		resp, err := healthServer.Check(ctx, &healthpb.HealthCheckRequest{Service: xidServiceHealthCheckName})
		if err != nil {
			t.Fatalf("healthServer.Check(%q) failed: %v", xidServiceHealthCheckName, err)
		}
		if resp.Status != healthpb.HealthCheckResponse_SERVING {
			t.Errorf("healthServer.Check(%q).Status = %v, want %v", xidServiceHealthCheckName, resp.Status, healthpb.HealthCheckResponse_SERVING)
		}

		// Also check empty service name remains SERVING
		respEmpty, err := healthServer.Check(ctx, &healthpb.HealthCheckRequest{Service: ""})
		if err != nil {
			t.Fatalf("healthServer.Check(%q) failed: %v", "", err)
		}
		if respEmpty.Status != healthpb.HealthCheckResponse_SERVING {
			t.Errorf("healthServer.Check(%q).Status = %v, want %v", "", respEmpty.Status, healthpb.HealthCheckResponse_SERVING)
		}
	})

	t.Run("NOT_SERVING when spanner down", func(t *testing.T) {
		mockClient := &mockSpannerHealthClient{
			err: errors.New("spanner down"),
		}
		healthServer := RegisterHealthServer(grpc.NewServer())

		updateHealthStatus(ctx, mockClient, healthServer)

		resp, err := healthServer.Check(ctx, &healthpb.HealthCheckRequest{Service: xidServiceHealthCheckName})
		if err != nil {
			t.Fatalf("healthServer.Check(%q) failed: %v", xidServiceHealthCheckName, err)
		}
		if resp.Status != healthpb.HealthCheckResponse_NOT_SERVING {
			t.Errorf("healthServer.Check(%q).Status = %v, want %v", xidServiceHealthCheckName, resp.Status, healthpb.HealthCheckResponse_NOT_SERVING)
		}

		// Empty service name should STILL be SERVING even if Spanner is down
		respEmpty, err := healthServer.Check(ctx, &healthpb.HealthCheckRequest{Service: ""})
		if err != nil {
			t.Fatalf("healthServer.Check(%q) failed: %v", "", err)
		}
		if respEmpty.Status != healthpb.HealthCheckResponse_SERVING {
			t.Errorf("healthServer.Check(%q).Status = %v, want %v", "", respEmpty.Status, healthpb.HealthCheckResponse_SERVING)
		}
	})

	t.Run("NOT_SERVING when spanner client is nil", func(t *testing.T) {
		healthServer := RegisterHealthServer(grpc.NewServer())

		updateHealthStatus(ctx, nil, healthServer)

		resp, err := healthServer.Check(ctx, &healthpb.HealthCheckRequest{Service: xidServiceHealthCheckName})
		if err != nil {
			t.Fatalf("healthServer.Check(%q) failed: %v", xidServiceHealthCheckName, err)
		}
		if resp.Status != healthpb.HealthCheckResponse_NOT_SERVING {
			t.Errorf("healthServer.Check(%q).Status = %v, want %v", xidServiceHealthCheckName, resp.Status, healthpb.HealthCheckResponse_NOT_SERVING)
		}
	})

	t.Run("NOT_SERVING when spanner dirty schema", func(t *testing.T) {
		mockClient := &mockSpannerHealthClient{
			version: version.XidExpectedSchemaVersion(),
			dirty:   true,
		}
		healthServer := RegisterHealthServer(grpc.NewServer())

		updateHealthStatus(ctx, mockClient, healthServer)

		resp, err := healthServer.Check(ctx, &healthpb.HealthCheckRequest{Service: xidServiceHealthCheckName})
		if err != nil {
			t.Fatalf("healthServer.Check(%q) failed: %v", xidServiceHealthCheckName, err)
		}
		if resp.Status != healthpb.HealthCheckResponse_NOT_SERVING {
			t.Errorf("healthServer.Check(%q).Status = %v, want %v", xidServiceHealthCheckName, resp.Status, healthpb.HealthCheckResponse_NOT_SERVING)
		}
	})

	t.Run("NOT_SERVING when spanner version mismatch", func(t *testing.T) {
		mockClient := &mockSpannerHealthClient{
			version: version.XidExpectedSchemaVersion() - 1,
			dirty:   false,
		}
		healthServer := RegisterHealthServer(grpc.NewServer())

		updateHealthStatus(ctx, mockClient, healthServer)

		resp, err := healthServer.Check(ctx, &healthpb.HealthCheckRequest{Service: xidServiceHealthCheckName})
		if err != nil {
			t.Fatalf("healthServer.Check(%q) failed: %v", xidServiceHealthCheckName, err)
		}
		if resp.Status != healthpb.HealthCheckResponse_NOT_SERVING {
			t.Errorf("healthServer.Check(%q).Status = %v, want %v", xidServiceHealthCheckName, resp.Status, healthpb.HealthCheckResponse_NOT_SERVING)
		}
	})
}

func TestCheckSpanner(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		mockClient := &mockSpannerHealthClient{
			version: version.XidExpectedSchemaVersion(),
			dirty:   false,
		}
		if err := checkSpanner(context.Background(), mockClient); err != nil {
			t.Errorf("checkSpanner() error = %v, want nil", err)
		}
	})

	t.Run("error", func(t *testing.T) {
		t.Parallel()
		expectedErr := errors.New("spanner connection failure")
		mockClient := &mockSpannerHealthClient{
			err: expectedErr,
		}
		if err := checkSpanner(context.Background(), mockClient); err == nil {
			t.Errorf("checkSpanner() = nil, want error")
		}
	})
}

func TestStartHealthMonitor_Cancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	mockClient := &mockSpannerHealthClient{
		version: version.XidExpectedSchemaVersion(),
		dirty:   false,
	}
	healthServer := RegisterHealthServer(grpc.NewServer())

	done := make(chan struct{})
	go func() {
		StartHealthMonitor(ctx, mockClient, healthServer)
		close(done)
	}()

	// Cancel context to stop monitor
	cancel()

	select {
	case <-done:
		// Success: StartHealthMonitor terminated cleanly
	case <-time.After(2 * time.Second):
		t.Fatal("StartHealthMonitor did not terminate upon context cancellation")
	}
}
