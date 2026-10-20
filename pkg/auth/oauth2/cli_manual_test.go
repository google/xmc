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
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/iam/apiv1/iampb"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

// cliCodePagePattern extracts the one-time code from the code page.
var cliCodePagePattern = regexp.MustCompile(`<code style="[^"]*">([A-Za-z0-9_-]{43})</code>`)

// baseCLIManualConfirmation is a pending manual login of the shared test user.
func baseCLIManualConfirmation() cliManualConfirmation {
	return cliManualConfirmation{
		Email:         loginTestEmail,
		Name:          "Test User",
		CodeChallenge: cliTestChallenge,
		ExpiresAt:     time.Now().Add(cliManualConfirmationTTL).Unix(),
	}
}

func mustSealConfirmation(t *testing.T, a *Authenticator, c cliManualConfirmation) string {
	t.Helper()
	return mustSealSigned(t, a.JWTSecret, cliConfirmationAudience, c)
}

func mustSealSigned(t *testing.T, key []byte, audience string, c cliManualConfirmation) string {
	t.Helper()
	sealed, err := sealSigned(key, audience, c, c.ExpiresAt)
	if err != nil {
		t.Fatalf("sealSigned() failed: %v", err)
	}
	return sealed
}

// newCLICodeRequest is the interstitial's same-origin form POST to /api/cli/code.
func newCLICodeRequest(form url.Values) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "https://xmc.example.com/api/cli/code", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Sec-Fetch-Dest", "document")
	return req
}

func confirmForm(confirmation string) url.Values {
	return actionForm("continue", confirmation)
}

func actionForm(action, confirmation string) url.Values {
	return url.Values{"action": {action}, "confirmation": {confirmation}}
}

func serveCLICode(h http.Handler, req *http.Request) (cliPageResult, string) {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return readCLIPage(w.Result())
}

// confirmationFromPage returns the sealed confirmation carried by the interstitial's form.
func confirmationFromPage(body string) string {
	return between(body, `name="confirmation" value="`, `"`)
}

func TestHandleCallbackCLIManualAsksForConfirmation(t *testing.T) {
	a, store, provider := newCLITestAuthenticator(t, baseLoginConfig())
	provider.userInfo.Email = "  User@MyCompany.com "
	resp := serveCallback(a, newCLICallbackRequest(idpCode, mustSealCLILoginTicket(t, a, baseCLIManualTicket(loginTestOAuthState))))
	got, body := readCLIPage(resp)

	if diff := cmp.Diff(cliPage(http.StatusOK, "Sign in to the XMC command-line tool as user@mycompany.com?"), got); diff != "" {
		t.Fatalf("callback response mismatch (-want +got):\n%s", diff)
	}
	if !strings.Contains(body, `<form method="post" action="/api/cli/code">`) {
		t.Errorf("interstitial has no form posting to /api/cli/code:\n%s", body)
	}
	confirmation, err := a.openCLIManualConfirmation(confirmationFromPage(body), time.Now())
	if err != nil {
		t.Fatalf("interstitial confirmation does not open: %v", err)
	}
	if diff := cmp.Diff(baseCLIManualConfirmation(), confirmation, cmpopts.IgnoreFields(cliManualConfirmation{}, "ExpiresAt")); diff != "" {
		t.Errorf("confirmation mismatch (-want +got):\n%s", diff)
	}
	if ttl := time.Until(time.Unix(confirmation.ExpiresAt, 0)); ttl <= 0 || ttl > cliManualConfirmationTTL {
		t.Errorf("confirmation expires in %v, want within %v", ttl, cliManualConfirmationTTL)
	}
	// No login code exists before the user confirms.
	if len(store.codes) != 0 {
		t.Errorf("store holds %d records before confirmation, want 0", len(store.codes))
	}
	if c := responseCookie(resp, cliLoginCookieName); c == nil || c.MaxAge != -1 {
		t.Errorf("cli_login cookie = %v, want it deleted", c)
	}
	if c := responseCookie(resp, "session_token"); c != nil {
		t.Error("CLI login created a Dashboard session cookie")
	}
}

func TestHandleCallbackCLIManualEscapesIdentity(t *testing.T) {
	a, _, provider := newCLITestAuthenticator(t, baseLoginConfig())
	provider.userInfo.Email = "<script>alert(1)</script>@mycompany.com"
	provider.userInfo.Name = "<script>alert(2)</script>"
	got, body := readCLIPage(serveCallback(a, newCLICallbackRequest(idpCode, mustSealCLILoginTicket(t, a, baseCLIManualTicket(loginTestOAuthState)))))

	want := cliPage(http.StatusOK, "Sign in to the XMC command-line tool as &lt;script&gt;alert(1)&lt;/script&gt;@mycompany.com?")
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("callback response mismatch (-want +got):\n%s", diff)
	}
	if strings.Contains(body, "<script") {
		t.Errorf("interstitial reflects unescaped markup:\n%s", body)
	}
}

func TestHandleCLICodeIssuesCode(t *testing.T) {
	a, store, _ := newCLITestAuthenticator(t, baseLoginConfig())
	sealed := mustSealConfirmation(t, a, baseCLIManualConfirmation())
	got, body := serveCLICode(http.HandlerFunc(a.HandleCLICode), newCLICodeRequest(confirmForm(sealed)))

	if diff := cmp.Diff(cliPage(http.StatusOK, "Your sign-in code"), got); diff != "" {
		t.Fatalf("code page mismatch (-want +got):\n%s", diff)
	}
	match := cliCodePagePattern.FindStringSubmatch(body)
	if match == nil {
		t.Fatalf("code page shows no code:\n%s", body)
	}
	stored := store.only(t)
	want := CLILoginCode{Hash: hashCLILoginCode(match[1]), Email: loginTestEmail, Name: "Test User", CodeChallenge: cliTestChallenge}
	if diff := cmp.Diff(want, stored, cmpopts.IgnoreFields(CLILoginCode{}, "ExpiresAt")); diff != "" {
		t.Errorf("stored code mismatch (-want +got):\n%s", diff)
	}
	if ttl := time.Until(stored.ExpiresAt); ttl <= cliLoopbackCodeTTL || ttl > cliManualCodeTTL {
		t.Errorf("stored code expires in %v, want within (%v, %v]", ttl, cliLoopbackCodeTTL, cliManualCodeTTL)
	}
}

func TestHandleCLICodeRejects(t *testing.T) {
	expired := baseCLIManualConfirmation()
	expired.ExpiresAt = time.Now().Add(-time.Second).Unix()
	revoked := baseCLIManualConfirmation()
	revoked.Email = "user@other.com"
	errStore := errors.New("spanner unavailable")
	tests := []struct {
		name string
		cfg  func(*Config)
		// confirmation is the pending confirmation sealed; it defaults to baseCLIManualConfirmation.
		confirmation *cliManualConfirmation
		// form builds the request form from the sealed confirmation; it defaults to confirmForm.
		form  func(sealed string) url.Values
		req   func(*http.Request)
		setup func(*Authenticator, *fakeCLICodeStore)
		want  cliPageResult
	}{
		{name: "cancel", form: func(s string) url.Values { return actionForm("cancel", s) }, want: cliPage(http.StatusOK, "Sign-in cancelled")},
		{name: "cancel with a tampered confirmation", form: func(s string) url.Values { return actionForm("cancel", "x"+s) }, want: cliPage(http.StatusBadRequest, "Sign-in expired")},
		{name: "missing action", form: func(s string) url.Values { return url.Values{"confirmation": {s}} }, want: cliPageResult{Status: http.StatusBadRequest}},
		{name: "unknown action", form: func(s string) url.Values { return actionForm("approve", s) }, want: cliPageResult{Status: http.StatusBadRequest}},
		{name: "tampered confirmation", form: func(s string) url.Values { return confirmForm("x" + s) }, want: cliPage(http.StatusBadRequest, "Sign-in expired")},
		{name: "expired confirmation", confirmation: &expired, want: cliPage(http.StatusBadRequest, "Sign-in expired")},
		{name: "admission revoked since the callback", confirmation: &revoked, want: cliPage(http.StatusForbidden, "Sign-in failed")},
		{
			name: "authorization pipeline failure",
			cfg:  func(c *Config) { c.AllowedDomains = nil; c.GCPProjectID = "test-project" },
			setup: func(a *Authenticator, _ *fakeCLICodeStore) {
				a.PolicyFetcher = func(context.Context) (*iampb.Policy, error) { return nil, errors.New("iam unavailable") }
			},
			want: cliPage(http.StatusInternalServerError, "Sign-in failed"),
		},
		{name: "code store failure on save", setup: func(_ *Authenticator, s *fakeCLICodeStore) { s.saveErr = errStore }, want: cliPage(http.StatusInternalServerError, "Sign-in failed")},
		{name: "cross-site POST", req: func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") }, want: cliPageResult{Status: http.StatusForbidden}},
		{name: "same-site POST", req: func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "same-site") }, want: cliPageResult{Status: http.StatusForbidden}},
		{name: "submitted by script", req: func(r *http.Request) { r.Header.Set("Sec-Fetch-Dest", "empty") }, want: cliPageResult{Status: http.StatusBadRequest}},
		{name: "GET", req: func(r *http.Request) { r.Method = http.MethodGet }, want: cliPageResult{Status: http.StatusMethodNotAllowed}},
		{name: "not form-encoded", req: func(r *http.Request) { r.Header.Set("Content-Type", "application/json") }, want: cliPageResult{Status: http.StatusBadRequest}},
		{name: "oversized form", form: func(s string) url.Values { return actionForm(strings.Repeat("a", maxCLICodeRequestBytes), s) }, want: cliPageResult{Status: http.StatusBadRequest}},
		{name: "repeated confirmation", form: func(s string) url.Values { return url.Values{"confirmation": {s, s}} }, want: cliPageResult{Status: http.StatusBadRequest}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := baseLoginConfig()
			if tt.cfg != nil {
				tt.cfg(cfg)
			}
			a, store, _ := newCLITestAuthenticator(t, cfg)
			confirmation := baseCLIManualConfirmation()
			if tt.confirmation != nil {
				confirmation = *tt.confirmation
			}
			sealed := mustSealConfirmation(t, a, confirmation)
			if tt.setup != nil {
				tt.setup(a, store)
			}
			form := confirmForm(sealed)
			if tt.form != nil {
				form = tt.form(sealed)
			}
			req := newCLICodeRequest(form)
			if tt.req != nil {
				tt.req(req)
			}
			got, _ := serveCLICode(http.HandlerFunc(a.HandleCLICode), req)
			if tt.want.Status != http.StatusOK && tt.want.Title == "" {
				// Malformed requests get a plain-text error; only the status matters.
				got = cliPageResult{Status: got.Status}
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("HandleCLICode() mismatch (-want +got):\n%s", diff)
			}
			if len(store.codes) != 0 {
				t.Errorf("store = %d records, want no login code", len(store.codes))
			}
		})
	}
}

func TestHandleCLICodeRandomFailure(t *testing.T) {
	a, _, _ := newCLITestAuthenticator(t, baseLoginConfig())
	sealed := mustSealConfirmation(t, a, baseCLIManualConfirmation())
	withFailingRandom(t)
	got, _ := serveCLICode(http.HandlerFunc(a.HandleCLICode), newCLICodeRequest(confirmForm(sealed)))
	if diff := cmp.Diff(cliPage(http.StatusInternalServerError, "Sign-in failed"), got); diff != "" {
		t.Errorf("HandleCLICode() mismatch (-want +got):\n%s", diff)
	}
}

func TestHandleCLICodeDisabled(t *testing.T) {
	a, _, _ := newCLITestAuthenticator(t, baseLoginConfig())
	a.cliCodes = nil
	if got, _ := serveCLICode(http.HandlerFunc(a.HandleCLICode), newCLICodeRequest(confirmForm("x"))); got.Status != http.StatusNotFound {
		t.Errorf("status = %d, want %d", got.Status, http.StatusNotFound)
	}
}

// TestCLIManualLoginEndToEnd runs a manual login through the security middlewares: start, IdP
// callback, confirmation, and token exchange with the PKCE verifier.
func TestCLIManualLoginEndToEnd(t *testing.T) {
	a, _, _ := newCLITestAuthenticator(t, baseLoginConfig())
	mux := http.NewServeMux()
	mux.HandleFunc("/api/cli/login", a.HandleCLILogin)
	mux.HandleFunc("/api/callback", a.HandleCallback)
	mux.HandleFunc("/api/cli/code", a.HandleCLICode)
	mux.HandleFunc("/api/cli/token", a.HandleCLIToken)
	h := a.SecurityMiddlewares(mux)

	q := baseCLILoginQuery()
	q.Del("port")
	q.Set("mode", "manual")
	start := httptest.NewRecorder()
	h.ServeHTTP(start, newCLILoginRequest(q))
	cookies := start.Result().Cookies()
	oauthState := responseCookie(start.Result(), "oauth_state")
	if oauthState == nil {
		t.Fatalf("login set cookies %v, want oauth_state", cookies)
	}

	callback := httptest.NewRequest(http.MethodGet, "https://xmc.example.com/api/callback?state="+oauthState.Value+idpCode, nil)
	for _, c := range cookies {
		callback.AddCookie(c)
	}
	page, body := serveCLICode(h, callback)
	if page.Status != http.StatusOK {
		t.Fatalf("callback status = %d, want %d", page.Status, http.StatusOK)
	}

	crossSite := newCLICodeRequest(confirmForm(confirmationFromPage(body)))
	crossSite.Header.Set("Sec-Fetch-Site", "cross-site")
	if got, _ := serveCLICode(h, crossSite); got.Status != http.StatusForbidden {
		t.Errorf("cross-site confirmation status = %d, want %d", got.Status, http.StatusForbidden)
	}

	page, body = serveCLICode(h, newCLICodeRequest(confirmForm(confirmationFromPage(body))))
	match := cliCodePagePattern.FindStringSubmatch(body)
	if page.Status != http.StatusOK || match == nil {
		t.Fatalf("confirmation = %d with body:\n%s\nwant a code page", page.Status, body)
	}

	got, token := serveCLIToken(h, newCLITokenRequest(cliTokenForm(match[1], cliTestVerifier)))
	if got.Status != http.StatusOK || token.Email != loginTestEmail || token.AccessToken == "" {
		t.Errorf("token exchange = %+v %+v, want 200 with a token for %s", got, token, loginTestEmail)
	}
}

func TestOpenCLIManualConfirmationRejects(t *testing.T) {
	a, _, _ := newCLITestAuthenticator(t, baseLoginConfig())
	expired := baseCLIManualConfirmation()
	expired.ExpiresAt = time.Now().Unix()
	tests := []struct {
		name   string
		sealed string
	}{
		{name: "malformed", sealed: "not-sealed"},
		{name: "sealed with the ticket audience", sealed: mustSealSigned(t, a.JWTSecret, cliLoginTicketAudience, baseCLIManualConfirmation())},
		{name: "expired", sealed: mustSealSigned(t, a.JWTSecret, cliConfirmationAudience, expired)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := a.openCLIManualConfirmation(tt.sealed, time.Now()); !errors.Is(err, errInvalidCLIConfirmation) {
				t.Errorf("openCLIManualConfirmation() error = %v, want %v", err, errInvalidCLIConfirmation)
			}
		})
	}
}
