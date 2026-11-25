// Copyright 2026 Google LLC
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

package ingestiongateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"xmanager_cloud/pkg/auth/oauth2"
)

func TestAuthMiddleware(t *testing.T) {
	jwtSecret := "my-secret-that-must-be-long-enough-for-hs256"
	token, _ := oauth2.GenerateSessionToken("user@example.com", "Test User", "", time.Hour, []byte(jwtSecret))

	tests := []struct {
		name       string
		reqAuth    bool
		secret     string
		authHeader string
		wantCode   int
	}{
		{"disabled", false, "", "", http.StatusOK},
		{"missing token when required", true, jwtSecret, "", http.StatusUnauthorized},
		{"valid token", true, jwtSecret, "Bearer " + token, http.StatusOK},
		{"invalid token", true, jwtSecret, "Bearer bogus", http.StatusUnauthorized},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{RequireAuth: tc.reqAuth, JWTSecret: tc.secret}
			am := NewAuthMiddleware(cfg, nil)

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tc.authHeader != "" {
				req.Header.Set("Authorization", tc.authHeader)
			}

			rr := httptest.NewRecorder()
			handler := am.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))

			handler.ServeHTTP(rr, req)

			if status := rr.Code; status != tc.wantCode {
				t.Errorf("handler returned wrong status code: got %v want %v", status, tc.wantCode)
			}
		})
	}
}
