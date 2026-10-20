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

package oauth2

import (
	"net/http"
	"strings"
	"time"

	"xmanager_cloud/internal/logging"
)

// tokenErrorInvalidToken is the RFC 6750 error code for a missing, malformed or expired bearer
// token.
const tokenErrorInvalidToken = "invalid_token"

// HandleCLILogout revokes the session of the CLI session token in the Authorization header
// (`POST /api/cli/logout`, used by `xmc logout`). Only CLI session tokens are accepted, never
// cookies or Dashboard tokens, so a browser cannot be made to log a CLI out. Revoking is
// idempotent: an already revoked session still gets 204. This replica stops accepting the token at
// once; other replicas and services within the cliSessionActiveTTL cache lifetime.
func (a *Authenticator) HandleCLILogout(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !a.checkCLILogoutRequest(w, r) {
		return
	}
	claims, ok := a.cliLogoutClaims(r)
	if !ok {
		w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
		writeTokenError(w, http.StatusUnauthorized, tokenErrorInvalidToken)
		return
	}
	ctx := r.Context()
	if err := a.cliSessionStore.Revoke(ctx, claims.ID, time.Now()); err != nil {
		logging.Error(ctx, "[CLI Logout] Failed to revoke session", "jti", claims.ID, "error", err)
		writeTokenError(w, http.StatusInternalServerError, tokenErrorServerError)
		return
	}
	// EnableCLISessions installs cliSessions along with cliSessionStore.
	a.cliSessions.markRevoked(claims.ID)
	logging.Info(ctx, "[CLI Logout] Revoked CLI session", "email", claims.Email, "jti", claims.ID)
	w.WriteHeader(http.StatusNoContent)
}

// checkCLILogoutRequest admits POSTs while CLI sessions are recorded and writes the error response
// otherwise.
func (a *Authenticator) checkCLILogoutRequest(w http.ResponseWriter, r *http.Request) bool {
	if a.cliSessionStore == nil {
		http.NotFound(w, r)
		return false
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeTokenError(w, http.StatusMethodNotAllowed, tokenErrorInvalidRequest)
		return false
	}
	return true
}

// cliLogoutClaims returns the claims of the CLI session token in the Authorization header.
func (a *Authenticator) cliLogoutClaims(r *http.Request) (*SessionClaims, bool) {
	tokenStr, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || tokenStr == "" {
		return nil, false
	}
	claims, err := ParseSessionToken(tokenStr, a.JWTSecret, CLIAudience)
	if err != nil || claims.ID == "" {
		return nil, false
	}
	return claims, true
}
