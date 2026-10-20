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
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"regexp"
	"time"

	"golang.org/x/oauth2"
	"xmanager_cloud/internal/logging"
)

// maxCLITokenRequestBytes bounds the token request body; a valid request is under 300 bytes.
const maxCLITokenRequestBytes = 4096

// OAuth 2.0 token endpoint error codes (RFC 6749 section 5.2), plus too_many_requests for 429.
const (
	tokenErrorInvalidRequest       = "invalid_request"
	tokenErrorUnsupportedGrantType = "unsupported_grant_type"
	tokenErrorInvalidGrant         = "invalid_grant"
	tokenErrorAccessDenied         = "access_denied"
	tokenErrorServerError          = "server_error"
	tokenErrorTooManyRequests      = "too_many_requests"
)

// cliVerifierPattern matches a PKCE code verifier (RFC 7636 section 4.1).
var cliVerifierPattern = regexp.MustCompile(`^[A-Za-z0-9._~-]{43,128}$`)

// errInvalidGrant reports any problem with the presented code or verifier. Callers get the same
// response whatever the cause, so the endpoint does not reveal which check failed.
var errInvalidGrant = errors.New("invalid grant")

// cliTokenResponse is the JSON body of a successful `POST /api/cli/token`.
type cliTokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int64  `json:"expires_in"`
	Email       string `json:"email"`
}

// HandleCLIToken redeems a one-time CLI login code and its PKCE verifier for a CLI session token.
// It reads no cookies: the CLI proves possession of the code with the verifier.
func (a *Authenticator) HandleCLIToken(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !a.checkCLITokenRequest(w, r) {
		return
	}
	code, verifier, errCode := parseCLITokenRequest(w, r)
	if errCode != "" {
		writeTokenError(w, http.StatusBadRequest, errCode)
		return
	}
	// Only well-formed requests spend rate limit tokens, so malformed floods cannot lock out real
	// logins. Well-formed requests with random codes still reach the store, at most at this rate.
	if !a.cliTokenLimiter.Allow() {
		writeTokenError(w, http.StatusTooManyRequests, tokenErrorTooManyRequests)
		return
	}
	ctx := r.Context()
	record, err := a.redeemCLILoginCode(ctx, code, verifier, time.Now())
	if err != nil {
		a.writeRedeemError(ctx, w, err)
		return
	}
	if !a.checkStillAdmitted(ctx, w, record.Email) {
		return
	}
	a.writeCLIToken(ctx, w, record, time.Now())
}

// checkCLITokenRequest admits POSTs of the enabled CLI login flow and writes the error response
// otherwise.
func (a *Authenticator) checkCLITokenRequest(w http.ResponseWriter, r *http.Request) bool {
	if a.cliCodes == nil {
		http.NotFound(w, r)
		return false
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeTokenError(w, http.StatusMethodNotAllowed, tokenErrorInvalidRequest)
		return false
	}
	return true
}

// checkStillAdmitted re-runs deployment admission and writes the error response if it fails.
func (a *Authenticator) checkStillAdmitted(ctx context.Context, w http.ResponseWriter, email string) bool {
	authorized, err := a.IsUserAuthorized(ctx, email)
	if err != nil {
		logging.Error(ctx, "[CLI Token] Authorization pipeline check failed", "error", err)
		writeTokenError(w, http.StatusInternalServerError, tokenErrorServerError)
		return false
	}
	if !authorized {
		logging.Warn(ctx, "[CLI Token] Access forbidden: user is no longer admitted", "email", email)
		writeTokenError(w, http.StatusForbidden, tokenErrorAccessDenied)
		return false
	}
	return true
}

// parseCLITokenRequest reads the form body (never the URL query) and checks every field's format
// before the code store is touched. It returns the code, the verifier, and a token error code on
// failure.
func parseCLITokenRequest(w http.ResponseWriter, r *http.Request) (string, string, string) {
	if mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mediaType != "application/x-www-form-urlencoded" {
		return "", "", tokenErrorInvalidRequest
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxCLITokenRequestBytes)
	if err := r.ParseForm(); err != nil {
		return "", "", tokenErrorInvalidRequest
	}
	form, err := singleValues(r.PostForm, "grant_type", "code", "code_verifier")
	if err != nil || form["grant_type"] == "" {
		return "", "", tokenErrorInvalidRequest
	}
	if form["grant_type"] != "authorization_code" {
		return "", "", tokenErrorUnsupportedGrantType
	}
	if !base64URL256BitPattern.MatchString(form["code"]) || !cliVerifierPattern.MatchString(form["code_verifier"]) {
		return "", "", tokenErrorInvalidGrant
	}
	return form["code"], form["code_verifier"], ""
}

// redeemCLILoginCode consumes the code, then checks its expiry and PKCE verifier. Consuming first
// burns the code even when the verifier is wrong, so a stolen code cannot be retried.
func (a *Authenticator) redeemCLILoginCode(ctx context.Context, plaintext, verifier string, now time.Time) (CLILoginCode, error) {
	hash := hashCLILoginCode(plaintext)
	code, err := a.cliCodes.Consume(ctx, hash)
	if errors.Is(err, ErrCLICodeNotFound) {
		return CLILoginCode{}, fmt.Errorf("%w: unknown or used code %s", errInvalidGrant, codeHashPrefix(hash))
	}
	if err != nil {
		return CLILoginCode{}, fmt.Errorf("failed to consume code %s: %w", codeHashPrefix(hash), err)
	}
	if !now.Before(code.ExpiresAt) {
		return CLILoginCode{}, fmt.Errorf("%w: expired code %s", errInvalidGrant, codeHashPrefix(hash))
	}
	if !verifyPKCES256(verifier, code.CodeChallenge) {
		return CLILoginCode{}, fmt.Errorf("%w: verifier mismatch for code %s", errInvalidGrant, codeHashPrefix(hash))
	}
	return code, nil
}

func verifyPKCES256(verifier, challenge string) bool {
	return subtle.ConstantTimeCompare([]byte(oauth2.S256ChallengeFromVerifier(verifier)), []byte(challenge)) == 1
}

func (a *Authenticator) writeRedeemError(ctx context.Context, w http.ResponseWriter, err error) {
	if errors.Is(err, errInvalidGrant) {
		logging.Warn(ctx, "[CLI Token] Rejected login code", "error", err)
		writeTokenError(w, http.StatusBadRequest, tokenErrorInvalidGrant)
		return
	}
	logging.Error(ctx, "[CLI Token] Failed to redeem login code", "error", err)
	writeTokenError(w, http.StatusInternalServerError, tokenErrorServerError)
}

// writeCLIToken mints the CLI session token for code and writes the token response.
func (a *Authenticator) writeCLIToken(ctx context.Context, w http.ResponseWriter, code CLILoginCode, now time.Time) {
	ttl := a.Config.CLISessionTTL
	if ttl == 0 {
		ttl = defaultCLISessionTTL
	}
	jti, err := GenerateRandomString()
	if err != nil {
		logging.Error(ctx, "[CLI Token] Failed to generate token ID", "error", err)
		writeTokenError(w, http.StatusInternalServerError, tokenErrorServerError)
		return
	}
	token, err := generateCLISessionTokenAt(code.Email, code.Name, jti, ttl, a.JWTSecret, now)
	if err != nil {
		logging.Error(ctx, "[CLI Token] Failed to sign token", "error", err)
		writeTokenError(w, http.StatusInternalServerError, tokenErrorServerError)
		return
	}
	logging.Info(ctx, "[CLI Token] Issued CLI session token", "email", code.Email, "jti", jti, "expires_in", ttl.String())
	writeJSON(w, http.StatusOK, cliTokenResponse{
		AccessToken: token,
		TokenType:   "Bearer",
		ExpiresIn:   int64(ttl / time.Second),
		Email:       code.Email,
	})
}

func writeTokenError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]string{"error": code})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// The status is already sent; an encoding error only means the client went away.
	_ = json.NewEncoder(w).Encode(body)
}
