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
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var (
	ErrInvalidToken = errors.New("invalid session token")
	ErrExpiredToken = errors.New("session token has expired")
)

const (
	// SessionIssuer is the `iss` claim of every session token this backend mints.
	SessionIssuer = "xmanager-cloud-backend"
	// FrontendAudience is the `aud` claim of the browser (Dashboard) session token.
	FrontendAudience = "xmanager-cloud-frontend"
	// CLIAudience is the `aud` claim of the session token issued to the CLI by `/api/cli/token`.
	// It is accepted as a bearer token but never as the Dashboard session cookie.
	CLIAudience = "xmanager-cloud-cli"
)

// SessionClaims defines the schema for stateless JWT user sessions.
type SessionClaims struct {
	Email   string `json:"email"`
	Name    string `json:"name"`
	Picture string `json:"picture"`
	jwt.RegisteredClaims
}

// GenerateSessionToken issues a new signed JWT session token.
func GenerateSessionToken(email, name, picture string, expiry time.Duration, secretKey []byte) (string, error) {
	return generateSessionTokenAt(email, name, picture, expiry, secretKey, time.Now())
}

func generateSessionTokenAt(email, name, picture string, expiry time.Duration, secretKey []byte, now time.Time) (string, error) {
	claims := newSessionClaims(email, name, FrontendAudience, expiry, now)
	claims.Picture = picture
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(secretKey)
}

// generateCLISessionTokenAt issues the CLI session token: CLIAudience, `sub` set to the email and
// `jti` set so the token can be identified in logs.
func generateCLISessionTokenAt(email, name, jti string, expiry time.Duration, secretKey []byte, now time.Time) (string, error) {
	claims := newSessionClaims(email, name, CLIAudience, expiry, now)
	claims.Subject = email
	claims.ID = jti
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(secretKey)
}

func newSessionClaims(email, name, audience string, expiry time.Duration, now time.Time) *SessionClaims {
	return &SessionClaims{
		Email: email,
		Name:  name,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(expiry)),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			Issuer:    SessionIssuer,
			Audience:  jwt.ClaimStrings{audience},
		},
	}
}

// ParseSessionToken verifies and parses a signed JWT session token.
//
// The token must be signed with HS256, carry SessionIssuer as `iss`, an `exp`, and an `aud` that
// contains at least one of audiences. Each caller names the audiences it serves, so a token minted
// for one surface cannot be replayed against another. Calling it without any audience or with an
// empty secretKey rejects every token.
func ParseSessionToken(tokenStr string, secretKey []byte, audiences ...string) (*SessionClaims, error) {
	// HMAC accepts an empty key, so a missing secret would let anyone mint valid tokens: fail closed.
	if len(secretKey) == 0 {
		return nil, ErrInvalidToken
	}
	// jwt.WithAudience skips the audience check entirely when given no audiences, so fail closed.
	if len(audiences) == 0 {
		return nil, ErrInvalidToken
	}
	keyFunc := func(*jwt.Token) (any, error) { return secretKey, nil }
	token, err := jwt.ParseWithClaims(tokenStr, &SessionClaims{}, keyFunc,
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(SessionIssuer),
		jwt.WithAudience(audiences...),
		jwt.WithExpirationRequired(),
	)

	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrExpiredToken
		}
		return nil, ErrInvalidToken
	}

	claims, ok := token.Claims.(*SessionClaims)
	if !ok || !token.Valid {
		return nil, ErrInvalidToken
	}

	return claims, nil
}
