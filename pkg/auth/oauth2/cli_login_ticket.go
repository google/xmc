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
	"encoding/base64"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	cliLoginTicketAudience = "xmanager-cloud-cli-login"
	cliLoginModeLoopback   = "loopback"
)

// cliLoginTicket is the state of a pending CLI login, carried from `/api/cli/login` to
// `/api/callback` in the signed cli_login cookie.
type cliLoginTicket struct {
	Port           int    `json:"port"`
	State          string `json:"state"`
	CodeChallenge  string `json:"code_challenge"`
	Mode           string `json:"mode"`
	OAuthStateHash string `json:"oauth_state_hash"`
	ExpiresAt      int64  `json:"exp"`
}

func hashOAuthState(oauthState string) string {
	sum := sha256.Sum256([]byte(oauthState))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func (a *Authenticator) sealCLILoginTicket(t cliLoginTicket) (string, error) {
	return sealSigned(a.JWTSecret, cliLoginTicketAudience, t, t.ExpiresAt)
}

type signedClaims[T any] struct {
	Payload T `json:"p"`
	jwt.RegisteredClaims
}

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
