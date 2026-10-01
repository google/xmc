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

	"xmanager_cloud/cmd/actuator/pausescheduling"
	"xmanager_cloud/internal/featureflags"
	"xmanager_cloud/kubernetes/actuator/k8s_client/k8sclient"
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

	t.Run("cleanup_multiple_errors_joined", func(t *testing.T) {
		err1 := errors.New("spanner close failed")
		err2 := errors.New("redis close failed")
		p := &ProdServer{
			cleanup: func() error {
				return errors.Join(err1, err2)
			},
		}
		err := p.Close()
		if err == nil {
			t.Fatalf("Close() expected joined error, got nil")
		}
		if !errors.Is(err, err1) || !errors.Is(err, err2) {
			t.Errorf("Close() error = %v, want joined err1 and err2", err)
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

	t.Run("populated_server", func(t *testing.T) {
		fakeRedis := &mockRedisConsumer{}
		p := &ProdServer{
			K8sClient:   &k8sclient.Client{},
			RedisClient: fakeRedis,
		}
		k8s, sp, rd := p.HealthCheckers()
		if k8s == nil {
			t.Errorf("HealthCheckers() k8s client = nil, want non-nil")
		}
		if sp != nil {
			t.Errorf("HealthCheckers() spanner client = %v, want nil", sp)
		}
		if rd != fakeRedis {
			t.Errorf("HealthCheckers() redis client = %v, want %v", rd, fakeRedis)
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

	t.Run("invalid_spanner_database_name", func(t *testing.T) {
		cfg := Config{
			RuntimeConfig: k8sclient.ActuatorRuntimeConfig{
				SpannerDatabaseName: "invalid-db-format",
			},
		}
		server, err := NewProd(ctx, &cfg, "test-project")
		if err == nil {
			t.Fatalf("NewProd(invalid spanner db) expected error, got nil (server: %v)", server)
		}
		if !strings.Contains(err.Error(), "failed to create spanner client") {
			t.Errorf("NewProd error mismatch: got %q, want 'failed to create spanner client'", err.Error())
		}
	})

	t.Run("k8s_incluster_error_with_spanner_rollback", func(t *testing.T) {
		// Outside GKE cluster, k8sclient.New fails to find in-cluster config,
		// verifying the LIFO rollback stack cleanly closes the Spanner client on error.
		cfg := Config{
			RuntimeConfig: k8sclient.ActuatorRuntimeConfig{
				SpannerDatabaseName: "projects/test-project/instances/test-instance/databases/test-db",
			},
		}
		server, err := NewProd(ctx, &cfg, "test-project")
		if err == nil {
			t.Fatalf("NewProd outside GKE expected k8s config error, got nil (server: %v)", server)
		}
		if !strings.Contains(err.Error(), "failed to create kubernetes client") {
			t.Errorf("NewProd error mismatch: got %q, want 'failed to create kubernetes client'", err.Error())
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

func TestInitJWKSTokenValidator(t *testing.T) {
	t.Run("empty_jwks_url", func(t *testing.T) {
		cfg := &Config{
			JWKSURL: "",
		}
		validator := initJWKSTokenValidator(cfg)
		if validator != nil {
			t.Errorf("initJWKSTokenValidator() = %v, want nil", validator)
		}
	})

	t.Run("populated_jwks_url", func(t *testing.T) {
		t.Setenv("CLIENT_ID", "env-client-id")
		t.Setenv("OIDC_CLIENT_ID", "env-oidc-client-id")
		t.Setenv("OIDC_USERNAME", "oidc-user@example.com")
		t.Setenv("AUTH_USERNAME", "auth-user@example.com")

		cfg := &Config{
			JWKSURL:                "http://localhost:8080/jwks",
			OIDCIssuerURL:          "http://localhost:8080",
			Audience:               "config-audience",
			XManagerServiceAccount: "config-sa@example.com",
		}
		validator := initJWKSTokenValidator(cfg)
		if validator == nil {
			t.Fatalf("initJWKSTokenValidator() returned nil, want non-nil validator")
		}
	})
}

func TestInitPauseListener(t *testing.T) {
	ctx := context.Background()

	t.Run("feature_flag_disabled", func(t *testing.T) {
		cfg := &Config{
			FeatureFlags: featureflags.Flags{
				PauseSchedulingEnabled: false,
			},
		}
		listener, err := initPauseListener(ctx, cfg, pausescheduling.NewCache(), nil)
		if err != nil {
			t.Errorf("initPauseListener() error = %v, want nil", err)
		}
		if listener != nil {
			t.Errorf("initPauseListener() listener = %v, want nil", listener)
		}
	})

	t.Run("missing_redis_addr", func(t *testing.T) {
		cfg := &Config{
			FeatureFlags: featureflags.Flags{
				PauseSchedulingEnabled: true,
			},
			RedisConfig: RedisConfig{
				RedisAddr: "",
			},
		}
		listener, err := initPauseListener(ctx, cfg, pausescheduling.NewCache(), nil)
		if err == nil {
			t.Fatalf("initPauseListener() expected error for empty redis address, got nil (listener: %v)", listener)
		}
		if !strings.Contains(err.Error(), "redis address must be provided") {
			t.Errorf("initPauseListener() error = %q, want 'redis address must be provided'", err.Error())
		}
	})

	t.Run("missing_storage_client", func(t *testing.T) {
		cfg := &Config{
			FeatureFlags: featureflags.Flags{
				PauseSchedulingEnabled: true,
			},
			RedisConfig: RedisConfig{
				RedisAddr: "localhost:6379",
			},
		}
		listener, err := initPauseListener(ctx, cfg, pausescheduling.NewCache(), nil)
		if err == nil {
			t.Fatalf("initPauseListener() expected error for nil storage client, got nil (listener: %v)", listener)
		}
		if !strings.Contains(err.Error(), "without spanner storage client") {
			t.Errorf("initPauseListener() error = %q, want 'without spanner storage client'", err.Error())
		}
	})
}
