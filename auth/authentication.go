package auth

import (
	"context"
	"fmt"
	"strings"

	"golang/google_api/idtoken/idtoken"
	"golang/grpc/metadata/metadata"
	"xmanager_cloud/logging/logging"
	"task/go/status"
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

	email, err := extractEmail(ctx, payload)
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

// extractEmail extracts email from metadata or JWT payload.
func extractEmail(ctx context.Context, payload *idtoken.Payload) (string, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if ok {
		emails := md.Get("x-goog-user-email")
		if len(emails) > 0 && emails[0] != "" {
			logging.Info(ctx, "Using x-goog-user-email from metadata", "email", emails[0])
			return emails[0], nil
		}
		if len(emails) == 0 {
			logging.Info(ctx, "x-goog-user-email not in metadata, falling back to JWT payload")
		} else {
			logging.Info(ctx, "x-goog-user-email is empty in metadata, falling back to JWT payload")
		}
	} else {
		logging.Info(ctx, "gRPC metadata not in context, falling back to JWT payload")
	}

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
	if len(jwtValues) == 0 {
		logging.Warn(ctx, "Missing IAP JWT in metadata")
		return "", fmt.Errorf("missing IAP JWT in metadata: %w", status.ErrUnauthenticated)
	}

	return jwtValues[0], nil
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
		if payload.Issuer != iapIssuer {
			logging.Warn(ctx, "Invalid issuer", "issuer", payload.Issuer, "expected", iapIssuer)
			return nil, fmt.Errorf("idtoken: invalid issuer %q, expected %q", payload.Issuer, iapIssuer)
		}
		return payload, nil
	}

	logging.Warn(ctx, "idtoken.Validate: no audience matched", "errors", strings.Join(errors, "; "))
	return nil, fmt.Errorf("idtoken.Validate: no audience matched: %s", strings.Join(errors, "; "))
}
