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
	"errors"
	"fmt"
	"mime"
	"net/http"
	"time"

	"xmanager_cloud/internal/logging"
)

const (
	// cliManualConfirmationInfo is the HKDF info of the confirmation MAC key (see deriveCLIKeys).
	cliManualConfirmationInfo = "xmc-cli-manual-confirm-v1"
	// cliConfirmationNonceDomain prefixes a nonce before hashing it into the code store, so a nonce
	// record never matches a login code, which `/api/cli/token` hashes without the prefix.
	cliConfirmationNonceDomain = "xmc-cli-manual-confirm-nonce-v1:"
	// cliManualConfirmationTTL bounds how long the confirmation page can be submitted.
	cliManualConfirmationTTL = 5 * time.Minute
	// cliManualCodeTTL is longer than cliLoopbackCodeTTL: the user copies the code by hand.
	cliManualCodeTTL = 120 * time.Second
	// maxCLICodeRequestBytes bounds the form, which carries a sealed email, name and challenge.
	maxCLICodeRequestBytes = 16 << 10
)

var errInvalidCLIConfirmation = errors.New("invalid cli manual confirmation")

// cliManualConfirmation is the pending manual login of a signed-in, admitted user. The callback's
// interstitial page carries it, MAC'd, to `POST /api/cli/code`. It holds no login code.
type cliManualConfirmation struct {
	Email         string `json:"email"`
	Name          string `json:"name"`
	CodeChallenge string `json:"code_challenge"`
	// Nonce makes the confirmation single-use (see takeCLIManualConfirmation).
	Nonce string `json:"nonce"`
	// ExpiresAt is the Unix time after which the confirmation is rejected.
	ExpiresAt int64 `json:"exp"`
}

// askCLIManualConfirmation shows the admitted user an interstitial page asking them to confirm the
// manual login. The code is minted only once they confirm, so a link an attacker sends (consent
// phishing) shows the victim a warning before any code exists.
func (a *Authenticator) askCLIManualConfirmation(ctx context.Context, w http.ResponseWriter, identity loginIdentity, codeChallenge string, now time.Time) {
	sealed, err := a.issueCLIManualConfirmation(ctx, identity, codeChallenge, now)
	if err != nil {
		logging.Error(ctx, "[CLI Login] Failed to issue manual login confirmation", "email", identity.email, "error", err)
		writeCLIManualError(w, cliErrorServerError)
		return
	}
	writeCLITemplatePage(w, cliConfirmationPage, cliConfirmationPageData{Email: identity.email, Confirmation: sealed})
}

// issueCLIManualConfirmation records a single-use nonce in the code store and returns the sealed
// confirmation that carries it.
func (a *Authenticator) issueCLIManualConfirmation(ctx context.Context, identity loginIdentity, codeChallenge string, now time.Time) (string, error) {
	nonce, err := GenerateRandomString()
	if err != nil {
		return "", fmt.Errorf("failed to generate confirmation nonce: %w", err)
	}
	expiresAt := now.Add(cliManualConfirmationTTL)
	record := CLILoginCode{
		Hash:          hashCLIConfirmationNonce(nonce),
		Email:         identity.email,
		Name:          identity.name,
		CodeChallenge: codeChallenge,
		ExpiresAt:     expiresAt,
	}
	if err := a.cliCodes.Save(ctx, record); err != nil {
		return "", err
	}
	return sealSigned(a.cliConfirmationKey, cliManualConfirmation{
		Email:         identity.email,
		Name:          identity.name,
		CodeChallenge: codeChallenge,
		Nonce:         nonce,
		ExpiresAt:     expiresAt.Unix(),
	})
}

func hashCLIConfirmationNonce(nonce string) []byte {
	sum := sha256.Sum256([]byte(cliConfirmationNonceDomain + nonce))
	return sum[:]
}

// openCLIManualConfirmation verifies the MAC and expiry of a sealed confirmation and decodes it.
func (a *Authenticator) openCLIManualConfirmation(sealed string, now time.Time) (cliManualConfirmation, error) {
	var c cliManualConfirmation
	if !openSigned(a.cliConfirmationKey, sealed, &c) {
		return cliManualConfirmation{}, errInvalidCLIConfirmation
	}
	if now.Unix() >= c.ExpiresAt {
		return cliManualConfirmation{}, fmt.Errorf("%w: expired", errInvalidCLIConfirmation)
	}
	return c, nil
}

// HandleCLICode serves the interstitial's form. Both buttons verify and use up the confirmation.
// Continue then re-runs admission and shows a one-time code for the CLI, only in the response
// body; Cancel issues nothing.
func (a *Authenticator) HandleCLICode(w http.ResponseWriter, r *http.Request) {
	if !a.checkCLICodeRequest(w, r) {
		return
	}
	form, err := parseCLICodeForm(w, r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	confirmation, ok := a.takeCLIManualConfirmation(ctx, w, form["confirmation"])
	if !ok {
		return
	}
	if form["action"] == "cancel" {
		writeCLIResultPage(w, http.StatusOK, "Sign-in cancelled", "No code was issued. You can close this tab.")
		return
	}
	if !a.checkCLIManualStillAdmitted(ctx, w, confirmation.Email) {
		return
	}
	identity := loginIdentity{email: confirmation.Email, name: confirmation.Name}
	code, err := a.issueCLILoginCode(ctx, identity, confirmation.CodeChallenge, cliManualCodeTTL, time.Now())
	if err != nil {
		logging.Error(ctx, "[CLI Login] Failed to issue manual login code", "email", confirmation.Email, "error", err)
		writeCLIManualError(w, cliErrorServerError)
		return
	}
	writeCLITemplatePage(w, cliCodePage, code)
}

// checkCLICodeRequest admits same-origin form POSTs of the enabled CLI login flow and writes the
// error response otherwise. CrossOriginProtection already rejects cross-origin POSTs; requiring
// Sec-Fetch-Site same-origin also rejects same-site ones. Browsers that send Sec-Fetch-Dest must
// send "document", so the form cannot be submitted by script to read the code.
func (a *Authenticator) checkCLICodeRequest(w http.ResponseWriter, r *http.Request) bool {
	if a.cliCodes == nil {
		http.NotFound(w, r)
		return false
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return false
	}
	if !headerAbsentOrEqual(r, "Sec-Fetch-Site", "same-origin") {
		http.Error(w, "The CLI login form must be submitted from its own page", http.StatusForbidden)
		return false
	}
	if !headerAbsentOrEqual(r, "Sec-Fetch-Dest", "document") {
		http.Error(w, "The CLI login form must be submitted as a top-level page", http.StatusBadRequest)
		return false
	}
	return true
}

// parseCLICodeForm reads the confirmation form from the request body, never the URL query.
func parseCLICodeForm(w http.ResponseWriter, r *http.Request) (map[string]string, error) {
	if mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mediaType != "application/x-www-form-urlencoded" {
		return nil, errors.New("the CLI login form must be form-encoded")
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxCLICodeRequestBytes)
	if err := r.ParseForm(); err != nil {
		return nil, errors.New("the CLI login form could not be read")
	}
	form, err := singleValues(r.PostForm, "action", "confirmation")
	if err != nil {
		return nil, err
	}
	if form["action"] != "continue" && form["action"] != "cancel" {
		return nil, errors.New("action must be continue or cancel")
	}
	return form, nil
}

// takeCLIManualConfirmation verifies the sealed confirmation and consumes its nonce, so each
// confirmation yields at most one code; it writes the error page otherwise. A resubmitted form
// (for example after a reload) is rejected and the user starts a new login.
func (a *Authenticator) takeCLIManualConfirmation(ctx context.Context, w http.ResponseWriter, sealed string) (cliManualConfirmation, bool) {
	confirmation, err := a.openCLIManualConfirmation(sealed, time.Now())
	if err != nil {
		logging.Warn(ctx, "[CLI Login] Rejected manual login confirmation", "error", err)
		writeCLIManualExpired(w)
		return cliManualConfirmation{}, false
	}
	hash := hashCLIConfirmationNonce(confirmation.Nonce)
	_, err = a.cliCodes.Consume(ctx, hash)
	if errors.Is(err, ErrCLICodeNotFound) {
		logging.Warn(ctx, "[CLI Login] Rejected used manual login confirmation", "email", confirmation.Email, "nonce_hash_prefix", codeHashPrefix(hash))
		writeCLIManualExpired(w)
		return cliManualConfirmation{}, false
	}
	if err != nil {
		logging.Error(ctx, "[CLI Login] Failed to consume manual login confirmation", "email", confirmation.Email, "error", err)
		writeCLIManualError(w, cliErrorServerError)
		return cliManualConfirmation{}, false
	}
	return confirmation, true
}

// checkCLIManualStillAdmitted re-runs deployment admission, which may have changed since the
// callback, and writes the error page if it fails.
func (a *Authenticator) checkCLIManualStillAdmitted(ctx context.Context, w http.ResponseWriter, email string) bool {
	admitted, err := a.stillAdmitted(ctx, email)
	if err != nil {
		writeCLIManualError(w, cliErrorServerError)
		return false
	}
	if !admitted {
		writeCLIManualError(w, cliErrorAccessDenied)
	}
	return admitted
}
