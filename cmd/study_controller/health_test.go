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

package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewHTTPHandler(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		method       string
		path         string
		wantStatus   int
		wantContains string
	}{
		{name: "liveness", method: http.MethodGet, path: "/healthz", wantStatus: http.StatusOK, wantContains: "OK"},
		{name: "readiness", method: http.MethodGet, path: "/readyz", wantStatus: http.StatusOK, wantContains: "OK"},
		{name: "metrics", method: http.MethodGet, path: "/metrics", wantStatus: http.StatusOK, wantContains: "go_goroutines"},
		{name: "unknown path", method: http.MethodGet, path: "/unknown", wantStatus: http.StatusNotFound},
		{name: "non-GET liveness", method: http.MethodPost, path: "/healthz", wantStatus: http.StatusMethodNotAllowed},
	}
	handler := newHTTPHandler()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))
			if rec.Code != tc.wantStatus {
				t.Errorf("%s %s status = %d, want %d", tc.method, tc.path, rec.Code, tc.wantStatus)
			}
			if !strings.Contains(rec.Body.String(), tc.wantContains) {
				t.Errorf("%s %s body = %q, want it to contain %q", tc.method, tc.path, rec.Body.String(), tc.wantContains)
			}
		})
	}
}
