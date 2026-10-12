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
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/xmc/internal/logging"
	"golang.org/x/oauth2"
)

var randReader io.Reader = rand.Reader

// GoogleUserInfo holds fields returned by OpenID Connect userinfo endpoint.
type GoogleUserInfo struct {
	Email   string `json:"email"`
	Name    string `json:"name"`
	Picture string `json:"picture"`
}

// HandleLogin serves the initial redirection endpoint.
func (a *Authenticator) HandleLogin(w http.ResponseWriter, r *http.Request) {
	state, err := GenerateRandomString()
	if err != nil {
		http.Error(w, "Failed to generate secure random state", http.StatusInternalServerError)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "oauth_state",
		Value:    state,
		Path:     "/",
		Domain:   a.getCookieDomain(r),
		Expires:  time.Now().Add(5 * time.Minute),
		HttpOnly: true,
		Secure:   a.isCookieSecure(r),
		SameSite: http.SameSiteLaxMode,
	})

	authURL := a.OAuthProvider.AuthCodeURL(state, r)
	http.Redirect(w, r, authURL, http.StatusTemporaryRedirect)
}

// HandleCallback processes the authentication callback.
func (a *Authenticator) HandleCallback(w http.ResponseWriter, r *http.Request) {
	_, err := a.validateState(r)
	if err != nil {
		http.Error(w, fmt.Sprintf("State validation failed: %v", err), http.StatusBadRequest)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "oauth_state",
		Value:    "",
		Path:     "/",
		Domain:   a.getCookieDomain(r),
		MaxAge:   -1,
		HttpOnly: true,
	})

	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "Code parameter is missing", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	userInfo, err := a.OAuthProvider.ExchangeAndGetUserInfo(ctx, r, code)
	if err != nil {
		logging.Error(ctx, "[Callback Auth] Failed to get user info", "error", err)
		http.Error(w, "Failed to get user info", http.StatusInternalServerError)
		return
	}

	dynamicFrontendURL := a.getDynamicFrontendURL(r)

	authorized, err := a.IsUserAuthorized(ctx, userInfo.Email)
	if err != nil {
		logging.Error(ctx, "[Callback Auth] Authorization pipeline check failed", "error", err)
		permissionErrorURL := fmt.Sprintf("%s/?error=permission_error", dynamicFrontendURL)
		http.Redirect(w, r, permissionErrorURL, http.StatusTemporaryRedirect)
		return
	}

	if !authorized {
		logging.Warn(ctx, "[Callback Auth] Access forbidden: user is not authorized", "email", userInfo.Email)
		forbiddenURL := fmt.Sprintf("%s/?error=forbidden&email=%s", dynamicFrontendURL, url.QueryEscape(userInfo.Email))
		http.Redirect(w, r, forbiddenURL, http.StatusTemporaryRedirect)
		return
	}

	if err := a.createSession(w, r, userInfo.Email, userInfo.Name, userInfo.Picture); err != nil {
		http.Error(w, "Failed to create session", http.StatusInternalServerError)
		return
	}

	dashboardURL := fmt.Sprintf("%s/experiments", dynamicFrontendURL)
	http.Redirect(w, r, dashboardURL, http.StatusTemporaryRedirect)
}

func (a *Authenticator) validateState(r *http.Request) (string, error) {
	stateParam := r.URL.Query().Get("state")
	stateCookie, err := r.Cookie("oauth_state")
	if err != nil {
		return "", fmt.Errorf("csrf cookie missing: %w", err)
	}
	if stateCookie.Value == "" {
		return "", fmt.Errorf("csrf cookie is empty")
	}
	if stateCookie.Value != stateParam {
		return "", fmt.Errorf("csrf state mismatch")
	}
	return stateParam, nil
}

func (a *Authenticator) createSession(w http.ResponseWriter, r *http.Request, email, name, picture string) error {
	sessionTTL := a.Config.SessionTTL
	if sessionTTL == 0 {
		sessionTTL = defaultSessionTTL
	}
	if sessionTTL < time.Second {
		return fmt.Errorf("session lifetime must be at least `1s`")
	}
	now := time.Now()
	sessionToken, err := generateSessionTokenAt(email, name, picture, sessionTTL, a.JWTSecret, now)
	if err != nil {
		return fmt.Errorf("failed to generate session token: %w", err)
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "session_token",
		Value:    sessionToken,
		Path:     "/",
		Domain:   a.getCookieDomain(r),
		Expires:  now.Add(sessionTTL),
		MaxAge:   int(sessionTTL / time.Second),
		HttpOnly: true,
		Secure:   a.isCookieSecure(r),
		SameSite: a.getSameSiteMode(r),
	})
	return nil
}

// HandleLogout logs out the user.
func (a *Authenticator) HandleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     "session_token",
		Value:    "",
		Path:     "/",
		Domain:   a.getCookieDomain(r),
		MaxAge:   -1,
		Expires:  time.Now().Add(-24 * time.Hour),
		HttpOnly: true,
		Secure:   a.isCookieSecure(r),
		SameSite: a.getSameSiteMode(r),
	})

	http.Redirect(w, r, a.getDynamicFrontendURL(r), http.StatusTemporaryRedirect)
}

// UserResponse defines the JSON model returned by HandleUser.
type UserResponse struct {
	Email            string   `json:"email"`
	Name             string   `json:"name"`
	PictureURL       string   `json:"pictureUrl"`
	SessionExpiresAt int64    `json:"sessionExpiresAt"`
	AllowedDomains   []string `json:"allowedDomains"`
	IsNonProdAuth    bool     `json:"isNonProdAuth"`
}

// HandleUser returns current session claims if authenticated.
func (a *Authenticator) HandleUser(w http.ResponseWriter, r *http.Request) {
	claims, ok := GetClaims(r.Context())
	if !ok || claims == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var expiresAt int64
	if claims.ExpiresAt != nil {
		expiresAt = claims.ExpiresAt.Time.Unix()
	}

	res := &UserResponse{
		Email:            claims.Email,
		Name:             claims.Name,
		PictureURL:       claims.Picture,
		SessionExpiresAt: expiresAt,
		AllowedDomains:   a.Config.AllowedDomains,
		IsNonProdAuth:    a.Config.IsNonProdAuth(),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}

// Helper functions

func GenerateRandomString() (string, error) {
	b := make([]byte, 16)
	if _, err := io.ReadFull(randReader, b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func (a *Authenticator) getRequestSchemeAndHost(r *http.Request) (string, string) {
	scheme := "http"
	// We only trust X-Forwarded-Proto if configured to trust upstream proxies.
	// The operator must ensure the upstream proxy strips/overwrites spoofed headers.
	if r.TLS != nil || (a.Config.TrustXForwardedProto && r.Header.Get("X-Forwarded-Proto") == "https") {
		scheme = "https"
	}

	host := r.Host

	if !isLocalhost(host) {
		if strings.HasPrefix(strings.ToLower(a.Config.FrontendURL), "https://") ||
			strings.HasPrefix(strings.ToLower(a.Config.RedirectURL), "https://") {
			scheme = "https"
		}
	}

	if !isHostAllowed(host, a.Config.AllowedHosts, a.Config.MockAuth) {
		if u, err := url.Parse(a.Config.FrontendURL); err == nil {
			return u.Scheme, u.Host
		}
		return "https", a.Config.FrontendURL
	}

	return scheme, host
}

func isLocalhost(host string) bool {
	hostLower := strings.ToLower(host)
	return hostLower == "localhost" || strings.HasPrefix(hostLower, "localhost:") ||
		hostLower == "127.0.0.1" || strings.HasPrefix(hostLower, "127.0.0.1:") ||
		hostLower == "::1" || hostLower == "[::1]" || strings.HasPrefix(hostLower, "[::1]:")
}

func isHostAllowed(host string, allowedHosts []string, mockAuth bool) bool {
	if mockAuth && isLocalhost(host) {
		return true
	}

	hostLower := strings.ToLower(host)
	for _, ah := range allowedHosts {
		if ah == hostLower {
			return true
		}
	}
	return false
}

func (a *Authenticator) getDynamicOAuthConfig(r *http.Request) *oauth2.Config {
	scheme, host := a.getRequestSchemeAndHost(r)
	cfg := *a.OAuthConfig
	cfg.RedirectURL = fmt.Sprintf("%s://%s/api/callback", scheme, host)
	return &cfg
}

func (a *Authenticator) getDynamicFrontendURL(r *http.Request) string {
	scheme, host := a.getRequestSchemeAndHost(r)
	return fmt.Sprintf("%s://%s", scheme, host)
}

func (a *Authenticator) isCookieSecure(r *http.Request) bool {
	scheme, host := a.getRequestSchemeAndHost(r)
	if scheme == "https" {
		return true
	}
	if isLocalhost(host) {
		return a.Config.SecureCookie
	}
	return false
}

func (a *Authenticator) getCookieDomain(r *http.Request) string {
	return a.Config.CookieDomain
}

func (a *Authenticator) getSameSiteMode(r *http.Request) http.SameSite {
	scheme, _ := a.getRequestSchemeAndHost(r)
	if scheme == "https" {
		return http.SameSiteNoneMode
	}
	return http.SameSiteLaxMode
}
