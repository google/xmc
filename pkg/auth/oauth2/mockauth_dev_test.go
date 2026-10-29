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

import (
	"context"
	"testing"
)

func TestDisableAuthenticationHonoredWithMockAuthTag(t *testing.T) {
	if !DevAuthCompiledIn() {
		t.Fatal("DevAuthCompiledIn() = false in a build with the mockauth tag")
	}
	a := NewAuthenticator(&Config{DisableAuthentication: true})
	if got, err := a.IsUserAuthorized(context.Background(), "anyone@external.com"); !got || err != nil {
		t.Errorf("IsUserAuthorized() = (%v, %v), want (true, nil)", got, err)
	}
	if got, err := a.IsAdmin(context.Background(), "anyone@external.com"); !got || err != nil {
		t.Errorf("IsAdmin() = (%v, %v), want (true, nil)", got, err)
	}
}

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

func TestLoadConfigAcceptsDevAuthModesWithMockAuthTag(t *testing.T) {
	type modes struct {
		MockAuth, DisableAuthentication bool
		JWTSecret                       string
	}
	tests := []struct {
		name        string
		mockAuth    string
		disableAuth string
		jwtSecret   string
		want        modes
	}{
		{name: "mock auth, missing secret falls back to dev-only secret", mockAuth: "true", disableAuth: "false", jwtSecret: "", want: modes{MockAuth: true, JWTSecret: devOnlyJWTSecret}},
		{name: "mock auth, short secret allowed", mockAuth: "true", disableAuth: "false", jwtSecret: "short-secret", want: modes{MockAuth: true, JWTSecret: "short-secret"}},
		{name: "disabled auth, missing secret falls back to dev-only secret", mockAuth: "false", disableAuth: "true", jwtSecret: "", want: modes{DisableAuthentication: true, JWTSecret: devOnlyJWTSecret}},
		{name: "disabled auth, short secret allowed", mockAuth: "false", disableAuth: "true", jwtSecret: "short-secret", want: modes{DisableAuthentication: true, JWTSecret: "short-secret"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("MOCK_AUTH", tt.mockAuth)
			t.Setenv("DISABLE_AUTHENTICATION", tt.disableAuth)
			t.Setenv("JWT_SECRET", tt.jwtSecret)
			cfg, err := LoadConfig()
			if err != nil {
				t.Fatalf("LoadConfig() error = %v, want nil", err)
			}
			got := modes{MockAuth: cfg.MockAuth, DisableAuthentication: cfg.DisableAuthentication, JWTSecret: cfg.JWTSecret}
			if got != tt.want {
				t.Errorf("LoadConfig() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
