// Copyright 2025 Google LLC
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
package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"xmanager_cloud/storage/spanner/emulator/emulator"
)

func TestActiveHealthCheck(t *testing.T) {
	ctx := context.Background()

	t.Run("serving when all ok", func(t *testing.T) {
		xidClient, _, _ := emulator.SetupWithEmulator(ctx, t)
		listener := &Listener{}
		listener.isRunning.Store(true)

		handler := activeHealthCheck(xidClient, listener)
		req := httptest.NewRequest("GET", "/healthz", nil)
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("activeHealthCheck() got %d, want %d", w.Code, http.StatusOK)
		}
	})

	t.Run("503 when listener unhealthy", func(t *testing.T) {
		xidClient, _, _ := emulator.SetupWithEmulator(ctx, t)
		listener := &Listener{}
		listener.isRunning.Store(false)

		handler := activeHealthCheck(xidClient, listener)
		req := httptest.NewRequest("GET", "/healthz", nil)
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("activeHealthCheck() got %d, want %d", w.Code, http.StatusServiceUnavailable)
		}
	})

	t.Run("503 when spanner down", func(t *testing.T) {
		xidClient, _, _ := emulator.SetupWithEmulator(ctx, t)
		xidClient.Close()
		listener := &Listener{}
		listener.isRunning.Store(true)

		handler := activeHealthCheck(xidClient, listener)
		req := httptest.NewRequest("GET", "/healthz", nil)
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("activeHealthCheck() got %d, want %d", w.Code, http.StatusServiceUnavailable)
		}
	})

	t.Run("503 when spanner client is nil", func(t *testing.T) {
		listener := &Listener{}
		listener.isRunning.Store(true)

		handler := activeHealthCheck(nil, listener)
		req := httptest.NewRequest("GET", "/healthz", nil)
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("activeHealthCheck() got %d, want %d", w.Code, http.StatusServiceUnavailable)
		}
	})

	t.Run("503 when listener is nil", func(t *testing.T) {
		xidClient, _, _ := emulator.SetupWithEmulator(ctx, t)

		handler := activeHealthCheck(xidClient, nil)
		req := httptest.NewRequest("GET", "/healthz", nil)
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("activeHealthCheck() got %d, want %d", w.Code, http.StatusServiceUnavailable)
		}
	})
}
