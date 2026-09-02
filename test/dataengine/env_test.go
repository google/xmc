// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package dataengine

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadEnvFile(t *testing.T) {
	tempDir := t.TempDir()
	envPath := filepath.Join(tempDir, ".env.test")

	content := `
# Comment line
TEST_CLICKHOUSE_HOST=10.0.0.1
TEST_CLICKHOUSE_PORT="9000"
TEST_CLICKHOUSE_USER='custom_user'
INVALID_LINE_WITHOUT_EQUALS
`
	if err := os.WriteFile(envPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test env file: %v", err)
	}

	// Preset one env var to ensure existing vars are not overwritten
	t.Setenv("TEST_CLICKHOUSE_HOST", "existing-host")

	origPort, portExists := os.LookupEnv("TEST_CLICKHOUSE_PORT")
	origUser, userExists := os.LookupEnv("TEST_CLICKHOUSE_USER")
	t.Cleanup(func() {
		if portExists {
			_ = os.Setenv("TEST_CLICKHOUSE_PORT", origPort) // nolint:determinism // restoring original pre-existing env state
		} else {
			_ = os.Unsetenv("TEST_CLICKHOUSE_PORT")
		}
		if userExists {
			_ = os.Setenv("TEST_CLICKHOUSE_USER", origUser) // nolint:determinism // restoring original pre-existing env state
		} else {
			_ = os.Unsetenv("TEST_CLICKHOUSE_USER")
		}
	})

	if err := LoadEnvFile(envPath); err != nil {
		t.Fatalf("LoadEnvFile failed: %v", err)
	}

	if got := os.Getenv("TEST_CLICKHOUSE_HOST"); got != "existing-host" {
		t.Errorf("TEST_CLICKHOUSE_HOST = %q, want existing-host", got)
	}
	if got := os.Getenv("TEST_CLICKHOUSE_PORT"); got != "9000" {
		t.Errorf("TEST_CLICKHOUSE_PORT = %q, want 9000", got)
	}
	if got := os.Getenv("TEST_CLICKHOUSE_USER"); got != "custom_user" {
		t.Errorf("TEST_CLICKHOUSE_USER = %q, want custom_user", got)
	}
}
