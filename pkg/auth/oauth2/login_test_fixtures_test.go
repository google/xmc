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
	"net/http/httptest"

	"golang.org/x/oauth2"
)

// Shared fixtures for the login handler tests.
const (
	loginTestSecret     = "login-test-secret-0123456789abcdefgh"
	loginTestEmail      = "user@mycompany.com"
	loginTestIdPAuthURL = "https://idp.example.com/auth"
	loginTestOAuthState = "0123456789abcdef0123456789abcdef"
)

// stubOAuthProvider returns fixed userinfo and builds authorization URLs for loginTestIdPAuthURL.
type stubOAuthProvider struct {
	userInfo *GoogleUserInfo
	err      error
}

func (p *stubOAuthProvider) AuthCodeURL(state string, _ *http.Request) string {
	cfg := &oauth2.Config{ClientID: "test-client", Endpoint: oauth2.Endpoint{AuthURL: loginTestIdPAuthURL}}
	return cfg.AuthCodeURL(state)
}

func (p *stubOAuthProvider) ExchangeAndGetUserInfo(context.Context, *http.Request, string) (*GoogleUserInfo, error) {
	return p.userInfo, p.err
}

// baseLoginUserInfo is a verified Google Workspace user of the allowlisted domain.
func baseLoginUserInfo() *GoogleUserInfo {
	return &GoogleUserInfo{Email: loginTestEmail, EmailVerified: true, HostedDomain: "mycompany.com", Name: "Test User"}
}

// baseLoginConfig is a Google-backed deployment that admits mycompany.com.
func baseLoginConfig() *Config {
	return &Config{
		JWTSecret:      loginTestSecret,
		AllowedDomains: []string{"mycompany.com"},
		TargetIAMRole:  "roles/viewer",
		AuthURL:        "https://accounts.google.com/o/oauth2/v2/auth",
		FrontendURL:    "https://xmc.example.com",
		AllowedHosts:   []string{"xmc.example.com"},
	}
}

// newLoginTestAuthenticator returns an Authenticator whose identity provider returns
// baseLoginUserInfo.
func newLoginTestAuthenticator(cfg *Config) (*Authenticator, *stubOAuthProvider) {
	a := NewAuthenticator(cfg)
	provider := &stubOAuthProvider{userInfo: baseLoginUserInfo()}
	a.OAuthProvider = provider
	return a, provider
}

// newCallbackRequest is the identity provider's redirect to /api/callback with query for a round
// trip that used loginTestOAuthState, carrying cookies in addition to oauth_state.
func newCallbackRequest(query string, cookies ...*http.Cookie) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "https://xmc.example.com/api/callback?state="+loginTestOAuthState+query, nil)
	req.AddCookie(&http.Cookie{Name: "oauth_state", Value: loginTestOAuthState})
	for _, c := range cookies {
		req.AddCookie(c)
	}
	return req
}

func serveCallback(a *Authenticator, req *http.Request) *http.Response {
	w := httptest.NewRecorder()
	a.HandleCallback(w, req)
	return w.Result()
}

// responseCookie returns the named cookie set by the response, or nil.
func responseCookie(resp *http.Response, name string) *http.Cookie {
	for _, c := range resp.Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}
