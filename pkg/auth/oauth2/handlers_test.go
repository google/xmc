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
	"strings"
	"testing"
)

func TestLoginReturnPath(t *testing.T) {
	for _, tc := range []struct {
		name     string
		returnTo string
		want     string
	}{
		{name: "default", want: "/experiments"},
		{name: "experiment", returnTo: "/experiments/1", want: "/experiments/1"},
		{name: "query and fragment", returnTo: "/experiments/1?filter=a%2Fb&tab=logs#work-units", want: "/experiments/1?filter=a%2Fb&tab=logs#work-units"},
		{name: "external URL", returnTo: "https://example.com", want: "/experiments"},
		{name: "scheme-relative URL", returnTo: "//example.com", want: "/experiments"},
		{name: "triple-slash path", returnTo: "///example.com/experiments/1", want: "/experiments"},
		{name: "quadruple-slash path", returnTo: "////example.com/experiments/1", want: "/experiments"},
		{name: "encoded multi-slash path", returnTo: "/%2f/example.com/experiments/1", want: "/experiments"},
		{name: "encoded authority", returnTo: "%2f@example.com", want: "/experiments"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newTestAuthenticator(&Config{
				MockAuth:       true,
				JWTSecret:      "test-jwt-secret-key-123456789-abcd",
				FrontendURL:    "https://xmc.example.com",
				AllowedDomains: []string{"mycompany.com"},
			})
			login := httptest.NewRecorder()
			a.HandleLogin(login, httptest.NewRequest(http.MethodGet, "/api/login?return_to="+url.QueryEscape(tc.returnTo), nil))
			loginResponse := login.Result()
			defer loginResponse.Body.Close()
			if loginResponse.StatusCode != http.StatusTemporaryRedirect {
				t.Fatalf("login status = %d, want %d", loginResponse.StatusCode, http.StatusTemporaryRedirect)
			}
			callbackURL := loginResponse.Header.Get("Location")
			callback := httptest.NewRequest(http.MethodGet, callbackURL, nil)
			for _, cookie := range loginResponse.Cookies() {
				callback.AddCookie(cookie)
			}
			response := httptest.NewRecorder()
			a.HandleCallback(response, callback)
			if response.Code != http.StatusTemporaryRedirect {
				t.Fatalf("callback status = %d, want %d: %s", response.Code, http.StatusTemporaryRedirect, response.Body.String())
			}
			if got, want := response.Header().Get("Location"), "https://xmc.example.com"+tc.want; got != want {
				t.Errorf("callback destination = %q, want %q", got, want)
			}
			result := response.Result()
			defer result.Body.Close()
			cookies := make(map[string]*http.Cookie)
			for _, cookie := range result.Cookies() {
				cookies[cookie.Name] = cookie
			}
			if cookie := cookies["oauth_state"]; cookie == nil || cookie.MaxAge != -1 {
				t.Error("callback must clear the state cookie")
			}
			if cookie := cookies["session_token"]; cookie == nil || cookie.Value == "" {
				t.Error("callback must issue a session cookie")
			}
		})
	}
}

func TestLoginReturnPathStateBinding(t *testing.T) {
	a := newTestAuthenticator(&Config{MockAuth: true})
	login := httptest.NewRecorder()
	a.HandleLogin(login, httptest.NewRequest(http.MethodGet, "/api/login?return_to=%2Fexperiments%2F1", nil))
	result := login.Result()
	defer result.Body.Close()
	callbackURL, err := url.Parse(result.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	state := callbackURL.Query().Get("state")
	nonce, _, _ := strings.Cut(state, ".")
	for _, tc := range []struct {
		name  string
		state string
	}{
		{name: "changed path", state: stateWithReturnPath(nonce, "/experiments/2")},
		{name: "removed path", state: nonce},
		{name: "changed nonce", state: stateWithReturnPath("other-nonce", "/experiments/1")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			callback := httptest.NewRequest(http.MethodGet, "/api/callback?code=mock&state="+url.QueryEscape(tc.state), nil)
			for _, cookie := range result.Cookies() {
				callback.AddCookie(cookie)
			}
			response := httptest.NewRecorder()
			a.HandleCallback(response, callback)
			if response.Code != http.StatusBadRequest {
				t.Errorf("callback status = %d, want %d", response.Code, http.StatusBadRequest)
			}
			if got := response.Header().Get("Location"); got != "" {
				t.Errorf("invalid state redirected to %q", got)
			}
			if got := response.Header().Get("Set-Cookie"); got != "" {
				t.Errorf("invalid state set cookie %q", got)
			}
		})
	}
}
