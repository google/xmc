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
	"errors"
	"testing"
)

func TestGetOptionsUserNoPassword(t *testing.T) {
	t.Setenv("CLICKHOUSE_USER", "custom_user")
	t.Setenv("CLICKHOUSE_PASSWORD", "secret")

	opts, err := getOptionsUserNoPassword()
	if err != nil {
		t.Fatalf("getOptionsUserNoPassword failed: %v", err)
	}

	if opts.Auth.Username != "custom_user" {
		t.Errorf("got username %q, want %q", opts.Auth.Username, "custom_user")
	}
	if opts.Auth.Password != "" {
		t.Errorf("got password %q, want empty", opts.Auth.Password)
	}
}

func TestGetOptionsDefaultUser(t *testing.T) {
	t.Setenv("CLICKHOUSE_USER", "custom_user")
	t.Setenv("CLICKHOUSE_PASSWORD", "secret")

	opts, err := getOptionsDefaultUser()
	if err != nil {
		t.Fatalf("getOptionsDefaultUser failed: %v", err)
	}

	if opts.Auth.Username != "default" {
		t.Errorf("got username %q, want %q", opts.Auth.Username, "default")
	}
	if opts.Auth.Password != "" {
		t.Errorf("got password %q, want empty", opts.Auth.Password)
	}
}

func TestGetOptionsNoCredentials(t *testing.T) {
	t.Setenv("CLICKHOUSE_USER", "custom_user")
	t.Setenv("CLICKHOUSE_PASSWORD", "secret")

	opts, err := getOptionsNoCredentials()
	if err != nil {
		t.Fatalf("getOptionsNoCredentials failed: %v", err)
	}

	if opts.Auth.Username != "" {
		t.Errorf("got username %q, want empty", opts.Auth.Username)
	}
	if opts.Auth.Password != "" {
		t.Errorf("got password %q, want empty", opts.Auth.Password)
	}
}

func TestConnectWithFallbackFailure(t *testing.T) {
	t.Setenv("CLICKHOUSE_HOST", "127.0.0.1")
	t.Setenv("CLICKHOUSE_PORT", "1")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	conn, err := ConnectWithFallback(ctx)
	if err == nil {
		if conn != nil {
			_ = conn.Close()
		}
		t.Fatalf("expected error from ConnectWithFallback when database is unavailable")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected error chain to contain context.Canceled, got %v", err)
	}
}
