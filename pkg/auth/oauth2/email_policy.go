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
	"errors"
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

// EmailClaim is the OIDC_EMAIL_CLAIM setting: the claim that carries the user's email address.
// Claims other than `email` are read from the verified ID token of the sign-in (see
// Authenticator.applyIDTokenClaims) and are not covered by email_verified.
type EmailClaim string

const (
	// EmailClaimEmail reads the standard `email` claim. It is the default.
	EmailClaimEmail EmailClaim = "email"
	// EmailClaimPreferredUsername reads `preferred_username`.
	EmailClaimPreferredUsername EmailClaim = "preferred_username"
	// EmailClaimUPN reads the Microsoft Entra ID user principal name, `upn`.
	EmailClaimUPN EmailClaim = "upn"
)

var emailClaims = []EmailClaim{EmailClaimEmail, EmailClaimPreferredUsername, EmailClaimUPN}

// isEmail reports whether c reads the `email` claim; the zero value does.
func (c EmailClaim) isEmail() bool {
	return c == "" || c == EmailClaimEmail
}

// IdentityClaims are the claims that may carry a user's email address.
type IdentityClaims struct {
	Email             string
	PreferredUsername string
	UPN               string
}

// ErrGuestAccount reports a Microsoft Entra ID guest (B2B) account, whose user principal name is
// derived from its home address, as in jdoe_contoso.com#EXT#@fabrikam.onmicrosoft.com.
var ErrGuestAccount = errors.New("guest accounts of Microsoft Entra ID (#EXT# user principal names) are not supported")

// errNoEmailAddress reports that the configured email claim is missing or not an email address.
var errNoEmailAddress = errors.New("no email address")

// Address returns the email address held in claim c of claims (see BareEmailAddress). Entra ID
// guest accounts are refused with ErrGuestAccount.
func (c EmailClaim) Address(claims IdentityClaims) (string, error) {
	value := c.value(claims)
	if strings.Contains(strings.ToUpper(value), "#EXT#") {
		return "", ErrGuestAccount
	}
	email, ok := BareEmailAddress(value)
	if !ok {
		return "", fmt.Errorf("%w in the %q claim", errNoEmailAddress, cmp.Or(c, EmailClaimEmail))
	}
	return email, nil
}

func (c EmailClaim) value(claims IdentityClaims) string {
	switch c {
	case EmailClaimPreferredUsername:
		return claims.PreferredUsername
	case EmailClaimUPN:
		return claims.UPN
	default:
		return claims.Email
	}
}

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

// JWKSEmailPolicy is how the gRPC JWKS token path reads and verifies a token's email. The zero
// value is the strictest policy.
type JWKSEmailPolicy struct {
	// TrustMissingVerified accepts tokens without an email_verified claim. A claim that is present
	// but not true is always rejected.
	TrustMissingVerified bool
	// Claim carries the email. The zero value reads `email`.
	Claim EmailClaim
}

// AcceptsEmailVerified reports whether a token's raw email_verified claim, nil when absent, is
// acceptable. It only vouches for the `email` claim, so it is ignored when Claim is another claim.
func (p JWKSEmailPolicy) AcceptsEmailVerified(emailVerified any) bool {
	return !p.Claim.isEmail() || EmailVerifiedAccepted(emailVerified, !p.TrustMissingVerified)
}

// JWKSEmailPolicy returns the email policy of the JWKS token path. Not every OIDC provider emits
// email_verified in its tokens, so a missing claim is trusted unless a domain allowlist is
// configured under EmailVerificationRequire: admitting by email domain is only sound for verified
// emails.
func (c *Config) JWKSEmailPolicy() JWKSEmailPolicy {
	return JWKSEmailPolicy{
		TrustMissingVerified: !c.requiresVerifiedEmail() || len(c.AllowedDomains) == 0,
		Claim:                c.EmailClaim,
	}
}

// loadEmailPolicy reads OIDC_EMAIL_VERIFICATION and OIDC_EMAIL_CLAIM, validates them against cfg
// and logs a warning when they relax the checks. Unset or empty values select the defaults.
func loadEmailPolicy(ctx context.Context, cfg *Config) (EmailVerification, EmailClaim, error) {
	verification := EmailVerification(envSetting("OIDC_EMAIL_VERIFICATION", string(EmailVerificationRequire)))
	claim := EmailClaim(envSetting("OIDC_EMAIL_CLAIM", string(EmailClaimEmail)))
	if err := validateEmailVerification(cfg, verification, claim); err != nil {
		return "", "", err
	}
	if err := validateEmailClaim(cfg, verification, claim); err != nil {
		return "", "", err
	}
	if verification == EmailVerificationTrustIssuer {
		warnTrustIssuer(ctx, cfg, claim)
	}
	return verification, claim, nil
}

// validateEmailVerification rejects unknown policies and `trust-issuer` where the identity
// provider's `email` claim is not trustworthy: Google always sends email_verified, and Microsoft
// Entra ID lets tenant administrators set an unverified `email` (the nOAuth attack).
func validateEmailVerification(cfg *Config, verification EmailVerification, claim EmailClaim) error {
	if !slices.Contains(emailVerifications, verification) {
		return fmt.Errorf("`OIDC_EMAIL_VERIFICATION` must be one of %q, got %q", emailVerifications, verification)
	}
	if verification == EmailVerificationRequire {
		return nil
	}
	if !cfg.IsNonProdAuth() {
		return fmt.Errorf("`OIDC_EMAIL_VERIFICATION` must be %q with Google sign-in", EmailVerificationRequire)
	}
	if _, ok := entraTenant(cfg.AuthURL); ok && claim.isEmail() {
		return fmt.Errorf("`OIDC_EMAIL_VERIFICATION=%s` cannot be used with the `email` claim of Microsoft Entra ID: it is not verified", verification)
	}
	return nil
}

// validateEmailClaim rejects unknown claims and the configurations under which a claim other than
// `email` would not be trustworthy: email_verified does not vouch for it, so `trust-issuer` must be
// chosen explicitly, and it is only read from an ID token verified against a pinned issuer.
func validateEmailClaim(cfg *Config, verification EmailVerification, claim EmailClaim) error {
	if !slices.Contains(emailClaims, claim) {
		return fmt.Errorf("`OIDC_EMAIL_CLAIM` must be one of %q, got %q", emailClaims, claim)
	}
	if claim.isEmail() {
		return nil
	}
	if !cfg.IsNonProdAuth() {
		return fmt.Errorf("`OIDC_EMAIL_CLAIM` must be %q with Google sign-in", EmailClaimEmail)
	}
	if verification != EmailVerificationTrustIssuer {
		return fmt.Errorf("`OIDC_EMAIL_CLAIM=%s` requires `OIDC_EMAIL_VERIFICATION=%s`: email_verified does not cover it", claim, EmailVerificationTrustIssuer)
	}
	if claim == EmailClaimPreferredUsername && usesEntra(cfg) {
		return fmt.Errorf("`OIDC_EMAIL_CLAIM=%s` cannot be used with Microsoft Entra ID, whose users can change it; use %q", claim, EmailClaimUPN)
	}
	return validateIDTokenSource(cfg)
}

// usesEntra reports whether the sign-in authority or the ID token issuer is Microsoft Entra ID.
func usesEntra(cfg *Config) bool {
	_, authority := entraTenant(cfg.AuthURL)
	_, issuer := entraTenant(cfg.IDTokenIssuer)
	return authority || issuer
}

func warnTrustIssuer(ctx context.Context, cfg *Config, claim EmailClaim) {
	logging.Warn(ctx, "Accepting identity provider emails without an email_verified claim", "oidc_email_verification", EmailVerificationTrustIssuer, "oidc_email_claim", claim)
	if len(cfg.AllowedDomains) > 0 {
		logging.Warn(ctx, "ALLOWED_DOMAINS admits every address the identity provider releases in these domains without email_verified; the provider must guarantee domain ownership",
			"oidc_email_verification", EmailVerificationTrustIssuer, "oidc_email_claim", claim, "allowed_domains", cfg.AllowedDomains)
	}
}

// envSetting returns the trimmed, lowercased value of key, or defaultVal when it is unset or empty.
func envSetting(key, defaultVal string) string {
	return cmp.Or(strings.ToLower(strings.TrimSpace(getEnv(key, ""))), defaultVal)
}
