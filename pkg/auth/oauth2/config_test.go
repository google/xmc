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
	"os"
	"path/filepath"
	"testing"
)

func TestGetEnvOrFile(t *testing.T) {
	tmpDir := t.TempDir()

	t.Run("Default fallback", func(t *testing.T) {
		val, err := getEnvOrFile("TEST_KEY_NOT_EXIST", "my-default-value")
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
		if val != "my-default-value" {
			t.Errorf("Expected 'my-default-value', got %q", val)
		}
	})

	t.Run("Literal environment value", func(t *testing.T) {
		t.Setenv("TEST_LITERAL_KEY", "literal-secret-123")

		val, err := getEnvOrFile("TEST_LITERAL_KEY", "def")
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
		if val != "literal-secret-123" {
			t.Errorf("Expected 'literal-secret-123', got %q", val)
		}
	})

	t.Run("File suffix env value", func(t *testing.T) {
		secretPath := filepath.Join(tmpDir, "secret_file_suffix")
		err := os.WriteFile(secretPath, []byte("  secret-from-suffix-file  \n"), 0600)
		if err != nil {
			t.Fatalf("Failed to write temporary secret file: %v", err)
		}

		t.Setenv("TEST_SUFFIX_KEY_FILE", secretPath)

		val, err := getEnvOrFile("TEST_SUFFIX_KEY", "def")
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
		if val != "secret-from-suffix-file" {
			t.Errorf("Expected 'secret-from-suffix-file', got %q", val)
		}
	})

	t.Run("Literal key pointing to a file path", func(t *testing.T) {
		secretPath := filepath.Join(tmpDir, "secret_file_literal")
		err := os.WriteFile(secretPath, []byte("secret-from-literal-path"), 0600)
		if err != nil {
			t.Fatalf("Failed to write temporary secret file: %v", err)
		}

		t.Setenv("TEST_LITERAL_PATH_KEY", secretPath)

		val, err := getEnvOrFile("TEST_LITERAL_PATH_KEY", "def")
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
		if val != "secret-from-literal-path" {
			t.Errorf("Expected 'secret-from-literal-path', got %q", val)
		}
	})

	t.Run("File suffix env value read failure", func(t *testing.T) {
		t.Setenv("TEST_SUFFIX_KEY_FILE", "/nonexistent/path/to/secret")

		_, err := getEnvOrFile("TEST_SUFFIX_KEY", "def")
		if err == nil {
			t.Error("Expected error due to nonexistent file, got nil")
		}
	})

	t.Run("Literal key pointing to a file path read failure", func(t *testing.T) {
		// Create a directory, which will succeed os.Stat but fail os.ReadFile
		dirPath := filepath.Join(tmpDir, "secret_dir_literal")
		err := os.Mkdir(dirPath, 0755)
		if err != nil {
			t.Fatalf("Failed to create temporary directory: %v", err)
		}

		t.Setenv("TEST_LITERAL_DIR_KEY", dirPath)

		_, err = getEnvOrFile("TEST_LITERAL_DIR_KEY", "def")
		if err == nil {
			t.Error("Expected error due to directory path read failure, got nil")
		}
	})
}

func TestLoadConfig(t *testing.T) {
	os.Setenv("PORT", "9090")
	os.Setenv("MOCK_AUTH", "true")
	os.Setenv("DISABLE_AUTHENTICATION", "true")
	os.Setenv("ALLOWED_DOMAINS", "company.com, google.com")
	os.Setenv("ALLOWED_EMAILS", "a@b.com b@c.com")
	os.Setenv("ALLOWED_ORIGINS", "http://org1.com,http://org2.com")
	os.Setenv("CLIENT_ID", "my-client")
	os.Setenv("CLIENT_SECRET", "my-secret")

	defer func() {
		os.Unsetenv("PORT")
		os.Unsetenv("MOCK_AUTH")
		os.Unsetenv("DISABLE_AUTHENTICATION")
		os.Unsetenv("ALLOWED_DOMAINS")
		os.Unsetenv("ALLOWED_EMAILS")
		os.Unsetenv("ALLOWED_ORIGINS")
		os.Unsetenv("CLIENT_ID")
		os.Unsetenv("CLIENT_SECRET")
	}()

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() failed unexpectedly: %v", err)
	}
	if cfg.Port != "9090" {
		t.Errorf("Expected Port 9090, got %q", cfg.Port)
	}
	if !cfg.MockAuth {
		t.Error("Expected MockAuth true")
	}
	if !cfg.DisableAuthentication {
		t.Error("Expected DisableAuthentication true")
	}
	if len(cfg.AllowedDomains) != 2 || cfg.AllowedDomains[0] != "company.com" || cfg.AllowedDomains[1] != "google.com" {
		t.Errorf("Expected AllowedDomains [company.com, google.com], got %v", cfg.AllowedDomains)
	}
	if len(cfg.AllowedEmails) != 2 || cfg.AllowedEmails[0] != "a@b.com" || cfg.AllowedEmails[1] != "b@c.com" {
		t.Errorf("Expected AllowedEmails [a@b.com, b@c.com], got %v", cfg.AllowedEmails)
	}
	if len(cfg.AllowedOrigins) != 2 || cfg.AllowedOrigins[0] != "http://org1.com" || cfg.AllowedOrigins[1] != "http://org2.com" {
		t.Errorf("Expected AllowedOrigins [http://org1.com, http://org2.com], got %v", cfg.AllowedOrigins)
	}
}

func TestLoadConfig_JWTSecretValidation(t *testing.T) {
	const longSecret = "this-is-a-sufficiently-long-jwt-secret-value" // >= 32 bytes

	tests := []struct {
		name      string
		mockAuth  string
		jwtSecret string
		wantErr   bool
	}{
		{name: "auth enabled, missing secret", mockAuth: "false", jwtSecret: "", wantErr: true},
		{name: "auth enabled, secret too short", mockAuth: "false", jwtSecret: "short-secret", wantErr: true},
		{name: "auth enabled, secret long enough", mockAuth: "false", jwtSecret: longSecret, wantErr: false},
		{name: "dev mode, short secret allowed", mockAuth: "true", jwtSecret: "short-secret", wantErr: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("MOCK_AUTH", tc.mockAuth)
			t.Setenv("DISABLE_AUTHENTICATION", "false")
			t.Setenv("CLIENT_SECRET", "client-secret")
			t.Setenv("JWT_SECRET", tc.jwtSecret)
			if _, err := LoadConfig(); (err != nil) != tc.wantErr {
				t.Fatalf("LoadConfig() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestParseCommaSpaceSeparated(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "Empty input",
			input:    "",
			expected: nil,
		},
		{
			name:     "Whitespace only",
			input:    "   ",
			expected: nil,
		},
		{
			name:     "Commas only",
			input:    ",,,",
			expected: nil,
		},
		{
			name:     "Whitespace and commas only",
			input:    " , ,   ",
			expected: nil,
		},
		{
			name:     "Comma separated",
			input:    "a,b,c",
			expected: []string{"a", "b", "c"},
		},
		{
			name:     "Space separated",
			input:    "a b c",
			expected: []string{"a", "b", "c"},
		},
		{
			name:     "Mixed separated with extra spaces",
			input:    " a, b,  c d,e ",
			expected: []string{"a", "b", "c", "d", "e"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseCommaSpaceSeparated(tc.input)
			if len(got) != len(tc.expected) {
				t.Fatalf("Expected length %d, got %d (expected %v, got %v)", len(tc.expected), len(got), tc.expected, got)
			}
			for i := range got {
				if got[i] != tc.expected[i] {
					t.Errorf("At index %d: expected %q, got %q", i, tc.expected[i], got[i])
				}
			}
		})
	}
}

func TestLoadConfig_OIDCIssuer(t *testing.T) {
	const validJWT = "this-is-a-valid-length-jwt-secret-key-32bytes"

	tests := []struct {
		name              string
		env               map[string]string
		wantAuthURL       string
		wantTokenURL      string
		wantAPIURL        string
		wantClientID      string
		wantClientSecret  string
		wantOIDCIssuerURL string
	}{
		{
			name: "default Google endpoints when OIDC_ISSUER_URL unset",
			env: map[string]string{
				"JWT_SECRET":    validJWT,
				"CLIENT_SECRET": "test-secret",
				"CLIENT_ID":     "test-client",
			},
			wantAuthURL:       "https://accounts.google.com/o/oauth2/v2/auth",
			wantTokenURL:      "https://oauth2.googleapis.com/token",
			wantAPIURL:        "https://openidconnect.googleapis.com/v1/userinfo",
			wantClientID:      "test-client",
			wantClientSecret:  "test-secret",
			wantOIDCIssuerURL: "",
		},
		{
			name: "derive endpoints from OIDC_ISSUER_URL without trailing slash",
			env: map[string]string{
				"JWT_SECRET":      validJWT,
				"CLIENT_SECRET":   "test-secret",
				"OIDC_ISSUER_URL": "http://dex.example.com:5556/dex",
			},
			wantAuthURL:       "http://dex.example.com:5556/dex/auth",
			wantTokenURL:      "http://dex.example.com:5556/dex/token",
			wantAPIURL:        "http://dex.example.com:5556/dex/userinfo",
			wantOIDCIssuerURL: "http://dex.example.com:5556/dex",
		},
		{
			name: "derive endpoints from OIDC_ISSUER_URL with trailing slash",
			env: map[string]string{
				"JWT_SECRET":      validJWT,
				"CLIENT_SECRET":   "test-secret",
				"OIDC_ISSUER_URL": "http://dex.example.com:5556/dex/",
			},
			wantAuthURL:       "http://dex.example.com:5556/dex/auth",
			wantTokenURL:      "http://dex.example.com:5556/dex/token",
			wantAPIURL:        "http://dex.example.com:5556/dex/userinfo",
			wantOIDCIssuerURL: "http://dex.example.com:5556/dex/",
		},
		{
			name: "explicit AUTH_URL and TOKEN_URL override derived OIDC URLs",
			env: map[string]string{
				"JWT_SECRET":      validJWT,
				"CLIENT_SECRET":   "test-secret",
				"OIDC_ISSUER_URL": "http://dex.example.com:5556/dex",
				"AUTH_URL":        "http://custom.auth/authorize",
				"TOKEN_URL":       "http://custom.token/token",
				"API_URL":         "http://custom.userinfo/userinfo",
			},
			wantAuthURL:       "http://custom.auth/authorize",
			wantTokenURL:      "http://custom.token/token",
			wantAPIURL:        "http://custom.userinfo/userinfo",
			wantOIDCIssuerURL: "http://dex.example.com:5556/dex",
		},
		{
			name: "OIDC_CLIENT_ID fallback when CLIENT_ID is unset",
			env: map[string]string{
				"JWT_SECRET":     validJWT,
				"CLIENT_SECRET":  "test-secret",
				"OIDC_CLIENT_ID": "dex-client-id",
			},
			wantClientID:     "dex-client-id",
			wantClientSecret: "test-secret",
			wantAuthURL:      "https://accounts.google.com/o/oauth2/v2/auth",
			wantTokenURL:     "https://oauth2.googleapis.com/token",
			wantAPIURL:       "https://openidconnect.googleapis.com/v1/userinfo",
		},
		{
			name: "CLIENT_ID takes precedence over OIDC_CLIENT_ID",
			env: map[string]string{
				"JWT_SECRET":     validJWT,
				"CLIENT_SECRET":  "test-secret",
				"CLIENT_ID":      "primary-client-id",
				"OIDC_CLIENT_ID": "dex-client-id",
			},
			wantClientID:     "primary-client-id",
			wantClientSecret: "test-secret",
			wantAuthURL:      "https://accounts.google.com/o/oauth2/v2/auth",
			wantTokenURL:     "https://oauth2.googleapis.com/token",
			wantAPIURL:       "https://openidconnect.googleapis.com/v1/userinfo",
		},
		{
			name: "OIDC_CLIENT_SECRET fallback when CLIENT_SECRET is unset",
			env: map[string]string{
				"JWT_SECRET":         validJWT,
				"OIDC_CLIENT_SECRET": "dex-secret-fallback",
			},
			wantClientSecret: "dex-secret-fallback",
			wantAuthURL:      "https://accounts.google.com/o/oauth2/v2/auth",
			wantTokenURL:     "https://oauth2.googleapis.com/token",
			wantAPIURL:       "https://openidconnect.googleapis.com/v1/userinfo",
		},
		{
			name: "CLIENT_SECRET takes precedence over OIDC_CLIENT_SECRET",
			env: map[string]string{
				"JWT_SECRET":         validJWT,
				"CLIENT_SECRET":      "primary-secret",
				"OIDC_CLIENT_SECRET": "dex-secret-fallback",
			},
			wantClientSecret: "primary-secret",
			wantAuthURL:      "https://accounts.google.com/o/oauth2/v2/auth",
			wantTokenURL:     "https://oauth2.googleapis.com/token",
			wantAPIURL:       "https://openidconnect.googleapis.com/v1/userinfo",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			cfg, err := LoadConfig()
			if err != nil {
				t.Fatalf("LoadConfig() failed unexpectedly: %v", err)
			}
			if cfg.AuthURL != tc.wantAuthURL {
				t.Errorf("AuthURL = %q, want %q", cfg.AuthURL, tc.wantAuthURL)
			}
			if cfg.TokenURL != tc.wantTokenURL {
				t.Errorf("TokenURL = %q, want %q", cfg.TokenURL, tc.wantTokenURL)
			}
			if cfg.APIURL != tc.wantAPIURL {
				t.Errorf("APIURL = %q, want %q", cfg.APIURL, tc.wantAPIURL)
			}
			if tc.wantClientID != "" && cfg.ClientID != tc.wantClientID {
				t.Errorf("ClientID = %q, want %q", cfg.ClientID, tc.wantClientID)
			}
			if tc.wantClientSecret != "" && cfg.ClientSecret != tc.wantClientSecret {
				t.Errorf("ClientSecret = %q, want %q", cfg.ClientSecret, tc.wantClientSecret)
			}
			if cfg.OIDCIssuerURL != tc.wantOIDCIssuerURL {
				t.Errorf("OIDCIssuerURL = %q, want %q", cfg.OIDCIssuerURL, tc.wantOIDCIssuerURL)
			}
		})
	}
}

func TestIsNonProdAuth(t *testing.T) {
	tests := []struct {
		name     string
		config   Config
		expected bool
	}{
		{
			name: "Production Google authentication",
			config: Config{
				MockAuth:              false,
				DisableAuthentication: false,
				AuthURL:               "https://accounts.google.com/o/oauth2/v2/auth",
				TokenURL:              "https://oauth2.googleapis.com/token",
				APIURL:                "https://openidconnect.googleapis.com/v1/userinfo",
			},
			expected: false,
		},
		{
			name: "MockAuth active",
			config: Config{
				MockAuth: true,
				AuthURL:  "https://accounts.google.com/o/oauth2/v2/auth",
			},
			expected: true,
		},
		{
			name: "DisableAuthentication active",
			config: Config{
				DisableAuthentication: true,
				AuthURL:               "https://accounts.google.com/o/oauth2/v2/auth",
			},
			expected: true,
		},
		{
			name: "Custom OIDC Issuer (Dex)",
			config: Config{
				OIDCIssuerURL: "http://dex:5556/dex",
				AuthURL:       "http://dex:5556/dex/auth",
			},
			expected: true,
		},
		{
			name: "Google accounts OIDC Issuer",
			config: Config{
				OIDCIssuerURL: "https://accounts.google.com",
				AuthURL:       "https://accounts.google.com/o/oauth2/v2/auth",
			},
			expected: false,
		},
		{
			name: "Localhost AuthURL",
			config: Config{
				AuthURL: "http://localhost:8080/auth",
			},
			expected: true,
		},
		{
			name: "127.0.0.1 AuthURL",
			config: Config{
				AuthURL: "http://127.0.0.1:5556/dex/auth",
			},
			expected: true,
		},
		{
			name: "Dex in AuthURL",
			config: Config{
				AuthURL: "http://dex.auth.svc.cluster.local:5556/dex/auth",
			},
			expected: true,
		},
		{
			name: "Custom external IdP AuthURL",
			config: Config{
				AuthURL: "https://login.company.internal/oauth/authorize",
			},
			expected: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.config.IsNonProdAuth()
			if got != tc.expected {
				t.Errorf("IsNonProdAuth() = %v, want %v", got, tc.expected)
			}
		})
	}
}
