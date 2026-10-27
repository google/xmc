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

	"google.golang.org/grpc"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

// fakeReadinessChecker returns err from CheckReady and records whether the context had a deadline.
type fakeReadinessChecker struct {
	err         error
	hadDeadline bool
}

func (f *fakeReadinessChecker) CheckReady(ctx context.Context) error {
	_, f.hadDeadline = ctx.Deadline()
	return f.err
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

	t.Run("SERVING when database ready", func(t *testing.T) {
		db := &fakeReadinessChecker{}
		healthServer := RegisterHealthServer(grpc.NewServer())

		updateHealthStatus(ctx, db, healthServer)

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

	t.Run("NOT_SERVING when database not ready", func(t *testing.T) {
		db := &fakeReadinessChecker{
			err: errors.New("database down"),
		}
		healthServer := RegisterHealthServer(grpc.NewServer())

		updateHealthStatus(ctx, db, healthServer)

		resp, err := healthServer.Check(ctx, &healthpb.HealthCheckRequest{Service: xidServiceHealthCheckName})
		if err != nil {
			t.Fatalf("healthServer.Check(%q) failed: %v", xidServiceHealthCheckName, err)
		}
		if resp.Status != healthpb.HealthCheckResponse_NOT_SERVING {
			t.Errorf("healthServer.Check(%q).Status = %v, want %v", xidServiceHealthCheckName, resp.Status, healthpb.HealthCheckResponse_NOT_SERVING)
		}

		// Empty service name should STILL be SERVING even if the database is down
		respEmpty, err := healthServer.Check(ctx, &healthpb.HealthCheckRequest{Service: ""})
		if err != nil {
			t.Fatalf("healthServer.Check(%q) failed: %v", "", err)
		}
		if respEmpty.Status != healthpb.HealthCheckResponse_SERVING {
			t.Errorf("healthServer.Check(%q).Status = %v, want %v", "", respEmpty.Status, healthpb.HealthCheckResponse_SERVING)
		}
	})

	t.Run("NOT_SERVING when readiness checker is nil", func(t *testing.T) {
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
}

func TestCheckDatabase(t *testing.T) {
	t.Parallel()

	t.Run("success within a deadline", func(t *testing.T) {
		t.Parallel()
		db := &fakeReadinessChecker{}
		if err := checkDatabase(context.Background(), db); err != nil {
			t.Errorf("checkDatabase() error = %v, want nil", err)
		}
		if !db.hadDeadline {
			t.Error("checkDatabase() called CheckReady without a deadline, want databaseTimeout")
		}
	})

	t.Run("error", func(t *testing.T) {
		t.Parallel()
		expectedErr := errors.New("database connection failure")
		db := &fakeReadinessChecker{
			err: expectedErr,
		}
		if err := checkDatabase(context.Background(), db); !errors.Is(err, expectedErr) {
			t.Errorf("checkDatabase() = %v, want %v", err, expectedErr)
		}
	})
}

func TestStartHealthMonitor_Cancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	db := &fakeReadinessChecker{}
	healthServer := RegisterHealthServer(grpc.NewServer())

	done := make(chan struct{})
	go func() {
		StartHealthMonitor(ctx, db, healthServer)
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
