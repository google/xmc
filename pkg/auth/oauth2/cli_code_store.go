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
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"time"
)

// cliLoginCodeBytes is the entropy of a one-time CLI login code.
const cliLoginCodeBytes = 32

// CLILoginCode is a one-time CLI login code issued by the callback and redeemed by the token
// endpoint. Only the SHA-256 of the code is stored.
type CLILoginCode struct {
	// Hash is the SHA-256 of the code.
	Hash []byte
	// Email is the normalized (trimmed, lowercased) email of the admitted user.
	Email string
	// Name is the display name returned by the identity provider.
	Name string
	// CodeChallenge is the S256 PKCE challenge sent by the CLI to `/api/cli/login`.
	CodeChallenge string
	// ExpiresAt is when the code stops being redeemable.
	ExpiresAt time.Time
}

// CLICodeStore persists one-time CLI login codes.
type CLICodeStore interface {
	// Save stores a new code.
	Save(ctx context.Context, code CLILoginCode) error
}

// EnableCLILogin turns on the CLI login endpoints, storing one-time login codes in codes. It is an
// initialization step: call it once, after JWTSecret is final and before the Authenticator serves
// requests. The fields it sets are read without locking, so it refuses to run a second time.
func (a *Authenticator) EnableCLILogin(codes CLICodeStore) error {
	if codes == nil {
		return errors.New("cli login code store must not be nil")
	}
	if a.cliCodes != nil {
		return errors.New("cli login is already enabled")
	}
	key, err := deriveCLILoginMACKey(a.JWTSecret)
	if err != nil {
		return err
	}
	a.cliCodes = codes
	a.cliLoginKey = key
	return nil
}

// newCLILoginCode returns a random code (43 base64url characters) and its SHA-256.
func newCLILoginCode() (string, []byte, error) {
	b := make([]byte, cliLoginCodeBytes)
	if _, err := io.ReadFull(randReader, b); err != nil {
		return "", nil, fmt.Errorf("failed to generate cli login code: %w", err)
	}
	code := base64.RawURLEncoding.EncodeToString(b)
	return code, hashCLILoginCode(code), nil
}

func hashCLILoginCode(code string) []byte {
	sum := sha256.Sum256([]byte(code))
	return sum[:]
}

// codeHashPrefix returns the first 8 hex characters of a code hash, which is safe to log.
func codeHashPrefix(hash []byte) string {
	return hex.EncodeToString(hash[:min(len(hash), 4)])
}
