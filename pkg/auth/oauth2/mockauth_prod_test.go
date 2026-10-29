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
	"errors"
	"testing"
)

func TestNewAuthenticatorWithoutMockAuthTag(t *testing.T) {
	a := NewAuthenticator(&Config{MockAuth: true})
	if _, gotGoogle := a.OAuthProvider.(*GoogleOAuthProvider); !gotGoogle {
		t.Errorf("NewAuthenticator() installed %T in a build without the mockauth tag, want *GoogleOAuthProvider", a.OAuthProvider)
	}
}

func TestLoadConfigRejectsMockAuthWithoutMockAuthTag(t *testing.T) {
	for _, value := range []string{"true", "1", "YES"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("MOCK_AUTH", value)
			t.Setenv("JWT_SECRET", "this-is-a-sufficiently-long-jwt-secret-value")
			cfg, err := LoadConfig()
			if !errors.Is(err, ErrDevAuthNotCompiledIn) || cfg != nil {
				t.Errorf("LoadConfig() with MOCK_AUTH=%q = (%v, %v), want (nil, %v)", value, cfg, err, ErrDevAuthNotCompiledIn)
			}
		})
	}
}

func TestLoadConfigHasNoDevJWTSecretWithoutMockAuthTag(t *testing.T) {
	t.Setenv("MOCK_AUTH", "false")
	t.Setenv("DISABLE_AUTHENTICATION", "true")
	t.Setenv("JWT_SECRET", "")
	if cfg, err := LoadConfig(); err == nil {
		t.Errorf("LoadConfig() = (%+v, nil), want an error because the dev-only JWT secret is not compiled in", cfg)
	}
}
