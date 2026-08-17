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
	"strings"
	"testing"
)

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
		if !strings.Contains(err.Error(), "project ID is required") {
			t.Errorf("NewProd error mismatch: got %q, want 'project ID is required'", err.Error())
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
