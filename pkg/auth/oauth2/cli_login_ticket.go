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
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	// cliLoginMACInfo is the HKDF info that derives the cli_login cookie MAC key from the JWT
	// secret, so the cookie MAC and session tokens never share a key.
	cliLoginMACInfo = "xmc-cli-login-v1"
	// cliLoginModeLoopback marks a ticket whose code is delivered to the CLI's loopback listener.
	cliLoginModeLoopback = "loopback"
)

var (
	errInvalidCLILoginTicket = errors.New("invalid cli_login ticket")
	// errExpiredCLILoginTicket wraps errInvalidCLILoginTicket for an authentic ticket past its
	// expiry, which is a normal timeout rather than a sign of tampering.
	errExpiredCLILoginTicket = fmt.Errorf("%w: expired", errInvalidCLILoginTicket)
)

// cliLoginTicket is the state of a pending CLI login, carried from `/api/cli/login` to
// `/api/callback` in the MAC'd cli_login cookie.
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

// sealCLILoginTicket encodes the ticket as base64url(JSON) "." base64url(HMAC-SHA256).
func (a *Authenticator) sealCLILoginTicket(t cliLoginTicket) (string, error) {
	payload, err := json.Marshal(t)
	if err != nil {
		return "", fmt.Errorf("failed to encode cli_login ticket: %w", err)
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	return encoded + "." + base64.RawURLEncoding.EncodeToString(a.macCLILoginTicket(encoded)), nil
}

// openCLILoginTicket verifies the MAC and expiry of a sealed ticket and decodes it.
func (a *Authenticator) openCLILoginTicket(sealed string, now time.Time) (cliLoginTicket, error) {
	encoded, mac, ok := strings.Cut(sealed, ".")
	if !ok {
		return cliLoginTicket{}, errInvalidCLILoginTicket
	}
	gotMAC, err := base64.RawURLEncoding.DecodeString(mac)
	if err != nil || !hmac.Equal(gotMAC, a.macCLILoginTicket(encoded)) {
		return cliLoginTicket{}, errInvalidCLILoginTicket
	}
	return decodeCLILoginTicket(encoded, now)
}

func decodeCLILoginTicket(encoded string, now time.Time) (cliLoginTicket, error) {
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return cliLoginTicket{}, errInvalidCLILoginTicket
	}
	var t cliLoginTicket
	if err := json.Unmarshal(payload, &t); err != nil {
		return cliLoginTicket{}, errInvalidCLILoginTicket
	}
	if now.Unix() >= t.ExpiresAt {
		return cliLoginTicket{}, errExpiredCLILoginTicket
	}
	return t, nil
}

// deriveCLILoginMACKey derives the cli_login cookie MAC key from the JWT secret with
// HKDF-SHA256 (RFC 5869).
func deriveCLILoginMACKey(jwtSecret []byte) ([]byte, error) {
	key, err := hkdf.Key(sha256.New, jwtSecret, nil, cliLoginMACInfo, sha256.Size)
	if err != nil {
		return nil, fmt.Errorf("failed to derive cli_login key: %w", err)
	}
	return key, nil
}

func (a *Authenticator) macCLILoginTicket(encoded string) []byte {
	mac := hmac.New(sha256.New, a.cliLoginKey)
	mac.Write([]byte(encoded))
	return mac.Sum(nil)
}
