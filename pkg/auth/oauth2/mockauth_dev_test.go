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

//go:build mockauth

package oauth2

import "testing"

func TestNewAuthenticatorMockAuthTag(t *testing.T) {
	tests := []struct {
		name     string
		mockAuth bool
		wantMock bool
	}{
		{name: "mock auth enabled", mockAuth: true, wantMock: true},
		{name: "mock auth disabled", mockAuth: false, wantMock: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := NewAuthenticator(&Config{MockAuth: tt.mockAuth})
			_, gotMock := a.OAuthProvider.(*MockOAuthProvider)
			if gotMock != tt.wantMock {
				t.Errorf("NewAuthenticator() installed mock provider = %v, want %v", gotMock, tt.wantMock)
			}
		})
	}
}

func TestLoadConfigAcceptsMockAuthWithMockAuthTag(t *testing.T) {
	tests := []struct {
		name          string
		jwtSecret     string
		wantJWTSecret string
	}{
		{name: "missing secret falls back to dev-only secret", jwtSecret: "", wantJWTSecret: devOnlyJWTSecret},
		{name: "short secret allowed", jwtSecret: "short-secret", wantJWTSecret: "short-secret"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("MOCK_AUTH", "true")
			t.Setenv("DISABLE_AUTHENTICATION", "false")
			t.Setenv("JWT_SECRET", tt.jwtSecret)
			cfg, err := LoadConfig()
			if err != nil {
				t.Fatalf("LoadConfig() error = %v, want nil", err)
			}
			if !cfg.MockAuth || cfg.JWTSecret != tt.wantJWTSecret {
				t.Errorf("LoadConfig() = {MockAuth: %v, JWTSecret: %q}, want {MockAuth: true, JWTSecret: %q}", cfg.MockAuth, cfg.JWTSecret, tt.wantJWTSecret)
			}
		})
	}
}
