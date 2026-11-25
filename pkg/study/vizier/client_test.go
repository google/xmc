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
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/test/bufconn"
)

func dialBufconn(t *testing.T, register func(*grpc.Server)) (*Client, func()) {
	t.Helper()
	lis := bufconn.Listen(1024 * 1024)
	srv := grpc.NewServer()
	if register != nil {
		register(srv)
	}
	go func() { _ = srv.Serve(lis) }()
	client, err := Dial("127.0.0.1:7077", grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
		return lis.DialContext(ctx)
	}))
	if err != nil {
		srv.Stop()
		_ = lis.Close()
		t.Fatalf("Dial() error = %v", err)
	}
	t.Cleanup(func() { _ = client.Close(); srv.Stop(); _ = lis.Close() })
	return client, func() { srv.Stop(); _ = lis.Close() }
}

func TestClient_Ping(t *testing.T) {
	t.Parallel()

	t.Run("SERVING returns nil", func(t *testing.T) {
		t.Parallel()
		c, _ := dialBufconn(t, func(s *grpc.Server) {
			hs := health.NewServer()
			hs.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
			healthpb.RegisterHealthServer(s, hs)
		})
		if err := c.Ping(context.Background()); err != nil {
			t.Errorf("Ping() = %v, want nil", err)
		}
	})

	t.Run("Unimplemented returns nil for OSS Vizier", func(t *testing.T) {
		t.Parallel()
		c, _ := dialBufconn(t, nil)
		if err := c.Ping(context.Background()); err != nil {
			t.Errorf("Ping() on server without grpc.health.v1 = %v, want nil", err)
		}
	})

	t.Run("NOT_SERVING returns error", func(t *testing.T) {
		t.Parallel()
		c, _ := dialBufconn(t, func(s *grpc.Server) {
			hs := health.NewServer()
			hs.SetServingStatus("", healthpb.HealthCheckResponse_NOT_SERVING)
			healthpb.RegisterHealthServer(s, hs)
		})
		if err := c.Ping(context.Background()); err == nil {
			t.Error("Ping() = nil, want error for NOT_SERVING")
		}
	})

	t.Run("stopped server fails fast despite WaitForReady default", func(t *testing.T) {
		t.Parallel()
		c, stop := dialBufconn(t, nil)
		stop()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		start := time.Now()
		if err := c.Ping(ctx); err == nil {
			t.Fatal("Ping() on stopped server = nil, want error")
		}
		if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
			t.Errorf("Ping() took %v, want fast fail (<500ms) with WaitForReady(false)", elapsed)
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
	if _, err := Dial("https://user:secret@vizier:7077"); err == nil {
		t.Error("Dial(invalid) = nil, want error")
	}
}
