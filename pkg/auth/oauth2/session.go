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
	// sessionClockSkew is the leeway allowed on the exp, nbf and iat claims of a session token.
	sessionClockSkew = 30 * time.Second
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
	claims := &SessionClaims{
		Email:   email,
		Name:    name,
		Picture: picture,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(expiry)),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			Issuer:    SessionIssuer,
			Audience:  jwt.ClaimStrings{FrontendAudience},
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(secretKey)
}

// ParseSessionToken verifies and parses a signed JWT session token.
//
// The token must be signed with HS256, carry SessionIssuer as `iss`, an `exp` and a non-future
// `iat` (each with a sessionClockSkew leeway), and an `aud` that contains at least one of
// audiences. Each caller names the audiences it serves, so a token minted for one surface cannot be
// replayed against another. Calling it without any audience or with an empty secretKey rejects
// every token.
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
		jwt.WithIssuedAt(),
		jwt.WithLeeway(sessionClockSkew),
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
