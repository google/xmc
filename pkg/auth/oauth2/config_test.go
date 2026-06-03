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
		val := getEnvOrFile("TEST_KEY_NOT_EXIST", "my-default-value")
		if val != "my-default-value" {
			t.Errorf("Expected 'my-default-value', got %q", val)
		}
	})

	t.Run("Literal environment value", func(t *testing.T) {
		os.Setenv("TEST_LITERAL_KEY", "literal-secret-123")
		defer os.Unsetenv("TEST_LITERAL_KEY")

		val := getEnvOrFile("TEST_LITERAL_KEY", "def")
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

		val := getEnvOrFile("TEST_SUFFIX_KEY", "def")
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

		val := getEnvOrFile("TEST_LITERAL_PATH_KEY", "def")
		if val != "secret-from-literal-path" {
			t.Errorf("Expected 'secret-from-literal-path', got %q", val)
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

	cfg := LoadConfig()
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
