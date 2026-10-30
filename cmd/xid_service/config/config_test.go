// Copyright 2025 Google LLC
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
	"testing"
)

func TestConfig_Validate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{
			name:    "valid default port",
			cfg:     Config{Port: "50051"},
			wantErr: false,
		},
		{
			name:    "valid minimum port",
			cfg:     Config{Port: "1"},
			wantErr: false,
		},
		{
			name:    "valid maximum port",
			cfg:     Config{Port: "65535"},
			wantErr: false,
		},
		{
			name:    "invalid non-numeric port",
			cfg:     Config{Port: "abc"},
			wantErr: true,
		},
		{
			name:    "invalid empty port",
			cfg:     Config{Port: ""},
			wantErr: true,
		},
		{
			name:    "invalid zero port",
			cfg:     Config{Port: "0"},
			wantErr: true,
		},
		{
			name:    "invalid negative port",
			cfg:     Config{Port: "-80"},
			wantErr: true,
		},
		{
			name:    "invalid port out of range",
			cfg:     Config{Port: "65536"},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.cfg.validate()
			if (err != nil) != tc.wantErr {
				t.Errorf("validate() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestConfig_RegisterFlags(t *testing.T) {
	t.Parallel()

	fs := flag.NewFlagSet("test_flags", flag.ContinueOnError)
	var c Config
	c.registerFlags(fs)

	if c.Audience != "https://xmc.example.com" {
		t.Errorf("default c.Audience = %q, want %q", c.Audience, "https://xmc.example.com")
	}

	args := []string{
		"-xid_audience=https://custom-audience.example.com",
		"-xid_jwks_url=https://auth.example.com/keys",
	}
	if err := fs.Parse(args); err != nil {
		t.Fatalf("fs.Parse failed: %v", err)
	}

	if c.Audience != "https://custom-audience.example.com" {
		t.Errorf("c.Audience = %q, want %q", c.Audience, "https://custom-audience.example.com")
	}
	if c.JWKSURL != "https://auth.example.com/keys" {
		t.Errorf("c.JWKSURL = %q, want %q", c.JWKSURL, "https://auth.example.com/keys")
	}
}

func TestConfig_RegisterConfigs(t *testing.T) {
	t.Setenv("xid_grpc_port", "9090")
	t.Setenv("xid_quota_project_id", "my-test-proj")
	t.Setenv("xid_jwks_url", "https://auth.example.com/keys")

	var c Config
	c.registerConfigs()

	if c.Port != "9090" {
		t.Errorf("c.Port = %q, want %q", c.Port, "9090")
	}
	if c.QuotaProjectID != "my-test-proj" {
		t.Errorf("c.QuotaProjectID = %q, want %q", c.QuotaProjectID, "my-test-proj")
	}
	if c.JWKSURL != "https://auth.example.com/keys" {
		t.Errorf("c.JWKSURL = %q, want %q", c.JWKSURL, "https://auth.example.com/keys")
	}
}
