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
	"strings"

	"golang/grpc/metadata/metadata"
	"google.golang.org/api/idtoken"
	"task/go/status"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/pkg/auth/oauth2"
)

// validate is the function that validates the JWT. It is stubbed out for testing.
var validate = idtoken.Validate

// AuthenticateUserFromContext authenticates a user's identity from the context.
func (a *Authorizer) AuthenticateUserFromContext(ctx context.Context) (string, string, error) {
	// This is a temporary workaround to allow testing without IAP.
	if a.disableAuthentication {
		return "test@example.com", "test_user_id", nil
	}

	jwt, err := jwtFromMetadata(ctx)
	if err != nil {
		return "", "", err
	}

	// Try to parse the token using our custom JWT secret first, if one is configured.
	ident, err := a.parseCustomSessionToken(ctx, jwt)
	if err != nil {
		return "", "", err
	}
	if ident != nil {
		return ident.email, ident.userID, nil
	}

	payload, err := validateJWT(ctx, jwt, a.iapIssuer, a.iapAudiences)
	if err != nil {
		unverifiedPayload, parseErr := idtoken.ParsePayload(jwt)
		if parseErr == nil && unverifiedPayload != nil {
			logging.Error(ctx, "validateJWT: idtoken.Validate failed",
				"error", err,
				"received_audience", unverifiedPayload.Audience,
				"jwt_issuer", unverifiedPayload.Issuer,
				"jwt_subject", unverifiedPayload.Subject)
		} else {
			logging.Warn(ctx, "validateJWT: idtoken.Validate failed and could not parse payload for debugging", "error", err, "parse_error", parseErr)
		}
		return "", "", fmt.Errorf("JWT validation failed: %w", status.ErrUnauthenticated)
	}

	email, err := resolveUserEmail(ctx, payload)
	if err != nil {
		return "", "", err
	}

	userID := payload.Subject
	if userID == "" {
		return "", "", fmt.Errorf("user ID claim is empty: %w", status.ErrUnauthenticated)
	}

	logging.Info(ctx, "Authenticated request", "email", email, "user_id", userID)

	return email, userID, nil
}

type authIdentity struct {
	email  string
	userID string
}

func (a *Authorizer) parseCustomSessionToken(ctx context.Context, jwt string) (*authIdentity, error) {
	if len(a.jwtSecret) == 0 {
		return nil, nil
	}
	claims, parseErr := oauth2.ParseSessionToken(jwt, a.jwtSecret)
	if parseErr != nil {
		if !errors.Is(parseErr, oauth2.ErrInvalidToken) {
			logging.Info(ctx, "Custom session token parsing failed", "error", parseErr)
		}
		return nil, nil
	}
	if claims == nil || claims.Email == "" {
		return nil, fmt.Errorf("email in custom session token is empty: %w", status.ErrUnauthenticated)
	}
	return &authIdentity{email: claims.Email, userID: claims.Email}, nil
}

func extractEmailFromMetadata(ctx context.Context) (string, bool) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		logging.Info(ctx, "gRPC metadata not in context, falling back to JWT payload")
		return "", false
	}
	emails := md.Get("x-goog-user-email")
	if len(emails) > 0 && emails[0] != "" {
		logging.Info(ctx, "Using x-goog-user-email from metadata", "email", emails[0])
		return emails[0], true
	}
	if len(emails) == 0 {
		logging.Info(ctx, "x-goog-user-email not in metadata, falling back to JWT payload")
	} else {
		logging.Info(ctx, "x-goog-user-email is empty in metadata, falling back to JWT payload")
	}
	return "", false
}

// resolveUserEmail resolves the user's email by trying gRPC metadata first, then falling back to the JWT payload.
func resolveUserEmail(ctx context.Context, payload *idtoken.Payload) (string, error) {
	if email, ok := extractEmailFromMetadata(ctx); ok {
		return email, nil
	}
	return extractEmailFromPayload(ctx, payload)
}

func extractEmailFromPayload(ctx context.Context, payload *idtoken.Payload) (string, error) {
	email, ok := payload.Claims["email"].(string)
	if !ok {
		logging.Warn(ctx, "Email claim is missing or not a string")
		return "", fmt.Errorf("email claim is missing or not a string: %w", status.ErrUnauthenticated)
	}
	if email == "" {
		logging.Warn(ctx, "Email claim is empty")
		return "", fmt.Errorf("email claim is empty: %w", status.ErrUnauthenticated)
	}
	return email, nil
}

// jwtFromMetadata extracts the JWT from the incoming gRPC context.
func jwtFromMetadata(ctx context.Context) (string, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		logging.Warn(ctx, "Missing gRPC metadata")
		return "", fmt.Errorf("missing gRPC metadata: %w", status.ErrUnauthenticated)
	}

	jwtValues := md.Get("x-goog-iap-jwt-assertion")
	if len(jwtValues) > 0 && jwtValues[0] != "" {
		return jwtValues[0], nil
	}

	authValues := md.Get("authorization")
	if len(authValues) > 0 && authValues[0] != "" {
		tokenStr := authValues[0]
		if strings.HasPrefix(tokenStr, "Bearer ") {
			return strings.TrimPrefix(tokenStr, "Bearer "), nil
		}
		if strings.HasPrefix(tokenStr, "bearer ") {
			return strings.TrimPrefix(tokenStr, "bearer "), nil
		}
	}

	logging.Warn(ctx, "Missing IAP JWT or Bearer token in metadata")
	return "", fmt.Errorf("missing authentication token in metadata: %w", status.ErrUnauthenticated)
}

// validateJWT validates the JWT and returns the payload.
func validateJWT(ctx context.Context, jwt string, iapIssuer string, audiences []string) (*idtoken.Payload, error) {
	if len(audiences) == 0 {
		return nil, fmt.Errorf("no audiences to validate against")
	}
	var payload *idtoken.Payload
	var err error
	var errors []string
	for _, aud := range audiences {
		payload, err = validate(ctx, jwt, aud)
		if err != nil {
			errors = append(errors, fmt.Sprintf("audience %q: %v", aud, err))
			continue
		}
		if payload.Issuer != iapIssuer && payload.Issuer != "https://accounts.google.com" && payload.Issuer != "accounts.google.com" {
			logging.Warn(ctx, "Invalid issuer", "issuer", payload.Issuer, "expected", iapIssuer)
			return nil, fmt.Errorf("idtoken: invalid issuer %q, expected %q", payload.Issuer, iapIssuer)
		}
		return payload, nil
	}

	logging.Warn(ctx, "idtoken.Validate: no audience matched", "errors", strings.Join(errors, "; "))
	return nil, fmt.Errorf("idtoken.Validate: no audience matched: %s", strings.Join(errors, "; "))
}
