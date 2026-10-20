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

package oauth2

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestBareEmailAddress(t *testing.T) {
	type result struct {
		Email string
		OK    bool
	}
	tests := []struct {
		value string
		want  result
	}{
		{value: "user@example.com", want: result{"user@example.com", true}},
		{value: " Jane.Doe@Contoso.COM ", want: result{"jane.doe@contoso.com", true}},
		{value: " "},
		{value: "jdoe"},
		{value: "Jane <jane@example.com>"},
	}
	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			email, ok := BareEmailAddress(tt.value)
			if diff := cmp.Diff(tt.want, result{email, ok}); diff != "" {
				t.Errorf("BareEmailAddress(%q) mismatch (-want +got):\n%s", tt.value, diff)
			}
		})
	}
}

func TestEmailVerifiedAccepted(t *testing.T) {
	tests := []struct {
		name          string
		emailVerified any
		wantRequired  bool
		wantOptional  bool
	}{
		{name: "true", emailVerified: true, wantRequired: true, wantOptional: true},
		{name: "string true", emailVerified: "TRUE", wantRequired: true, wantOptional: true},
		{name: "missing", emailVerified: nil, wantRequired: false, wantOptional: true},
		{name: "false", emailVerified: false, wantRequired: false, wantOptional: false},
		{name: "string false", emailVerified: "false", wantRequired: false, wantOptional: false},
		{name: "non-boolean", emailVerified: 1.0, wantRequired: false, wantOptional: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := EmailVerifiedAccepted(tt.emailVerified, true); got != tt.wantRequired {
				t.Errorf("EmailVerifiedAccepted(%v, required) = %v, want %v", tt.emailVerified, got, tt.wantRequired)
			}
			if got := EmailVerifiedAccepted(tt.emailVerified, false); got != tt.wantOptional {
				t.Errorf("EmailVerifiedAccepted(%v, optional) = %v, want %v", tt.emailVerified, got, tt.wantOptional)
			}
		})
	}
}

func TestJWKSEmailPolicy(t *testing.T) {
	trustMissing := JWKSEmailPolicy{TrustMissingVerified: true}
	tests := []struct {
		name string
		cfg  Config
		want JWKSEmailPolicy
	}{
		{name: "no domain allowlist trusts a missing claim", cfg: Config{}, want: trustMissing},
		{name: "domain allowlist requires verified emails", cfg: Config{AllowedDomains: []string{"example.com"}}, want: JWKSEmailPolicy{}},
		{
			name: "explicit require with domain allowlist",
			cfg:  Config{AllowedDomains: []string{"example.com"}, EmailVerification: EmailVerificationRequire},
			want: JWKSEmailPolicy{},
		},
		{
			name: "trust-issuer with domain allowlist",
			cfg:  Config{AllowedDomains: []string{"example.com"}, EmailVerification: EmailVerificationTrustIssuer},
			want: trustMissing,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if diff := cmp.Diff(tt.want, tt.cfg.JWKSEmailPolicy()); diff != "" {
				t.Errorf("JWKSEmailPolicy() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestLoadConfigEmailVerification(t *testing.T) {
	const (
		dexAuthURL   = "https://dex.example.com/auth"
		entraAuthURL = "https://login.microsoftonline.com/contoso.onmicrosoft.com/oauth2/v2.0/authorize"
	)
	tests := []struct {
		name           string
		authURL        string
		allowedDomains string
		verification   string
		want           EmailVerification
		wantErr        bool
	}{
		{name: "Google default", want: EmailVerificationRequire},
		{name: "Google explicit default", verification: " Require ", want: EmailVerificationRequire},
		{name: "Google rejects trust-issuer", verification: "trust-issuer", wantErr: true},
		{name: "other provider default", authURL: dexAuthURL, want: EmailVerificationRequire},
		{name: "other provider trust-issuer", authURL: dexAuthURL, verification: "trust-issuer", want: EmailVerificationTrustIssuer},
		{
			name:    "trust-issuer with a domain allowlist",
			authURL: dexAuthURL, allowedDomains: "example.com", verification: "trust-issuer",
			want: EmailVerificationTrustIssuer,
		},
		{name: "unknown policy", authURL: dexAuthURL, verification: "skip", wantErr: true},
		{name: "Entra ID default", authURL: entraAuthURL, want: EmailVerificationRequire},
		{name: "Entra ID rejects trust-issuer (nOAuth)", authURL: entraAuthURL, verification: "trust-issuer", wantErr: true},
		{name: "Entra ID alias host rejects trust-issuer", authURL: "https://login.windows.net/contoso.com/oauth2/authorize", verification: "trust-issuer", wantErr: true},
		{name: "multi-tenant Entra ID authority", authURL: "https://login.microsoftonline.com/common/oauth2/v2.0/authorize", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("JWT_SECRET", loginTestSecret)
			t.Setenv("CLIENT_SECRET", "client-secret")
			// An empty AUTH_URL selects Google sign-in.
			t.Setenv("AUTH_URL", tt.authURL)
			t.Setenv("ALLOWED_DOMAINS", tt.allowedDomains)
			t.Setenv("OIDC_EMAIL_VERIFICATION", tt.verification)

			cfg, err := LoadConfig()
			if gotErr := err != nil; gotErr != tt.wantErr {
				t.Fatalf("LoadConfig() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if cfg.EmailVerification != tt.want {
				t.Errorf("LoadConfig() EmailVerification = %q, want %q", cfg.EmailVerification, tt.want)
			}
		})
	}
}
