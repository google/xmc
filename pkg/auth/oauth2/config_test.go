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
		os.Setenv("TEST_LITERAL_KEY", "literal-secret-123")
		defer os.Unsetenv("TEST_LITERAL_KEY")

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

		os.Setenv("TEST_SUFFIX_KEY_FILE", secretPath)
		defer os.Unsetenv("TEST_SUFFIX_KEY_FILE")

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

		os.Setenv("TEST_LITERAL_PATH_KEY", secretPath)
		defer os.Unsetenv("TEST_LITERAL_PATH_KEY")

		val, err := getEnvOrFile("TEST_LITERAL_PATH_KEY", "def")
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
		if val != "secret-from-literal-path" {
			t.Errorf("Expected 'secret-from-literal-path', got %q", val)
		}
	})

	t.Run("File suffix env value read failure", func(t *testing.T) {
		os.Setenv("TEST_SUFFIX_KEY_FILE", "/nonexistent/path/to/secret")
		defer os.Unsetenv("TEST_SUFFIX_KEY_FILE")

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

		os.Setenv("TEST_LITERAL_DIR_KEY", dirPath)
		defer os.Unsetenv("TEST_LITERAL_DIR_KEY")

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
