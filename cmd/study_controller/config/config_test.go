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
			name: "explicit redis, ess, and components_namespace flags",
			args: []string{
				"-redis_addr=redis-service.xmc.svc.cluster.local:6379",
				"-ess_addr=grpc-service:50051",
				"-ess_audience=ess-aud.apps.googleusercontent.com",
				"-components_namespace=xmc-custom",
			},
			want: Config{
				Port:                DefaultPort,
				RedisAddr:           "redis-service.xmc.svc.cluster.local:6379",
				ESSAddr:             "grpc-service:50051",
				ESSAudience:         "ess-aud.apps.googleusercontent.com",
				ComponentsNamespace: "xmc-custom",
			},
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
	return Config{
		Port:        DefaultPort,
		RedisAddr:   "redis-service:6379",
		ESSAddr:     "grpc-service:50051",
		ESSAudience: "ess-audience.apps.googleusercontent.com",
	}
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
		{name: "signed port", modify: func(c *Config) { c.Port = "+8080" }, wantErr: true},
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
		{name: "redis signed port", modify: func(c *Config) { c.RedisAddr = "redis-service:+6379" }, wantErr: true},
		{name: "redis multibyte port digits", modify: func(c *Config) { c.RedisAddr = "redis-service:６３７９" }, wantErr: true},
		{name: "redis port out of range", modify: func(c *Config) { c.RedisAddr = "redis-service:65536" }, wantErr: true},
		{name: "redis URL scheme", modify: func(c *Config) { c.RedisAddr = "redis://redis-service:6379" }, wantErr: true},
		{name: "redis credentials", modify: func(c *Config) { c.RedisAddr = "user:pass@redis-service:6379" }, wantErr: true},
		{name: "redis URL with credentials", modify: func(c *Config) { c.RedisAddr = "redis://u:p@redis-service:6379" }, wantErr: true},
		{name: "redis address with path", modify: func(c *Config) { c.RedisAddr = "redis-service:6379/0" }, wantErr: true},
		{name: "redis password as port", modify: func(c *Config) { c.RedisAddr = "redis-service:pass" }, wantErr: true},
		{name: "redis host with space", modify: func(c *Config) { c.RedisAddr = "redis service:6379" }, wantErr: true},
		{name: "ess IPv4 address", modify: func(c *Config) { c.ESSAddr = "10.0.0.5:50051" }},
		{name: "ess bracketed IPv6 address", modify: func(c *Config) { c.ESSAddr = "[::1]:50051" }},
		{name: "ess multibyte host", modify: func(c *Config) { c.ESSAddr = "éss.example:50051" }},
		{name: "empty ess address", modify: func(c *Config) { c.ESSAddr = "" }, wantErr: true},
		{name: "ess address without port", modify: func(c *Config) { c.ESSAddr = "grpc-service" }, wantErr: true},
		{name: "ess address with empty port", modify: func(c *Config) { c.ESSAddr = "grpc-service:" }, wantErr: true},
		{name: "ess address with empty host", modify: func(c *Config) { c.ESSAddr = ":50051" }, wantErr: true},
		{name: "ess unbracketed IPv6 address", modify: func(c *Config) { c.ESSAddr = "::1:50051" }, wantErr: true},
		{name: "ess non-numeric port", modify: func(c *Config) { c.ESSAddr = "grpc-service:grpc" }, wantErr: true},
		{name: "ess signed port", modify: func(c *Config) { c.ESSAddr = "grpc-service:+50051" }, wantErr: true},
		{name: "ess multibyte port digits", modify: func(c *Config) { c.ESSAddr = "grpc-service:５００５１" }, wantErr: true},
		{name: "ess port out of range", modify: func(c *Config) { c.ESSAddr = "grpc-service:65536" }, wantErr: true},
		{name: "ess URL scheme", modify: func(c *Config) { c.ESSAddr = "https://grpc-service:50051" }, wantErr: true},
		{name: "ess credentials", modify: func(c *Config) { c.ESSAddr = "user:secret@grpc-service:50051" }, wantErr: true},
		{name: "ess URL with credentials", modify: func(c *Config) { c.ESSAddr = "http://u:secret@grpc-service:50051" }, wantErr: true},
		{name: "ess address with path", modify: func(c *Config) { c.ESSAddr = "grpc-service:50051/v1" }, wantErr: true},
		{name: "ess secret as port", modify: func(c *Config) { c.ESSAddr = "grpc-service:secret" }, wantErr: true},
		{name: "ess host with space", modify: func(c *Config) { c.ESSAddr = "grpc service:50051" }, wantErr: true},
		{name: "ess host with newline", modify: func(c *Config) { c.ESSAddr = "grpc\nservice:50051" }, wantErr: true},
		{name: "ess host with NUL control char", modify: func(c *Config) { c.ESSAddr = "grpc\x00service:50051" }, wantErr: true},
		{name: "ess host with unicode non-breaking space", modify: func(c *Config) { c.ESSAddr = "grpc\u00a0service:50051" }, wantErr: true},
		{name: "ess audience URL", modify: func(c *Config) { c.ESSAudience = "https://xmc.example.com/grpc" }},
		{name: "ess audience multibyte", modify: func(c *Config) { c.ESSAudience = "aud-é-123" }},
		{name: "empty ess audience", modify: func(c *Config) { c.ESSAudience = "" }, wantErr: true},
		{name: "whitespace-only ess audience", modify: func(c *Config) { c.ESSAudience = " \t\n " }, wantErr: true},
		{name: "ess audience leading space", modify: func(c *Config) { c.ESSAudience = " secret-aud" }, wantErr: true},
		{name: "ess audience trailing space", modify: func(c *Config) { c.ESSAudience = "secret-aud " }, wantErr: true},
		{name: "ess audience embedded space", modify: func(c *Config) { c.ESSAudience = "secret aud" }, wantErr: true},
		{name: "ess audience newline", modify: func(c *Config) { c.ESSAudience = "secret\naud" }, wantErr: true},
		{name: "ess audience NUL control char", modify: func(c *Config) { c.ESSAudience = "secret\x00aud" }, wantErr: true},
		{name: "ess audience DEL control char", modify: func(c *Config) { c.ESSAudience = "secret\x7faud" }, wantErr: true},
		{name: "ess audience unicode non-breaking space", modify: func(c *Config) { c.ESSAudience = "secret\u00a0aud" }, wantErr: true},
		{name: "empty components namespace allowed", modify: func(c *Config) { c.ComponentsNamespace = "" }},
		{name: "valid components namespace", modify: func(c *Config) { c.ComponentsNamespace = "xmc-staging" }},
		{name: "components namespace with space", modify: func(c *Config) { c.ComponentsNamespace = "xmc ns" }, wantErr: true},
		{name: "components namespace with control char", modify: func(c *Config) { c.ComponentsNamespace = "xmc\x00" }, wantErr: true},
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
			if err != nil && secretInputLeaks(err.Error(), cfg.RedisAddr, cfg.ESSAddr, cfg.ESSAudience) {
				t.Errorf("Validate() error = %q, want it not to echo addresses, audience, or credential fragments", err)
			}
		})
	}
}

// secretInputLeaks reports whether msg contains any non-empty input or credential fragment.
func secretInputLeaks(msg string, inputs ...string) bool {
	for _, in := range inputs {
		if in != "" && strings.Contains(msg, in) {
			return true
		}
	}
	for _, fragment := range []string{"pass", "p@", "user", "secret"} {
		if strings.Contains(msg, fragment) {
			return true
		}
	}
	return false
}
