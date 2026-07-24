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

package clickhouse

import (
	"context"
	"os"
	"testing"
)

func TestAuthOptionStrategies(t *testing.T) {
	setEnv := func(t *testing.T, key, val string) {
		t.Helper()
		old := os.Getenv(key)
		if err := os.Setenv(key, val); err != nil {
			t.Fatalf("failed to set env %s: %v", key, err)
		}
		t.Cleanup(func() {
			if old == "" {
				_ = os.Unsetenv(key)
			} else {
				_ = os.Setenv(key, old)
			}
		})
	}

	setEnv(t, "CLICKHOUSE_USER", "custom_user")
	setEnv(t, "CLICKHOUSE_PASSWORD", "custom_pass")

	t.Run("getOptionsUserNoPassword", func(t *testing.T) {
		opts, err := getOptionsUserNoPassword()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if opts.Auth.Username != "custom_user" {
			t.Errorf("got username %q, want %q", opts.Auth.Username, "custom_user")
		}
		if opts.Auth.Password != "" {
			t.Errorf("got password %q, want empty", opts.Auth.Password)
		}
	})

	t.Run("getOptionsDefaultUser", func(t *testing.T) {
		opts, err := getOptionsDefaultUser()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if opts.Auth.Username != "default" {
			t.Errorf("got username %q, want %q", opts.Auth.Username, "default")
		}
		if opts.Auth.Password != "" {
			t.Errorf("got password %q, want empty", opts.Auth.Password)
		}
	})

	t.Run("getOptionsNoCredentials", func(t *testing.T) {
		opts, err := getOptionsNoCredentials()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if opts.Auth.Username != "" {
			t.Errorf("got username %q, want empty", opts.Auth.Username)
		}
		if opts.Auth.Password != "" {
			t.Errorf("got password %q, want empty", opts.Auth.Password)
		}
	})
}

func TestConnectWithFallbackFailure(t *testing.T) {
	oldHost := os.Getenv("CLICKHOUSE_HOST")
	oldPort := os.Getenv("CLICKHOUSE_PORT")
	_ = os.Setenv("CLICKHOUSE_HOST", "127.0.0.1")
	_ = os.Setenv("CLICKHOUSE_PORT", "1")
	defer func() {
		_ = os.Setenv("CLICKHOUSE_HOST", oldHost)
		_ = os.Setenv("CLICKHOUSE_PORT", oldPort)
	}()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	conn, err := ConnectWithFallback(ctx)
	if err == nil {
		if conn != nil {
			conn.Close()
		}
		t.Fatalf("expected error from ConnectWithFallback when database is unavailable")
	}
}
