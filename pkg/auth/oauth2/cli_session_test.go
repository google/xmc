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
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/go-cmp/cmp"
)

// fakeCLISessionStore is an in-memory CLISessionStore.
type fakeCLISessionStore struct {
	mu        sync.Mutex
	sessions  map[string]CLISession
	revoked   map[string]time.Time
	createErr error
	checkErr  error
	checks    int
}

func newFakeCLISessionStore(activeJTIs ...string) *fakeCLISessionStore {
	s := &fakeCLISessionStore{sessions: make(map[string]CLISession), revoked: make(map[string]time.Time)}
	for _, jti := range activeJTIs {
		s.sessions[jti] = CLISession{JTI: jti, Email: loginTestEmail}
	}
	return s
}

func (s *fakeCLISessionStore) Create(_ context.Context, session CLISession) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.createErr != nil {
		return s.createErr
	}
	s.sessions[session.JTI] = session
	return nil
}

func (s *fakeCLISessionStore) IsRevoked(_ context.Context, jti string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.checks++
	if s.checkErr != nil {
		return false, s.checkErr
	}
	_, known := s.sessions[jti]
	_, revoked := s.revoked[jti]
	return !known || revoked, nil
}

func (s *fakeCLISessionStore) checkCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.checks
}

// sessionStoreOf returns the fake session store installed by newCLITestAuthenticator.
func sessionStoreOf(t *testing.T, a *Authenticator) *fakeCLISessionStore {
	t.Helper()
	store, ok := a.cliSessionStore.(*fakeCLISessionStore)
	if !ok {
		t.Fatalf("session store is %T, want *fakeCLISessionStore", a.cliSessionStore)
	}
	return store
}

func cliClaims(jti string) *SessionClaims {
	return &SessionClaims{Email: loginTestEmail, RegisteredClaims: jwt.RegisteredClaims{ID: jti, Audience: jwt.ClaimStrings{CLIAudience}}}
}

func TestVerifyCLISession(t *testing.T) {
	failing := newFakeCLISessionStore("active")
	failing.checkErr = errors.New("spanner unavailable")
	frontend := &SessionClaims{Email: loginTestEmail, RegisteredClaims: jwt.RegisteredClaims{Audience: jwt.ClaimStrings{FrontendAudience}}}
	tests := []struct {
		name        string
		checker     CLISessionChecker
		claims      *SessionClaims
		wantErr     bool
		wantRevoked bool
	}{
		{name: "dashboard token needs no checker", claims: frontend},
		{name: "dashboard token ignores checker failures", checker: failing, claims: frontend},
		{name: "active CLI session", checker: newFakeCLISessionStore("active"), claims: cliClaims("active")},
		{name: "revoked CLI session", checker: revokedStore("active"), claims: cliClaims("active"), wantErr: true, wantRevoked: true},
		{name: "unknown CLI session", checker: newFakeCLISessionStore("active"), claims: cliClaims("unknown"), wantErr: true, wantRevoked: true},
		{name: "no checker", claims: cliClaims("active"), wantErr: true, wantRevoked: true},
		{name: "lookup failure", checker: failing, claims: cliClaims("active"), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := VerifyCLISession(context.Background(), tt.checker, tt.claims)
			if gotErr := err != nil; gotErr != tt.wantErr {
				t.Fatalf("VerifyCLISession() error = %v, want error %t", err, tt.wantErr)
			}
			if gotRevoked := errors.Is(err, ErrCLISessionRevoked); gotRevoked != tt.wantRevoked {
				t.Errorf("VerifyCLISession() error = %v, want ErrCLISessionRevoked %t", err, tt.wantRevoked)
			}
		})
	}
}

func revokedStore(jti string) *fakeCLISessionStore {
	s := newFakeCLISessionStore(jti)
	s.revoked[jti] = time.Now()
	return s
}

func TestEnableCLISessionsRejectsMisuse(t *testing.T) {
	a := newTestAuthenticator(baseLoginConfig())
	if err := a.EnableCLISessions(nil); err == nil {
		t.Error("EnableCLISessions(nil) error = nil, want non-nil")
	}
	if err := a.CheckCLISessions(nil); err == nil {
		t.Error("CheckCLISessions(nil) error = nil, want non-nil")
	}
	if err := a.EnableCLISessions(newFakeCLISessionStore()); err != nil {
		t.Fatalf("EnableCLISessions() unexpected error: %v", err)
	}
	if err := a.EnableCLISessions(newFakeCLISessionStore()); err == nil {
		t.Error("second EnableCLISessions() error = nil, want non-nil")
	}
	if err := a.CheckCLISessions(newFakeCLISessionStore()); err == nil {
		t.Error("CheckCLISessions() after EnableCLISessions() error = nil, want non-nil")
	}
}

func TestAuthenticatorIsRevoked(t *testing.T) {
	unwired := newTestAuthenticator(baseLoginConfig())
	wired := newTestAuthenticator(baseLoginConfig())
	if err := wired.CheckCLISessions(newFakeCLISessionStore("active")); err != nil {
		t.Fatalf("CheckCLISessions() unexpected error: %v", err)
	}
	var got []bool
	for _, a := range []*Authenticator{unwired, wired} {
		revoked, err := a.IsRevoked(context.Background(), "active")
		if err != nil {
			t.Fatalf("IsRevoked() unexpected error: %v", err)
		}
		got = append(got, revoked)
	}
	if diff := cmp.Diff([]bool{true, false}, got); diff != "" {
		t.Errorf("IsRevoked() without and with a checker mismatch (-want +got):\n%s", diff)
	}
}

// TestAuthenticateChecksCLISessions runs bearer tokens through the HTTP middleware.
func TestAuthenticateChecksCLISessions(t *testing.T) {
	secret := []byte(loginTestSecret)
	cliToken, err := generateCLISessionTokenAt(loginTestEmail, "Test User", "jti-1", time.Hour, secret, time.Now())
	if err != nil {
		t.Fatalf("generateCLISessionTokenAt() failed: %v", err)
	}
	dashboardToken, err := GenerateSessionToken(loginTestEmail, "User", "", time.Hour, secret)
	if err != nil {
		t.Fatalf("GenerateSessionToken() failed: %v", err)
	}
	failing := newFakeCLISessionStore("jti-1")
	failing.checkErr = errors.New("spanner unavailable")
	tests := []struct {
		name       string
		store      *fakeCLISessionStore
		bearer     string
		wantStatus int
	}{
		{name: "active CLI session", store: newFakeCLISessionStore("jti-1"), bearer: cliToken, wantStatus: http.StatusOK},
		{name: "revoked CLI session", store: revokedStore("jti-1"), bearer: cliToken, wantStatus: http.StatusUnauthorized},
		{name: "unknown CLI session", store: newFakeCLISessionStore(), bearer: cliToken, wantStatus: http.StatusUnauthorized},
		{name: "no session checker", bearer: cliToken, wantStatus: http.StatusUnauthorized},
		{name: "session lookup failure", store: failing, bearer: cliToken, wantStatus: http.StatusServiceUnavailable},
		{name: "dashboard token is not looked up", store: failing, bearer: dashboardToken, wantStatus: http.StatusOK},
	}
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := newTestAuthenticator(&Config{JWTSecret: loginTestSecret, AllowedDomains: []string{"mycompany.com"}})
			if tt.store != nil {
				if err := a.CheckCLISessions(tt.store); err != nil {
					t.Fatalf("CheckCLISessions() unexpected error: %v", err)
				}
			}
			req := httptest.NewRequest(http.MethodGet, "/api/data", nil)
			req.Header.Set("Authorization", "Bearer "+tt.bearer)
			w := httptest.NewRecorder()
			a.Authenticate(ok).ServeHTTP(w, req)
			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", w.Code, tt.wantStatus)
			}
		})
	}
}

func TestHandleCLITokenRecordsSession(t *testing.T) {
	a, _ := newCLITokenTest(t, baseLoginConfig(), time.Now().Add(cliLoopbackCodeTTL))
	_, body := serveCLIToken(http.HandlerFunc(a.HandleCLIToken), newCLITokenRequest(validTokenForm))
	claims, err := ParseSessionToken(body.AccessToken, []byte(loginTestSecret), CLIAudience)
	if err != nil {
		t.Fatalf("access token does not parse as a CLI session token: %v", err)
	}
	store := sessionStoreOf(t, a)
	got, ok := store.sessions[claims.ID]
	if !ok {
		t.Fatalf("no session recorded for jti %q", claims.ID)
	}
	want := CLISession{JTI: claims.ID, Email: loginTestEmail, IssuedAt: claims.IssuedAt.Time, ExpiresAt: claims.ExpiresAt.Time}
	if diff := cmp.Diff(want, got, cmp.Comparer(sameSecond)); diff != "" {
		t.Errorf("recorded session mismatch (-want +got):\n%s", diff)
	}
}

// sameSecond compares times at the one-second precision of JWT claims.
func sameSecond(x, y time.Time) bool {
	return x.Truncate(time.Second).Equal(y.Truncate(time.Second))
}
