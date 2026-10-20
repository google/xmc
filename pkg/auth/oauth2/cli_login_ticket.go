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
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	// cliLoginTicketAudience is the JWT audience claim that separates cli_login cookie tokens from
	// session tokens.
	cliLoginTicketAudience = "xmanager-cloud-cli-login"
	// cliLoginModeLoopback marks a ticket whose code is delivered to the CLI's loopback listener.
	cliLoginModeLoopback = "loopback"
	// cliLoginModeManual marks a ticket whose code is shown in the browser, after the user
	// confirms, for the user to paste into the CLI.
	cliLoginModeManual = "manual"
)

var (
	errInvalidCLILoginTicket = errors.New("invalid cli_login ticket")
	// errExpiredCLILoginTicket wraps errInvalidCLILoginTicket for an authentic ticket past its
	// expiry, which is a normal timeout rather than a sign of tampering.
	errExpiredCLILoginTicket = fmt.Errorf("%w: expired", errInvalidCLILoginTicket)
)

// cliLoginTicket is the state of a pending CLI login, carried from `/api/cli/login` to
// `/api/callback` in the signed cli_login cookie.
type cliLoginTicket struct {
	Port          int    `json:"port"`
	State         string `json:"state"`
	CodeChallenge string `json:"code_challenge"`
	Mode          string `json:"mode"`
	// OAuthStateHash is base64url(SHA-256(oauth_state)). It binds the ticket to one IdP round trip,
	// so a stale cookie cannot turn a later Dashboard login into a CLI login.
	OAuthStateHash string `json:"oauth_state_hash"`
	// ExpiresAt is the Unix time after which the ticket is rejected.
	ExpiresAt int64 `json:"exp"`
}

func hashOAuthState(oauthState string) string {
	sum := sha256.Sum256([]byte(oauthState))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// boundTo reports whether the ticket was issued for the IdP round trip that used oauthState.
func (t cliLoginTicket) boundTo(oauthState string) bool {
	return subtle.ConstantTimeCompare([]byte(t.OAuthStateHash), []byte(hashOAuthState(oauthState))) == 1
}

// sealCLILoginTicket signs the ticket as an HS256 JWT with the JWT secret and cliLoginTicketAudience.
func (a *Authenticator) sealCLILoginTicket(t cliLoginTicket) (string, error) {
	return sealSigned(a.JWTSecret, cliLoginTicketAudience, t, t.ExpiresAt)
}

// openCLILoginTicket verifies the signature, audience, and expiry of a sealed ticket and decodes it.
func (a *Authenticator) openCLILoginTicket(sealed string, now time.Time) (cliLoginTicket, error) {
	t, err := openSigned[cliLoginTicket](a.JWTSecret, cliLoginTicketAudience, sealed, now)
	if errors.Is(err, jwt.ErrTokenExpired) {
		return cliLoginTicket{}, errExpiredCLILoginTicket
	}
	if err != nil {
		return cliLoginTicket{}, errInvalidCLILoginTicket
	}
	return t, nil
}

type signedClaims[T any] struct {
	Payload T `json:"p"`
	jwt.RegisteredClaims
}

// sealSigned encodes v into an HS256 JWT signed with key, bound to audience, and expiring at Unix time expiresAt.
func sealSigned[T any](key []byte, audience string, v T, expiresAt int64) (string, error) {
	claims := signedClaims[T]{
		Payload: v,
		RegisteredClaims: jwt.RegisteredClaims{
			Audience:  jwt.ClaimStrings{audience},
			ExpiresAt: jwt.NewNumericDate(time.Unix(expiresAt, 0)),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(key)
}

// openSigned verifies the HS256 signature, audience, and expiry of a token sealed by sealSigned and returns
// its payload.
func openSigned[T any](key []byte, audience, sealed string, now time.Time) (T, error) {
	var zero T
	if len(key) == 0 {
		return zero, errInvalidCLILoginTicket
	}
	var claims signedClaims[T]
	keyFunc := func(*jwt.Token) (any, error) { return key, nil }
	token, err := jwt.ParseWithClaims(sealed, &claims, keyFunc,
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithAudience(audience),
		jwt.WithExpirationRequired(),
		jwt.WithTimeFunc(func() time.Time { return time.Unix(now.Unix(), 0) }),
	)
	if err != nil || !token.Valid {
		return zero, err
	}
	return claims.Payload, nil
}
