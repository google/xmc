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
	"context"
	"encoding/json"
	"errors"
	"testing"

	"cloud.google.com/go/iam/apiv1/iampb"
	"github.com/google/go-cmp/cmp"
)

// identityResult is the comparable outcome of verifyUserInfo and admitLogin.
type identityResult struct {
	Email       string
	Name        string
	Picture     string
	NotAdmitted bool
	Err         bool
}

func toIdentityResult(identity loginIdentity, err error) identityResult {
	return identityResult{
		Email:       identity.email,
		Name:        identity.name,
		Picture:     identity.picture,
		NotAdmitted: errors.Is(err, errNotAdmitted),
		Err:         err != nil,
	}
}

func TestVerifyUserInfo(t *testing.T) {
	admitted := identityResult{Email: loginTestEmail, Name: "Test User", Picture: "https://example.com/p.png"}
	rejected := admitted
	rejected.NotAdmitted, rejected.Err = true, true
	aliasUser := identityResult{Email: "user@alias.com", Name: "Test User", Picture: "https://example.com/p.png"}
	tests := []struct {
		name   string
		cfg    func(*Config)
		mutate func(*GoogleUserInfo)
		want   identityResult
	}{
		{name: "verified workspace user", want: admitted},
		{name: "email is normalized", mutate: func(u *GoogleUserInfo) { u.Email = " User@MyCompany.COM " }, want: admitted},
		{name: "hosted domain is case-insensitive", mutate: func(u *GoogleUserInfo) { u.HostedDomain = "MyCompany.com" }, want: admitted},
		{
			name:   "alias domain of an allowlisted workspace",
			cfg:    func(c *Config) { c.AllowedDomains = []string{"mycompany.com", "alias.com"} },
			mutate: func(u *GoogleUserInfo) { u.Email = "user@alias.com" },
			want:   aliasUser,
		},
		{
			name: "secondary allowlisted workspace",
			cfg:  func(c *Config) { c.AllowedDomains = []string{"mycompany.com", "alias.com"} },
			mutate: func(u *GoogleUserInfo) {
				u.Email = "user@alias.com"
				u.HostedDomain = "alias.com"
			},
			want: aliasUser,
		},
		{name: "missing email", mutate: func(u *GoogleUserInfo) { u.Email = " " }, want: identityResult{Name: "Test User", Picture: "https://example.com/p.png", NotAdmitted: true, Err: true}},
		{name: "unverified email", mutate: func(u *GoogleUserInfo) { u.EmailVerified = false }, want: rejected},
		{name: "consumer account on an allowlisted domain", mutate: func(u *GoogleUserInfo) { u.HostedDomain = "" }, want: rejected},
		{name: "workspace that is not allowlisted", mutate: func(u *GoogleUserInfo) { u.HostedDomain = "other.com" }, want: rejected},
		{
			name:   "hosted domain not required outside the domain allowlist",
			cfg:    func(c *Config) { c.AllowedDomains = nil; c.AllowedEmails = []string{loginTestEmail} },
			mutate: func(u *GoogleUserInfo) { u.HostedDomain = "" },
			want:   admitted,
		},
		{
			name:   "hosted domain not required for non-Google identity providers",
			cfg:    func(c *Config) { c.AuthURL = "https://dex.example.com/auth" },
			mutate: func(u *GoogleUserInfo) { u.HostedDomain = "" },
			want:   admitted,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := baseLoginConfig()
			if tt.cfg != nil {
				tt.cfg(cfg)
			}
			info := baseLoginUserInfo()
			info.Picture = "https://example.com/p.png"
			if tt.mutate != nil {
				tt.mutate(info)
			}
			got := toIdentityResult(NewAuthenticator(cfg).verifyUserInfo(info))
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("verifyUserInfo() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestVerifyUserInfoRejectsNil(t *testing.T) {
	got := toIdentityResult(NewAuthenticator(baseLoginConfig()).verifyUserInfo(nil))
	if diff := cmp.Diff(identityResult{NotAdmitted: true, Err: true}, got); diff != "" {
		t.Errorf("verifyUserInfo(nil) mismatch (-want +got):\n%s", diff)
	}
}

// TestVerifyUserInfoEmailPolicy decodes userinfo responses of a non-Google identity provider and
// checks them against each email verification policy.
func TestVerifyUserInfoEmailPolicy(t *testing.T) {
	const (
		verified      = `{"email": "JDoe@Example.com", "email_verified": true}`
		verifiedStr   = `{"email": "jdoe@example.com", "email_verified": "true"}`
		noClaim       = `{"email": "jdoe@example.com"}`
		unverified    = `{"email": "jdoe@example.com", "email_verified": false}`
		unverifiedStr = `{"email": "jdoe@example.com", "email_verified": "false"}`
		notAnEmail    = `{"email": "jdoe", "email_verified": true}`
	)
	require, trust := EmailVerificationRequire, EmailVerificationTrustIssuer
	admitted := identityResult{Email: "jdoe@example.com"}
	rejected := identityResult{Email: "jdoe@example.com", NotAdmitted: true, Err: true}
	tests := []struct {
		name     string
		policy   EmailVerification
		userinfo string
		want     identityResult
	}{
		{name: "require: verified, email is normalized", policy: require, userinfo: verified, want: admitted},
		{name: "require: verified as string", policy: require, userinfo: verifiedStr, want: admitted},
		{name: "require: missing claim", policy: require, userinfo: noClaim, want: rejected},
		{name: "require: unverified", policy: require, userinfo: unverified, want: rejected},
		{name: "zero policy behaves as require", userinfo: noClaim, want: rejected},
		{name: "trust-issuer: verified", policy: trust, userinfo: verified, want: admitted},
		{name: "trust-issuer: missing claim", policy: trust, userinfo: noClaim, want: admitted},
		{name: "trust-issuer: unverified", policy: trust, userinfo: unverified, want: rejected},
		{name: "trust-issuer: unverified as string", policy: trust, userinfo: unverifiedStr, want: rejected},
		{name: "email that is not an address", policy: trust, userinfo: notAnEmail, want: identityResult{NotAdmitted: true, Err: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := oidcLoginConfig()
			cfg.EmailVerification = tt.policy
			var info GoogleUserInfo
			if err := json.Unmarshal([]byte(tt.userinfo), &info); err != nil {
				t.Fatalf("json.Unmarshal(%s) failed: %v", tt.userinfo, err)
			}
			got := toIdentityResult(NewAuthenticator(cfg).verifyUserInfo(&info))
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("verifyUserInfo() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestVerifyUserInfoEmailClaim checks identity claims that applyIDTokenClaims set from a verified
// ID token. email_verified does not apply to them.
func TestVerifyUserInfoEmailClaim(t *testing.T) {
	tests := []struct {
		name  string
		claim EmailClaim
		info  *GoogleUserInfo
		want  identityResult
	}{
		{name: "upn ignores email_verified", claim: EmailClaimUPN, info: &GoogleUserInfo{UPN: "JDoe@Contoso.com", EmailVerified: false}, want: identityResult{Email: entraLoginEmail}},
		{name: "preferred_username", claim: EmailClaimPreferredUsername, info: &GoogleUserInfo{PreferredUsername: "jdoe@contoso.com", UPN: "other@contoso.com"}, want: identityResult{Email: entraLoginEmail}},
		{name: "upn that is not an email", claim: EmailClaimUPN, info: &GoogleUserInfo{Email: entraLoginEmail, UPN: "jdoe"}, want: identityResult{NotAdmitted: true, Err: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := entraLoginConfig()
			cfg.EmailClaim = tt.claim
			got := toIdentityResult(NewAuthenticator(cfg).verifyUserInfo(tt.info))
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("verifyUserInfo() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestGoogleUserInfoIgnoresIdentityClaims checks that userinfo, which is not signed, cannot set
// the claims read from the ID token.
func TestGoogleUserInfoIgnoresIdentityClaims(t *testing.T) {
	var info GoogleUserInfo
	if err := json.Unmarshal([]byte(`{"preferred_username": "a@contoso.com", "upn": "b@contoso.com"}`), &info); err != nil {
		t.Fatalf("json.Unmarshal() failed: %v", err)
	}
	if info.PreferredUsername != "" || info.UPN != "" {
		t.Errorf("userinfo set identity claims: preferred_username %q, upn %q", info.PreferredUsername, info.UPN)
	}
}

func TestUsesGoogleSignIn(t *testing.T) {
	tests := []struct {
		name string
		cfg  func(*Config)
		want bool
	}{
		{name: "Google", want: true},
		{name: "Dex", cfg: func(c *Config) { c.AuthURL = "https://dex.example.com/auth" }, want: false},
		{name: "mock auth", cfg: func(c *Config) { c.MockAuth = true }, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := baseLoginConfig()
			if tt.cfg != nil {
				tt.cfg(cfg)
			}
			if got := NewAuthenticator(cfg).usesGoogleSignIn(); got != tt.want {
				t.Errorf("usesGoogleSignIn() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAdmitLogin(t *testing.T) {
	admitted := identityResult{Email: loginTestEmail, Name: "Test User"}
	tests := []struct {
		name   string
		cfg    func(*Config)
		mutate func(*GoogleUserInfo)
		setup  func(*Authenticator)
		want   identityResult
	}{
		{name: "admitted", want: admitted},
		{
			name:   "unverified identity",
			mutate: func(u *GoogleUserInfo) { u.EmailVerified = false },
			want:   identityResult{Email: loginTestEmail, Name: "Test User", NotAdmitted: true, Err: true},
		},
		{
			name:   "not authorized",
			mutate: func(u *GoogleUserInfo) { u.Email = "user@other.com"; u.HostedDomain = "other.com" },
			want:   identityResult{Email: "user@other.com", Name: "Test User", NotAdmitted: true, Err: true},
		},
		{
			name: "authorization pipeline failure",
			cfg:  func(c *Config) { c.AllowedDomains = nil; c.GCPProjectID = "test-project" },
			setup: func(a *Authenticator) {
				a.PolicyFetcher = func(context.Context) (*iampb.Policy, error) { return nil, errors.New("iam unavailable") }
			},
			want: identityResult{Email: loginTestEmail, Name: "Test User", Err: true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := baseLoginConfig()
			if tt.cfg != nil {
				tt.cfg(cfg)
			}
			a := NewAuthenticator(cfg)
			if tt.setup != nil {
				tt.setup(a)
			}
			info := baseLoginUserInfo()
			if tt.mutate != nil {
				tt.mutate(info)
			}
			got := toIdentityResult(a.admitLogin(context.Background(), info))
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("admitLogin() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
