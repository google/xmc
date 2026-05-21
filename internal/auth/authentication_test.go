// Copyright 2025 Google LLC
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
package auth

import (
	"context"
	"errors"
	"testing"

	"golang/google_api/idtoken/idtoken"
	"golang/grpc/metadata/metadata"
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
	a := &Authorizer{iapAudiences: []string{"other_audience", "test_audience"}, iapIssuer: "https://cloud.google.com/iap"}
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
			name:     "success with x-goog-user-email",
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
			wantEmail:  "user@example.com",
			wantUserID: "userID",
		},
		{
			name:     "empty x-goog-user-email fallbacks to payload email",
			metadata: metadata.Pairs("x-goog-iap-jwt-assertion", "jwt", "x-goog-user-email", ""),
			validator: func(ctx context.Context, jwt, audience string) (*idtoken.Payload, error) {
				if audience == "test_audience" {
					return &idtoken.Payload{
						Issuer:  "https://cloud.google.com/iap",
						Subject: "userID",
						Claims:  map[string]any{"email": "payload@example.com"},
					}, nil
				}
				return nil, errors.New("validation failed for audience " + audience)
			},
			wantEmail:  "payload@example.com",
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
