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

package config

import (
	"flag"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestRegisterFlags(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want Config
	}{
		{name: "defaults", args: nil, want: Config{Port: DefaultPort}},
		{name: "explicit port", args: []string{"-port=9090"}, want: Config{Port: "9090"}},
		{
			name: "explicit redis address",
			args: []string{"-redis_addr=redis-service.xmc.svc.cluster.local:6379"},
			want: Config{Port: DefaultPort, RedisAddr: "redis-service.xmc.svc.cluster.local:6379"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fs := flag.NewFlagSet("test", flag.ContinueOnError)
			got := RegisterFlags(fs)
			if err := fs.Parse(tc.args); err != nil {
				t.Fatalf("fs.Parse(%v) error = %v", tc.args, err)
			}
			if diff := cmp.Diff(tc.want, *got); diff != "" {
				t.Errorf("RegisterFlags() config mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// validConfig returns a Config that passes Validate; test cases override single fields.
func validConfig() Config {
	return Config{Port: DefaultPort, RedisAddr: "redis-service:6379"}
}

func TestConfig_Validate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		modify  func(c *Config)
		wantErr bool
	}{
		{name: "valid config", modify: func(*Config) {}},
		{name: "minimum port", modify: func(c *Config) { c.Port = "1" }},
		{name: "maximum port", modify: func(c *Config) { c.Port = "65535" }},
		{name: "empty port", modify: func(c *Config) { c.Port = "" }, wantErr: true},
		{name: "non-numeric port", modify: func(c *Config) { c.Port = "http" }, wantErr: true},
		{name: "zero port", modify: func(c *Config) { c.Port = "0" }, wantErr: true},
		{name: "negative port", modify: func(c *Config) { c.Port = "-80" }, wantErr: true},
		{name: "port out of range", modify: func(c *Config) { c.Port = "65536" }, wantErr: true},
		{name: "redis IPv4 address", modify: func(c *Config) { c.RedisAddr = "10.0.0.3:6379" }},
		{name: "redis bracketed IPv6 address", modify: func(c *Config) { c.RedisAddr = "[::1]:6379" }},
		{name: "redis multibyte host", modify: func(c *Config) { c.RedisAddr = "rédis.example:6379" }},
		{name: "empty redis address", modify: func(c *Config) { c.RedisAddr = "" }, wantErr: true},
		{name: "redis address without port", modify: func(c *Config) { c.RedisAddr = "redis-service" }, wantErr: true},
		{name: "redis address with empty port", modify: func(c *Config) { c.RedisAddr = "redis-service:" }, wantErr: true},
		{name: "redis address with empty host", modify: func(c *Config) { c.RedisAddr = ":6379" }, wantErr: true},
		{name: "redis unbracketed IPv6 address", modify: func(c *Config) { c.RedisAddr = "::1:6379" }, wantErr: true},
		{name: "redis non-numeric port", modify: func(c *Config) { c.RedisAddr = "redis-service:redis" }, wantErr: true},
		{name: "redis multibyte port digits", modify: func(c *Config) { c.RedisAddr = "redis-service:６３７９" }, wantErr: true},
		{name: "redis port out of range", modify: func(c *Config) { c.RedisAddr = "redis-service:65536" }, wantErr: true},
		{name: "redis URL scheme", modify: func(c *Config) { c.RedisAddr = "redis://redis-service:6379" }, wantErr: true},
		{name: "redis credentials", modify: func(c *Config) { c.RedisAddr = "user:pass@redis-service:6379" }, wantErr: true},
		{name: "redis URL with credentials", modify: func(c *Config) { c.RedisAddr = "redis://u:p@redis-service:6379" }, wantErr: true},
		{name: "redis address with path", modify: func(c *Config) { c.RedisAddr = "redis-service:6379/0" }, wantErr: true},
		{name: "redis password as port", modify: func(c *Config) { c.RedisAddr = "redis-service:pass" }, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := validConfig()
			tc.modify(&cfg)
			err := cfg.Validate()
			if (err != nil) != tc.wantErr {
				t.Errorf("%+v.Validate() error = %v, wantErr %v", cfg, err, tc.wantErr)
			}
			if err != nil && cfg.RedisAddr != "" && redisAddrLeaks(err.Error(), cfg.RedisAddr) {
				t.Errorf("Validate() error = %q, want it not to echo redis_addr", err)
			}
		})
	}
}

// redisAddrLeaks reports whether msg contains addr or any credential fragment of the test inputs.
func redisAddrLeaks(msg, addr string) bool {
	for _, fragment := range []string{addr, "pass", "p@", "user"} {
		if strings.Contains(msg, fragment) {
			return true
		}
	}
	return false
}
