// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package redisclient

import (
	"testing"
)

func TestGetRedisOptions(t *testing.T) {
	t.Run("empty_password", func(t *testing.T) {
		t.Setenv("REDIS_PASSWORD", "")
		opts := GetRedisOptions(Config{Addr: "127.0.0.1:6379"})
		if opts.Addr != "127.0.0.1:6379" {
			t.Errorf("Addr = %q, want %q", opts.Addr, "127.0.0.1:6379")
		}
		if opts.Password != "" {
			t.Errorf("Password = %q, want empty", opts.Password)
		}
	})

	t.Run("configured_password", func(t *testing.T) {
		t.Setenv("REDIS_PASSWORD", "secret-pass")
		opts := GetRedisOptions(Config{Addr: "10.0.0.5:6379"})
		if opts.Addr != "10.0.0.5:6379" {
			t.Errorf("Addr = %q, want %q", opts.Addr, "10.0.0.5:6379")
		}
		if opts.Password != "secret-pass" {
			t.Errorf("Password = %q, want %q", opts.Password, "secret-pass")
		}
	})
}

func TestNewClient(t *testing.T) {
	t.Setenv("REDIS_PASSWORD", "client-pass")
	client := NewClient(Config{Addr: "127.0.0.1:6379"})
	if client == nil {
		t.Fatal("NewClient() returned nil")
	}
	defer client.Close()

	opts := client.Options()
	if opts.Addr != "127.0.0.1:6379" {
		t.Errorf("Addr = %q, want %q", opts.Addr, "127.0.0.1:6379")
	}
	if opts.Password != "client-pass" {
		t.Errorf("Password = %q, want %q", opts.Password, "client-pass")
	}
}
