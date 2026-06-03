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
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestSessionLifecycle(t *testing.T) {
	secret := []byte("my-test-secret-key-123456789-abcd")
	email := "test@company.com"
	name := "Test User"
	picture := "https://example.com/avatar.png"
	expiry := 5 * time.Minute

	// 1. Generate and Parse Token successfully
	tokenStr, err := GenerateSessionToken(email, name, picture, expiry, secret)
	if err != nil {
		t.Fatalf("Failed to generate token: %v", err)
	}

	claims, err := ParseSessionToken(tokenStr, secret)
	if err != nil {
		t.Fatalf("Failed to parse valid token: %v", err)
	}

	if claims.Email != email {
		t.Errorf("Expected email %q, got %q", email, claims.Email)
	}
	if claims.Name != name {
		t.Errorf("Expected name %q, got %q", name, claims.Name)
	}
	if claims.Picture != picture {
		t.Errorf("Expected picture %q, got %q", picture, claims.Picture)
	}

	// 2. Signature verification failure with invalid key
	wrongSecret := []byte("wrong-secret-key-987654321-zyxw")
	_, err = ParseSessionToken(tokenStr, wrongSecret)
	if err == nil || !errors.Is(err, ErrInvalidToken) {
		t.Errorf("Expected ErrInvalidToken for incorrect key, got: %v", err)
	}

	// 3. Expiration verification failure
	expiredTokenStr, err := GenerateSessionToken(email, name, picture, -1*time.Second, secret)
	if err != nil {
		t.Fatalf("Failed to generate expired token: %v", err)
	}

	_, err = ParseSessionToken(expiredTokenStr, secret)
	if err == nil || !errors.Is(err, ErrExpiredToken) {
		t.Errorf("Expected ErrExpiredToken for expired token, got: %v", err)
	}
}

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

	a := NewAuthenticator(cfg)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/login", a.HandleLogin)
	mux.HandleFunc("/api/callback", a.HandleCallback)
	mux.HandleFunc("/api/logout", a.HandleLogout)
	mux.Handle("/api/secret", a.Authenticate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, _ := GetClaims(r.Context())
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("authorized:" + claims.Email))
	})))

	ts := httptest.NewServer(a.CORSMiddleware(mux))
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
		name           string
		allowedDomains []string
		allowedEmails  []string
		gcpProjectID   string
		targetRole     string
		mockAuth       bool
		emailToCheck   string
		want           bool
		wantErr        bool
	}{
		{
			name:           "Stage 1: Match direct email whitelist (Fast path)",
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
			emailToCheck:   "stranger@mycompany.com",
			mockAuth:       true,
			want:           false,
			wantErr:        false,
		},
		{
			name:           "No parameters: Open access compatibility",
			allowedDomains: []string{},
			allowedEmails:  []string{},
			gcpProjectID:   "",
			emailToCheck:   "anyone@external.com",
			mockAuth:       true,
			want:           true,
			wantErr:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				AllowedDomains: tt.allowedDomains,
				AllowedEmails:  tt.allowedEmails,
				GCPProjectID:   tt.gcpProjectID,
				TargetIAMRole:  tt.targetRole,
				MockAuth:       tt.mockAuth,
			}
			a := NewAuthenticator(cfg)

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

	a := NewAuthenticator(&Config{})
	req := httptest.NewRequest("GET", "/api/login", nil)
	w := httptest.NewRecorder()

	a.HandleLogin(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("Expected status code 500, got %d", resp.StatusCode)
	}
}

