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
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/iam/apiv1/iampb"
	"github.com/golang-jwt/jwt/v5"
	"google.golang.org/api/idtoken"
)

func TestIntegrationMockAuth(t *testing.T) {
	cfg := &Config{
		Port:           "8080",
		MockAuth:       true,
		AllowedDomains: []string{"mycompany.com"},
		ClientID:       "test-client",
		ClientSecret:   "test-secret",
		JWTSecret:      "test-jwt-secret-key-123456789-abcd",
		RedirectURL:    "http://localhost/api/callback",
		AuthURL:        "http://localhost/auth",
		TokenURL:       "http://localhost/token",
		APIURL:         "http://localhost/api",
		UsePKCE:        true,
		FrontendURL:    "http://localhost:8080",
	}

	a := newTestAuthenticator(cfg)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/login", a.HandleLogin)
	mux.HandleFunc("/api/callback", a.HandleCallback)
	mux.HandleFunc("/api/logout", a.HandleLogout)
	mux.Handle("/api/secret", a.Authenticate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, _ := GetClaims(r.Context())
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("authorized:" + claims.Email))
	})))

	ts := httptest.NewServer(a.corsMiddleware(mux))
	defer ts.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("Failed to initialize cookie jar: %v", err)
	}

	client := &http.Client{
		Jar: jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	// 1. Login Redirection Check
	resp, err := client.Get(ts.URL + "/api/login")
	if err != nil {
		t.Fatalf("Login request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusTemporaryRedirect {
		t.Errorf("Expected login redirect status 307, got %d", resp.StatusCode)
	}

	redirectURLStr := resp.Header.Get("Location")
	if !strings.Contains(redirectURLStr, "/api/callback") {
		t.Fatalf("Expected callback redirect path, got: %q", redirectURLStr)
	}

	// 2. Process Mock Callback
	resp2, err := client.Get(ts.URL + redirectURLStr)
	if err != nil {
		t.Fatalf("Callback request failed: %v", err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusTemporaryRedirect {
		t.Errorf("Expected callback redirect 307, got %d", resp2.StatusCode)
	}

	// Verify session cookie in jar
	u, _ := url.Parse(ts.URL)
	cookies := jar.Cookies(u)
	var sessionCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == "session_token" {
			sessionCookie = c
			break
		}
	}

	if sessionCookie == nil {
		t.Fatal("Expected session_token cookie in cookie jar, got none")
	}

	// 3. Verify Secure Route Access
	resp3, err := client.Get(ts.URL + "/api/secret")
	if err != nil {
		t.Fatalf("Secret request failed: %v", err)
	}
	defer resp3.Body.Close()

	if resp3.StatusCode != http.StatusOK {
		t.Fatalf("Expected status 200 OK, got %d", resp3.StatusCode)
	}

	// 4. Verify Programmatic Service Account Bearer Token Bypassing Cookies
	cleanClient := &http.Client{}
	req, err := http.NewRequest("GET", ts.URL+"/api/secret", nil)
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer mock-sa-token")

	resp4, err := cleanClient.Do(req)
	if err != nil {
		t.Fatalf("Bearer request failed: %v", err)
	}
	defer resp4.Body.Close()

	if resp4.StatusCode != http.StatusOK {
		t.Fatalf("Expected status 200 OK with Bearer SA token, got %d", resp4.StatusCode)
	}
}

func TestHybridAuthorizationPipeline(t *testing.T) {
	tests := []struct {
		name                  string
		allowedDomains        []string
		allowedEmails         []string
		gcpProjectID          string
		targetRole            string
		mockAuth              bool
		disableAuthentication bool
		emailToCheck          string
		want                  bool
		wantErr               bool
	}{
		{
			name:           "Stage 1: Match direct email allowlist (Fast path)",
			allowedDomains: []string{"company.com"},
			allowedEmails:  []string{"guest@external.com"},
			gcpProjectID:   "my-project",
			emailToCheck:   "guest@external.com",
			mockAuth:       true,
			want:           true,
			wantErr:        false,
		},
		{
			name:           "Stage 2: Match allowed domain suffix (Fast path)",
			allowedDomains: []string{"company.com"},
			allowedEmails:  []string{"guest@external.com"},
			gcpProjectID:   "my-project",
			emailToCheck:   "employee@company.com",
			mockAuth:       true,
			want:           true,
			wantErr:        false,
		},
		{
			name:           "Stage 3 Mock: Match simulated IAM admin",
			allowedDomains: []string{"company.com"},
			allowedEmails:  []string{"guest@external.com"},
			gcpProjectID:   "my-project",
			targetRole:     "roles/viewer",
			emailToCheck:   "mock-admin@mycompany.com",
			mockAuth:       true,
			want:           true,
			wantErr:        false,
		},
		{
			name:           "Stage 3 Mock: Block unmatched simulated user",
			allowedDomains: []string{"company.com"},
			allowedEmails:  []string{"guest@external.com"},
			gcpProjectID:   "my-project",
			targetRole:     "roles/viewer",
			emailToCheck:   "stranger@mycompany.com",
			mockAuth:       true,
			want:           false,
			wantErr:        false,
		},
		{
			name:                  "No parameters: Open access compatibility with disableAuthentication=true",
			allowedDomains:        []string{},
			allowedEmails:         []string{},
			gcpProjectID:          "",
			disableAuthentication: true,
			emailToCheck:          "anyone@external.com",
			mockAuth:              true,
			want:                  true,
			wantErr:               false,
		},
		{
			name:                  "No parameters: Fail-closed default",
			allowedDomains:        []string{},
			allowedEmails:         []string{},
			gcpProjectID:          "",
			disableAuthentication: false,
			emailToCheck:          "anyone@external.com",
			mockAuth:              true,
			want:                  false,
			wantErr:               true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				AllowedDomains:        tt.allowedDomains,
				AllowedEmails:         tt.allowedEmails,
				GCPProjectID:          tt.gcpProjectID,
				TargetIAMRole:         tt.targetRole,
				MockAuth:              tt.mockAuth,
				DisableAuthentication: tt.disableAuthentication,
			}
			a := newTestAuthenticator(cfg)

			ctx := context.Background()
			got, err := a.IsUserAuthorized(ctx, tt.emailToCheck)
			if (err != nil) != tt.wantErr {
				t.Errorf("IsUserAuthorized() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("IsUserAuthorized(%q) = %v, want %v", tt.emailToCheck, got, tt.want)
			}
		})
	}
}

type errorReader struct{}

func (errorReader) Read(p []byte) (n int, err error) {
	return 0, errors.New("mock read error")
}

func TestGenerateRandomStringFailure(t *testing.T) {
	oldRandReader := randReader
	defer func() { randReader = oldRandReader }()
	randReader = errorReader{}

	_, err := GenerateRandomString()
	if err == nil {
		t.Fatal("Expected error when random reader fails, got nil")
	}
}

func TestHandleLoginGenerateStateFailure(t *testing.T) {
	oldRandReader := randReader
	defer func() { randReader = oldRandReader }()
	randReader = errorReader{}

	a := newTestAuthenticator(&Config{})
	req := httptest.NewRequest("GET", "/api/login", nil)
	w := httptest.NewRecorder()

	a.HandleLogin(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("Expected status code 500, got %d", resp.StatusCode)
	}
}

func TestXForwardedProtoTrust(t *testing.T) {
	tests := []struct {
		name                 string
		trustXForwardedProto bool
		headerVal            string
		tls                  bool
		wantScheme           string
	}{
		{
			name:                 "TLS set directly",
			trustXForwardedProto: false,
			headerVal:            "",
			tls:                  true,
			wantScheme:           "https",
		},
		{
			name:                 "Header set, not trusted",
			trustXForwardedProto: false,
			headerVal:            "https",
			tls:                  false,
			wantScheme:           "http",
		},
		{
			name:                 "Header set, trusted",
			trustXForwardedProto: true,
			headerVal:            "https",
			tls:                  false,
			wantScheme:           "https",
		},
		{
			name:                 "Header set to HTTP, trusted",
			trustXForwardedProto: true,
			headerVal:            "http",
			tls:                  false,
			wantScheme:           "http",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				TrustXForwardedProto: tt.trustXForwardedProto,
				FrontendURL:          "http://localhost:8080",
				AllowedHosts:         []string{"localhost:8080"},
			}
			a := newTestAuthenticator(cfg)
			req := httptest.NewRequest("GET", "http://localhost:8080/api/login", nil)
			if tt.headerVal != "" {
				req.Header.Set("X-Forwarded-Proto", tt.headerVal)
			}
			if tt.tls {
				req.TLS = &tls.ConnectionState{}
			}

			scheme, _ := a.getRequestSchemeAndHost(req)
			if scheme != tt.wantScheme {
				t.Errorf("getRequestSchemeAndHost() scheme = %q, want %q", scheme, tt.wantScheme)
			}
		})
	}
}

func TestCORSMiddleware(t *testing.T) {
	cfg := &Config{
		FrontendURL:    "http://frontend.com",
		AllowedOrigins: []string{"http://allowed1.com", "http://allowed2.com"},
	}
	a := newTestAuthenticator(cfg)

	tests := []struct {
		name       string
		origin     string
		wantOrigin string
	}{
		{
			name:       "Empty origin returns no CORS headers",
			origin:     "",
			wantOrigin: "",
		},
		{
			name:       "FrontendURL matches",
			origin:     "http://frontend.com",
			wantOrigin: "http://frontend.com",
		},
		{
			name:       "AllowedOrigin matches 1",
			origin:     "http://allowed1.com",
			wantOrigin: "http://allowed1.com",
		},
		{
			name:       "AllowedOrigin matches 2",
			origin:     "http://allowed2.com",
			wantOrigin: "http://allowed2.com",
		},
		{
			name:       "Disallowed origin returns no CORS headers",
			origin:     "http://disallowed.com",
			wantOrigin: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := a.corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))

			req := httptest.NewRequest("GET", "http://frontend.com/api/test", nil)
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			gotOrigin := w.Header().Get("Access-Control-Allow-Origin")
			if gotOrigin != tt.wantOrigin {
				t.Errorf("Access-Control-Allow-Origin = %q, want %q", gotOrigin, tt.wantOrigin)
			}

			gotCreds := w.Header().Get("Access-Control-Allow-Credentials")
			wantCreds := ""
			if tt.wantOrigin != "" {
				wantCreds = "true"
			}
			if gotCreds != wantCreds {
				t.Errorf("Access-Control-Allow-Credentials = %q, want %q", gotCreds, wantCreds)
			}
		})
	}
}

func TestHandleCallbackProduction(t *testing.T) {
	jwtSecret := "test-jwt-secret-key-123456789-abcd"
	userEmail := "real-user@mycompany.com"

	// 1. Spin up mock OIDC / OAuth2 Server
	mockOIDC := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth":
			// Simulate OAuth login page redirection back to redirect_uri
			redirectURI := r.URL.Query().Get("redirect_uri")
			state := r.URL.Query().Get("state")
			http.Redirect(w, r, fmt.Sprintf("%s?code=mock_auth_code_999&state=%s", redirectURI, state), http.StatusFound)
		case "/token":
			// Respond with mock access token json
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"access_token": "mock-access-token-999", "token_type": "Bearer"}`))
		case "/userinfo":
			// Check bearer authorization
			auth := r.Header.Get("Authorization")
			if auth != "Bearer mock-access-token-999" {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			res := GoogleUserInfo{
				Email:   userEmail,
				Name:    "Real User",
				Picture: "https://example.com/avatar.png",
			}
			_ = json.NewEncoder(w).Encode(res)
		default:
			http.Error(w, "Not Found", http.StatusNotFound)
		}
	}))
	defer mockOIDC.Close()

	// 2. Initialize Authenticator pointing to mock OIDC server
	cfg := &Config{
		MockAuth:       false,
		ClientID:       "test-client",
		ClientSecret:   "test-secret",
		JWTSecret:      jwtSecret,
		AuthURL:        mockOIDC.URL + "/auth",
		TokenURL:       mockOIDC.URL + "/token",
		APIURL:         mockOIDC.URL + "/userinfo",
		AllowedDomains: []string{"mycompany.com"},
		FrontendURL:    "http://localhost:8080",
	}
	a := newTestAuthenticator(cfg)

	// Create request handlers
	mux := http.NewServeMux()
	mux.HandleFunc("/api/login", a.HandleLogin)
	mux.HandleFunc("/api/callback", a.HandleCallback)

	ts := httptest.NewServer(mux)
	defer ts.Close()

	if u, err := url.Parse(ts.URL); err == nil {
		cfg.AllowedHosts = append(cfg.AllowedHosts, strings.ToLower(u.Host))
	}

	// 3. Perform login flow
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("Failed to initialize cookie jar: %v", err)
	}
	client := &http.Client{
		Jar: jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// Do not follow callback redirection to frontend (since dashboard doesn't exist in test server)
			if strings.Contains(req.URL.Path, "/experiments") {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}

	// Login triggers redirect to mockOIDC /auth -> redirects to /api/callback
	resp, err := client.Get(ts.URL + "/api/login")
	if err != nil {
		t.Fatalf("Login workflow failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusTemporaryRedirect {
		t.Errorf("Expected final dashboard redirect 307, got %d", resp.StatusCode)
	}

	// 4. Verify session cookie
	u, _ := url.Parse(ts.URL)
	cookies := jar.Cookies(u)
	var sessionCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == "session_token" {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil {
		t.Fatal("Expected session_token cookie, found none")
	}

	claims, err := ParseSessionToken(sessionCookie.Value, []byte(jwtSecret), FrontendAudience)
	if err != nil {
		t.Fatalf("Parsed session token is invalid: %v", err)
	}
	if claims.Email != userEmail {
		t.Errorf("Expected email %q, got %q", userEmail, claims.Email)
	}
}

func TestBearerTokenValidation(t *testing.T) {
	jwtSecret := []byte("test-jwt-secret-key-123456789-abcd")
	cfg := &Config{
		ClientID:       "test-client-id",
		JWTSecret:      string(jwtSecret),
		AllowedDomains: []string{"mycompany.com"},
	}
	a := newTestAuthenticator(cfg)

	// Simple target handler that outputs authenticated user email
	targetHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, ok := GetClaims(r.Context())
		if !ok {
			http.Error(w, "No Claims", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(claims.Email))
	})

	t.Run("Valid Session JWT Bearer Token", func(t *testing.T) {
		token, err := GenerateSessionToken("user@mycompany.com", "Test", "", 1*time.Hour, jwtSecret)
		if err != nil {
			t.Fatalf("Failed to generate token: %v", err)
		}

		req := httptest.NewRequest("GET", "/api/data", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()

		a.Authenticate(targetHandler).ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d. Body: %s", w.Code, w.Body.String())
		}
		if w.Body.String() != "user@mycompany.com" {
			t.Errorf("Expected body 'user@mycompany.com', got %q", w.Body.String())
		}
	})

	t.Run("Expired Session JWT Bearer Token", func(t *testing.T) {
		// Expired by more than the session clock-skew leeway.
		token, err := GenerateSessionToken("user@mycompany.com", "Test", "", -1*time.Minute, jwtSecret)
		if err != nil {
			t.Fatalf("Failed to generate token: %v", err)
		}

		req := httptest.NewRequest("GET", "/api/data", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()

		a.Authenticate(targetHandler).ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("Expected status 401, got %d", w.Code)
		}
	})

	t.Run("Valid Google OIDC ID Token", func(t *testing.T) {
		a.IDTokenValidator = func(ctx context.Context, token string, audience string) (*idtoken.Payload, error) {
			if token != "mock-oidc-token-123" || audience != "test-client-id" {
				return nil, errors.New("invalid oidc token or audience")
			}
			return &idtoken.Payload{
				Issuer: "https://accounts.google.com",
				Claims: map[string]interface{}{
					"email": "sa@mycompany.com",
					"name":  "Service Account",
				},
			}, nil
		}

		req := httptest.NewRequest("GET", "/api/data", nil)
		req.Header.Set("Authorization", "Bearer mock-oidc-token-123")
		w := httptest.NewRecorder()

		a.Authenticate(targetHandler).ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d. Body: %s", w.Code, w.Body.String())
		}
		if w.Body.String() != "sa@mycompany.com" {
			t.Errorf("Expected body 'sa@mycompany.com', got %q", w.Body.String())
		}
	})

	t.Run("De-authorized OIDC Token", func(t *testing.T) {
		a.IDTokenValidator = func(ctx context.Context, token string, audience string) (*idtoken.Payload, error) {
			return &idtoken.Payload{
				Issuer: "https://accounts.google.com",
				Claims: map[string]interface{}{
					"email": "unauthorized-sa@external.com",
					"name":  "External SA",
				},
			}, nil
		}

		req := httptest.NewRequest("GET", "/api/data", nil)
		req.Header.Set("Authorization", "Bearer mock-oidc-token-123")
		w := httptest.NewRecorder()

		a.Authenticate(targetHandler).ServeHTTP(w, req)

		if w.Code != http.StatusForbidden {
			t.Errorf("Expected status 403, got %d", w.Code)
		}
	})
}

func TestSecurityHeadersMiddleware(t *testing.T) {
	cfg := &Config{
		TrustXForwardedProto: true,
		FrontendURL:          "http://frontend.com",
	}
	a := newTestAuthenticator(cfg)

	targetHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	t.Run("Standard HTTP Security Headers", func(t *testing.T) {
		req := httptest.NewRequest("GET", "http://frontend.com/api", nil)
		w := httptest.NewRecorder()

		a.securityHeadersMiddleware(targetHandler).ServeHTTP(w, req)

		if w.Header().Get("X-Frame-Options") != "DENY" {
			t.Errorf("Expected X-Frame-Options: DENY, got %q", w.Header().Get("X-Frame-Options"))
		}
		if w.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("Expected X-Content-Type-Options: nosniff, got %q", w.Header().Get("X-Content-Type-Options"))
		}
		if w.Header().Get("Strict-Transport-Security") != "" {
			t.Errorf("HSTS header set on HTTP request: %q", w.Header().Get("Strict-Transport-Security"))
		}
	})

	t.Run("HTTPS via TLS Context HSTS", func(t *testing.T) {
		req := httptest.NewRequest("GET", "https://frontend.com/api", nil)
		req.TLS = &tls.ConnectionState{}
		w := httptest.NewRecorder()

		a.securityHeadersMiddleware(targetHandler).ServeHTTP(w, req)

		hsts := w.Header().Get("Strict-Transport-Security")
		if !strings.Contains(hsts, "max-age=63072000") {
			t.Errorf("Expected HSTS header containing max-age=63072000, got %q", hsts)
		}
	})

	t.Run("HTTPS via X-Forwarded-Proto Header HSTS", func(t *testing.T) {
		req := httptest.NewRequest("GET", "http://frontend.com/api", nil)
		req.Header.Set("X-Forwarded-Proto", "https")
		w := httptest.NewRecorder()

		a.securityHeadersMiddleware(targetHandler).ServeHTTP(w, req)

		hsts := w.Header().Get("Strict-Transport-Security")
		if !strings.Contains(hsts, "max-age=63072000") {
			t.Errorf("Expected HSTS header containing max-age=63072000, got %q", hsts)
		}
	})
}

func TestAuthenticateCookie(t *testing.T) {
	jwtSecret := []byte("test-jwt-secret-key-123456789-abcd")
	cfg := &Config{
		JWTSecret:      string(jwtSecret),
		AllowedDomains: []string{"mycompany.com"},
	}
	a := newTestAuthenticator(cfg)

	targetHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	t.Run("Cookie Missing", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/data", nil)
		w := httptest.NewRecorder()
		handled := a.authenticateCookie(w, req, targetHandler)
		if handled {
			t.Error("Expected authenticateCookie to return false for missing cookie")
		}
		if w.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d", w.Code)
		}
	})

	t.Run("Cookie Present but Invalid Signature", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/data", nil)
		req.AddCookie(&http.Cookie{Name: "session_token", Value: "invalid-token"})
		w := httptest.NewRecorder()
		handled := a.authenticateCookie(w, req, targetHandler)
		if handled {
			t.Error("Expected authenticateCookie to return false for invalid cookie")
		}
	})

	t.Run("Cookie Present & Valid User", func(t *testing.T) {
		token, err := GenerateSessionToken("user@mycompany.com", "Test", "", 1*time.Hour, jwtSecret)
		if err != nil {
			t.Fatalf("Failed to generate token: %v", err)
		}
		req := httptest.NewRequest("GET", "/api/data", nil)
		req.AddCookie(&http.Cookie{Name: "session_token", Value: token})
		w := httptest.NewRecorder()
		handled := a.authenticateCookie(w, req, targetHandler)
		if !handled {
			t.Error("Expected authenticateCookie to return true")
		}
		if w.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d. Body: %s", w.Code, w.Body.String())
		}
		if req.Header.Get("Authorization") != "Bearer "+token {
			t.Errorf("Expected Authorization header 'Bearer %s', got %q", token, req.Header.Get("Authorization"))
		}
	})

	t.Run("Cookie Present but De-authorized User", func(t *testing.T) {
		token, err := GenerateSessionToken("deauthorized@external.com", "Test", "", 1*time.Hour, jwtSecret)
		if err != nil {
			t.Fatalf("Failed to generate token: %v", err)
		}
		req := httptest.NewRequest("GET", "/api/data", nil)
		req.AddCookie(&http.Cookie{Name: "session_token", Value: token})
		w := httptest.NewRecorder()
		handled := a.authenticateCookie(w, req, targetHandler)
		if !handled {
			t.Error("Expected authenticateCookie to return true")
		}
		if w.Code != http.StatusForbidden {
			t.Errorf("Expected status 403, got %d", w.Code)
		}
	})
}

func TestAuthenticateBearer(t *testing.T) {
	jwtSecret := []byte("test-jwt-secret-key-123456789-abcd")
	cfg := &Config{
		JWTSecret:      string(jwtSecret),
		AllowedDomains: []string{"mycompany.com"},
		MockAuth:       true,
		ClientID:       "test-client-id",
	}
	a := newTestAuthenticator(cfg)

	targetHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	t.Run("Header Missing", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/data", nil)
		w := httptest.NewRecorder()
		handled := a.authenticateBearer(w, req, targetHandler)
		if handled {
			t.Error("Expected authenticateBearer to return false")
		}
	})

	t.Run("Mock Token Support", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/data", nil)
		req.Header.Set("Authorization", "Bearer mock-sa-token")
		w := httptest.NewRecorder()
		handled := a.authenticateBearer(w, req, targetHandler)
		if !handled {
			t.Error("Expected authenticateBearer to return true")
		}
		if w.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d", w.Code)
		}
	})

	t.Run("Valid Session JWT Bearer Token", func(t *testing.T) {
		token, err := GenerateSessionToken("user@mycompany.com", "Test", "", 1*time.Hour, jwtSecret)
		if err != nil {
			t.Fatalf("Failed to generate token: %v", err)
		}
		req := httptest.NewRequest("GET", "/api/data", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		handled := a.authenticateBearer(w, req, targetHandler)
		if !handled {
			t.Error("Expected authenticateBearer to return true")
		}
		if w.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d", w.Code)
		}
	})

	t.Run("Valid OIDC Token Success", func(t *testing.T) {
		a.IDTokenValidator = func(ctx context.Context, token string, audience string) (*idtoken.Payload, error) {
			return &idtoken.Payload{
				Issuer: "https://accounts.google.com",
				Claims: map[string]interface{}{
					"email": "sa@mycompany.com",
				},
			}, nil
		}
		req := httptest.NewRequest("GET", "/api/data", nil)
		req.Header.Set("Authorization", "Bearer oidc-token-123")
		w := httptest.NewRecorder()
		handled := a.authenticateBearer(w, req, targetHandler)
		if !handled {
			t.Error("Expected authenticateBearer to return true")
		}
		if w.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d", w.Code)
		}
	})
}

func TestValidateState(t *testing.T) {
	a := newTestAuthenticator(&Config{
		FrontendURL: "http://localhost:8080",
	})

	t.Run("Valid state cookie matches query parameter", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/callback?state=mystate123", nil)
		req.AddCookie(&http.Cookie{
			Name:  "oauth_state",
			Value: "mystate123",
		})
		state, err := a.validateState(req)
		if err != nil {
			t.Fatalf("validateState returned unexpected error: %v", err)
		}
		if state != "mystate123" {
			t.Errorf("validateState returned %q, want %q", state, "mystate123")
		}
	})

	t.Run("Missing state cookie", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/callback?state=mystate123", nil)
		_, err := a.validateState(req)
		if err == nil {
			t.Fatal("validateState expected error, got nil")
		}
	})

	t.Run("Empty state cookie", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/callback?state=mystate123", nil)
		req.AddCookie(&http.Cookie{
			Name:  "oauth_state",
			Value: "",
		})
		_, err := a.validateState(req)
		if err == nil {
			t.Fatal("validateState expected error, got nil")
		}
	})

	t.Run("State mismatch", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/callback?state=mismatch", nil)
		req.AddCookie(&http.Cookie{
			Name:  "oauth_state",
			Value: "mystate123",
		})
		_, err := a.validateState(req)
		if err == nil {
			t.Fatal("validateState expected error, got nil")
		}
	})
}

func TestGetUserInfo(t *testing.T) {
	t.Run("Mock Mode returns mock profile immediately", func(t *testing.T) {
		a := newTestAuthenticator(&Config{
			MockAuth: true,
		})
		req := httptest.NewRequest("GET", "/api/callback", nil)
		info, err := a.OAuthProvider.ExchangeAndGetUserInfo(context.Background(), req, "anycode")
		if err != nil {
			t.Fatalf("getUserInfo failed: %v", err)
		}
		if info.Email != "mock-user@mycompany.com" {
			t.Errorf("expected mock email, got %q", info.Email)
		}
	})

	t.Run("Real Mode OIDC Success", func(t *testing.T) {
		mockOIDC := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/token" {
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"access_token": "token-xyz", "token_type": "Bearer"}`))
				return
			}
			if r.URL.Path == "/userinfo" {
				if r.Header.Get("Authorization") != "Bearer token-xyz" {
					http.Error(w, "Unauthorized", http.StatusUnauthorized)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"email": "real-user@mycompany.com", "name": "Real User", "picture": "pic-url"}`))
				return
			}
		}))
		defer mockOIDC.Close()

		a := newTestAuthenticator(&Config{
			MockAuth:     false,
			ClientID:     "test-client",
			ClientSecret: "test-secret",
			TokenURL:     mockOIDC.URL + "/token",
			APIURL:       mockOIDC.URL + "/userinfo",
		})

		req := httptest.NewRequest("GET", "/api/callback", nil)
		info, err := a.OAuthProvider.ExchangeAndGetUserInfo(context.Background(), req, "validcode")
		if err != nil {
			t.Fatalf("getUserInfo failed: %v", err)
		}
		if info.Email != "real-user@mycompany.com" {
			t.Errorf("expected email real-user@mycompany.com, got %q", info.Email)
		}
	})
}

func TestCreateSession(t *testing.T) {
	const jwtSecret = "test-jwt-secret-key-123456789-abcd"
	for _, tc := range []struct {
		name string
		ttl  time.Duration
		want time.Duration
	}{
		{name: "default", want: time.Hour},
		{name: "one second", ttl: time.Second, want: time.Second},
		{name: "one week", ttl: 7 * 24 * time.Hour, want: 7 * 24 * time.Hour},
		{name: "thirty days", ttl: 30 * 24 * time.Hour, want: 30 * 24 * time.Hour},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newTestAuthenticator(&Config{
				JWTSecret:    jwtSecret,
				SessionTTL:   tc.ttl,
				FrontendURL:  "https://xmc.example.com",
				CookieDomain: "xmc.example.com",
				SecureCookie: true,
			})
			req := httptest.NewRequest("GET", "https://xmc.example.com/api/callback", nil)
			w := httptest.NewRecorder()
			if err := a.createSession(w, req, "test@mycompany.com", "Test User", "https://example.com/pic"); err != nil {
				t.Fatalf("createSession failed: %v", err)
			}
			resp := w.Result()
			defer resp.Body.Close()
			cookies := resp.Cookies()
			if len(cookies) != 1 || cookies[0].Name != "session_token" {
				t.Fatal("expected a session cookie")
			}
			cookie := cookies[0]
			if want := int(tc.want / time.Second); cookie.MaxAge != want {
				t.Errorf("cookie MaxAge = %d, want %d", cookie.MaxAge, want)
			}
			claims := &SessionClaims{}
			keyFunc := func(*jwt.Token) (interface{}, error) { return []byte(jwtSecret), nil }
			if _, err := jwt.ParseWithClaims(cookie.Value, claims, keyFunc, jwt.WithTimeFunc(func() time.Time {
				return cookie.Expires.Add(-time.Second)
			})); err != nil {
				t.Fatalf("session invalid before cookie expiry: %v", err)
			}
			if claims.Email != "test@mycompany.com" || claims.Name != "Test User" || claims.Picture != "https://example.com/pic" {
				t.Error("session identity does not match the authenticated user")
			}
			if got := claims.ExpiresAt.Sub(claims.IssuedAt.Time); got != tc.want {
				t.Errorf("token lifetime = %v, want %v", got, tc.want)
			}
			if !cookie.Expires.Equal(claims.ExpiresAt.Time) {
				t.Errorf("cookie expiry = %v, token expiry = %v", cookie.Expires, claims.ExpiresAt.Time)
			}
			if !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteNoneMode || cookie.Path != "/" || cookie.Domain != "xmc.example.com" {
				t.Error("session cookie has unexpected security attributes")
			}
			if _, err := jwt.ParseWithClaims(cookie.Value, &SessionClaims{}, keyFunc, jwt.WithTimeFunc(func() time.Time {
				return cookie.Expires
			})); !errors.Is(err, jwt.ErrTokenExpired) {
				t.Errorf("error at expiry = %v, want an expired token", err)
			}
		})
	}
}

func TestCreateSessionRejectsInvalidTTL(t *testing.T) {
	for _, ttl := range []time.Duration{-time.Hour, time.Millisecond} {
		t.Run(ttl.String(), func(t *testing.T) {
			a := newTestAuthenticator(&Config{SessionTTL: ttl})
			w := httptest.NewRecorder()
			req := httptest.NewRequest("GET", "/api/callback", nil)
			if err := a.createSession(w, req, "test@mycompany.com", "Test User", ""); err == nil {
				t.Fatal("expected an invalid session lifetime error")
			}
			if w.Header().Get("Set-Cookie") != "" {
				t.Error("invalid session lifetime issued a cookie")
			}
		})
	}
}

type testMockOAuthProvider struct {
	MockEmail string
	MockName  string
	MockPic   string
}

func (m *testMockOAuthProvider) AuthCodeURL(state string, r *http.Request) string {
	return fmt.Sprintf("/api/callback?code=mock_code_123&state=%s", state)
}

func (m *testMockOAuthProvider) ExchangeAndGetUserInfo(ctx context.Context, r *http.Request, code string) (*GoogleUserInfo, error) {
	return &GoogleUserInfo{
		Email:   m.MockEmail,
		Name:    m.MockName,
		Picture: m.MockPic,
	}, nil
}

func TestSecurityMiddlewares(t *testing.T) {
	cfg := &Config{
		AllowedOrigins: []string{"http://allowed.com"},
		FrontendURL:    "http://frontend.com",
	}
	a := newTestAuthenticator(cfg)

	targetHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := a.SecurityMiddlewares(targetHandler)

	t.Run("Applies CORS and Security Headers on GET", func(t *testing.T) {
		req := httptest.NewRequest("GET", "http://frontend.com/api", nil)
		req.Header.Set("Origin", "http://allowed.com")
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d", w.Code)
		}
		// Check Security Headers (from securityHeadersMiddleware)
		if w.Header().Get("X-Frame-Options") != "DENY" {
			t.Errorf("Expected X-Frame-Options: DENY, got %q", w.Header().Get("X-Frame-Options"))
		}
		// Check CORS Headers (from corsMiddleware)
		if w.Header().Get("Access-Control-Allow-Origin") != "http://allowed.com" {
			t.Errorf("Expected Access-Control-Allow-Origin: http://allowed.com, got %q", w.Header().Get("Access-Control-Allow-Origin"))
		}
	})

	t.Run("Applies CSRF and early terminates POST without token, keeping Security Headers", func(t *testing.T) {
		req := httptest.NewRequest("POST", "http://frontend.com/api", nil)
		req.Header.Set("Origin", "http://disallowed.com")
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusForbidden {
			t.Errorf("Expected status 403, got %d", w.Code)
		}
		// Check Security Headers (should still be present!)
		if w.Header().Get("X-Frame-Options") != "DENY" {
			t.Errorf("Expected X-Frame-Options: DENY, got %q", w.Header().Get("X-Frame-Options"))
		}
		// Check CORS Headers (should NOT be present because CORS is innermost)
		if w.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Errorf("Expected no Access-Control-Allow-Origin header, got %q", w.Header().Get("Access-Control-Allow-Origin"))
		}
	})

	t.Run("GET with disallowed origin omits CORS headers but keeps Security Headers", func(t *testing.T) {
		req := httptest.NewRequest("GET", "http://frontend.com/api", nil)
		req.Header.Set("Origin", "http://disallowed.com")
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d", w.Code)
		}
		// Check Security Headers (should still be present!)
		if w.Header().Get("X-Frame-Options") != "DENY" {
			t.Errorf("Expected X-Frame-Options: DENY, got %q", w.Header().Get("X-Frame-Options"))
		}
		// Check CORS Headers (should NOT be present because origin is disallowed)
		if w.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Errorf("Expected no Access-Control-Allow-Origin header, got %q", w.Header().Get("Access-Control-Allow-Origin"))
		}
	})
}

func TestHandleUser(t *testing.T) {
	t.Run("Unauthorized when no claims in context", func(t *testing.T) {
		cfg := &Config{
			Port:     "8080",
			MockAuth: true,
		}
		a := newTestAuthenticator(cfg)

		req := httptest.NewRequest("GET", "/api/user", nil)
		w := httptest.NewRecorder()

		a.HandleUser(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("Expected status 401 Unauthorized, got %d", w.Code)
		}
	})

	t.Run("Returns UserResponse with isNonProdAuth true for mock auth", func(t *testing.T) {
		cfg := &Config{
			Port:           "8080",
			MockAuth:       true,
			AllowedDomains: []string{"example.com"},
		}
		a := newTestAuthenticator(cfg)

		claims := &SessionClaims{
			Email:   "user@example.com",
			Name:    "Test User",
			Picture: "https://example.com/pic.png",
			RegisteredClaims: jwt.RegisteredClaims{
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(1 * time.Hour)),
			},
		}

		req := httptest.NewRequest("GET", "/api/user", nil)
		req = req.WithContext(context.WithValue(req.Context(), UserClaimsContextKey, claims))
		w := httptest.NewRecorder()

		a.HandleUser(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("Expected status 200 OK, got %d", w.Code)
		}

		var res UserResponse
		if err := json.NewDecoder(w.Body).Decode(&res); err != nil {
			t.Fatalf("Failed to decode UserResponse: %v", err)
		}

		if res.Email != "user@example.com" {
			t.Errorf("Expected email %q, got %q", "user@example.com", res.Email)
		}
		if res.Name != "Test User" {
			t.Errorf("Expected name %q, got %q", "Test User", res.Name)
		}
		if res.PictureURL != "https://example.com/pic.png" {
			t.Errorf("Expected pictureUrl %q, got %q", "https://example.com/pic.png", res.PictureURL)
		}
		if !res.IsNonProdAuth {
			t.Errorf("Expected isNonProdAuth true, got false")
		}
		if len(res.AllowedDomains) != 1 || res.AllowedDomains[0] != "example.com" {
			t.Errorf("Expected allowedDomains [example.com], got %v", res.AllowedDomains)
		}
	})

	t.Run("Returns UserResponse with isNonProdAuth false for production Google OAuth", func(t *testing.T) {
		cfg := &Config{
			Port:           "8080",
			AuthURL:        "https://accounts.google.com/o/oauth2/v2/auth",
			AllowedDomains: []string{"google.com"},
		}
		a := newTestAuthenticator(cfg)

		claims := &SessionClaims{
			Email: "prod-user@google.com",
			Name:  "Prod User",
		}

		req := httptest.NewRequest("GET", "/api/user", nil)
		req = req.WithContext(context.WithValue(req.Context(), UserClaimsContextKey, claims))
		w := httptest.NewRecorder()

		a.HandleUser(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("Expected status 200 OK, got %d", w.Code)
		}

		var res UserResponse
		if err := json.NewDecoder(w.Body).Decode(&res); err != nil {
			t.Fatalf("Failed to decode UserResponse: %v", err)
		}

		if res.Email != "prod-user@google.com" {
			t.Errorf("Expected email %q, got %q", "prod-user@google.com", res.Email)
		}
		if res.IsNonProdAuth {
			t.Errorf("Expected isNonProdAuth false, got true")
		}
	})
}

func newTestAuthenticator(cfg *Config) *Authenticator {
	a := NewAuthenticator(cfg)
	if cfg.MockAuth {
		a.OAuthProvider = &testMockOAuthProvider{
			MockEmail: "mock-user@mycompany.com",
			MockName:  "Mock User",
			MockPic:   "https://example.com/mock-avatar.png",
		}
		a.IDTokenValidator = func(ctx context.Context, idToken string, audience string) (*idtoken.Payload, error) {
			if idToken == "mock-sa-token" {
				return &idtoken.Payload{
					Issuer:   "google-service-account",
					Audience: audience,
					Claims: map[string]interface{}{
						"email":   "service-account@mycompany.com",
						"name":    "Mock Service Account",
						"picture": "https://example.com/sa-avatar.png",
					},
				}, nil
			}
			return nil, fmt.Errorf("invalid token: %s", idToken)
		}
		a.PolicyFetcher = func(ctx context.Context) (*iampb.Policy, error) {
			return &iampb.Policy{
				Bindings: []*iampb.Binding{
					{
						Role:    a.Config.TargetIAMRole,
						Members: []string{"user:mock-admin@mycompany.com", "user:mock-sa-user@mycompany.com", "serviceaccount:service-account@mycompany.com"},
					},
				},
			}, nil
		}
	}
	return a
}
