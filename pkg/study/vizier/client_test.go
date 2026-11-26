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

package vizier

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

func dialBufconn(t *testing.T, register func(*grpc.Server)) (*Client, func()) {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	if register != nil {
		register(srv)
	}
	go func() { _ = srv.Serve(lis) }()
	c, err := dial("127.0.0.1:7077", grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }))
	if err != nil {
		srv.Stop()
		_ = lis.Close()
		t.Fatalf("dial() = %v", err)
	}
	stop := func() { srv.Stop(); _ = lis.Close() }
	t.Cleanup(func() { _ = c.Close(); stop() })
	return c, stop
}

func TestClient_Ping(t *testing.T) {
	t.Parallel()
	setHealth := func(st healthpb.HealthCheckResponse_ServingStatus) func(*grpc.Server) {
		return func(s *grpc.Server) {
			hs := health.NewServer()
			hs.SetServingStatus("", st)
			healthpb.RegisterHealthServer(s, hs)
		}
	}
	for _, tc := range []struct {
		name    string
		reg     func(*grpc.Server)
		wantErr bool
	}{
		{"SERVING returns nil", setHealth(healthpb.HealthCheckResponse_SERVING), false},
		{"Unimplemented returns nil for OSS Vizier", nil, false},
		{"NOT_SERVING returns error", setHealth(healthpb.HealthCheckResponse_NOT_SERVING), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c, _ := dialBufconn(t, tc.reg)
			if err := c.Ping(context.Background()); (err != nil) != tc.wantErr {
				t.Errorf("Ping() = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
	t.Run("stopped server returns Unavailable", func(t *testing.T) {
		t.Parallel()
		c, stop := dialBufconn(t, nil)
		if err := c.Ping(context.Background()); err != nil {
			t.Fatalf("pre-stop Ping() = %v, want nil", err)
		}
		stop()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		start := time.Now()
		err := c.Ping(ctx)
		if status.Code(err) != codes.Unavailable {
			t.Fatalf("Ping() on stopped server code = %v (err=%v), want %v", status.Code(err), err, codes.Unavailable)
		}
		if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
			t.Errorf("Ping() took %v, want fast fail (<500ms)", elapsed)
		}
	})
	t.Run("nil client", func(t *testing.T) {
		t.Parallel()
		var nilClient *Client
		if err := nilClient.Ping(context.Background()); err == nil {
			t.Error("(*Client)(nil).Ping() = nil, want error")
		}
		if err := nilClient.Close(); err != nil {
			t.Errorf("(*Client)(nil).Close() = %v, want nil", err)
		}
	})
}

func TestDial_RejectsInvalidTarget(t *testing.T) {
	t.Parallel()
	if _, err := Dial("https://user:secret@vizier:7077"); err == nil || strings.Contains(err.Error(), "secret") {
		t.Errorf("Dial(invalid) = %v, want non-nil error without secret", err)
	}
}
