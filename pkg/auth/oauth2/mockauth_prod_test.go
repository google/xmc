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

//go:build !mockauth

package oauth2

import (
	"context"
	"errors"
	"testing"
)

func TestNewAuthenticatorWithoutMockAuthTag(t *testing.T) {
	a := NewAuthenticator(&Config{MockAuth: true})
	if _, gotGoogle := a.OAuthProvider.(*GoogleOAuthProvider); !gotGoogle {
		t.Errorf("NewAuthenticator() installed %T in a build without the mockauth tag, want *GoogleOAuthProvider", a.OAuthProvider)
	}
}

func TestLoadConfigRejectsDevAuthModesWithoutMockAuthTag(t *testing.T) {
	tests := []struct {
		name        string
		mockAuth    string
		disableAuth string
	}{
		{name: "MOCK_AUTH=true", mockAuth: "true", disableAuth: "false"},
		{name: "MOCK_AUTH=1", mockAuth: "1", disableAuth: "false"},
		{name: "MOCK_AUTH=YES", mockAuth: "YES", disableAuth: "false"},
		{name: "DISABLE_AUTHENTICATION=true", mockAuth: "false", disableAuth: "true"},
		{name: "DISABLE_AUTHENTICATION=1", mockAuth: "false", disableAuth: "1"},
		{name: "both", mockAuth: "true", disableAuth: "true"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("MOCK_AUTH", tt.mockAuth)
			t.Setenv("DISABLE_AUTHENTICATION", tt.disableAuth)
			t.Setenv("JWT_SECRET", "this-is-a-sufficiently-long-jwt-secret-value")
			cfg, err := LoadConfig()
			if !errors.Is(err, ErrDevAuthNotCompiledIn) || cfg != nil {
				t.Errorf("LoadConfig() = (%v, %v), want (nil, %v)", cfg, err, ErrDevAuthNotCompiledIn)
			}
		})
	}
}

func TestDisableAuthenticationNotHonoredWithoutMockAuthTag(t *testing.T) {
	if DevAuthCompiledIn() {
		t.Fatal("DevAuthCompiledIn() = true in a build without the mockauth tag")
	}
	a := NewAuthenticator(&Config{DisableAuthentication: true})
	if got, err := a.IsUserAuthorized(context.Background(), "anyone@external.com"); got || err == nil {
		t.Errorf("IsUserAuthorized() = (%v, %v), want (false, fail-closed error)", got, err)
	}
	if got, err := a.IsAdmin(context.Background(), "anyone@external.com"); got || !errors.Is(err, ErrAdminRoleNotConfigured) {
		t.Errorf("IsAdmin() = (%v, %v), want (false, %v)", got, err, ErrAdminRoleNotConfigured)
	}
}
