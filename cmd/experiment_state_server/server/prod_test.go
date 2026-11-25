// Copyright 2025 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package server

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"xmanager_cloud/cmd/experiment_state_server/config"
	"xmanager_cloud/internal/storage"
)

func TestProdServer_Close(t *testing.T) {
	t.Parallel()

	t.Run("nil cleanup returns nil", func(t *testing.T) {
		t.Parallel()
		ps := &ProdServer{}
		if err := ps.Close(); err != nil {
			t.Errorf("Close() = %v, want nil", err)
		}
	})

	t.Run("cleanup called and returns error", func(t *testing.T) {
		t.Parallel()
		expectedErr := errors.New("cleanup error")
		ps := &ProdServer{
			cleanup: func() error {
				return expectedErr
			},
		}
		if err := ps.Close(); !errors.Is(err, expectedErr) {
			t.Errorf("Close() = %v, want %v", err, expectedErr)
		}
	})

	t.Run("bgTaskWG waits before cleanup", func(t *testing.T) {
		t.Parallel()
		var wg sync.WaitGroup
		wg.Add(1)
		var order []string
		go func() {
			time.Sleep(20 * time.Millisecond)
			order = append(order, "taskDone")
			wg.Done()
		}()
		ps := &ProdServer{
			bgTaskWG: &wg,
			cleanup: func() error {
				order = append(order, "cleanupDone")
				return nil
			},
		}
		if err := ps.Close(); err != nil {
			t.Errorf("Close() = %v, want nil", err)
		}
		if len(order) != 2 || order[0] != "taskDone" || order[1] != "cleanupDone" {
			t.Errorf("execution order = %v, want [taskDone cleanupDone]", order)
		}
	})
}

func TestInitDashboardClient_EmptyAddr(t *testing.T) {
	t.Parallel()
	client, conn, err := initDashboardClient(t.Context(), &config.Config{DashboardServiceAddr: ""})
	if err != nil {
		t.Errorf("initDashboardClient with empty addr = %v, want nil error", err)
	}
	if client != nil || conn != nil {
		t.Errorf("initDashboardClient with empty addr = (%v, %v), want (nil, nil)", client, conn)
	}
}

func TestInitObservers_EmptyAddr(t *testing.T) {
	t.Parallel()
	var wg sync.WaitGroup
	observers, cleanup, err := initObservers(t.Context(), &config.Config{DashboardServiceAddr: ""}, nil, &wg)
	if err != nil {
		t.Errorf("initObservers with empty addr = %v, want nil error", err)
	}
	if len(observers) != 0 {
		t.Errorf("len(observers) = %d, want 0", len(observers))
	}
	if cleanup == nil {
		t.Fatal("cleanup func is nil")
	}
	if err := cleanup(); err != nil {
		t.Errorf("cleanup() = %v, want nil", err)
	}
}

func TestInitStudyReconcilePublisher(t *testing.T) {
	t.Parallel()
	publisher, cleanup := initStudyReconcilePublisher(&config.Config{RedisAddr: "redis-service:6379"})

	if publisher.StreamName != "xmc-study-reconcile-stream" {
		t.Errorf("publisher stream = %q, want %q", publisher.StreamName, "xmc-study-reconcile-stream")
	}
	if err := cleanup(); err != nil {
		t.Errorf("cleanup() = %v, want nil", err)
	}
	if err := cleanup(); err == nil {
		t.Error("second cleanup() = nil, want the error of closing a closed publisher")
	}
}

// writeSigningKeyFile writes a PKCS#8 PEM RSA private key to a temporary file and returns its path.
func writeSigningKeyFile(t *testing.T) string {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey() error = %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatalf("x509.MarshalPKCS8PrivateKey() error = %v", err)
	}
	path := filepath.Join(t.TempDir(), "signing-key.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0600); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}
	return path
}

// TestInitTaskQueueClient_HungRedisBoundedByContext uses the production task queue settings
// against a Redis server that never replies: a call fails at its context deadline instead of
// go-redis's fixed 3s read timeout.
func TestInitTaskQueueClient_HungRedisBoundedByContext(t *testing.T) {
	cfg := &config.Config{SigningKeyFile: writeSigningKeyFile(t), RedisAddr: startSilentRedis(t)}
	client, err := initTaskQueueClient(t.Context(), cfg, nil)
	if err != nil {
		t.Fatalf("initTaskQueueClient() error = %v", err)
	}
	defer client.Close()
	const deadline = 200 * time.Millisecond
	ctx, cancel := context.WithTimeout(t.Context(), deadline)
	defer cancel()

	start := time.Now()
	err = client.PingRedis(ctx)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("PingRedis() with a hung Redis = nil, want error")
	}
	if limit := deadline + time.Second; elapsed > limit {
		t.Errorf("PingRedis() with a hung Redis took %v, want at most %v", elapsed, limit)
	}
}

func TestNewProd_Validation(t *testing.T) {
	t.Parallel()

	t.Run("nil config", func(t *testing.T) {
		t.Parallel()
		ps, err := NewProd(t.Context(), nil, nil, nil)
		if ps != nil || err == nil {
			t.Errorf("NewProd(nil, nil) = (%v, %v), want (nil, error)", ps, err)
		}
	})

	t.Run("nil oauth config", func(t *testing.T) {
		t.Parallel()
		ps, err := NewProd(t.Context(), &config.Config{}, nil, nil)
		if ps != nil || err == nil {
			t.Errorf("NewProd(cfg, nil) = (%v, %v), want (nil, error)", ps, err)
		}
	})
}

func TestStartBackgroundWorkers_StartsExpiredRowSweeper(t *testing.T) {
	t.Parallel()
	cleanups, err := startBackgroundWorkers(t.Context(), &config.Config{}, nil, &storage.MockStorage{})
	if err != nil {
		t.Fatalf("startBackgroundWorkers() unexpected error: %v", err)
	}
	if len(cleanups) != 1 {
		t.Fatalf("len(cleanups) = %d, want 1 (the expired row sweeper)", len(cleanups))
	}
	if err := runCleanups(cleanups); err != nil {
		t.Errorf("runCleanups() = %v, want nil", err)
	}
}

func TestProdServer_HealthCheckers(t *testing.T) {
	t.Parallel()

	t.Run("nil ProdServer returns nil checkers", func(t *testing.T) {
		t.Parallel()
		var ps *ProdServer
		sp, xid, tq := ps.HealthCheckers()
		if sp != nil || xid != nil || tq != nil {
			t.Errorf("HealthCheckers() on nil ProdServer = (%v, %v, %v), want all nil", sp, xid, tq)
		}
		if ps.Storage() != nil {
			t.Errorf("Storage() on nil ProdServer = %v, want nil", ps.Storage())
		}
	})
}
