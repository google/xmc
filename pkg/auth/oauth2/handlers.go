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
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

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

	if a.Config.MockAuth {
		redirectURL := fmt.Sprintf("/api/callback?code=mock_code_123&state=%s", state)
		http.Redirect(w, r, redirectURL, http.StatusTemporaryRedirect)
		return
	}

	oauthConfig := a.getDynamicOAuthConfig(r)
	authURL := oauthConfig.AuthCodeURL(state, oauth2.AccessTypeOffline)
	http.Redirect(w, r, authURL, http.StatusTemporaryRedirect)
}

// HandleCallback processes the authentication callback.
func (a *Authenticator) HandleCallback(w http.ResponseWriter, r *http.Request) {
	stateParam := r.URL.Query().Get("state")
	stateCookie, err := r.Cookie("oauth_state")
	if err != nil || stateCookie.Value == "" || stateCookie.Value != stateParam {
		http.Error(w, "State validation failed (CSRF check failed)", http.StatusBadRequest)
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

	var email, name, picture string

	if a.Config.MockAuth {
		email = "mock-user@mycompany.com"
		name = "Mock User"
		picture = "https://example.com/mock-avatar.png"
	} else {
		code := r.URL.Query().Get("code")
		if code == "" {
			http.Error(w, "Code parameter is missing", http.StatusBadRequest)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		oauthConfig := a.getDynamicOAuthConfig(r)
		token, err := oauthConfig.Exchange(ctx, code)
		if err != nil {
			http.Error(w, fmt.Sprintf("Token exchange failed: %v", err), http.StatusInternalServerError)
			return
		}

		client := oauthConfig.Client(ctx, token)
		resp, err := client.Get(a.Config.APIURL)
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to fetch user info: %v", err), http.StatusInternalServerError)
			return
		}
		defer resp.Body.Close()

		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			http.Error(w, "Failed to read user info response", http.StatusInternalServerError)
			return
		}

		if resp.StatusCode != http.StatusOK {
			http.Error(w, fmt.Sprintf("User info call failed: %s", string(bodyBytes)), http.StatusBadRequest)
			return
		}

		var userInfo GoogleUserInfo
		if err := json.Unmarshal(bodyBytes, &userInfo); err != nil {
			http.Error(w, "Failed to decode user info details", http.StatusInternalServerError)
			return
		}

		email = userInfo.Email
		name = userInfo.Name
		picture = userInfo.Picture
	}

	dynamicFrontendURL := a.getDynamicFrontendURL(r)

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	authorized, err := a.IsUserAuthorized(ctx, email)
	if err != nil {
		log.Printf("[Callback Auth] Authorization pipeline check failed: %v", err)
		permissionErrorURL := fmt.Sprintf("%s/?error=permission_error", dynamicFrontendURL)
		http.Redirect(w, r, permissionErrorURL, http.StatusTemporaryRedirect)
		return
	}

	if !authorized {
		log.Printf("[Callback Auth] Access forbidden: user %q is not authorized", email)
		forbiddenURL := fmt.Sprintf("%s/?error=forbidden&email=%s", dynamicFrontendURL, url.QueryEscape(email))
		http.Redirect(w, r, forbiddenURL, http.StatusTemporaryRedirect)
		return
	}

	sessionToken, err := GenerateSessionToken(email, name, picture, 1*time.Hour, a.JWTSecret)
	if err != nil {
		http.Error(w, "Failed to generate session token", http.StatusInternalServerError)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "session_token",
		Value:    sessionToken,
		Path:     "/",
		Domain:   a.getCookieDomain(r),
		Expires:  time.Now().Add(1 * time.Hour),
		HttpOnly: true,
		Secure:   a.isCookieSecure(r),
		SameSite: a.getSameSiteMode(r),
	})

	dashboardURL := fmt.Sprintf("%s/experiments", dynamicFrontendURL)
	http.Redirect(w, r, dashboardURL, http.StatusTemporaryRedirect)
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
	PictureUrl       string   `json:"pictureUrl"`
	SessionExpiresAt int64    `json:"sessionExpiresAt"`
	AllowedDomains   []string `json:"allowedDomains"`
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
		PictureUrl:       claims.Picture,
		SessionExpiresAt: expiresAt,
		AllowedDomains:   a.Config.AllowedDomains,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
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
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}

	host := r.Host

	hostLower := strings.ToLower(host)
	isLocal := hostLower == "localhost" || strings.HasPrefix(hostLower, "localhost:") ||
		hostLower == "127.0.0.1" || strings.HasPrefix(hostLower, "127.0.0.1:")

	if !isLocal {
		if strings.HasPrefix(strings.ToLower(a.Config.FrontendURL), "https://") ||
			strings.HasPrefix(strings.ToLower(a.Config.RedirectURL), "https://") {
			scheme = "https"
		}
	}

	if !isHostAllowed(host, a.Config.FrontendURL, a.Config.RedirectURL) {
		if u, err := url.Parse(a.Config.FrontendURL); err == nil {
			return u.Scheme, u.Host
		}
		return "https", a.Config.FrontendURL
	}

	return scheme, host
}

func isHostAllowed(host string, allowedURLs ...string) bool {
	hostLower := strings.ToLower(host)
	if hostLower == "localhost" || strings.HasPrefix(hostLower, "localhost:") ||
		hostLower == "127.0.0.1" || strings.HasPrefix(hostLower, "127.0.0.1:") {
		return true
	}

	for _, allowedURL := range allowedURLs {
		if allowedURL == "" {
			continue
		}
		if u, err := url.Parse(allowedURL); err == nil {
			if strings.ToLower(u.Host) == hostLower {
				return true
			}
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
	hostLower := strings.ToLower(host)
	if hostLower == "localhost" || strings.HasPrefix(hostLower, "localhost:") ||
		hostLower == "127.0.0.1" || strings.HasPrefix(hostLower, "127.0.0.1:") {
		return a.Config.SecureCookie
	}
	return false
}

func (a *Authenticator) getCookieDomain(r *http.Request) string {
	if u, err := url.Parse(a.Config.FrontendURL); err == nil {
		host := u.Hostname()
		hostLower := strings.ToLower(host)
		if hostLower == "localhost" || strings.HasPrefix(hostLower, "127.0.0.1") {
			return ""
		}
		return host
	}
	return ""
}

func (a *Authenticator) getSameSiteMode(r *http.Request) http.SameSite {
	scheme, _ := a.getRequestSchemeAndHost(r)
	if scheme == "https" {
		return http.SameSiteNoneMode
	}
	return http.SameSiteLaxMode
}


