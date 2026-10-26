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
)

type mockReadinessChecker struct {
	err error
}

func (m *mockReadinessChecker) CheckReady(ctx context.Context) error {
	return m.err
}

func TestActiveHealthCheck(t *testing.T) {
	t.Run("serving when all ok", func(t *testing.T) {
		mockDB := &mockReadinessChecker{}
		listener := &Listener{}
		listener.isRunning.Store(true)

		handler := activeHealthCheck(mockDB, listener)
		req := httptest.NewRequest("GET", "/readyz", nil)
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("expected status OK, got %v", w.Code)
		}
	})

	t.Run("503 when listener unhealthy", func(t *testing.T) {
		mockDB := &mockReadinessChecker{}
		listener := &Listener{}
		listener.isRunning.Store(false)

		handler := activeHealthCheck(mockDB, listener)
		req := httptest.NewRequest("GET", "/readyz", nil)
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("expected status ServiceUnavailable, got %v", w.Code)
		}
	})

	t.Run("503 when database down", func(t *testing.T) {
		mockDB := &mockReadinessChecker{
			err: errors.New("database down"),
		}
		listener := &Listener{}
		listener.isRunning.Store(true)

		handler := activeHealthCheck(mockDB, listener)
		req := httptest.NewRequest("GET", "/readyz", nil)
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("expected status ServiceUnavailable, got %v", w.Code)
		}
	})

	t.Run("503 when database nil", func(t *testing.T) {
		listener := &Listener{}
		listener.isRunning.Store(true)

		handler := activeHealthCheck(nil, listener)
		req := httptest.NewRequest("GET", "/readyz", nil)
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("expected status ServiceUnavailable, got %v", w.Code)
		}
	})
}

func TestLivenessCheck(t *testing.T) {
	running := &Listener{}
	running.isRunning.Store(true)
	stopped := &Listener{}

	tests := []struct {
		name     string
		listener *Listener
		want     int
	}{
		{name: "ok when listener running", listener: running, want: http.StatusOK},
		{name: "503 when listener stopped", listener: stopped, want: http.StatusServiceUnavailable},
		{name: "503 when listener nil", listener: nil, want: http.StatusServiceUnavailable},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			livenessCheck(tc.listener).ServeHTTP(w, httptest.NewRequest("GET", "/healthz", nil))
			if w.Code != tc.want {
				t.Errorf("livenessCheck() status = %v, want %v", w.Code, tc.want)
			}
		})
	}
}
