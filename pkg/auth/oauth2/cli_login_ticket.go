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

// sealCLILoginTicket seals the ticket with the cli_login MAC key (see sealSigned).
func (a *Authenticator) sealCLILoginTicket(t cliLoginTicket) (string, error) {
	return sealSigned(a.cliLoginKey, t)
}

// openCLILoginTicket verifies the MAC and expiry of a sealed ticket and decodes it.
func (a *Authenticator) openCLILoginTicket(sealed string, now time.Time) (cliLoginTicket, error) {
	var t cliLoginTicket
	if !openSigned(a.cliLoginKey, sealed, &t) {
		return cliLoginTicket{}, errInvalidCLILoginTicket
	}
	if now.Unix() >= t.ExpiresAt {
		return cliLoginTicket{}, errExpiredCLILoginTicket
	}
	return t, nil
}

// sealSigned encodes v as base64url(JSON) "." base64url(HMAC-SHA256 of the encoded JSON).
func sealSigned(key []byte, v any) (string, error) {
	payload, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("failed to encode signed value: %w", err)
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	return encoded + "." + base64.RawURLEncoding.EncodeToString(hmacSHA256(key, []byte(encoded))), nil
}

// openSigned verifies the MAC of a value sealed by sealSigned with key and decodes it into v. It
// reports whether the value is authentic and well formed.
func openSigned(key []byte, sealed string, v any) bool {
	encoded, mac, ok := strings.Cut(sealed, ".")
	if !ok {
		return false
	}
	gotMAC, err := base64.RawURLEncoding.DecodeString(mac)
	if err != nil || !hmac.Equal(gotMAC, hmacSHA256(key, []byte(encoded))) {
		return false
	}
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return false
	}
	return json.Unmarshal(payload, v) == nil
}

// deriveCLIKeys derives the cli_login cookie MAC key and the manual-mode confirmation MAC key from
// the JWT secret with HKDF-SHA256 (RFC 5869). Distinct HKDF infos keep the two MACs and session
// tokens from ever sharing a key.
func deriveCLIKeys(jwtSecret []byte) (ticketKey, confirmationKey []byte, err error) {
	var keys [2][]byte
	for i, info := range [2]string{cliLoginMACInfo, cliManualConfirmationInfo} {
		if keys[i], err = hkdf.Key(sha256.New, jwtSecret, nil, info, sha256.Size); err != nil {
			return nil, nil, fmt.Errorf("failed to derive %s key: %w", info, err)
		}
	}
	return keys[0], keys[1], nil
}

func hmacSHA256(key, data []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	return mac.Sum(nil)
}
