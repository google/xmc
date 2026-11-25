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

package ingestiongateway

import (
	"context"
	"net/http"
	"strings"

	"xmanager_cloud/pkg/auth/oauth2"
)

// AuthMiddleware enforces Bearer token authentication.
type AuthMiddleware struct {
	Config  *Config
	Metrics *GatewayMetrics
}

func NewAuthMiddleware(cfg *Config, metrics *GatewayMetrics) *AuthMiddleware {
	return &AuthMiddleware{
		Config:  cfg,
		Metrics: metrics,
	}
}

func (am *AuthMiddleware) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !am.Config.RequireAuth && am.Config.JWTSecret == "" {
			next.ServeHTTP(w, r)
			return
		}

		authHeader := r.Header.Get("Authorization")
		if !strings.HasPrefix(authHeader, "Bearer ") {
			if am.Metrics != nil {
				am.Metrics.RecordAuthFailure("missing_token")
			}
			http.Error(w, "Unauthorized: missing or invalid Authorization header", http.StatusUnauthorized)
			return
		}

		tokenStr := strings.TrimPrefix(authHeader, "Bearer ")
		if am.validateToken(r.Context(), tokenStr) {
			next.ServeHTTP(w, r)
			return
		}

		if am.Metrics != nil {
			am.Metrics.RecordAuthFailure("invalid_token")
		}
		http.Error(w, "Unauthorized: invalid token", http.StatusUnauthorized)
	})
}

func (am *AuthMiddleware) validateToken(ctx context.Context, tokenStr string) bool {
	// Check JWT
	if am.Config.JWTSecret != "" {
		if _, err := oauth2.ParseSessionToken(tokenStr, []byte(am.Config.JWTSecret), oauth2.CLIAudience, oauth2.FrontendAudience); err == nil {
			return true
		}
	}

	return false
}
