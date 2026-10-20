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
	"errors"
	"fmt"
	"mime"
	"net/http"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"xmanager_cloud/internal/logging"
)

const (
	// cliConfirmationAudience is the JWT audience claim that separates manual confirmation tokens
	// from session tokens and cli_login cookie tokens.
	cliConfirmationAudience = "xmanager-cloud-cli-confirm"
	// cliManualConfirmationTTL bounds how long the confirmation page can be submitted.
	cliManualConfirmationTTL = 5 * time.Minute
	// cliManualCodeTTL is longer than cliLoopbackCodeTTL: the user copies the code by hand.
	cliManualCodeTTL = 120 * time.Second
	// maxCLICodeRequestBytes bounds the form, which carries a sealed email, name and challenge.
	maxCLICodeRequestBytes = 16 << 10
)

var errInvalidCLIConfirmation = errors.New("invalid cli manual confirmation")

// cliManualConfirmation is the pending manual login of a signed-in, admitted user. The callback's
// interstitial page carries it, signed with the JWT secret, to `POST /api/cli/code`. It holds no
// login code.
type cliManualConfirmation struct {
	Email         string `json:"email"`
	Name          string `json:"name"`
	CodeChallenge string `json:"code_challenge"`
	// ExpiresAt is the Unix time after which the confirmation is rejected.
	ExpiresAt int64 `json:"exp"`
}

// askCLIManualConfirmation shows the admitted user an interstitial page asking them to confirm the
// manual login. The code is minted only once they confirm, so a link an attacker sends (consent
// phishing) shows the victim a warning before any code exists.
func (a *Authenticator) askCLIManualConfirmation(ctx context.Context, w http.ResponseWriter, identity loginIdentity, codeChallenge string, now time.Time) {
	sealed, err := a.issueCLIManualConfirmation(identity, codeChallenge, now)
	if err != nil {
		logging.Error(ctx, "[CLI Login] Failed to issue manual login confirmation", "email", identity.email, "error", err)
		writeCLIManualError(w, cliErrorServerError)
		return
	}
	writeCLITemplatePage(w, cliConfirmationPage, cliConfirmationPageData{Email: identity.email, Confirmation: sealed})
}

// issueCLIManualConfirmation signs and returns a manual confirmation token.
func (a *Authenticator) issueCLIManualConfirmation(identity loginIdentity, codeChallenge string, now time.Time) (string, error) {
	expiresAt := now.Add(cliManualConfirmationTTL).Unix()
	return sealSigned(a.JWTSecret, cliConfirmationAudience, cliManualConfirmation{
		Email:         identity.email,
		Name:          identity.name,
		CodeChallenge: codeChallenge,
		ExpiresAt:     expiresAt,
	}, expiresAt)
}

// openCLIManualConfirmation verifies the signature, audience, and expiry of a sealed confirmation and decodes it.
func (a *Authenticator) openCLIManualConfirmation(sealed string, now time.Time) (cliManualConfirmation, error) {
	c, err := openSigned[cliManualConfirmation](a.JWTSecret, cliConfirmationAudience, sealed, now)
	if errors.Is(err, jwt.ErrTokenExpired) {
		return cliManualConfirmation{}, fmt.Errorf("%w: expired", errInvalidCLIConfirmation)
	}
	if err != nil {
		return cliManualConfirmation{}, errInvalidCLIConfirmation
	}
	return c, nil
}

// HandleCLICode serves the interstitial's form. Both buttons verify the confirmation.
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
	confirmation, err := a.openCLIManualConfirmation(form["confirmation"], time.Now())
	if err != nil {
		logging.Warn(ctx, "[CLI Login] Rejected manual login confirmation", "error", err)
		writeCLIManualExpired(w)
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
