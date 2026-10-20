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
	"errors"
	"fmt"
	"slices"
	"time"

	"golang.org/x/oauth2"
	"xmanager_cloud/pkg/auth/jwks"
)

// idTokenClockSkew tolerates clock differences with the identity provider when checking `iat`.
const idTokenClockSkew = time.Minute

// googleIssuers are the Google and IAP issuers. They cannot pin a non-Google identity provider,
// and IAP_EXPECTED_ISSUER defaults to one of them.
var googleIssuers = []string{"https://cloud.google.com/iap", "https://iap.googleapis.com", "https://accounts.google.com", "accounts.google.com"}

// IDTokenClaimsVerifier verifies an ID token's signature, expiry, issuer and audience and returns
// its claims. *jwks.Validator satisfies it.
type IDTokenClaimsVerifier interface {
	ValidateClaimsAnyAudience(ctx context.Context, tokenStr string, audiences []string) (*jwks.Claims, error)
}

// validateIDTokenSource requires the settings that pin sign-in ID tokens to the identity provider:
// its JWKS URL (OIDC_JWKS_URL) and its issuer (IAP_EXPECTED_ISSUER, the issuer the gRPC JWKS path
// accepts), which must be tenant-specific for Microsoft Entra ID.
func validateIDTokenSource(cfg *Config) error {
	if cfg.IDTokenJWKSURL == "" {
		return errors.New("`OIDC_JWKS_URL` must be set to read the email from an ID token claim")
	}
	if cfg.IDTokenIssuer == "" || slices.Contains(googleIssuers, cfg.IDTokenIssuer) {
		return fmt.Errorf("`IAP_EXPECTED_ISSUER` must be the identity provider's issuer to read the email from an ID token claim, got %q", cfg.IDTokenIssuer)
	}
	return validateEntraAuthority(cfg.IDTokenIssuer)
}

// newIDTokenClaimsVerifier returns the verifier of sign-in ID tokens, or nil when the email is
// read from the `email` userinfo claim.
func newIDTokenClaimsVerifier(cfg *Config) IDTokenClaimsVerifier {
	if cfg.EmailClaim.isEmail() {
		return nil
	}
	return jwks.NewValidator(jwks.ValidatorConfig{JWKSURL: cfg.IDTokenJWKSURL, ExpectedIssuer: cfg.IDTokenIssuer})
}

// applyIDTokenClaims sets the identity claims of info from the verified ID token of the sign-in
// when Config.EmailClaim is not `email`: the userinfo response is not signed, and Microsoft Entra
// ID only sends `preferred_username` and `upn` in ID tokens.
func (a *Authenticator) applyIDTokenClaims(ctx context.Context, token *oauth2.Token, info *GoogleUserInfo) error {
	if a.Config.EmailClaim.isEmail() {
		return nil
	}
	claims, err := a.verifyIDToken(ctx, token)
	if err != nil {
		return err
	}
	info.PreferredUsername, info.UPN = claims.PreferredUsername, claims.UPN
	return nil
}

// verifyIDToken verifies the ID token of a code exchange: IDTokenClaimsVerifier checks its
// signature, issuer and expiry, its audience must be ClientID, and its `iat` must not be in the
// future.
func (a *Authenticator) verifyIDToken(ctx context.Context, token *oauth2.Token) (*jwks.Claims, error) {
	raw, _ := token.Extra("id_token").(string)
	if raw == "" {
		return nil, errors.New("token response has no id_token")
	}
	if a.IDTokenClaimsVerifier == nil {
		return nil, errors.New("no ID token verifier is configured")
	}
	claims, err := a.IDTokenClaimsVerifier.ValidateClaimsAnyAudience(ctx, raw, []string{a.Config.ClientID})
	if err != nil {
		return nil, fmt.Errorf("invalid ID token: %w", err)
	}
	if claims.IssuedAt == nil || claims.IssuedAt.After(time.Now().Add(idTokenClockSkew)) {
		return nil, errors.New("invalid ID token: missing or future iat")
	}
	return claims, nil
}
