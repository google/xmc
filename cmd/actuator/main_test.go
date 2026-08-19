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
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSetupMux(t *testing.T) {
	t.Run("nil_prod_server_routes", func(t *testing.T) {
		mux := setupMux(nil)

		// Test /healthz
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("GET /healthz status = %d, want %d", rec.Code, http.StatusOK)
		}
		if rec.Body.String() != "ok" {
			t.Errorf("GET /healthz body = %q, want 'ok'", rec.Body.String())
		}

		// Test /readyz on nil server (fails active checks because no clients configured)
		req = httptest.NewRequest(http.MethodGet, "/readyz", nil)
		rec = httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("GET /readyz on nil server status = %d, want %d", rec.Code, http.StatusInternalServerError)
		}

		// Test /metrics
		req = httptest.NewRequest(http.MethodGet, "/metrics", nil)
		rec = httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("GET /metrics status = %d, want %d", rec.Code, http.StatusOK)
		}

		// Test unknown route -> 404
		req = httptest.NewRequest(http.MethodGet, "/nonexistent", nil)
		rec = httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET /nonexistent status = %d, want %d", rec.Code, http.StatusNotFound)
		}
	})

	t.Run("populated_prod_server_routes", func(t *testing.T) {
		server := &ProdServer{}
		mux := setupMux(server)

		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("GET /healthz status = %d, want %d", rec.Code, http.StatusOK)
		}
	})
}
