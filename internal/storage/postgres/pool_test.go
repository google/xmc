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

package postgres

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"xmanager_cloud/internal/grpcerr"
)

func TestParsePoolConfig(t *testing.T) {
	tests := []struct {
		name         string
		dsn          string
		maxConns     int32
		wantMaxConns int32
		wantErr      bool
		wantCode     codes.Code
	}{
		{
			name:     "empty DSN returns InvalidArgument",
			dsn:      "   ",
			maxConns: 10,
			wantErr:  true,
			wantCode: codes.InvalidArgument,
		},
		{
			name:     "malformed DSN returns InvalidArgument",
			dsn:      "postgres://[::1:5432/db",
			maxConns: 10,
			wantErr:  true,
			wantCode: codes.InvalidArgument,
		},
		{
			name:         "valid DSN with positive maxConns sets MaxConns",
			dsn:          "postgres://user:pass@localhost:5432/xmc?sslmode=disable",
			maxConns:     25,
			wantMaxConns: 25,
		},
		{
			name:     "valid DSN with zero maxConns preserves default MaxConns",
			dsn:      "postgres://user:pass@localhost:5432/xmc?sslmode=disable",
			maxConns: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := parsePoolConfig(tt.dsn, tt.maxConns)
			if tt.wantErr {
				if err == nil {
					t.Fatal("parsePoolConfig() = nil, want error")
				}
				if !errors.Is(err, grpcerr.ErrInvalidArgument) {
					t.Errorf("errors.Is(%v, grpcerr.ErrInvalidArgument) = false, want true", err)
				}
				if got := status.Code(err); got != tt.wantCode {
					t.Errorf("status.Code() = %v, want %v", got, tt.wantCode)
				}
				return
			}
			if err != nil {
				t.Fatalf("parsePoolConfig() unexpected error: %v", err)
			}
			if tt.wantMaxConns > 0 && cfg.MaxConns != tt.wantMaxConns {
				t.Errorf("cfg.MaxConns = %d, want %d", cfg.MaxConns, tt.wantMaxConns)
			}
			if tt.wantMaxConns == 0 && cfg.MaxConns <= 0 {
				t.Errorf("cfg.MaxConns = %d, want positive default", cfg.MaxConns)
			}
		})
	}
}

func TestNewPoolValidationAndPingFailure(t *testing.T) {
	t.Run("empty DSN fails validation", func(t *testing.T) {
		pool, err := NewPool(context.Background(), "", 4)
		if err == nil {
			pool.Close()
			t.Fatal("NewPool() = nil, want error for empty DSN")
		}
		if !errors.Is(err, grpcerr.ErrInvalidArgument) {
			t.Errorf("errors.Is(%v, grpcerr.ErrInvalidArgument) = false, want true", err)
		}
	})

	t.Run("unreachable database fails on Ping and closes pool", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		pool, err := NewPool(ctx, "postgres://user:pass@127.0.0.1:1/xmc?sslmode=disable", 2)
		if err == nil {
			pool.Close()
			t.Fatal("NewPool() = nil, want ping error on canceled context")
		}
		if !errors.Is(err, context.Canceled) {
			t.Errorf("errors.Is(%v, context.Canceled) = false, want true", err)
		}
	})

	t.Run("pool outlives canceled initialization context while ping observes caller context", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("net.Listen() failed: %v", err)
		}
		t.Cleanup(func() { _ = ln.Close() })

		var dials atomic.Int32
		go func() {
			for {
				conn, acceptErr := ln.Accept()
				if acceptErr != nil {
					return
				}
				dials.Add(1)
				_ = conn.Close()
			}
		}()

		initCtx, cancel := context.WithCancel(context.Background())
		dsn := fmt.Sprintf("postgres://user:pass@%s/xmc?sslmode=disable", ln.Addr().String())
		pool, err := newPool(initCtx, dsn, 2, func(_ *pgxpool.Pool, pingCtx context.Context) error {
			if pingCtx.Done() == nil {
				t.Error("ping received non-cancelable context, want caller context")
			}
			return nil
		})
		if err != nil {
			t.Fatalf("newPool() unexpected error: %v", err)
		}
		t.Cleanup(pool.Close)

		cancel()

		pingErr := pool.Ping(context.Background())
		if errors.Is(pingErr, context.Canceled) {
			t.Fatalf("pool.Ping() after initCtx cancel failed with context.Canceled: %v", pingErr)
		}
		if dials.Load() == 0 {
			t.Error("expected pool.Ping() to dial listener after initCtx cancel, got 0 dials")
		}
	})
}
