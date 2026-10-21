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
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

var testSessionSecret = []byte("session-test-secret-key-0123456789")

// parseResult is the comparable outcome of a ParseSessionToken call.
type parseResult struct {
	Email string
	Err   error
}

func signSessionClaims(t *testing.T, method jwt.SigningMethod, key any, claims *SessionClaims) string {
	t.Helper()
	tokenStr, err := jwt.NewWithClaims(method, claims).SignedString(key)
	if err != nil {
		t.Fatalf("SignedString(%s) failed: %v", method.Alg(), err)
	}
	return tokenStr
}

func TestGenerateSessionToken(t *testing.T) {
	tokenStr, err := GenerateSessionToken("user@example.com", "User", "https://example.com/p.png", time.Hour, testSessionSecret)
	if err != nil {
		t.Fatalf("GenerateSessionToken() failed: %v", err)
	}

	got, err := ParseSessionToken(tokenStr, testSessionSecret, FrontendAudience)
	if err != nil {
		t.Fatalf("ParseSessionToken() failed: %v", err)
	}

	want := &SessionClaims{
		Email:   "user@example.com",
		Name:    "User",
		Picture: "https://example.com/p.png",
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:   SessionIssuer,
			Audience: jwt.ClaimStrings{FrontendAudience},
		},
	}
	ignoreTimes := cmpopts.IgnoreFields(jwt.RegisteredClaims{}, "ExpiresAt", "IssuedAt", "NotBefore")
	if diff := cmp.Diff(want, got, ignoreTimes); diff != "" {
		t.Errorf("ParseSessionToken() claims mismatch (-want +got):\n%s", diff)
	}
}

func TestGenerateCLISessionToken(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	tokenStr, err := generateCLISessionTokenAt("user@example.com", "User", "0123abcd", 12*time.Hour, testSessionSecret, now)
	if err != nil {
		t.Fatalf("generateCLISessionTokenAt() failed: %v", err)
	}
	got, err := ParseSessionToken(tokenStr, testSessionSecret, CLIAudience)
	if err != nil {
		t.Fatalf("ParseSessionToken(CLIAudience) failed: %v", err)
	}
	want := &SessionClaims{Email: "user@example.com", Name: "User", RegisteredClaims: jwt.RegisteredClaims{
		Issuer: SessionIssuer, Subject: "user@example.com", Audience: jwt.ClaimStrings{CLIAudience}, ID: "0123abcd",
		ExpiresAt: jwt.NewNumericDate(now.Add(12 * time.Hour)), NotBefore: jwt.NewNumericDate(now), IssuedAt: jwt.NewNumericDate(now),
	}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("ParseSessionToken() claims mismatch (-want +got):\n%s", diff)
	}
}

func TestParseSessionToken(t *testing.T) {
	now := time.Now()
	baseClaims := func() *SessionClaims {
		return &SessionClaims{
			Email: "user@example.com",
			RegisteredClaims: jwt.RegisteredClaims{
				Issuer:    SessionIssuer,
				Audience:  jwt.ClaimStrings{FrontendAudience},
				IssuedAt:  jwt.NewNumericDate(now),
				ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
			},
		}
	}
	valid := parseResult{Email: "user@example.com"}
	invalid := parseResult{Err: ErrInvalidToken}

	tests := []struct {
		name   string
		method jwt.SigningMethod
		key    any
		mutate func(*SessionClaims)
		// audiences defaults to FrontendAudience when nil.
		audiences []string
		want      parseResult
	}{
		{name: "valid", want: valid},
		{name: "matches any allowed audience", audiences: []string{"other-audience", FrontendAudience}, want: valid},
		{
			name:   "token with several audiences matches an allowed one",
			mutate: func(c *SessionClaims) { c.Audience = jwt.ClaimStrings{"other-audience", FrontendAudience} },
			want:   valid,
		},
		{
			name:   "expired",
			mutate: func(c *SessionClaims) { c.ExpiresAt = jwt.NewNumericDate(now.Add(-time.Minute)) },
			want:   parseResult{Err: ErrExpiredToken},
		},
		{name: "missing exp", mutate: func(c *SessionClaims) { c.ExpiresAt = nil }, want: invalid},
		{name: "wrong issuer", mutate: func(c *SessionClaims) { c.Issuer = "someone-else" }, want: invalid},
		{name: "missing issuer", mutate: func(c *SessionClaims) { c.Issuer = "" }, want: invalid},
		{name: "wrong audience", audiences: []string{"xmanager-cloud-other"}, want: invalid},
		{name: "missing audience", mutate: func(c *SessionClaims) { c.Audience = nil }, want: invalid},
		{name: "no allowed audiences", audiences: []string{}, want: invalid},
		{name: "HS512 signature", method: jwt.SigningMethodHS512, want: invalid},
		{name: "none signature", method: jwt.SigningMethodNone, key: jwt.UnsafeAllowNoneSignatureType, want: invalid},
		{name: "wrong secret", key: []byte("another-session-secret-0123456789"), want: invalid},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			method, key, audiences := tt.method, tt.key, tt.audiences
			if method == nil {
				method = jwt.SigningMethodHS256
			}
			if key == nil {
				key = testSessionSecret
			}
			if audiences == nil {
				audiences = []string{FrontendAudience}
			}
			claims := baseClaims()
			if tt.mutate != nil {
				tt.mutate(claims)
			}
			tokenStr := signSessionClaims(t, method, key, claims)

			parsed, err := ParseSessionToken(tokenStr, testSessionSecret, audiences...)
			got := parseResult{Err: err}
			if parsed != nil {
				got.Email = parsed.Email
			}
			if diff := cmp.Diff(tt.want, got, cmpopts.EquateErrors()); diff != "" {
				t.Errorf("ParseSessionToken() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestParseSessionTokenRejectsEmptySecret(t *testing.T) {
	now := time.Now()
	claims := &SessionClaims{
		Email: "user@example.com",
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    SessionIssuer,
			Audience:  jwt.ClaimStrings{FrontendAudience},
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
		},
	}
	tokenStr := signSessionClaims(t, jwt.SigningMethodHS256, []byte{}, claims)

	if _, err := ParseSessionToken(tokenStr, nil, FrontendAudience); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("ParseSessionToken(empty secret) error = %v, want %v", err, ErrInvalidToken)
	}
}
