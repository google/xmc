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
			Issuer:    "xmanager-cloud-backend",
			Audience:  jwt.ClaimStrings{"xmanager-cloud-frontend"},
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(secretKey)
}

// ParseSessionToken verifies and parses a signed JWT session token.
func ParseSessionToken(tokenStr string, secretKey []byte) (*SessionClaims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &SessionClaims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, ErrInvalidToken
		}
		return secretKey, nil
	})

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
