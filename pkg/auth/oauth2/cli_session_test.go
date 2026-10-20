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
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

// fakeCLISessionStore is an in-memory CLISessionStore.
type fakeCLISessionStore struct {
	mu        sync.Mutex
	sessions  map[string]CLISession
	createErr error
}

func newFakeCLISessionStore() *fakeCLISessionStore {
	return &fakeCLISessionStore{sessions: make(map[string]CLISession)}
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

// sessionStoreOf returns the fake session store installed by newCLITestAuthenticator.
func sessionStoreOf(t *testing.T, a *Authenticator) *fakeCLISessionStore {
	t.Helper()
	store, ok := a.cliSessionStore.(*fakeCLISessionStore)
	if !ok {
		t.Fatalf("session store is %T, want *fakeCLISessionStore", a.cliSessionStore)
	}
	return store
}

func TestEnableCLISessionsRejectsMisuse(t *testing.T) {
	a := newTestAuthenticator(baseLoginConfig())
	if err := a.EnableCLISessions(nil); err == nil {
		t.Error("EnableCLISessions(nil) error = nil, want non-nil")
	}
	if err := a.EnableCLISessions(newFakeCLISessionStore()); err != nil {
		t.Fatalf("EnableCLISessions() unexpected error: %v", err)
	}
	if err := a.EnableCLISessions(newFakeCLISessionStore()); err == nil {
		t.Error("second EnableCLISessions() error = nil, want non-nil")
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
