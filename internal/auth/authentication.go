// Copyright 2025 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package auth

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"

	"google.golang.org/api/idtoken"
	"google.golang.org/grpc/metadata"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/pkg/auth/jwks"
	"xmanager_cloud/pkg/auth/oauth2"
)

// validate is the function that validates the JWT. It is stubbed out for testing.
var validate = idtoken.Validate

// AuthenticateUserFromContext authenticates a user's identity from the context.
func (a *Authorizer) AuthenticateUserFromContext(ctx context.Context) (string, string, error) {
	ident, err := a.resolveIdentity(ctx)
	if err != nil {
		return "", "", err
	}
	return ident.email, ident.userID, nil
}

type authIdentity struct {
	email  string
	userID string
}

// resolveIdentity authenticates the bearer token in the incoming metadata as, in order, an XMC
// session token, a JWKS-validated OIDC token, and a Google-signed ID token.
func (a *Authorizer) resolveIdentity(ctx context.Context) (*authIdentity, error) {
	jwt, err := jwtFromMetadata(ctx)
	if err != nil {
		return nil, err
	}

	// Try to parse the token using our custom JWT secret first, if one is configured.
	ident, err := a.parseCustomSessionToken(ctx, jwt)
	if err != nil || ident != nil {
		return ident, err
	}

	// Try to validate the token against JWKS if a validator is configured.
	ident, err = a.authenticateViaJWKS(ctx, jwt)
	if err != nil || ident != nil {
		return ident, err
	}

	return a.authenticateViaGoogleIDToken(ctx, jwt)
}

func (a *Authorizer) authenticateViaGoogleIDToken(ctx context.Context, jwt string) (*authIdentity, error) {
	payload, err := validateJWT(ctx, jwt, a.iapIssuer, a.iapAudiences)
	if err != nil {
		logIDTokenValidationFailure(ctx, jwt, err)
		return nil, fmt.Errorf("JWT validation failed: %w", grpcerr.ErrUnauthenticated)
	}

	email, err := resolveUserEmail(ctx, payload)
	if err != nil {
		return nil, err
	}
	if err := verifyIDTokenEmail(ctx, payload); err != nil {
		return nil, err
	}

	userID := payload.Subject
	if userID == "" {
		return nil, fmt.Errorf("user ID claim is empty: %w", grpcerr.ErrUnauthenticated)
	}

	logging.Info(ctx, "Authenticated request", "email", email, "user_id", userID)
	return &authIdentity{email: email, userID: userID}, nil
}

func logIDTokenValidationFailure(ctx context.Context, jwt string, err error) {
	unverifiedPayload, parseErr := idtoken.ParsePayload(jwt)
	if parseErr != nil || unverifiedPayload == nil {
		logging.Warn(ctx, "validateJWT: idtoken.Validate failed and could not parse payload for debugging", "error", err, "parse_error", parseErr)
		return
	}
	logging.Error(ctx, "validateJWT: idtoken.Validate failed",
		"error", err,
		"received_audience", unverifiedPayload.Audience,
		"jwt_issuer", unverifiedPayload.Issuer,
		"jwt_subject", unverifiedPayload.Subject)
}

// verifyIDTokenEmail rejects Google-signed ID tokens whose email is not verified.
//
// Tokens from accounts.google.com carry email_verified whenever they carry email: user tokens,
// service account tokens from iamcredentials generateIdToken with includeEmail, and metadata server
// tokens with format=full (what idtoken.NewTokenSource and google-auth fetch_id_token request).
// A missing claim is therefore rejected there. IAP-signed assertions never carry the claim because
// IAP only admits verified Google identities, so only an explicit non-true value is rejected there.
func verifyIDTokenEmail(ctx context.Context, payload *idtoken.Payload) error {
	verified, present := payload.Claims["email_verified"]
	if isTrueClaim(verified) {
		return nil
	}
	if !present && !isGoogleAccountsIssuer(payload.Issuer) {
		return nil
	}
	logging.Warn(ctx, "Rejecting ID token without a verified email", "issuer", payload.Issuer, "email_verified", verified)
	return fmt.Errorf("email is not verified: %w", grpcerr.ErrUnauthenticated)
}

// isTrueClaim reports whether a boolean-valued claim asserts true. Identity providers disagree on
// whether such claims are JSON booleans or strings, so the string "true" is accepted too.
func isTrueClaim(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return strings.EqualFold(t, "true")
	default:
		return false
	}
}

func isGoogleAccountsIssuer(issuer string) bool {
	return issuer == "https://accounts.google.com" || issuer == "accounts.google.com"
}

// authenticateViaJWKS returns (nil, nil) when no validator is configured or the token does not
// validate, so the caller falls back to the next authentication method.
func (a *Authorizer) authenticateViaJWKS(ctx context.Context, jwt string) (*authIdentity, error) {
	if a.jwksValidator == nil {
		return nil, nil
	}
	claims, err := a.jwksValidator.ValidateClaimsAnyAudience(ctx, jwt, a.iapAudiences)
	if err != nil || claims == nil {
		logging.Debug(ctx, "authenticateViaJWKS: JWKS validation failed", "error", err)
		return nil, nil
	}
	if err := verifyJWKSEmail(ctx, claims); err != nil {
		return nil, err
	}
	email, ok := jwksEmail(claims)
	if !ok {
		logging.Warn(ctx, "authenticateViaJWKS: token does not contain a valid email address", "email", claims.Email, "sub", claims.Subject)
		return nil, nil
	}
	userID := claims.Subject
	if userID == "" {
		userID = email
	}
	logging.Info(ctx, "Authenticated request via JWKS", "email", email, "user_id", userID)
	return &authIdentity{email: email, userID: userID}, nil
}

// verifyJWKSEmail rejects JWKS tokens whose email is not verified. Dex sets email_verified alongside
// email, but not every OIDC provider emits the claim, so only an explicit non-true value is
// rejected.
func verifyJWKSEmail(ctx context.Context, claims *jwks.Claims) error {
	if claims.EmailVerified == nil || isTrueClaim(claims.EmailVerified) {
		return nil
	}
	logging.Warn(ctx, "authenticateViaJWKS: rejecting token without a verified email", "email", claims.Email, "email_verified", claims.EmailVerified)
	return fmt.Errorf("email is not verified: %w", grpcerr.ErrUnauthenticated)
}

// jwksEmail returns the token's email claim, falling back to an email-shaped subject (as used by
// service account tokens).
func jwksEmail(claims *jwks.Claims) (string, bool) {
	if _, err := mail.ParseAddress(claims.Email); err == nil {
		return claims.Email, true
	}
	if _, err := mail.ParseAddress(claims.Subject); err == nil {
		return claims.Subject, true
	}
	return "", false
}

func (a *Authorizer) parseCustomSessionToken(ctx context.Context, jwt string) (*authIdentity, error) {
	if len(a.jwtSecret) == 0 {
		return nil, nil
	}
	claims, parseErr := oauth2.ParseSessionToken(jwt, a.jwtSecret, oauth2.FrontendAudience)
	if parseErr != nil {
		if !errors.Is(parseErr, oauth2.ErrInvalidToken) {
			logging.Info(ctx, "Custom session token parsing failed", "error", parseErr)
		}
		return nil, nil
	}
	if claims == nil || claims.Email == "" {
		return nil, fmt.Errorf("email in custom session token is empty: %w", grpcerr.ErrUnauthenticated)
	}
	return &authIdentity{email: claims.Email, userID: claims.Email}, nil
}

// resolveUserEmail resolves the user's email strictly and exclusively from the verified JWT payload claims.
func resolveUserEmail(ctx context.Context, payload *idtoken.Payload) (string, error) {
	email, ok := payload.Claims["email"].(string)
	if !ok || email == "" {
		logging.Warn(ctx, "Email claim is missing or empty")
		return "", fmt.Errorf("email claim is missing or empty: %w", grpcerr.ErrUnauthenticated)
	}
	return email, nil
}

// jwtFromMetadata extracts the JWT from the incoming gRPC context.
func jwtFromMetadata(ctx context.Context) (string, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		logging.Warn(ctx, "Missing gRPC metadata")
		return "", fmt.Errorf("missing gRPC metadata: %w", grpcerr.ErrUnauthenticated)
	}

	jwtValues := md.Get("x-goog-iap-jwt-assertion")
	if len(jwtValues) > 0 && jwtValues[0] != "" {
		return jwtValues[0], nil
	}

	authValues := md.Get("authorization")
	if len(authValues) > 0 && len(authValues[0]) > 7 && strings.EqualFold(authValues[0][:7], "bearer ") {
		return authValues[0][7:], nil
	}

	logging.Warn(ctx, "Missing IAP JWT or Bearer token in metadata")
	return "", fmt.Errorf("missing authentication token in metadata: %w", grpcerr.ErrUnauthenticated)
}

// validateJWT validates the JWT and returns the payload.
func validateJWT(ctx context.Context, jwt string, iapIssuer string, audiences []string) (*idtoken.Payload, error) {
	if len(audiences) == 0 {
		return nil, fmt.Errorf("no audiences to validate against")
	}
	var payload *idtoken.Payload
	var err error
	var failures []string
	for _, aud := range audiences {
		payload, err = validate(ctx, jwt, aud)
		if err != nil {
			failures = append(failures, fmt.Sprintf("audience %q: %v", aud, err))
			continue
		}
		if payload.Issuer != iapIssuer && !isGoogleAccountsIssuer(payload.Issuer) {
			logging.Warn(ctx, "Invalid issuer", "issuer", payload.Issuer, "expected", iapIssuer)
			return nil, fmt.Errorf("idtoken: invalid issuer %q, expected %q", payload.Issuer, iapIssuer)
		}
		return payload, nil
	}

	logging.Warn(ctx, "idtoken.Validate: no audience matched", "errors", strings.Join(failures, "; "))
	return nil, fmt.Errorf("idtoken.Validate: no audience matched: %s", strings.Join(failures, "; "))
}
