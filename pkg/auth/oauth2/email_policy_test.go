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

func TestEmailClaimAddress(t *testing.T) {
	claims := IdentityClaims{Email: "user@example.com", PreferredUsername: " Jane.Doe@Contoso.com ", UPN: "jdoe@contoso.com"}
	type result struct {
		Email string
		OK    bool
	}
	tests := []struct {
		name   string
		claim  EmailClaim
		claims IdentityClaims
		want   result
	}{
		{name: "email", claim: EmailClaimEmail, claims: claims, want: result{"user@example.com", true}},
		{name: "zero claim reads email", claims: claims, want: result{"user@example.com", true}},
		{name: "preferred_username", claim: EmailClaimPreferredUsername, claims: claims, want: result{"jane.doe@contoso.com", true}},
		{name: "upn", claim: EmailClaimUPN, claims: claims, want: result{"jdoe@contoso.com", true}},
		{name: "configured claim missing", claim: EmailClaimUPN, claims: IdentityClaims{Email: "user@example.com"}},
		{name: "username that is not an email", claim: EmailClaimPreferredUsername, claims: IdentityClaims{PreferredUsername: "jdoe"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			email, ok := tt.claim.Address(tt.claims)
			if diff := cmp.Diff(tt.want, result{email, ok}); diff != "" {
				t.Errorf("Address() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestJWKSEmailPolicyAcceptsEmailVerified(t *testing.T) {
	tests := []struct {
		name          string
		policy        JWKSEmailPolicy
		emailVerified any
		want          bool
	}{
		{name: "strict: missing", emailVerified: nil, want: false},
		{name: "strict: true", emailVerified: true, want: true},
		{name: "trusted missing", policy: JWKSEmailPolicy{TrustMissingVerified: true}, emailVerified: nil, want: true},
		{name: "trusted missing: false", policy: JWKSEmailPolicy{TrustMissingVerified: true}, emailVerified: false, want: false},
		{name: "upn ignores email_verified", policy: JWKSEmailPolicy{Claim: EmailClaimUPN}, emailVerified: false, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.policy.AcceptsEmailVerified(tt.emailVerified); got != tt.want {
				t.Errorf("AcceptsEmailVerified(%v) = %v, want %v", tt.emailVerified, got, tt.want)
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
		{
			name: "upn claim",
			cfg:  Config{AllowedDomains: []string{"contoso.com"}, EmailVerification: EmailVerificationTrustIssuer, EmailClaim: EmailClaimUPN},
			want: JWKSEmailPolicy{TrustMissingVerified: true, Claim: EmailClaimUPN},
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

// emailPolicy is the comparable email policy part of a loaded Config.
type emailPolicy struct {
	Verification EmailVerification
	Claim        EmailClaim
}

func TestLoadConfigEmailPolicy(t *testing.T) {
	const (
		dexAuthURL   = "https://dex.example.com/auth"
		entraAuthURL = "https://login.microsoftonline.com/contoso.onmicrosoft.com/oauth2/v2.0/authorize"
	)
	defaults := emailPolicy{EmailVerificationRequire, EmailClaimEmail}
	trustIssuer := emailPolicy{EmailVerificationTrustIssuer, EmailClaimEmail}
	// entraUPN reads `upn` from Entra ID tokens pinned to the tenant issuer; mutate varies it.
	entraUPN := func(mutate func(map[string]string)) map[string]string {
		env := map[string]string{
			"AUTH_URL": entraAuthURL, "OIDC_EMAIL_VERIFICATION": "trust-issuer", "OIDC_EMAIL_CLAIM": "upn",
			"OIDC_JWKS_URL": "https://login.microsoftonline.com/contoso.onmicrosoft.com/discovery/v2.0/keys", "IAP_EXPECTED_ISSUER": entraIDTokenIssuer,
		}
		if mutate != nil {
			mutate(env)
		}
		return env
	}
	tests := []struct {
		name    string
		env     map[string]string
		want    emailPolicy
		wantErr bool
	}{
		{name: "Google defaults", want: defaults},
		{name: "Google explicit defaults", env: map[string]string{"OIDC_EMAIL_VERIFICATION": " Require ", "OIDC_EMAIL_CLAIM": "EMAIL"}, want: defaults},
		{name: "Google rejects trust-issuer", env: map[string]string{"OIDC_EMAIL_VERIFICATION": "trust-issuer"}, wantErr: true},
		{name: "Google rejects upn", env: map[string]string{"OIDC_EMAIL_CLAIM": "upn"}, wantErr: true},
		{name: "other provider defaults", env: map[string]string{"AUTH_URL": dexAuthURL}, want: defaults},
		{name: "other provider trust-issuer", env: map[string]string{"AUTH_URL": dexAuthURL, "OIDC_EMAIL_VERIFICATION": "trust-issuer"}, want: trustIssuer},
		{
			name: "trust-issuer with a domain allowlist",
			env:  map[string]string{"AUTH_URL": dexAuthURL, "ALLOWED_DOMAINS": "example.com", "OIDC_EMAIL_VERIFICATION": "trust-issuer"},
			want: trustIssuer,
		},
		{name: "unknown policy", env: map[string]string{"AUTH_URL": dexAuthURL, "OIDC_EMAIL_VERIFICATION": "skip"}, wantErr: true},
		{name: "unknown claim", env: map[string]string{"AUTH_URL": dexAuthURL, "OIDC_EMAIL_CLAIM": "sub"}, wantErr: true},
		{name: "Entra ID default", env: map[string]string{"AUTH_URL": entraAuthURL}, want: defaults},
		{name: "Entra ID rejects trust-issuer for email (nOAuth)", env: map[string]string{"AUTH_URL": entraAuthURL, "OIDC_EMAIL_VERIFICATION": "trust-issuer"}, wantErr: true},
		{name: "multi-tenant Entra ID authority", env: map[string]string{"AUTH_URL": "https://login.microsoftonline.com/common/oauth2/v2.0/authorize"}, wantErr: true},
		{name: "Entra ID upn", env: entraUPN(nil), want: emailPolicy{EmailVerificationTrustIssuer, EmailClaimUPN}},
		{name: "upn requires trust-issuer", env: entraUPN(func(e map[string]string) { e["OIDC_EMAIL_VERIFICATION"] = "require" }), wantErr: true},
		{name: "upn requires a JWKS URL", env: entraUPN(func(e map[string]string) { delete(e, "OIDC_JWKS_URL") }), wantErr: true},
		{name: "upn requires the provider's issuer", env: entraUPN(func(e map[string]string) { e["IAP_EXPECTED_ISSUER"] = "https://iap.googleapis.com" }), wantErr: true},
		{
			name: "upn refuses a multi-tenant issuer",
			env: entraUPN(func(e map[string]string) {
				e["IAP_EXPECTED_ISSUER"] = "https://login.microsoftonline.com/organizations/v2.0"
			}),
			wantErr: true,
		},
		{
			name:    "Entra ID alias host rejects trust-issuer for email",
			env:     map[string]string{"AUTH_URL": "https://login.windows.net/contoso.com/oauth2/authorize", "OIDC_EMAIL_VERIFICATION": "trust-issuer"},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("JWT_SECRET", loginTestSecret)
			t.Setenv("CLIENT_SECRET", "client-secret")
			// An empty AUTH_URL selects Google sign-in.
			for _, key := range []string{"AUTH_URL", "ALLOWED_DOMAINS", "OIDC_EMAIL_VERIFICATION", "OIDC_EMAIL_CLAIM", "OIDC_JWKS_URL", "IAP_EXPECTED_ISSUER"} {
				t.Setenv(key, tt.env[key])
			}

			cfg, err := LoadConfig()
			if gotErr := err != nil; gotErr != tt.wantErr {
				t.Fatalf("LoadConfig() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if diff := cmp.Diff(tt.want, emailPolicy{cfg.EmailVerification, cfg.EmailClaim}); diff != "" {
				t.Errorf("LoadConfig() email policy mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
