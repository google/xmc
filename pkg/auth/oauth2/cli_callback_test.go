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
	"net/url"
	"testing"
	"time"

	"cloud.google.com/go/iam/apiv1/iampb"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

// cliLoopback is the loopback callback URL the browser is sent to for the shared test ticket.
const cliLoopback = "http://127.0.0.1:51234/callback"

// idpCode is the query of an identity provider redirect that carries an authorization code.
const idpCode = "&code=idp-code"

func TestHandleCallbackCLILoginSuccess(t *testing.T) {
	a, store, provider := newCLITestAuthenticator(t, baseLoginConfig())
	provider.userInfo.Email = "  User@MyCompany.com "
	resp := serveCallback(a, newCLICallbackRequest(idpCode, mustSealCLILoginTicket(t, a, baseCLITicket(loginTestOAuthState))))

	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusFound)
	}
	location, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatalf("Location is not a URL: %v", err)
	}
	code := location.Query().Get("code")
	if !base64URL256BitPattern.MatchString(code) {
		t.Fatalf("code = %q, want 43 base64url characters", code)
	}
	wantLocation := cliLoopback + "?" + url.Values{"code": {code}, "state": {cliTestState}}.Encode()
	if got := location.String(); got != wantLocation {
		t.Errorf("Location = %q, want %q", got, wantLocation)
	}
	wantHeaders := map[string]string{"Cache-Control": "no-store", "Referrer-Policy": "no-referrer"}
	gotHeaders := map[string]string{"Cache-Control": resp.Header.Get("Cache-Control"), "Referrer-Policy": resp.Header.Get("Referrer-Policy")}
	if diff := cmp.Diff(wantHeaders, gotHeaders); diff != "" {
		t.Errorf("response headers mismatch (-want +got):\n%s", diff)
	}

	stored := store.only(t)
	want := CLILoginCode{
		Hash:          hashCLILoginCode(code),
		Email:         loginTestEmail,
		Name:          "Test User",
		CodeChallenge: cliTestChallenge,
	}
	if diff := cmp.Diff(want, stored, cmpopts.IgnoreFields(CLILoginCode{}, "ExpiresAt")); diff != "" {
		t.Errorf("stored code mismatch (-want +got):\n%s", diff)
	}
	if ttl := time.Until(stored.ExpiresAt); ttl <= 0 || ttl > cliLoopbackCodeTTL {
		t.Errorf("stored code expires in %v, want within %v", ttl, cliLoopbackCodeTTL)
	}
	if c := responseCookie(resp, cliLoginCookieName); c == nil || c.MaxAge != -1 {
		t.Errorf("cli_login cookie = %v, want it deleted", c)
	}
	if c := responseCookie(resp, "session_token"); c != nil {
		t.Error("CLI login created a Dashboard session cookie")
	}
}

func TestHandleCallbackCLILoginErrors(t *testing.T) {
	errStore := errors.New("spanner unavailable")
	tests := []struct {
		name string
		// query defaults to idpCode.
		query        string
		cfg          func(*Config)
		setup        func(*Authenticator, *fakeCLICodeStore, *stubOAuthProvider)
		random       bool
		loopbackOnly bool
		want         string
	}{
		{name: "user cancelled at the identity provider", query: "&error=access_denied", want: cliErrorAccessDenied},
		{name: "identity provider returned no code", query: "&unused=1", want: cliErrorAccessDenied},
		{
			name: "code exchange failure",
			setup: func(_ *Authenticator, _ *fakeCLICodeStore, p *stubOAuthProvider) {
				p.err = errors.New("token endpoint unavailable")
			},
			want: cliErrorServerError,
		},
		{
			name:  "user not admitted",
			setup: func(_ *Authenticator, _ *fakeCLICodeStore, p *stubOAuthProvider) { p.userInfo.Email = "user@other.com" },
			want:  cliErrorAccessDenied,
		},
		{
			name:  "email not verified",
			setup: func(_ *Authenticator, _ *fakeCLICodeStore, p *stubOAuthProvider) { p.userInfo.EmailVerified = false },
			want:  cliErrorAccessDenied,
		},
		{
			name: "authorization pipeline failure",
			cfg:  func(c *Config) { c.AllowedDomains = nil; c.GCPProjectID = "test-project" },
			setup: func(a *Authenticator, _ *fakeCLICodeStore, _ *stubOAuthProvider) {
				a.PolicyFetcher = func(context.Context) (*iampb.Policy, error) { return nil, errors.New("iam unavailable") }
			},
			want: cliErrorServerError,
		},
		{
			name:         "code store failure",
			setup:        func(_ *Authenticator, s *fakeCLICodeStore, _ *stubOAuthProvider) { s.saveErr = errStore },
			loopbackOnly: true,
			want:         cliErrorServerError,
		},
		{name: "random source failure", random: true, loopbackOnly: true, want: cliErrorServerError},
	}
	// Loopback logins report the error to the loopback listener; manual logins, which have none,
	// show an error page.
	manualStatus := map[string]int{cliErrorAccessDenied: http.StatusForbidden, cliErrorServerError: http.StatusInternalServerError}
	for _, ticket := range []cliLoginTicket{baseCLITicket(loginTestOAuthState), baseCLIManualTicket(loginTestOAuthState)} {
		for _, tt := range tests {
			if ticket.Mode == cliLoginModeManual && tt.loopbackOnly {
				continue
			}
			t.Run(ticket.Mode+"/"+tt.name, func(t *testing.T) {
				cfg := baseLoginConfig()
				if tt.cfg != nil {
					tt.cfg(cfg)
				}
				a, store, provider := newCLITestAuthenticator(t, cfg)
				if tt.setup != nil {
					tt.setup(a, store, provider)
				}
				query := idpCode
				if tt.query != "" {
					query = tt.query
				}
				req := newCLICallbackRequest(query, mustSealCLILoginTicket(t, a, ticket))
				if tt.random {
					withFailingRandom(t)
				}
				resp := serveCallback(a, req)
				got, _ := readCLIPage(resp)

				want := cliRedirect(cliLoopback + "?" + url.Values{"error": {tt.want}, "state": {cliTestState}}.Encode())
				if ticket.Mode == cliLoginModeManual {
					want = cliPage(manualStatus[tt.want], "Sign-in failed")
				}
				if diff := cmp.Diff(want, got); diff != "" {
					t.Errorf("response mismatch (-want +got):\n%s", diff)
				}
				if len(store.codes) != 0 {
					t.Errorf("store holds %d codes, want none", len(store.codes))
				}
			})
		}
	}
}

// TestHandleCallbackIgnoresInvalidCLILoginCookie checks that a cli_login cookie that is forged,
// expired, bound to another round trip or unusable never takes the CLI branch: the callback stays a
// Dashboard login and the cookie is deleted.
func TestHandleCallbackIgnoresInvalidCLILoginCookie(t *testing.T) {
	expired := baseCLITicket(loginTestOAuthState)
	expired.ExpiresAt = time.Now().Add(-time.Second).Unix()
	unsupported := baseCLITicket(loginTestOAuthState)
	unsupported.Mode = "device"
	tests := []struct {
		name     string
		cookie   func(*Authenticator) string
		disabled bool
	}{
		{name: "forged", cookie: func(*Authenticator) string { return "e30.AAAA" }},
		{name: "expired", cookie: func(a *Authenticator) string { return mustSealCLILoginTicket(t, a, expired) }},
		{name: "another round trip", cookie: func(a *Authenticator) string { return mustSealCLILoginTicket(t, a, baseCLITicket("other-state")) }},
		{name: "unsupported mode", cookie: func(a *Authenticator) string { return mustSealCLILoginTicket(t, a, unsupported) }},
		{
			name:     "CLI login disabled",
			cookie:   func(a *Authenticator) string { return mustSealCLILoginTicket(t, a, baseCLITicket(loginTestOAuthState)) },
			disabled: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, store, _ := newCLITestAuthenticator(t, baseLoginConfig())
			cookie := tt.cookie(a)
			if tt.disabled {
				a.cliCodes = nil
			}
			resp := serveCallback(a, newCLICallbackRequest(idpCode, cookie))

			if got, want := resp.Header.Get("Location"), "https://xmc.example.com/experiments"; got != want {
				t.Errorf("Location = %q, want the Dashboard %q", got, want)
			}
			if responseCookie(resp, "session_token") == nil {
				t.Error("Dashboard session cookie not set")
			}
			if c := responseCookie(resp, cliLoginCookieName); c == nil || c.MaxAge != -1 {
				t.Errorf("cli_login cookie = %v, want it deleted", c)
			}
			if len(store.codes) != 0 {
				t.Errorf("store holds %d codes, want none", len(store.codes))
			}
		})
	}
}

// TestHandleCallbackDashboardErrorsUnchanged checks that without a CLI ticket the callback keeps
// its Dashboard error responses.
func TestHandleCallbackDashboardErrorsUnchanged(t *testing.T) {
	tests := []struct {
		name       string
		query      string
		exchange   error
		wantStatus int
	}{
		{name: "missing code", query: "&error=access_denied", wantStatus: http.StatusBadRequest},
		{name: "code exchange failure", query: idpCode, exchange: errors.New("token endpoint unavailable"), wantStatus: http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, _, provider := newCLITestAuthenticator(t, baseLoginConfig())
			provider.err = tt.exchange
			if got := serveCallback(a, newCLICallbackRequest(tt.query, "")).StatusCode; got != tt.wantStatus {
				t.Errorf("status = %d, want %d", got, tt.wantStatus)
			}
		})
	}
}

func TestHandleCallbackDashboardLeavesNoCLICookie(t *testing.T) {
	a, _, _ := newCLITestAuthenticator(t, baseLoginConfig())
	resp := serveCallback(a, newCLICallbackRequest(idpCode, ""))
	if got, want := resp.Header.Get("Location"), "https://xmc.example.com/experiments"; got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
	if c := responseCookie(resp, cliLoginCookieName); c != nil {
		t.Errorf("Dashboard login without a cli_login cookie wrote one: %v", c)
	}
}
