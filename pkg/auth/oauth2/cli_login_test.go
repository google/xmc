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
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

// baseCLILoginQuery is a valid `/api/cli/login` query.
func baseCLILoginQuery() url.Values {
	return url.Values{
		"port":                  {strconv.Itoa(cliTestPort)},
		"state":                 {cliTestState},
		"code_challenge":        {cliTestChallenge},
		"code_challenge_method": {"S256"},
		"login_hint":            {loginTestEmail},
	}
}

func newCLILoginRequest(q url.Values) *http.Request {
	return httptest.NewRequest(http.MethodGet, "https://xmc.example.com/api/cli/login?"+q.Encode(), nil)
}

func TestHandleCLILoginSuccess(t *testing.T) {
	a, _, _ := newCLITestAuthenticator(t, baseLoginConfig())
	req := newCLILoginRequest(baseCLILoginQuery())
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Site", "none")
	w := httptest.NewRecorder()
	a.HandleCLILogin(w, req)
	resp := w.Result()

	if resp.StatusCode != http.StatusTemporaryRedirect {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusTemporaryRedirect)
	}
	stateCookie := responseCookie(resp, "oauth_state")
	if stateCookie == nil || stateCookie.Value == "" {
		t.Fatal("oauth_state cookie not set")
	}
	location, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatalf("Location is not a URL: %v", err)
	}
	wantQuery := url.Values{
		"client_id":     {"test-client"},
		"response_type": {"code"},
		"state":         {stateCookie.Value},
		"login_hint":    {loginTestEmail},
	}
	if diff := cmp.Diff(wantQuery, location.Query()); diff != "" {
		t.Errorf("IdP authorization query mismatch (-want +got):\n%s", diff)
	}

	cliCookie := responseCookie(resp, cliLoginCookieName)
	if cliCookie == nil {
		t.Fatal("cli_login cookie not set")
	}
	gotAttrs := http.Cookie{Path: cliCookie.Path, Domain: cliCookie.Domain, MaxAge: cliCookie.MaxAge, HttpOnly: cliCookie.HttpOnly, Secure: cliCookie.Secure, SameSite: cliCookie.SameSite}
	wantAttrs := http.Cookie{Path: "/api/callback", MaxAge: 300, HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode}
	if diff := cmp.Diff(wantAttrs, gotAttrs); diff != "" {
		t.Errorf("cli_login cookie attributes mismatch (-want +got):\n%s", diff)
	}
	ticket, err := a.openCLILoginTicket(cliCookie.Value, time.Now())
	if err != nil {
		t.Fatalf("cli_login cookie does not open: %v", err)
	}
	wantTicket := baseCLITicket(stateCookie.Value)
	wantTicket.ExpiresAt = ticket.ExpiresAt
	if diff := cmp.Diff(wantTicket, ticket); diff != "" {
		t.Errorf("cli_login ticket mismatch (-want +got):\n%s", diff)
	}
	if ttl := time.Until(time.Unix(ticket.ExpiresAt, 0)); ttl <= 0 || ttl > cliLoginTicketTTL {
		t.Errorf("ticket expires in %v, want within %v", ttl, cliLoginTicketTTL)
	}
}

// TestHandleCLILoginManualMode checks that mode=manual, given instead of a port, issues a manual
// ticket.
func TestHandleCLILoginManualMode(t *testing.T) {
	a, _, _ := newCLITestAuthenticator(t, baseLoginConfig())
	q := baseCLILoginQuery()
	q.Del("port")
	q.Set("mode", "manual")
	w := httptest.NewRecorder()
	a.HandleCLILogin(w, newCLILoginRequest(q))
	resp := w.Result()

	if resp.StatusCode != http.StatusTemporaryRedirect {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusTemporaryRedirect)
	}
	stateCookie, cliCookie := responseCookie(resp, "oauth_state"), responseCookie(resp, cliLoginCookieName)
	if stateCookie == nil || cliCookie == nil {
		t.Fatalf("cookies = %v, want oauth_state and cli_login", resp.Cookies())
	}
	ticket, err := a.openCLILoginTicket(cliCookie.Value, time.Now())
	if err != nil {
		t.Fatalf("cli_login cookie does not open: %v", err)
	}
	wantTicket := baseCLIManualTicket(stateCookie.Value)
	wantTicket.ExpiresAt = ticket.ExpiresAt
	if diff := cmp.Diff(wantTicket, ticket); diff != "" {
		t.Errorf("cli_login ticket mismatch (-want +got):\n%s", diff)
	}
}

func TestHandleCLILoginWithoutLoginHint(t *testing.T) {
	a, _, _ := newCLITestAuthenticator(t, baseLoginConfig())
	q := baseCLILoginQuery()
	q.Del("login_hint")
	w := httptest.NewRecorder()
	a.HandleCLILogin(w, newCLILoginRequest(q))
	location, err := url.Parse(w.Result().Header.Get("Location"))
	if err != nil {
		t.Fatalf("Location is not a URL: %v", err)
	}
	if location.Query().Has("login_hint") {
		t.Errorf("IdP authorization URL %q has a login_hint", location)
	}
}

// TestHandleCLILoginFetchMetadata checks the login CSRF and embedding defenses: only top-level
// navigations the user started (Sec-Fetch-Site "none") or from browsers without fetch metadata
// may start a CLI login.
func TestHandleCLILoginFetchMetadata(t *testing.T) {
	tests := []struct {
		name       string
		headers    map[string][]string
		wantStatus int
	}{
		{name: "opened by the CLI", headers: map[string][]string{"Sec-Fetch-Site": {"none"}, "Sec-Fetch-Dest": {"document"}}, wantStatus: http.StatusTemporaryRedirect},
		{name: "browser without fetch metadata", wantStatus: http.StatusTemporaryRedirect},
		{name: "cross-site navigation", headers: map[string][]string{"Sec-Fetch-Site": {"cross-site"}}, wantStatus: http.StatusForbidden},
		{name: "same-site navigation", headers: map[string][]string{"Sec-Fetch-Site": {"same-site"}}, wantStatus: http.StatusForbidden},
		{name: "same-origin navigation", headers: map[string][]string{"Sec-Fetch-Site": {"same-origin"}}, wantStatus: http.StatusForbidden},
		{name: "repeated Sec-Fetch-Site", headers: map[string][]string{"Sec-Fetch-Site": {"none", "cross-site"}}, wantStatus: http.StatusForbidden},
		{name: "fetched by script", headers: map[string][]string{"Sec-Fetch-Dest": {"empty"}}, wantStatus: http.StatusBadRequest},
		{name: "embedded in an iframe", headers: map[string][]string{"Sec-Fetch-Dest": {"iframe"}}, wantStatus: http.StatusBadRequest},
		{name: "repeated Sec-Fetch-Dest", headers: map[string][]string{"Sec-Fetch-Dest": {"document", "iframe"}}, wantStatus: http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, _, _ := newCLITestAuthenticator(t, baseLoginConfig())
			req := newCLILoginRequest(baseCLILoginQuery())
			for name, values := range tt.headers {
				for _, v := range values {
					req.Header.Add(name, v)
				}
			}
			w := httptest.NewRecorder()
			a.HandleCLILogin(w, req)
			resp := w.Result()
			if resp.StatusCode != tt.wantStatus {
				t.Errorf("status = %d, want %d", resp.StatusCode, tt.wantStatus)
			}
			if gotCookie := responseCookie(resp, cliLoginCookieName) != nil; gotCookie != (tt.wantStatus == http.StatusTemporaryRedirect) {
				t.Errorf("cli_login cookie set = %v, want %v", gotCookie, !gotCookie)
			}
		})
	}
}

func TestHandleCLILoginRejects(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		mutate     func(url.Values)
		disabled   bool
		wantStatus int
	}{
		{name: "disabled", disabled: true, wantStatus: http.StatusNotFound},
		{name: "POST", method: http.MethodPost, wantStatus: http.StatusMethodNotAllowed},
		{name: "invalid parameter", mutate: func(q url.Values) { q.Set("port", "80") }, wantStatus: http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, _, _ := newCLITestAuthenticator(t, baseLoginConfig())
			if tt.disabled {
				a.cliCodes = nil
			}
			q := baseCLILoginQuery()
			if tt.mutate != nil {
				tt.mutate(q)
			}
			req := newCLILoginRequest(q)
			if tt.method != "" {
				req.Method = tt.method
			}
			w := httptest.NewRecorder()
			a.HandleCLILogin(w, req)
			resp := w.Result()
			if resp.StatusCode != tt.wantStatus {
				t.Errorf("status = %d, want %d", resp.StatusCode, tt.wantStatus)
			}
			if len(resp.Cookies()) != 0 {
				t.Errorf("rejected request set cookies %v", resp.Cookies())
			}
		})
	}
}

func TestHandleCLILoginRandomFailure(t *testing.T) {
	a, _, _ := newCLITestAuthenticator(t, baseLoginConfig())
	withFailingRandom(t)
	w := httptest.NewRecorder()
	a.HandleCLILogin(w, newCLILoginRequest(baseCLILoginQuery()))
	if got := w.Result().StatusCode; got != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", got, http.StatusInternalServerError)
	}
}

func TestParseCLILoginRequest(t *testing.T) {
	base := cliLoginRequest{mode: cliLoginModeLoopback, port: cliTestPort, state: cliTestState, codeChallenge: cliTestChallenge, loginHint: loginTestEmail}
	manual := func(q url.Values) {
		q.Del("port")
		q.Set("mode", "manual")
	}
	tests := []struct {
		name    string
		mutate  func(url.Values)
		want    cliLoginRequest
		wantErr string
	}{
		{name: "valid", want: base},
		{name: "lowest port", mutate: func(q url.Values) { q.Set("port", "1024") }, want: cliLoginRequest{mode: base.mode, port: 1024, state: base.state, codeChallenge: base.codeChallenge, loginHint: base.loginHint}},
		{name: "highest port", mutate: func(q url.Values) { q.Set("port", "65535") }, want: cliLoginRequest{mode: base.mode, port: 65535, state: base.state, codeChallenge: base.codeChallenge, loginHint: base.loginHint}},
		{name: "no login hint", mutate: func(q url.Values) { q.Del("login_hint") }, want: cliLoginRequest{mode: base.mode, port: base.port, state: base.state, codeChallenge: base.codeChallenge}},
		{name: "manual mode", mutate: manual, want: cliLoginRequest{mode: cliLoginModeManual, state: base.state, codeChallenge: base.codeChallenge, loginHint: base.loginHint}},
		{name: "port and manual mode", mutate: func(q url.Values) { q.Set("mode", "manual") }, wantErr: "exactly one of port and mode=manual"},
		{name: "missing port", mutate: func(q url.Values) { q.Del("port") }, wantErr: "exactly one of port and mode=manual"},
		{name: "unknown mode", mutate: func(q url.Values) { manual(q); q.Set("mode", "loopback") }, wantErr: "mode must be manual"},
		{name: "repeated mode", mutate: func(q url.Values) { manual(q); q.Add("mode", "manual") }, wantErr: "mode must not be repeated"},
		{name: "non-numeric port", mutate: func(q url.Values) { q.Set("port", "80a") }, wantErr: "port"},
		{name: "signed port", mutate: func(q url.Values) { q.Set("port", "+8080") }, wantErr: "port"},
		{name: "privileged port", mutate: func(q url.Values) { q.Set("port", "1023") }, wantErr: "port"},
		{name: "port above range", mutate: func(q url.Values) { q.Set("port", "65536") }, wantErr: "port"},
		{name: "overlong port", mutate: func(q url.Values) { q.Set("port", "000008080") }, wantErr: "port"},
		{name: "repeated port", mutate: func(q url.Values) { q.Add("port", "8080") }, wantErr: "port must not be repeated"},
		{name: "missing state", mutate: func(q url.Values) { q.Del("state") }, wantErr: "state"},
		{name: "state with forbidden characters", mutate: func(q url.Values) { q.Set("state", "a&b=c") }, wantErr: "state"},
		{name: "overlong state", mutate: func(q url.Values) { q.Set("state", strings.Repeat("a", 129)) }, wantErr: "state"},
		{name: "missing challenge", mutate: func(q url.Values) { q.Del("code_challenge") }, wantErr: "code_challenge"},
		{name: "short challenge", mutate: func(q url.Values) { q.Set("code_challenge", cliTestChallenge[:42]) }, wantErr: "code_challenge"},
		{name: "padded challenge", mutate: func(q url.Values) { q.Set("code_challenge", cliTestChallenge[:42]+"=") }, wantErr: "code_challenge"},
		{name: "plain method", mutate: func(q url.Values) { q.Set("code_challenge_method", "plain") }, wantErr: "code_challenge_method"},
		{name: "missing method", mutate: func(q url.Values) { q.Del("code_challenge_method") }, wantErr: "code_challenge_method"},
		{name: "login hint with display name", mutate: func(q url.Values) { q.Set("login_hint", "User <user@mycompany.com>") }, wantErr: "login_hint"},
		{name: "login hint not an email", mutate: func(q url.Values) { q.Set("login_hint", "user") }, wantErr: "login_hint"},
		{name: "overlong login hint", mutate: func(q url.Values) { q.Set("login_hint", strings.Repeat("a", 64)+"@"+strings.Repeat("b", 190)+".com") }, wantErr: "login_hint"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := baseCLILoginQuery()
			if tt.mutate != nil {
				tt.mutate(q)
			}
			got, err := parseCLILoginRequest(q)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("parseCLILoginRequest() error = %v, want one mentioning %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseCLILoginRequest() failed: %v", err)
			}
			if diff := cmp.Diff(tt.want, got, cmp.AllowUnexported(cliLoginRequest{})); diff != "" {
				t.Errorf("parseCLILoginRequest() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestClearCLILoginCookie(t *testing.T) {
	a := NewAuthenticator(baseLoginConfig())
	w := httptest.NewRecorder()
	a.clearCLILoginCookie(w, httptest.NewRequest(http.MethodGet, "https://xmc.example.com/api/login", nil))
	got := responseCookie(w.Result(), cliLoginCookieName)
	if got == nil {
		t.Fatal("cli_login deletion cookie not set")
	}
	gotAttrs := http.Cookie{Value: got.Value, Path: got.Path, Domain: got.Domain, MaxAge: got.MaxAge, HttpOnly: got.HttpOnly, Secure: got.Secure, SameSite: got.SameSite}
	wantAttrs := http.Cookie{Path: "/api/callback", MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode}
	if diff := cmp.Diff(wantAttrs, gotAttrs); diff != "" {
		t.Errorf("cli_login deletion cookie mismatch (-want +got):\n%s", diff)
	}
}
