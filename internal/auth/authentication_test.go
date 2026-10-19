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
	"github.com/google/go-cmp/cmp"
	"google.golang.org/api/idtoken"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
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
			if diff := cmp.Diff(codes.Unauthenticated, status.Code(err)); diff != "" {
				t.Errorf("AuthenticateUserFromContext() code mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestAuthenticateUserFromContext_Authenticated(t *testing.T) {
	a := &Authorizer{
		iapAudiences: []string{"other_audience", "test_audience"},
		iapIssuer:    "https://cloud.google.com/iap",
		jwtSecret:    []byte("default-jwt-secret-for-local-dev-only-12345"),
		admission:    &fakeAdmissionChecker{admitted: true},
	}
	ctx := context.Background()

	oldValidate := validate
	defer func() { validate = oldValidate }()

	testCases := []struct {
		name       string
		metadata   metadata.MD
		validator  func(ctx context.Context, jwt, audience string) (*idtoken.Payload, error)
		wantEmail  string
		wantUserID string
		wantCode   codes.Code
	}{
		{
			name:     "validation fails",
			metadata: metadata.Pairs("x-goog-iap-jwt-assertion", "jwt"),
			validator: func(ctx context.Context, jwt, audience string) (*idtoken.Payload, error) {
				return nil, errors.New("validation failed")
			},
			wantCode: codes.Unauthenticated,
		},
		{
			name:     "invalid issuer",
			metadata: metadata.Pairs("x-goog-iap-jwt-assertion", "jwt"),
			validator: func(ctx context.Context, jwt, audience string) (*idtoken.Payload, error) {
				return &idtoken.Payload{Issuer: "invalid"}, nil
			},
			wantCode: codes.Unauthenticated,
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
			wantCode: codes.Unauthenticated,
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
			wantCode: codes.Unauthenticated,
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
			wantCode: codes.Unauthenticated,
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
			wantCode: codes.Unauthenticated,
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
			wantCode: codes.Unauthenticated,
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
			want := authOutcome{Email: tc.wantEmail, UserID: tc.wantUserID, Code: tc.wantCode}
			got := authOutcome{Email: email, UserID: userID, Code: status.Code(err)}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("AuthenticateUserFromContext() mismatch (-want +got):\n%s\nerror: %v", diff, err)
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
		a := &Authorizer{iapAudiences: []string{"test_audience"}, admission: &fakeAdmissionChecker{admitted: true}}
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
		a := &Authorizer{iapAudiences: []string{"test_audience"}, admission: &fakeAdmissionChecker{admitted: true}}
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
			admission:    &fakeAdmissionChecker{admitted: true},
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
		if diff := cmp.Diff(codes.Unauthenticated, status.Code(err)); diff != "" {
			t.Errorf("AuthenticateUserFromContext() code mismatch (-want +got):\n%s", diff)
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
		if diff := cmp.Diff(codes.Unauthenticated, status.Code(err)); diff != "" {
			t.Errorf("AuthenticateUserFromContext() code mismatch (-want +got):\n%s", diff)
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
		if diff := cmp.Diff(codes.Unauthenticated, status.Code(err)); diff != "" {
			t.Errorf("AuthenticateUserFromContext() code mismatch for numeric non-email subject (-want +got):\n%s", diff)
		}
	})
}

const (
	tokenTestSecret   = "default-jwt-secret-for-local-dev-only-12345"
	tokenTestAudience = "test_audience"
)

// authOutcome is the comparable result of an AuthenticateUserFromContext call, including the emails
// the admission checker was asked about.
type authOutcome struct {
	Email   string
	UserID  string
	Code    codes.Code
	Checked []string
}

func testTokens(t *testing.T) (map[string]*jwks.Claims, map[string]*idtoken.Payload) {
	t.Helper()
	jwksClaims := map[string]*jwks.Claims{
		"jwks-token":                   {Email: "dex-user@example.com", EmailVerified: true, RegisteredClaims: jwt.RegisteredClaims{Subject: "dex-sub"}},
		"jwks-unverified-token":        {Email: "dex-user@example.com", EmailVerified: false, RegisteredClaims: jwt.RegisteredClaims{Subject: "dex-sub"}},
		"jwks-unverified-string-token": {Email: "dex-user@example.com", EmailVerified: "false", RegisteredClaims: jwt.RegisteredClaims{Subject: "dex-sub"}},
		"jwks-no-claim-token":          {Email: "dex-user@example.com", RegisteredClaims: jwt.RegisteredClaims{Subject: "dex-sub"}},
	}
	googlePayload := func(issuer, email string, verified any) *idtoken.Payload {
		claims := map[string]any{"email": email}
		if verified != nil {
			claims["email_verified"] = verified
		}
		return &idtoken.Payload{Issuer: issuer, Subject: "google-sub", Claims: claims}
	}
	const google, iap = "https://accounts.google.com", DefaultIAPIssuer
	idTokens := map[string]*idtoken.Payload{
		"google-token":             googlePayload(google, "sa@xmc-project.iam.gserviceaccount.com", true),
		"foreign-sa-token":         googlePayload(google, "attacker@other-project.iam.gserviceaccount.com", true),
		"google-unverified-token":  googlePayload(google, "user@example.com", false),
		"google-no-claim-token":    googlePayload(google, "user@example.com", nil),
		"google-string-true-token": googlePayload(google, "user@example.com", "true"),
		"iap-token":                googlePayload(iap, "user@example.com", nil),
		"iap-unverified-token":     googlePayload(iap, "user@example.com", false),
	}
	return jwksClaims, idTokens
}

func signForeignAudienceSessionToken(t *testing.T) string {
	t.Helper()
	now := time.Now()
	claims := &oauth2.SessionClaims{
		Email: "session-user@example.com",
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    oauth2.SessionIssuer,
			Audience:  jwt.ClaimStrings{"some-other-audience"},
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
		},
	}
	tok, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(tokenTestSecret))
	if err != nil {
		t.Fatalf("SignedString() failed: %v", err)
	}
	return tok
}

func TestAuthenticateUserFromContext_Admission(t *testing.T) {
	sessionToken, err := oauth2.GenerateSessionToken("session-user@example.com", "", "", time.Hour, []byte(tokenTestSecret))
	if err != nil {
		t.Fatalf("GenerateSessionToken() failed: %v", err)
	}
	jwksClaims, idTokens := testTokens(t)
	oldValidate := validate
	defer func() { validate = oldValidate }()
	validate = func(_ context.Context, jwt, audience string) (*idtoken.Payload, error) {
		if p, ok := idTokens[jwt]; ok && audience == tokenTestAudience {
			return p, nil
		}
		return nil, errors.New("invalid ID token")
	}
	crmDown := errors.New("crm unavailable")
	sessionUser, dexUser, googleSA := "session-user@example.com", "dex-user@example.com", "sa@xmc-project.iam.gserviceaccount.com"

	tests := []struct {
		name     string
		token    string
		admitted bool
		checkErr error
		disabled bool
		// requireVerified models a configured domain allowlist.
		requireVerified bool
		want            authOutcome
	}{
		{name: "session token admitted", token: sessionToken, admitted: true, want: authOutcome{Email: sessionUser, UserID: sessionUser, Checked: []string{sessionUser}}},
		{name: "session token not admitted", token: sessionToken, want: authOutcome{Code: codes.PermissionDenied, Checked: []string{sessionUser}}},
		{name: "session token checker error", token: sessionToken, checkErr: crmDown, want: authOutcome{Code: codes.Internal, Checked: []string{sessionUser}}},
		{name: "session token for another audience", token: signForeignAudienceSessionToken(t), admitted: true, want: authOutcome{Code: codes.Unauthenticated}},
		{name: "JWKS token admitted", token: "jwks-token", admitted: true, want: authOutcome{Email: dexUser, UserID: "dex-sub", Checked: []string{dexUser}}},
		{name: "JWKS token not admitted", token: "jwks-token", want: authOutcome{Code: codes.PermissionDenied, Checked: []string{dexUser}}},
		{name: "JWKS token checker error", token: "jwks-token", checkErr: crmDown, want: authOutcome{Code: codes.Internal, Checked: []string{dexUser}}},
		{name: "JWKS token with email_verified false", token: "jwks-unverified-token", admitted: true, want: authOutcome{Code: codes.Unauthenticated}},
		{name: "JWKS token with email_verified string false", token: "jwks-unverified-string-token", admitted: true, want: authOutcome{Code: codes.Unauthenticated}},
		{name: "JWKS token without email_verified", token: "jwks-no-claim-token", admitted: true, want: authOutcome{Email: dexUser, UserID: "dex-sub", Checked: []string{dexUser}}},
		{name: "Google ID token admitted", token: "google-token", admitted: true, want: authOutcome{Email: googleSA, UserID: "google-sub", Checked: []string{googleSA}}},
		{name: "foreign project SA ID token not admitted", token: "foreign-sa-token", want: authOutcome{Code: codes.PermissionDenied, Checked: []string{"attacker@other-project.iam.gserviceaccount.com"}}},
		{name: "Google ID token checker error", token: "google-token", checkErr: crmDown, want: authOutcome{Code: codes.Internal, Checked: []string{googleSA}}},
		{name: "Google ID token with email_verified false", token: "google-unverified-token", admitted: true, want: authOutcome{Code: codes.Unauthenticated}},
		{name: "Google ID token without email_verified", token: "google-no-claim-token", admitted: true, want: authOutcome{Code: codes.Unauthenticated}},
		{name: "Google ID token with email_verified string true", token: "google-string-true-token", admitted: true, want: authOutcome{Email: "user@example.com", UserID: "google-sub", Checked: []string{"user@example.com"}}},
		{name: "IAP assertion without email_verified", token: "iap-token", admitted: true, want: authOutcome{Email: "user@example.com", UserID: "google-sub", Checked: []string{"user@example.com"}}},
		{name: "IAP assertion with email_verified false", token: "iap-unverified-token", admitted: true, want: authOutcome{Code: codes.Unauthenticated}},
		{name: "disabled authentication skips admission", token: "google-token", disabled: true, want: authOutcome{Email: googleSA, UserID: "google-sub"}},
		{name: "disabled authentication still authenticates", token: "garbage", disabled: true, want: authOutcome{Code: codes.Unauthenticated}},
		{name: "domain allowlist: JWKS token without email_verified", token: "jwks-no-claim-token", admitted: true, requireVerified: true, want: authOutcome{Code: codes.Unauthenticated}},
		{name: "domain allowlist: JWKS token with verified email", token: "jwks-token", admitted: true, requireVerified: true, want: authOutcome{Email: dexUser, UserID: "dex-sub", Checked: []string{dexUser}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checker := &fakeAdmissionChecker{admitted: tt.admitted, err: tt.checkErr}
			var admission AdmissionChecker = checker
			if tt.disabled {
				admission = AdmitAll()
			}
			a, err := NewAuthorizer(AuthorizerParams{
				IAPAudiences:             []string{tokenTestAudience},
				IAPIssuer:                DefaultIAPIssuer,
				JWTSecret:                []byte(tokenTestSecret),
				Admission:                admission,
				RequireVerifiedJWKSEmail: tt.requireVerified,
			})
			if err != nil {
				t.Fatalf("NewAuthorizer() failed: %v", err)
			}
			a.SetJWKSValidator(&mockTokenValidator{validateFn: func(_ context.Context, tok string, _ []string) (*jwks.Claims, error) {
				if c, ok := jwksClaims[tok]; ok {
					return c, nil
				}
				return nil, jwks.ErrInvalidToken
			}})

			ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+tt.token))
			email, userID, err := a.AuthenticateUserFromContext(ctx)

			got := authOutcome{Email: email, UserID: userID, Code: status.Code(err), Checked: checker.checked}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("AuthenticateUserFromContext() mismatch (-want +got):\n%s\nerror: %v", diff, err)
			}
		})
	}
}
