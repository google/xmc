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
	"net/http"
	"net/http/httptest"
	"testing"

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

func TestActiveHealthCheck(t *testing.T) {
	t.Run("serving when all ok", func(t *testing.T) {
		mockClient := &mockSpannerHealthClient{
			version: version.EssExpectedSchemaVersion(),
			dirty:   false,
		}
		listener := &Listener{}
		listener.isRunning.Store(true)

		handler := activeHealthCheck(mockClient, listener)
		req := httptest.NewRequest("GET", "/healthz", nil)
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("expected status OK, got %v", w.Code)
		}
	})

	t.Run("503 when listener unhealthy", func(t *testing.T) {
		mockClient := &mockSpannerHealthClient{
			version: version.EssExpectedSchemaVersion(),
			dirty:   false,
		}
		listener := &Listener{}
		listener.isRunning.Store(false)

		handler := activeHealthCheck(mockClient, listener)
		req := httptest.NewRequest("GET", "/healthz", nil)
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("expected status ServiceUnavailable, got %v", w.Code)
		}
	})

	t.Run("503 when spanner down", func(t *testing.T) {
		mockClient := &mockSpannerHealthClient{
			err: errors.New("spanner down"),
		}
		listener := &Listener{}
		listener.isRunning.Store(true)

		handler := activeHealthCheck(mockClient, listener)
		req := httptest.NewRequest("GET", "/healthz", nil)
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("expected status ServiceUnavailable, got %v", w.Code)
		}
	})
}
