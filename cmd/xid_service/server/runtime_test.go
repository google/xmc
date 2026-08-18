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
	"errors"
	"testing"

	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	oauth "xmanager_cloud/pkg/auth/oauth2"
	"xmanager_cloud/xid_service/config"
)

func TestRuntimeServer_Close(t *testing.T) {
	t.Parallel()

	t.Run("nil cleanup returns nil", func(t *testing.T) {
		t.Parallel()
		rs := &RuntimeServer{}
		if err := rs.Close(); err != nil {
			t.Errorf("Close() = %v, want nil", err)
		}
	})

	t.Run("nil RuntimeServer returns nil", func(t *testing.T) {
		t.Parallel()
		var rs *RuntimeServer
		if err := rs.Close(); err != nil {
			t.Errorf("Close() = %v, want nil", err)
		}
	})

	t.Run("cleanup called and returns error", func(t *testing.T) {
		t.Parallel()
		expectedErr := errors.New("cleanup error")
		rs := &RuntimeServer{
			cleanup: func() error {
				return expectedErr
			},
		}
		if err := rs.Close(); !errors.Is(err, expectedErr) {
			t.Errorf("Close() = %v, want %v", err, expectedErr)
		}
	})

	t.Run("idempotent close", func(t *testing.T) {
		t.Parallel()
		var callCount int
		expectedErr := errors.New("idempotent cleanup error")
		rs := &RuntimeServer{
			cleanup: func() error {
				callCount++
				return expectedErr
			},
		}
		if err := rs.Close(); !errors.Is(err, expectedErr) {
			t.Errorf("first Close() = %v, want %v", err, expectedErr)
		}
		if err := rs.Close(); !errors.Is(err, expectedErr) {
			t.Errorf("second Close() = %v, want %v", err, expectedErr)
		}
		if callCount != 1 {
			t.Errorf("cleanup callCount = %d, want 1", callCount)
		}
	})

	t.Run("concurrent close", func(t *testing.T) {
		t.Parallel()
		var callCount int
		expectedErr := errors.New("concurrent cleanup error")
		rs := &RuntimeServer{
			cleanup: func() error {
				callCount++
				return expectedErr
			},
		}
		const goroutines = 10
		errChan := make(chan error, goroutines)
		for i := 0; i < goroutines; i++ {
			go func() {
				errChan <- rs.Close()
			}()
		}
		for i := 0; i < goroutines; i++ {
			if err := <-errChan; !errors.Is(err, expectedErr) {
				t.Errorf("concurrent Close() = %v, want %v", err, expectedErr)
			}
		}
		if callCount != 1 {
			t.Errorf("cleanup callCount = %d, want 1", callCount)
		}
	})

	t.Run("multiple cleanup errors joined", func(t *testing.T) {
		t.Parallel()
		err1 := errors.New("cleanup error 1")
		err2 := errors.New("cleanup error 2")
		cleanups := []func() error{
			func() error { return err1 },
			func() error { return err2 },
		}
		cleanup := func() error {
			var errs []error
			for i := len(cleanups) - 1; i >= 0; i-- {
				if err := cleanups[i](); err != nil {
					errs = append(errs, err)
				}
			}
			return errors.Join(errs...)
		}
		rs := &RuntimeServer{cleanup: cleanup}
		err := rs.Close()
		if !errors.Is(err, err1) || !errors.Is(err, err2) {
			t.Errorf("Close() = %v, want joined error containing %v and %v", err, err1, err2)
		}
	})
}

func TestNewRuntimeServer_Validation(t *testing.T) {
	t.Parallel()

	t.Run("nil config", func(t *testing.T) {
		t.Parallel()
		rs, err := NewRuntimeServer(context.Background(), nil, nil)
		if rs != nil || err == nil {
			t.Errorf("NewRuntimeServer(nil, nil) = (%v, %v), want (nil, error)", rs, err)
		}
	})

	t.Run("nil oauth config", func(t *testing.T) {
		t.Parallel()
		rs, err := NewRuntimeServer(context.Background(), &config.Config{}, nil)
		if rs != nil || err == nil {
			t.Errorf("NewRuntimeServer(cfg, nil) = (%v, %v), want (nil, error)", rs, err)
		}
	})

	t.Run("invalid spanner database name returns error", func(t *testing.T) {
		t.Parallel()
		cfg := &config.Config{
			SpannerDatabaseName: "invalid-database-name",
			QuotaProjectID:      "test-proj",
		}
		oauthCfg := &oauth.Config{
			ClientID:  "test-client-id",
			JWTSecret: "test-secret",
		}
		rs, err := NewRuntimeServer(context.Background(), cfg, oauthCfg)
		if rs != nil || err == nil {
			t.Errorf("NewRuntimeServer(invalidDbName) = (%v, %v), want (nil, error)", rs, err)
		}
	})
}

func TestRuntimeServer_HealthCheckers(t *testing.T) {
	t.Parallel()

	t.Run("nil RuntimeServer returns nil checkers", func(t *testing.T) {
		t.Parallel()
		var rs *RuntimeServer
		sp := rs.HealthCheckers()
		if sp != nil {
			t.Errorf("HealthCheckers() on nil RuntimeServer = %v, want nil", sp)
		}
	})

	t.Run("nil spanner client returns nil checkers", func(t *testing.T) {
		t.Parallel()
		rs := &RuntimeServer{}
		sp := rs.HealthCheckers()
		if sp != nil {
			t.Errorf("HealthCheckers() on RuntimeServer with nil spannerClient = %v, want nil", sp)
		}
	})

	t.Run("non-nil spanner client returns checkers", func(t *testing.T) {
		t.Parallel()
		rs := &RuntimeServer{
			spannerClient: &spanner.Client{},
		}
		sp := rs.HealthCheckers()
		if sp == nil {
			t.Error("HealthCheckers() on RuntimeServer with non-nil spannerClient returned nil, want non-nil")
		}
	})
}
