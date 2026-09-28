// Copyright 2026 Google LLC
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

package metricsingester

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

type fakeRunning bool

func (r fakeRunning) IsRunning() bool { return bool(r) }

type fakePinger struct{ err error }

func (p fakePinger) Ping(context.Context) error { return p.err }

func TestHealthMux(t *testing.T) {
	down := errors.New("connection refused")
	tests := []struct {
		name     string
		path     string
		running  bool
		pingErr  error
		wantCode int
	}{
		{name: "liveness running", path: "/healthz", running: true, wantCode: http.StatusOK},
		{name: "liveness ignores clickhouse outage", path: "/healthz", running: true, pingErr: down, wantCode: http.StatusOK},
		{name: "liveness receive loop stopped", path: "/healthz", running: false, wantCode: http.StatusServiceUnavailable},
		{name: "readiness healthy", path: "/readyz", running: true, wantCode: http.StatusOK},
		{name: "readiness clickhouse outage", path: "/readyz", running: true, pingErr: down, wantCode: http.StatusServiceUnavailable},
		{name: "readiness receive loop stopped", path: "/readyz", running: false, wantCode: http.StatusServiceUnavailable},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mux := NewHealthMux(fakeRunning(tc.running), fakePinger{err: tc.pingErr})
			rec := httptest.NewRecorder()

			mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))

			if rec.Code != tc.wantCode {
				t.Errorf("GET %s status = %d, want %d", tc.path, rec.Code, tc.wantCode)
			}
		})
	}
}
