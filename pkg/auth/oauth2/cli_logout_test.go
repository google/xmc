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
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

const logoutTestJTI = "jti-logout"

// newCLILogoutTest returns an Authenticator whose session store holds logoutTestJTI, and a CLI
// token for that session.
func newCLILogoutTest(t *testing.T) (*Authenticator, *fakeCLISessionStore, string) {
	t.Helper()
	a, _, _ := newCLITestAuthenticator(t, baseLoginConfig())
	store := sessionStoreOf(t, a)
	store.sessions[logoutTestJTI] = CLISession{JTI: logoutTestJTI, Email: loginTestEmail}
	return a, store, mustCLIToken(t, logoutTestJTI, time.Now())
}

func mustCLIToken(t *testing.T, jti string, issuedAt time.Time) string {
	t.Helper()
	token, err := generateCLISessionTokenAt(loginTestEmail, "Test User", jti, time.Hour, []byte(loginTestSecret), issuedAt)
	if err != nil {
		t.Fatalf("generateCLISessionTokenAt() failed: %v", err)
	}
	return token
}

func newCLILogoutRequest(bearer string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "https://xmc.example.com/api/cli/logout", nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	return req
}

func TestHandleCLILogout(t *testing.T) {
	dashboardToken, err := GenerateSessionToken(loginTestEmail, "User", "", time.Hour, []byte(loginTestSecret))
	if err != nil {
		t.Fatalf("GenerateSessionToken() failed: %v", err)
	}
	unauthorized := tokenError(http.StatusUnauthorized, tokenErrorInvalidToken)
	tests := []struct {
		name string
		// request builds the request from a valid CLI token for logoutTestJTI.
		request     func(t *testing.T, cliToken string) *http.Request
		setup       func(*Authenticator, *fakeCLISessionStore)
		want        tokenResult
		wantRevoked bool
	}{
		{name: "CLI token", request: func(_ *testing.T, tok string) *http.Request { return newCLILogoutRequest(tok) }, want: tokenResult{Status: http.StatusNoContent, CacheControl: "no-store"}, wantRevoked: true},
		{name: "already revoked session", request: func(_ *testing.T, tok string) *http.Request { return newCLILogoutRequest(tok) },
			setup: func(_ *Authenticator, s *fakeCLISessionStore) { s.revoked[logoutTestJTI] = time.Now() }, want: tokenResult{Status: http.StatusNoContent, CacheControl: "no-store"}, wantRevoked: true},
		{name: "Dashboard token", request: func(_ *testing.T, _ string) *http.Request { return newCLILogoutRequest(dashboardToken) }, want: unauthorized},
		{name: "Dashboard session cookie", request: func(_ *testing.T, _ string) *http.Request {
			req := newCLILogoutRequest("")
			req.AddCookie(&http.Cookie{Name: "session_token", Value: dashboardToken})
			return req
		}, want: unauthorized},
		{name: "CLI token as a cookie", request: func(_ *testing.T, tok string) *http.Request {
			req := newCLILogoutRequest("")
			req.AddCookie(&http.Cookie{Name: "session_token", Value: tok})
			return req
		}, want: unauthorized},
		{name: "missing bearer", request: func(_ *testing.T, _ string) *http.Request { return newCLILogoutRequest("") }, want: unauthorized},
		{name: "expired CLI token", request: func(t *testing.T, _ string) *http.Request {
			return newCLILogoutRequest(mustCLIToken(t, logoutTestJTI, time.Now().Add(-2*time.Hour)))
		}, want: unauthorized},
		{name: "CLI token without jti", request: func(t *testing.T, _ string) *http.Request {
			return newCLILogoutRequest(mustCLIToken(t, "", time.Now()))
		}, want: unauthorized},
		{name: "GET", request: func(_ *testing.T, tok string) *http.Request {
			req := newCLILogoutRequest(tok)
			req.Method = http.MethodGet
			return req
		}, want: tokenError(http.StatusMethodNotAllowed, tokenErrorInvalidRequest)},
		{name: "session store failure", request: func(_ *testing.T, tok string) *http.Request { return newCLILogoutRequest(tok) },
			setup: func(_ *Authenticator, s *fakeCLISessionStore) { s.revokeErr = errors.New("spanner unavailable") }, want: tokenError(http.StatusInternalServerError, tokenErrorServerError)},
		{name: "disabled", request: func(_ *testing.T, tok string) *http.Request { return newCLILogoutRequest(tok) },
			setup: func(a *Authenticator, _ *fakeCLISessionStore) { a.cliSessionStore = nil }, want: tokenError(http.StatusNotFound, "")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, store, cliToken := newCLILogoutTest(t)
			if tt.setup != nil {
				tt.setup(a, store)
			}
			got, _ := serveCLIToken(http.HandlerFunc(a.HandleCLILogout), tt.request(t, cliToken))
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("logout response mismatch (-want +got):\n%s", diff)
			}
			if _, revoked := store.revoked[logoutTestJTI]; revoked != tt.wantRevoked {
				t.Errorf("session revoked = %t, want %t", revoked, tt.wantRevoked)
			}
		})
	}
}

func TestHandleCLILogoutKeepsTheFirstRevocationTime(t *testing.T) {
	a, store, cliToken := newCLILogoutTest(t)
	first := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	store.revoked[logoutTestJTI] = first
	serveCLIToken(http.HandlerFunc(a.HandleCLILogout), newCLILogoutRequest(cliToken))
	if got := store.revoked[logoutTestJTI]; !got.Equal(first) {
		t.Errorf("revocation time = %v, want %v", got, first)
	}
}

// TestCLILogoutRevokesTheToken logs out through the CLI's plain POST behind the security
// middlewares and checks that the token stops authenticating at once on this replica, even though
// its active session was cached, while a cross-site browser POST is refused.
func TestCLILogoutRevokesTheToken(t *testing.T) {
	a, _, cliToken := newCLILogoutTest(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/cli/logout", a.HandleCLILogout)
	mux.Handle("/api/data", a.Authenticate(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })))
	h := a.SecurityMiddlewares(mux)
	serve := func(req *http.Request) int {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w.Code
	}
	dataRequest := func() *http.Request {
		req := httptest.NewRequest(http.MethodGet, "https://xmc.example.com/api/data", nil)
		req.Header.Set("Authorization", "Bearer "+cliToken)
		return req
	}
	crossSite := newCLILogoutRequest(cliToken)
	crossSite.Header.Set("Sec-Fetch-Site", "cross-site")
	got := []int{serve(dataRequest()), serve(crossSite), serve(newCLILogoutRequest(cliToken)), serve(dataRequest())}
	want := []int{http.StatusOK, http.StatusForbidden, http.StatusNoContent, http.StatusUnauthorized}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("statuses (request, cross-site logout, logout, request after logout) mismatch (-want +got):\n%s", diff)
	}
}
