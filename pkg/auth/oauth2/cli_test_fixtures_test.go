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
	"io"
	"net/http"
	"sync"
	"testing"
	"time"
)

// Shared fixtures for the CLI login tests.
const (
	cliTestPort  = 51234
	cliTestState = "cli-state_123"
	// RFC 7636 appendix B example verifier and its S256 challenge.
	cliTestVerifier  = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	cliTestChallenge = "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
)

// fakeCLICodeStore is an in-memory CLICodeStore.
type fakeCLICodeStore struct {
	mu           sync.Mutex
	codes        map[string]CLILoginCode
	saveErr      error
	consumeErr   error
	consumeCalls int
}

func newFakeCLICodeStore() *fakeCLICodeStore {
	return &fakeCLICodeStore{codes: make(map[string]CLILoginCode)}
}

func (s *fakeCLICodeStore) Save(_ context.Context, code CLILoginCode) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.saveErr != nil {
		return s.saveErr
	}
	s.codes[string(code.Hash)] = code
	return nil
}

func (s *fakeCLICodeStore) Consume(_ context.Context, hash []byte) (CLILoginCode, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.consumeCalls++
	if s.consumeErr != nil {
		return CLILoginCode{}, s.consumeErr
	}
	code, ok := s.codes[string(hash)]
	if !ok {
		return CLILoginCode{}, ErrCLICodeNotFound
	}
	delete(s.codes, string(hash))
	return code, nil
}

// put stores code under the hash of plaintext and returns the stored record.
func (s *fakeCLICodeStore) put(plaintext string, code CLILoginCode) CLILoginCode {
	code.Hash = hashCLILoginCode(plaintext)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.codes[string(code.Hash)] = code
	return code
}

// only returns the single stored code, failing the test otherwise.
func (s *fakeCLICodeStore) only(t *testing.T) CLILoginCode {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.codes) != 1 {
		t.Fatalf("store holds %d codes, want 1", len(s.codes))
	}
	for _, code := range s.codes {
		return code
	}
	return CLILoginCode{}
}

// newCLITestAuthenticator returns an Authenticator with CLI login enabled on a fake store and a
// stub identity provider returning baseLoginUserInfo.
func newCLITestAuthenticator(t *testing.T, cfg *Config) (*Authenticator, *fakeCLICodeStore, *stubOAuthProvider) {
	t.Helper()
	a, provider := newLoginTestAuthenticator(cfg)
	store := newFakeCLICodeStore()
	if err := a.EnableCLILogin(store); err != nil {
		t.Fatalf("EnableCLILogin() failed: %v", err)
	}
	return a, store, provider
}

// baseCLITicket is a pending loopback login bound to oauthState.
func baseCLITicket(oauthState string) cliLoginTicket {
	return cliLoginTicket{
		Port:           cliTestPort,
		State:          cliTestState,
		CodeChallenge:  cliTestChallenge,
		Mode:           cliLoginModeLoopback,
		OAuthStateHash: hashOAuthState(oauthState),
		ExpiresAt:      time.Now().Add(cliLoginTicketTTL).Unix(),
	}
}

// baseCLIManualTicket is a pending manual login bound to oauthState.
func baseCLIManualTicket(oauthState string) cliLoginTicket {
	ticket := baseCLITicket(oauthState)
	ticket.Port = 0
	ticket.Mode = cliLoginModeManual
	return ticket
}

func mustSealCLILoginTicket(t *testing.T, a *Authenticator, ticket cliLoginTicket) string {
	t.Helper()
	sealed, err := a.sealCLILoginTicket(ticket)
	if err != nil {
		t.Fatalf("sealCLILoginTicket() failed: %v", err)
	}
	return sealed
}

// newCLICallbackRequest is the identity provider's redirect to /api/callback with query, carrying
// cliCookie as the cli_login cookie when it is not empty.
func newCLICallbackRequest(query, cliCookie string) *http.Request {
	if cliCookie == "" {
		return newCallbackRequest(query)
	}
	return newCallbackRequest(query, &http.Cookie{Name: cliLoginCookieName, Value: cliCookie})
}

// cliPageResult is the comparable outcome of a CLI login response to the browser.
type cliPageResult struct {
	Status                       int
	CacheControl, ReferrerPolicy string
	// CSP is the Content-Security-Policy set by the handler; HTML pages deny framing.
	CSP      string
	Location string
	// Title is the <h1> heading of an HTML page.
	Title string
}

// cliPage is an HTML page with title, served with status.
func cliPage(status int, title string) cliPageResult {
	return cliPageResult{Status: status, CacheControl: "no-store", ReferrerPolicy: "no-referrer", CSP: "frame-ancestors 'none'; form-action 'self'", Title: title}
}

// cliRedirect is a redirect to location.
func cliRedirect(location string) cliPageResult {
	return cliPageResult{Status: http.StatusFound, CacheControl: "no-store", ReferrerPolicy: "no-referrer", Location: location}
}

// readCLIPage reads the outcome of resp and returns it with the body.
func readCLIPage(resp *http.Response) (cliPageResult, string) {
	body, _ := io.ReadAll(resp.Body)
	return cliPageResult{
		Status:         resp.StatusCode,
		CacheControl:   resp.Header.Get("Cache-Control"),
		ReferrerPolicy: resp.Header.Get("Referrer-Policy"),
		CSP:            resp.Header.Get("Content-Security-Policy"),
		Location:       resp.Header.Get("Location"),
		Title:          between(string(body), "<h1>", "</h1>"),
	}, string(body)
}

// withFailingRandom makes randReader fail for the rest of the test.
func withFailingRandom(t *testing.T) {
	t.Helper()
	orig := randReader
	randReader = errorReader{}
	t.Cleanup(func() { randReader = orig })
}
