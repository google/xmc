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
	"cmp"
	"context"
	"fmt"
	"net/mail"
	"slices"
	"strings"

	"xmanager_cloud/internal/logging"
)

// EmailVerification is the OIDC_EMAIL_VERIFICATION policy: how an identity provider that omits
// the email_verified claim is treated. Google sign-in always uses EmailVerificationRequire.
type EmailVerification string

const (
	// EmailVerificationRequire requires email_verified=true at sign-in. It is the default.
	EmailVerificationRequire EmailVerification = "require"
	// EmailVerificationTrustIssuer accepts a missing email_verified claim, trusting the identity
	// provider to release only addresses it verified or manages. A claim that is present but not
	// true is still rejected.
	EmailVerificationTrustIssuer EmailVerification = "trust-issuer"
)

var emailVerifications = []EmailVerification{EmailVerificationRequire, EmailVerificationTrustIssuer}

// BareEmailAddress returns value, trimmed and lowercased, when it is a bare email address. Empty
// values and display-name forms such as "Jane <jane@example.com>" are not.
func BareEmailAddress(value string) (string, bool) {
	value = strings.TrimSpace(value)
	addr, err := mail.ParseAddress(value)
	if err != nil || addr.Address != value {
		return "", false
	}
	return strings.ToLower(value), true
}

// IsTrueClaim reports whether a boolean-valued claim asserts true. Identity providers disagree on
// whether such claims are JSON booleans or strings, so the string "true" is accepted too.
func IsTrueClaim(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return strings.EqualFold(t, "true")
	default:
		return false
	}
}

// EmailVerifiedAccepted reports whether a raw email_verified claim, nil when absent, is
// acceptable: true always is, an absent claim is unless required, and any other value never is.
func EmailVerifiedAccepted(emailVerified any, required bool) bool {
	return IsTrueClaim(emailVerified) || (emailVerified == nil && !required)
}

// requiresVerifiedEmail reports whether sign-in rejects userinfo without email_verified=true.
func (c *Config) requiresVerifiedEmail() bool {
	return c.EmailVerification != EmailVerificationTrustIssuer
}

// JWKSEmailPolicy is how the gRPC JWKS token path verifies a token's email. The zero value is the
// strictest policy.
type JWKSEmailPolicy struct {
	// TrustMissingVerified accepts tokens without an email_verified claim. A claim that is present
	// but not true is always rejected.
	TrustMissingVerified bool
}

// JWKSEmailPolicy returns the email policy of the JWKS token path. Not every OIDC provider emits
// email_verified in its tokens, so a missing claim is trusted unless a domain allowlist is
// configured under EmailVerificationRequire: admitting by email domain is only sound for verified
// emails.
func (c *Config) JWKSEmailPolicy() JWKSEmailPolicy {
	return JWKSEmailPolicy{TrustMissingVerified: !c.requiresVerifiedEmail() || len(c.AllowedDomains) == 0}
}

// loadEmailVerification reads OIDC_EMAIL_VERIFICATION, validates it against cfg and logs a warning
// when it relaxes the checks. An unset or empty value selects EmailVerificationRequire.
func loadEmailVerification(ctx context.Context, cfg *Config) (EmailVerification, error) {
	verification := EmailVerification(envSetting("OIDC_EMAIL_VERIFICATION", string(EmailVerificationRequire)))
	if err := validateEmailVerification(cfg, verification); err != nil {
		return "", err
	}
	if verification == EmailVerificationTrustIssuer {
		warnTrustIssuer(ctx, cfg)
	}
	return verification, nil
}

// validateEmailVerification rejects unknown policies and `trust-issuer` where the identity
// provider's `email` claim is not trustworthy: Google always sends email_verified, and Microsoft
// Entra ID lets users and tenant admins set an unverified `email` (the nOAuth attack).
func validateEmailVerification(cfg *Config, verification EmailVerification) error {
	if !slices.Contains(emailVerifications, verification) {
		return fmt.Errorf("`OIDC_EMAIL_VERIFICATION` must be one of %q, got %q", emailVerifications, verification)
	}
	if verification == EmailVerificationRequire {
		return nil
	}
	if !cfg.IsNonProdAuth() {
		return fmt.Errorf("`OIDC_EMAIL_VERIFICATION` must be %q with Google sign-in", EmailVerificationRequire)
	}
	if _, ok := entraTenant(cfg.AuthURL); ok {
		return fmt.Errorf("`OIDC_EMAIL_VERIFICATION=%s` cannot be used with Microsoft Entra ID: its `email` claim is not verified", verification)
	}
	return nil
}

func warnTrustIssuer(ctx context.Context, cfg *Config) {
	logging.Warn(ctx, "Accepting identity provider emails without an email_verified claim", "oidc_email_verification", EmailVerificationTrustIssuer)
	if len(cfg.AllowedDomains) > 0 {
		logging.Warn(ctx, "ALLOWED_DOMAINS admits every address the identity provider releases in these domains without email_verified; the provider must guarantee domain ownership",
			"oidc_email_verification", EmailVerificationTrustIssuer, "allowed_domains", cfg.AllowedDomains)
	}
}

// envSetting returns the trimmed, lowercased value of key, or defaultVal when it is unset or empty.
func envSetting(key, defaultVal string) string {
	return cmp.Or(strings.ToLower(strings.TrimSpace(getEnv(key, ""))), defaultVal)
}
