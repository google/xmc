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
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"google.golang.org/api/idtoken"
	"google.golang.org/grpc/metadata"
	"xmanager_cloud/pkg/auth/jwks"
	"xmanager_cloud/pkg/auth/oauth2"
)

func TestAuthenticateUserFromContext_Unauthenticated(t *testing.T) {
	a := &Authorizer{iapAudiences: []string{"test_audience"}}
	ctx := context.Background()

	testCases := []struct {
		name     string
		metadata metadata.MD
	}{
		{
			name:     "no metadata",
			metadata: nil,
		},
		{
			name:     "no IAP JWT",
			metadata: metadata.Pairs("foo", "bar"),
		},
		{
			name:     "invalid jwt",
			metadata: metadata.Pairs("x-goog-iap-jwt-assertion", "bad-jwt-token"),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			currentCtx := ctx
			if tc.metadata != nil {
				currentCtx = metadata.NewIncomingContext(ctx, tc.metadata)
			}

			_, _, err := a.AuthenticateUserFromContext(currentCtx)
			if err == nil {
				t.Errorf("AuthenticateUserFromContext() succeeded, want error")
			}
		})
	}
}

func TestAuthenticateUserFromContext_Authenticated(t *testing.T) {
	a := &Authorizer{iapAudiences: []string{"other_audience", "test_audience"}, iapIssuer: "https://cloud.google.com/iap", jwtSecret: []byte("default-jwt-secret-for-local-dev-only-12345")}
	ctx := context.Background()

	oldValidate := validate
	defer func() { validate = oldValidate }()

	testCases := []struct {
		name       string
		metadata   metadata.MD
		validator  func(ctx context.Context, jwt, audience string) (*idtoken.Payload, error)
		wantEmail  string
		wantUserID string
		wantErr    bool
	}{
		{
			name:     "validation fails",
			metadata: metadata.Pairs("x-goog-iap-jwt-assertion", "jwt"),
			validator: func(ctx context.Context, jwt, audience string) (*idtoken.Payload, error) {
				return nil, errors.New("validation failed")
			},
			wantErr: true,
		},
		{
			name:     "invalid issuer",
			metadata: metadata.Pairs("x-goog-iap-jwt-assertion", "jwt"),
			validator: func(ctx context.Context, jwt, audience string) (*idtoken.Payload, error) {
				return &idtoken.Payload{Issuer: "invalid"}, nil
			},
			wantErr: true,
		},
		{
			name:     "missing email",
			metadata: metadata.Pairs("x-goog-iap-jwt-assertion", "jwt"),
			validator: func(ctx context.Context, jwt, audience string) (*idtoken.Payload, error) {
				return &idtoken.Payload{
					Issuer:  "https://cloud.google.com/iap",
					Subject: "userID",
					Claims:  map[string]any{},
				}, nil
			},
			wantErr: true,
		},
		{
			name:     "non-string email",
			metadata: metadata.Pairs("x-goog-iap-jwt-assertion", "jwt"),
			validator: func(ctx context.Context, jwt, audience string) (*idtoken.Payload, error) {
				return &idtoken.Payload{
					Issuer:  "https://cloud.google.com/iap",
					Subject: "userID",
					Claims:  map[string]any{"email": 123},
				}, nil
			},
			wantErr: true,
		},
		{
			name:     "empty email",
			metadata: metadata.Pairs("x-goog-iap-jwt-assertion", "jwt"),
			validator: func(ctx context.Context, jwt, audience string) (*idtoken.Payload, error) {
				return &idtoken.Payload{
					Issuer:  "https://cloud.google.com/iap",
					Subject: "userID",
					Claims:  map[string]any{"email": ""},
				}, nil
			},
			wantErr: true,
		},
		{
			name:     "empty user ID",
			metadata: metadata.Pairs("x-goog-iap-jwt-assertion", "jwt"),
			validator: func(ctx context.Context, jwt, audience string) (*idtoken.Payload, error) {
				return &idtoken.Payload{
					Issuer:  "https://cloud.google.com/iap",
					Subject: "",
					Claims:  map[string]any{"email": "email@example.com"},
				}, nil
			},
			wantErr: true,
		},
		{
			name:     "ignores x-goog-user-email and uses verified payload email",
			metadata: metadata.Pairs("x-goog-iap-jwt-assertion", "jwt", "x-goog-user-email", "user@example.com"),
			validator: func(ctx context.Context, jwt, audience string) (*idtoken.Payload, error) {
				if audience == "test_audience" {
					return &idtoken.Payload{
						Issuer:  "https://cloud.google.com/iap",
						Subject: "userID",
						Claims:  map[string]any{"email": "different@example.com"},
					}, nil
				}
				return nil, errors.New("validation failed for audience " + audience)
			},
			wantEmail:  "different@example.com",
			wantUserID: "userID",
		},
		{
			name:     "success",
			metadata: metadata.Pairs("x-goog-iap-jwt-assertion", "jwt"),
			validator: func(ctx context.Context, jwt, audience string) (*idtoken.Payload, error) {
				if audience == "test_audience" {
					return &idtoken.Payload{
						Issuer:  "https://cloud.google.com/iap",
						Subject: "userID",
						Claims:  map[string]any{"email": "email@example.com"},
					}, nil
				}
				return nil, errors.New("validation failed for audience " + audience)
			},
			wantEmail:  "email@example.com",
			wantUserID: "userID",
		},
		{
			name: "custom session token authentication success",
			metadata: func() metadata.MD {
				tok, err := oauth2.GenerateSessionToken("custom-sa@mycompany.com", "Custom SA", "", 1*time.Hour, []byte("default-jwt-secret-for-local-dev-only-12345"))
				if err != nil {
					t.Fatalf("Failed to generate custom session token: %v", err)
				}
				return metadata.Pairs("x-goog-iap-jwt-assertion", tok)
			}(),
			wantEmail:  "custom-sa@mycompany.com",
			wantUserID: "custom-sa@mycompany.com",
		},
		{
			name: "custom session token empty email failure",
			metadata: func() metadata.MD {
				tok, err := oauth2.GenerateSessionToken("", "Custom SA No Email", "", 1*time.Hour, []byte("default-jwt-secret-for-local-dev-only-12345"))
				if err != nil {
					t.Fatalf("Failed to generate custom session token: %v", err)
				}
				return metadata.Pairs("x-goog-iap-jwt-assertion", tok)
			}(),
			wantErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			validate = tc.validator
			currentCtx := ctx
			if tc.metadata != nil {
				currentCtx = metadata.NewIncomingContext(ctx, tc.metadata)
			}

			email, userID, err := a.AuthenticateUserFromContext(currentCtx)
			if tc.wantErr {
				if err == nil {
					t.Errorf("AuthenticateUserFromContext() succeeded, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("AuthenticateUserFromContext() failed: %v", err)
			}

			if email != tc.wantEmail || userID != tc.wantUserID {
				t.Errorf("AuthenticateUserFromContext() = (%q, %q), want (%q, %q)", email, userID, tc.wantEmail, tc.wantUserID)
			}
		})
	}
}

type mockTokenValidator struct {
	validateFn func(ctx context.Context, tokenStr string, audiences []string) (*jwks.Claims, error)
}

func (m *mockTokenValidator) ValidateClaimsAnyAudience(ctx context.Context, tokenStr string, audiences []string) (*jwks.Claims, error) {
	if m.validateFn != nil {
		return m.validateFn(ctx, tokenStr, audiences)
	}
	return nil, errors.New("unimplemented")
}

func TestAuthenticateUserFromContext_JWKS(t *testing.T) {
	ctx := context.Background()

	t.Run("JWKSValidationSuccessWithEmailClaim", func(t *testing.T) {
		a := &Authorizer{iapAudiences: []string{"test_audience"}}
		mockVal := &mockTokenValidator{
			validateFn: func(ctx context.Context, tokenStr string, audiences []string) (*jwks.Claims, error) {
				return &jwks.Claims{
					Email: "jwks-user@example.com",
					RegisteredClaims: jwt.RegisteredClaims{
						Subject: "sub-12345",
					},
				}, nil
			},
		}
		a.SetJWKSValidator(mockVal)

		reqCtx := metadata.NewIncomingContext(ctx, metadata.Pairs("x-goog-iap-jwt-assertion", "valid-jwks-jwt"))
		email, userID, err := a.AuthenticateUserFromContext(reqCtx)
		if err != nil {
			t.Fatalf("AuthenticateUserFromContext failed: %v", err)
		}
		if email != "jwks-user@example.com" {
			t.Errorf("got email %q, want %q", email, "jwks-user@example.com")
		}
		if userID != "sub-12345" {
			t.Errorf("got userID %q, want %q", userID, "sub-12345")
		}
	})

	t.Run("JWKSValidationSuccessWithSubjectEmailFallback", func(t *testing.T) {
		a := &Authorizer{iapAudiences: []string{"test_audience"}}
		mockVal := &mockTokenValidator{
			validateFn: func(ctx context.Context, tokenStr string, audiences []string) (*jwks.Claims, error) {
				return &jwks.Claims{
					Email: "",
					RegisteredClaims: jwt.RegisteredClaims{
						Subject: "service-account@iam.gserviceaccount.com",
					},
				}, nil
			},
		}
		a.SetJWKSValidator(mockVal)

		reqCtx := metadata.NewIncomingContext(ctx, metadata.Pairs("x-goog-iap-jwt-assertion", "valid-jwks-jwt"))
		email, userID, err := a.AuthenticateUserFromContext(reqCtx)
		if err != nil {
			t.Fatalf("AuthenticateUserFromContext failed: %v", err)
		}
		if email != "service-account@iam.gserviceaccount.com" {
			t.Errorf("got email %q, want %q", email, "service-account@iam.gserviceaccount.com")
		}
		if userID != "service-account@iam.gserviceaccount.com" {
			t.Errorf("got userID %q, want %q", userID, "service-account@iam.gserviceaccount.com")
		}
	})

	t.Run("JWKSValidationInvalidSignatureFallbackToIAP", func(t *testing.T) {
		a := &Authorizer{
			iapAudiences: []string{"test_audience"},
			iapIssuer:    "https://cloud.google.com/iap",
		}
		mockVal := &mockTokenValidator{
			validateFn: func(ctx context.Context, tokenStr string, audiences []string) (*jwks.Claims, error) {
				return nil, jwks.ErrInvalidToken
			},
		}
		a.SetJWKSValidator(mockVal)

		oldValidate := validate
		defer func() { validate = oldValidate }()
		validate = func(ctx context.Context, jwt, audience string) (*idtoken.Payload, error) {
			if audience == "test_audience" {
				return &idtoken.Payload{
					Issuer:  "https://cloud.google.com/iap",
					Subject: "iap-user-id",
					Claims:  map[string]any{"email": "iap-fallback@example.com"},
				}, nil
			}
			return nil, errors.New("mismatch")
		}

		reqCtx := metadata.NewIncomingContext(ctx, metadata.Pairs("x-goog-iap-jwt-assertion", "iap-jwt"))
		email, userID, err := a.AuthenticateUserFromContext(reqCtx)
		if err != nil {
			t.Fatalf("AuthenticateUserFromContext failed: %v", err)
		}
		if email != "iap-fallback@example.com" {
			t.Errorf("got email %q, want %q", email, "iap-fallback@example.com")
		}
		if userID != "iap-user-id" {
			t.Errorf("got userID %q, want %q", userID, "iap-user-id")
		}
	})

	t.Run("JWKSValidationExpiredTokenFallbackFailsIfIAPAlsoFails", func(t *testing.T) {
		a := &Authorizer{
			iapAudiences: []string{"test_audience"},
			iapIssuer:    "https://cloud.google.com/iap",
		}
		mockVal := &mockTokenValidator{
			validateFn: func(ctx context.Context, tokenStr string, audiences []string) (*jwks.Claims, error) {
				return nil, fmt.Errorf("%w: token is expired", jwks.ErrInvalidToken)
			},
		}
		a.SetJWKSValidator(mockVal)

		oldValidate := validate
		defer func() { validate = oldValidate }()
		validate = func(ctx context.Context, jwt, audience string) (*idtoken.Payload, error) {
			return nil, errors.New("idtoken validation failed")
		}

		reqCtx := metadata.NewIncomingContext(ctx, metadata.Pairs("x-goog-iap-jwt-assertion", "expired-jwt"))
		_, _, err := a.AuthenticateUserFromContext(reqCtx)
		if err == nil {
			t.Errorf("AuthenticateUserFromContext succeeded, want error")
		}
	})

	t.Run("JWKSValidationAudienceMismatchFallback", func(t *testing.T) {
		a := &Authorizer{
			iapAudiences: []string{"test_audience"},
			iapIssuer:    "https://cloud.google.com/iap",
		}
		mockVal := &mockTokenValidator{
			validateFn: func(ctx context.Context, tokenStr string, audiences []string) (*jwks.Claims, error) {
				return nil, jwks.ErrAudienceMismatch
			},
		}
		a.SetJWKSValidator(mockVal)

		oldValidate := validate
		defer func() { validate = oldValidate }()
		validate = func(ctx context.Context, jwt, audience string) (*idtoken.Payload, error) {
			return nil, errors.New("idtoken validation failed")
		}

		reqCtx := metadata.NewIncomingContext(ctx, metadata.Pairs("x-goog-iap-jwt-assertion", "mismatched-aud-jwt"))
		_, _, err := a.AuthenticateUserFromContext(reqCtx)
		if err == nil {
			t.Errorf("AuthenticateUserFromContext succeeded, want error")
		}
	})

	t.Run("JWKSValidationNonEmailSubjectRejection", func(t *testing.T) {
		a := &Authorizer{
			iapAudiences: []string{"test_audience"},
			iapIssuer:    "https://cloud.google.com/iap",
		}
		mockVal := &mockTokenValidator{
			validateFn: func(ctx context.Context, tokenStr string, audiences []string) (*jwks.Claims, error) {
				return &jwks.Claims{
					Email: "",
					RegisteredClaims: jwt.RegisteredClaims{
						Subject: "1029384756", // Numeric ID, not a valid email
					},
				}, nil
			},
		}
		a.SetJWKSValidator(mockVal)

		oldValidate := validate
		defer func() { validate = oldValidate }()
		validate = func(ctx context.Context, jwt, audience string) (*idtoken.Payload, error) {
			return nil, errors.New("idtoken validation failed")
		}

		reqCtx := metadata.NewIncomingContext(ctx, metadata.Pairs("x-goog-iap-jwt-assertion", "numeric-sub-jwt"))
		_, _, err := a.AuthenticateUserFromContext(reqCtx)
		if err == nil {
			t.Errorf("AuthenticateUserFromContext succeeded, want error for numeric non-email subject")
		}
	})
}
