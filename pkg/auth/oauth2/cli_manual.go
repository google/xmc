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
	cliConfirmationAudience  = "xmanager-cloud-cli-confirm"
	cliManualConfirmationTTL = 5 * time.Minute
	cliManualCodeTTL         = 120 * time.Second
	maxCLICodeRequestBytes   = 16 << 10
)

var errInvalidCLIConfirmation = errors.New("invalid cli manual confirmation")

// cliManualConfirmation is the signed payload carried from the interstitial page to POST /api/cli/code.
type cliManualConfirmation struct {
	Email         string `json:"email"`
	Name          string `json:"name"`
	CodeChallenge string `json:"code_challenge"`
	ExpiresAt     int64  `json:"exp"`
}

func (a *Authenticator) askCLIManualConfirmation(ctx context.Context, w http.ResponseWriter, identity loginIdentity, codeChallenge string, now time.Time) {
	sealed, err := a.issueCLIManualConfirmation(identity, codeChallenge, now)
	if err != nil {
		logging.Error(ctx, "[CLI Login] Failed to issue manual login confirmation", "email", identity.email, "error", err)
		writeCLIManualError(w, cliErrorServerError)
		return
	}
	writeCLITemplatePage(w, cliConfirmationPage, cliConfirmationPageData{Email: identity.email, Confirmation: sealed})
}

func (a *Authenticator) issueCLIManualConfirmation(identity loginIdentity, codeChallenge string, now time.Time) (string, error) {
	expiresAt := now.Add(cliManualConfirmationTTL).Unix()
	c := cliManualConfirmation{Email: identity.email, Name: identity.name, CodeChallenge: codeChallenge, ExpiresAt: expiresAt}
	return sealSigned(a.JWTSecret, cliConfirmationAudience, c, expiresAt)
}

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

// HandleCLICode serves the manual login confirmation form, re-running admission and issuing a one-time code on continue.
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
