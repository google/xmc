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
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/iam/apiv1/iampb"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"golang.org/x/time/rate"
)

// cliTestCode is the plaintext of the one-time code stored by newCLITokenTest.
const cliTestCode = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNO-_"

var (
	validTokenForm = cliTokenForm(cliTestCode, cliTestVerifier)
	wrongVerifier  = strings.Repeat("v", 43)
)

// newCLITokenTest returns an Authenticator whose store holds cliTestCode for loginTestEmail,
// expiring at expiresAt.
func newCLITokenTest(t *testing.T, cfg *Config, expiresAt time.Time) (*Authenticator, *fakeCLICodeStore) {
	t.Helper()
	a, store, _ := newCLITestAuthenticator(t, cfg)
	store.put(cliTestCode, CLILoginCode{Email: loginTestEmail, Name: "Test User", CodeChallenge: cliTestChallenge, ExpiresAt: expiresAt})
	return a, store
}

func cliTokenForm(code, verifier string) string {
	return url.Values{"grant_type": {"authorization_code"}, "code": {code}, "code_verifier": {verifier}}.Encode()
}

// newCLITokenRequest builds a form POST to /api/cli/token.
func newCLITokenRequest(form string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "https://xmc.example.com/api/cli/token", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

// tokenResult is the comparable outcome of a token request.
type tokenResult struct {
	Status       int
	Error        string
	CacheControl string
}

func tokenError(status int, code string) tokenResult {
	return tokenResult{Status: status, Error: code, CacheControl: "no-store"}
}

// serveCLIToken serves req through h and decodes the success or error body.
func serveCLIToken(h http.Handler, req *http.Request) (tokenResult, cliTokenResponse) {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	var body struct {
		cliTokenResponse
		Error string `json:"error"`
	}
	_ = json.NewDecoder(w.Body).Decode(&body)
	return tokenResult{Status: w.Code, Error: body.Error, CacheControl: w.Header().Get("Cache-Control")}, body.cliTokenResponse
}

func TestHandleCLITokenSuccess(t *testing.T) {
	for _, tt := range []struct {
		name     string
		ttl      time.Duration
		lifetime time.Duration
	}{
		{name: "default lifetime", lifetime: 12 * time.Hour},
		{name: "configured lifetime", ttl: time.Hour, lifetime: time.Hour},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := baseLoginConfig()
			cfg.CLISessionTTL = tt.ttl
			a, _ := newCLITokenTest(t, cfg, time.Now().Add(cliLoopbackCodeTTL))
			got, body := serveCLIToken(http.HandlerFunc(a.HandleCLIToken), newCLITokenRequest(validTokenForm))
			if diff := cmp.Diff(tokenResult{Status: http.StatusOK, CacheControl: "no-store"}, got); diff != "" {
				t.Fatalf("token response mismatch (-want +got):\n%s", diff)
			}
			wantBody := cliTokenResponse{TokenType: "Bearer", ExpiresIn: int64(tt.lifetime / time.Second), Email: loginTestEmail}
			if diff := cmp.Diff(wantBody, body, cmpopts.IgnoreFields(cliTokenResponse{}, "AccessToken")); diff != "" {
				t.Errorf("token body mismatch (-want +got):\n%s", diff)
			}
			claims, err := ParseSessionToken(body.AccessToken, []byte(loginTestSecret), CLIAudience)
			if err != nil {
				t.Fatalf("access token does not parse as a CLI session token: %v", err)
			}
			wantClaims := &SessionClaims{Email: loginTestEmail, Name: "Test User", RegisteredClaims: jwt.RegisteredClaims{
				Issuer: SessionIssuer, Subject: loginTestEmail, Audience: jwt.ClaimStrings{CLIAudience},
			}}
			if diff := cmp.Diff(wantClaims, claims, cmpopts.IgnoreFields(jwt.RegisteredClaims{}, "ID", "ExpiresAt", "IssuedAt", "NotBefore")); diff != "" {
				t.Errorf("token claims mismatch (-want +got):\n%s", diff)
			}
			if len(claims.ID) != 32 || claims.ExpiresAt.Sub(claims.IssuedAt.Time) != tt.lifetime {
				t.Errorf("jti = %q, lifetime = %v, want 32 hex characters and %v", claims.ID, claims.ExpiresAt.Sub(claims.IssuedAt.Time), tt.lifetime)
			}
			if _, err := ParseSessionToken(body.AccessToken, []byte(loginTestSecret), FrontendAudience); err == nil {
				t.Error("CLI token accepted for the Dashboard audience")
			}
		})
	}
}

func TestHandleCLITokenRejects(t *testing.T) {
	badRequest := func(code string) tokenResult { return tokenError(http.StatusBadRequest, code) }
	serverError := tokenError(http.StatusInternalServerError, tokenErrorServerError)
	failingIAM := func(a *Authenticator, _ *fakeCLICodeStore) {
		a.Config.AllowedDomains, a.Config.GCPProjectID = nil, "test-project"
		a.PolicyFetcher = func(context.Context) (*iampb.Policy, error) { return nil, errors.New("iam unavailable") }
	}
	tests := []struct {
		name string
		// form is the request body; empty means validTokenForm.
		form    string
		mutate  func(*http.Request)
		expired bool
		setup   func(*Authenticator, *fakeCLICodeStore)
		random  bool
		want    tokenResult
		// consumed reports whether the store must have been asked to consume the code.
		consumed bool
	}{
		{name: "wrong verifier", form: cliTokenForm(cliTestCode, wrongVerifier), want: badRequest(tokenErrorInvalidGrant), consumed: true},
		{name: "expired code", expired: true, want: badRequest(tokenErrorInvalidGrant), consumed: true},
		{name: "unknown code", form: cliTokenForm(strings.Repeat("A", 43), cliTestVerifier), want: badRequest(tokenErrorInvalidGrant), consumed: true},
		{name: "malformed code", form: cliTokenForm("short", cliTestVerifier), want: badRequest(tokenErrorInvalidGrant)},
		{name: "malformed verifier", form: cliTokenForm(cliTestCode, "short"), want: badRequest(tokenErrorInvalidGrant)},
		{name: "missing grant type", form: "code=" + cliTestCode, want: badRequest(tokenErrorInvalidRequest)},
		{name: "unsupported grant type", form: "grant_type=refresh_token", want: badRequest(tokenErrorUnsupportedGrantType)},
		{name: "repeated code", form: validTokenForm + "&code=" + cliTestCode, want: badRequest(tokenErrorInvalidRequest)},
		{name: "parameters in the query string", form: "unused=1", mutate: func(r *http.Request) { r.URL.RawQuery = validTokenForm }, want: badRequest(tokenErrorInvalidRequest)},
		{name: "JSON body", mutate: func(r *http.Request) { r.Header.Set("Content-Type", "application/json") }, want: badRequest(tokenErrorInvalidRequest)},
		{name: "oversized body", form: validTokenForm + "&pad=" + strings.Repeat("x", maxCLITokenRequestBytes), want: badRequest(tokenErrorInvalidRequest)},
		{name: "GET", mutate: func(r *http.Request) { r.Method = http.MethodGet }, want: tokenError(http.StatusMethodNotAllowed, tokenErrorInvalidRequest)},
		{name: "rate limited", setup: func(a *Authenticator, _ *fakeCLICodeStore) { a.cliTokenLimiter = rate.NewLimiter(0, 0) }, want: tokenError(http.StatusTooManyRequests, tokenErrorTooManyRequests)},
		{name: "disabled", setup: func(a *Authenticator, _ *fakeCLICodeStore) { a.cliCodes = nil }, want: tokenError(http.StatusNotFound, "")},
		{name: "code store failure", setup: func(_ *Authenticator, s *fakeCLICodeStore) { s.consumeErr = errors.New("spanner unavailable") }, want: serverError, consumed: true},
		{name: "user de-authorized since login", setup: func(a *Authenticator, _ *fakeCLICodeStore) { a.Config.AllowedDomains = []string{"other.com"} }, want: tokenError(http.StatusForbidden, tokenErrorAccessDenied), consumed: true},
		{name: "authorization pipeline failure", setup: failingIAM, want: serverError, consumed: true},
		{name: "random source failure", random: true, want: serverError, consumed: true},
		{name: "session store failure", setup: func(a *Authenticator, _ *fakeCLICodeStore) {
			a.cliSessionStore.(*fakeCLISessionStore).createErr = errors.New("spanner unavailable")
		}, want: serverError, consumed: true},
		{name: "no session store", setup: func(a *Authenticator, _ *fakeCLICodeStore) { a.cliSessionStore = nil }, want: serverError, consumed: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expiresAt := time.Now().Add(cliLoopbackCodeTTL)
			if tt.expired {
				expiresAt = time.Now().Add(-time.Second)
			}
			a, store := newCLITokenTest(t, baseLoginConfig(), expiresAt)
			if tt.setup != nil {
				tt.setup(a, store)
			}
			if tt.random {
				withFailingRandom(t)
			}
			form := tt.form
			if form == "" {
				form = validTokenForm
			}
			req := newCLITokenRequest(form)
			if tt.mutate != nil {
				tt.mutate(req)
			}
			got, _ := serveCLIToken(http.HandlerFunc(a.HandleCLIToken), req)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("token response mismatch (-want +got):\n%s", diff)
			}
			if consumed := store.consumeCalls > 0; consumed != tt.consumed {
				t.Errorf("code consumed = %v, want %v", consumed, tt.consumed)
			}
		})
	}
}

// TestHandleCLITokenCodeIsSingleUse checks that the first redemption burns the code, whether it
// succeeds or presents the wrong verifier.
func TestHandleCLITokenCodeIsSingleUse(t *testing.T) {
	for _, first := range []string{cliTestVerifier, wrongVerifier} {
		a, _ := newCLITokenTest(t, baseLoginConfig(), time.Now().Add(cliLoopbackCodeTTL))
		h := http.HandlerFunc(a.HandleCLIToken)
		serveCLIToken(h, newCLITokenRequest(cliTokenForm(cliTestCode, first)))
		if got, _ := serveCLIToken(h, newCLITokenRequest(validTokenForm)); got != tokenError(http.StatusBadRequest, tokenErrorInvalidGrant) {
			t.Errorf("redemption after verifier %q = %+v, want invalid_grant", first, got)
		}
	}
}

// TestHandleCLITokenRateLimitSpendsOnlyWellFormedRequests checks that malformed requests are
// rejected before the limiter, so they cannot exhaust it, while well-formed ones spend a token.
func TestHandleCLITokenRateLimitSpendsOnlyWellFormedRequests(t *testing.T) {
	a, _ := newCLITokenTest(t, baseLoginConfig(), time.Now().Add(cliLoopbackCodeTTL))
	a.cliTokenLimiter = rate.NewLimiter(0, 1)
	h := http.HandlerFunc(a.HandleCLIToken)
	var got []int
	for _, form := range []string{"code=short", "code=short", validTokenForm, validTokenForm} {
		result, _ := serveCLIToken(h, newCLITokenRequest(form))
		got = append(got, result.Status)
	}
	want := []int{http.StatusBadRequest, http.StatusBadRequest, http.StatusOK, http.StatusTooManyRequests}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("statuses mismatch (-want +got):\n%s", diff)
	}
}

// TestHandleCLITokenThroughSecurityMiddlewares checks that the cross-origin protection in front of
// the API admits the CLI's plain POST, which carries no Origin or fetch metadata, and still blocks
// cross-site browser requests.
func TestHandleCLITokenThroughSecurityMiddlewares(t *testing.T) {
	for fetchSite, wantStatus := range map[string]int{"": http.StatusOK, "cross-site": http.StatusForbidden} {
		a, _ := newCLITokenTest(t, baseLoginConfig(), time.Now().Add(cliLoopbackCodeTTL))
		mux := http.NewServeMux()
		mux.HandleFunc("/api/cli/token", a.HandleCLIToken)
		req := newCLITokenRequest(validTokenForm)
		if fetchSite != "" {
			req.Header.Set("Sec-Fetch-Site", fetchSite)
		}
		if got, _ := serveCLIToken(a.SecurityMiddlewares(mux), req); got.Status != wantStatus {
			t.Errorf("Sec-Fetch-Site %q: status = %d, want %d", fetchSite, got.Status, wantStatus)
		}
	}
}

func TestVerifyPKCES256(t *testing.T) {
	got := []bool{
		verifyPKCES256(cliTestVerifier, cliTestChallenge),     // RFC 7636 appendix B.
		verifyPKCES256(cliTestVerifier+"x", cliTestChallenge), // Wrong verifier.
		verifyPKCES256(cliTestVerifier, cliTestVerifier),      // Plain challenge.
	}
	if diff := cmp.Diff([]bool{true, false, false}, got); diff != "" {
		t.Errorf("verifyPKCES256() mismatch (-want +got):\n%s", diff)
	}
}
