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
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	kubernetesfake "golang/k8s_io/client_go/v/v0_23/kubernetes/fake/fake"
	"golang/k8s_io/client_go/v/v0_23/kubernetes/kubernetes"
	"xmanager_cloud/task_queue_redis/redisclient"
)

type mockK8sHealthClient struct {
	clientset kubernetes.Interface
}

func (m *mockK8sHealthClient) Clientset() kubernetes.Interface {
	return m.clientset
}

type mockSpannerHealthClient struct {
	pingErr error
}

func (m *mockSpannerHealthClient) Single() any {
	return nil
}

type mockRedisConsumer struct {
	redisclient.Consumer
	pingErr error
}

func (m *mockRedisConsumer) Ping(ctx context.Context) error {
	return m.pingErr
}

func (m *mockRedisConsumer) Close(ctx context.Context) error {
	return nil
}

func TestProdServer_Close(t *testing.T) {
	t.Run("nil_server", func(t *testing.T) {
		var p *ProdServer
		if err := p.Close(); err != nil {
			t.Errorf("Close() on nil ProdServer returned error: %v, want nil", err)
		}
	})

	t.Run("nil_cleanup", func(t *testing.T) {
		p := &ProdServer{}
		if err := p.Close(); err != nil {
			t.Errorf("Close() on ProdServer with nil cleanup returned error: %v, want nil", err)
		}
	})

	t.Run("cleanup_success", func(t *testing.T) {
		called := false
		p := &ProdServer{
			cleanup: func() error {
				called = true
				return nil
			},
		}
		if err := p.Close(); err != nil {
			t.Errorf("Close() returned error: %v, want nil", err)
		}
		if !called {
			t.Errorf("Close() did not call cleanup function")
		}
	})

	t.Run("cleanup_error", func(t *testing.T) {
		p := &ProdServer{
			cleanup: func() error {
				return errors.New("cleanup failed")
			},
		}
		if err := p.Close(); err == nil || !strings.Contains(err.Error(), "cleanup failed") {
			t.Errorf("Close() error = %v, want 'cleanup failed'", err)
		}
	})
}

func TestProdServer_HealthCheckers(t *testing.T) {
	t.Run("nil_server", func(t *testing.T) {
		var p *ProdServer
		k8s, sp, rd := p.HealthCheckers()
		if k8s != nil || sp != nil || rd != nil {
			t.Errorf("HealthCheckers() on nil ProdServer returned non-nil (%v, %v, %v), want all nil", k8s, sp, rd)
		}
	})

	t.Run("empty_server", func(t *testing.T) {
		p := &ProdServer{}
		k8s, sp, rd := p.HealthCheckers()
		if k8s != nil || sp != nil || rd != nil {
			t.Errorf("HealthCheckers() on empty ProdServer returned non-nil (%v, %v, %v), want all nil", k8s, sp, rd)
		}
	})
}

func TestNewProd_Validation(t *testing.T) {
	ctx := context.Background()

	t.Run("nil_config", func(t *testing.T) {
		server, err := NewProd(ctx, nil, "test-project")
		if err == nil {
			t.Fatalf("NewProd(nil cfg) expected error, got nil (server: %v)", server)
		}
		if !strings.Contains(err.Error(), "config cannot be nil") {
			t.Errorf("NewProd error mismatch: got %q, want 'config cannot be nil'", err.Error())
		}
	})

	t.Run("empty_project_id", func(t *testing.T) {
		cfg := Config{}
		server, err := NewProd(ctx, &cfg, "")
		if err == nil {
			t.Fatalf("NewProd(empty projectID) expected error, got nil (server: %v)", server)
		}
		if !strings.Contains(err.Error(), "project ID cannot be empty") {
			t.Errorf("NewProd error mismatch: got %q, want 'project ID cannot be empty'", err.Error())
		}
	})
}

func TestWorkerIDHelpers(t *testing.T) {
	ctx := context.Background()
	origHostname := osHostname
	t.Cleanup(func() {
		osHostname = origHostname
	})

	t.Run("hostname_success", func(t *testing.T) {
		osHostname = func() (string, error) {
			return "actuator-pod-123", nil
		}
		workerID := actuatorWorkerID(ctx)
		if !strings.HasSuffix(workerID, "actuator-pod-123") {
			t.Errorf("actuatorWorkerID() = %q, want suffix 'actuator-pod-123'", workerID)
		}
		cleanupID := actuatorCleanupWorkerID(ctx)
		if !strings.HasSuffix(cleanupID, "actuator-pod-123") {
			t.Errorf("actuatorCleanupWorkerID() = %q, want suffix 'actuator-pod-123'", cleanupID)
		}
	})

	t.Run("hostname_error_fallback", func(t *testing.T) {
		osHostname = func() (string, error) {
			return "", errors.New("hostname failed")
		}
		workerID := actuatorWorkerID(ctx)
		if !strings.HasSuffix(workerID, "unknown") {
			t.Errorf("actuatorWorkerID() = %q, want suffix 'unknown'", workerID)
		}
		cleanupID := actuatorCleanupWorkerID(ctx)
		if !strings.HasSuffix(cleanupID, "unknown") {
			t.Errorf("actuatorCleanupWorkerID() = %q, want suffix 'unknown'", cleanupID)
		}
	})
}

func TestActiveHealthCheck(t *testing.T) {
	fakeClientset := kubernetesfake.NewSimpleClientset()
	validK8s := &mockK8sHealthClient{clientset: fakeClientset}
	validRedis := &mockRedisConsumer{pingErr: nil}

	testCases := []struct {
		name       string
		k8s        k8sHealthClient
		spClient   *mockSpannerHealthClient
		redis      redisclient.Consumer
		wantStatus int
	}{
		{
			name:       "all_healthy_without_spanner",
			k8s:        validK8s,
			spClient:   nil,
			redis:      validRedis,
			wantStatus: http.StatusOK,
		},
		{
			name:       "nil_k8s_client",
			k8s:        nil,
			spClient:   nil,
			redis:      validRedis,
			wantStatus: http.StatusInternalServerError,
		},
		{
			name:       "k8s_clientset_nil",
			k8s:        &mockK8sHealthClient{clientset: nil},
			spClient:   nil,
			redis:      validRedis,
			wantStatus: http.StatusInternalServerError,
		},
		{
			name:       "nil_redis_client",
			k8s:        validK8s,
			spClient:   nil,
			redis:      nil,
			wantStatus: http.StatusInternalServerError,
		},
		{
			name:       "redis_ping_error",
			k8s:        validK8s,
			spClient:   nil,
			redis:      &mockRedisConsumer{pingErr: fmt.Errorf("connection refused")},
			wantStatus: http.StatusInternalServerError,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			handler := activeHealthCheck(tc.k8s, nil, tc.redis)
			req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Errorf("activeHealthCheck status = %d, want %d, body: %s", rec.Code, tc.wantStatus, rec.Body.String())
			}
		})
	}
}
