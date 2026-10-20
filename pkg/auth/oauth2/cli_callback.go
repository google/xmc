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
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"xmanager_cloud/internal/logging"
)

// cliLoopbackCodeTTL is how long a code delivered to the loopback listener stays redeemable.
const cliLoopbackCodeTTL = 60 * time.Second

// OAuth 2.0 error codes (RFC 6749 section 4.1.2.1) sent to the CLI's loopback listener.
const (
	cliErrorAccessDenied = "access_denied"
	cliErrorServerError  = "server_error"
)

// takeCLILoginTicket deletes any cli_login cookie and returns its ticket when the cookie is
// authentic, unexpired and bound to oauthState. Otherwise the callback is a Dashboard login.
func (a *Authenticator) takeCLILoginTicket(w http.ResponseWriter, r *http.Request, oauthState string) (cliLoginTicket, bool) {
	cookie, err := r.Cookie(cliLoginCookieName)
	if err != nil {
		return cliLoginTicket{}, false
	}
	a.clearCLILoginCookie(w, r)
	if a.cliCodes == nil {
		return cliLoginTicket{}, false
	}
	ticket, err := a.openCLILoginTicket(cookie.Value, time.Now())
	if err != nil {
		logging.Warn(r.Context(), "[CLI Login] Ignoring cli_login cookie", "error", err)
		return cliLoginTicket{}, false
	}
	if ticket.Mode != cliLoginModeLoopback || !ticket.boundTo(oauthState) {
		logging.Warn(r.Context(), "[CLI Login] Ignoring cli_login cookie issued for another login")
		return cliLoginTicket{}, false
	}
	return ticket, true
}

// handleCLICallback finishes the IdP round trip of a CLI login. Every outcome is reported to the
// CLI's loopback listener so it never waits for a result that will not come: an IdP error or a
// missing code (such as the user cancelling) is access_denied, and a failed code exchange is
// server_error.
func (a *Authenticator) handleCLICallback(ctx context.Context, w http.ResponseWriter, r *http.Request, ticket cliLoginTicket) {
	code := r.URL.Query().Get("code")
	if code == "" {
		logging.Warn(ctx, "[CLI Login] Identity provider returned no code", "idp_error", r.URL.Query().Get("error"))
		redirectToCLILoopback(w, r, ticket, url.Values{"error": {cliErrorAccessDenied}})
		return
	}
	userInfo, err := a.OAuthProvider.ExchangeAndGetUserInfo(ctx, r, code)
	if err != nil {
		logging.Error(ctx, "[CLI Login] Failed to get user info", "error", err)
		redirectToCLILoopback(w, r, ticket, url.Values{"error": {cliErrorServerError}})
		return
	}
	a.completeCLILogin(ctx, w, r, ticket, userInfo)
}

// completeCLILogin admits the user and redirects the browser to the CLI's loopback listener with
// a one-time code, or with an error. The email is never put in the loopback URL.
func (a *Authenticator) completeCLILogin(ctx context.Context, w http.ResponseWriter, r *http.Request, ticket cliLoginTicket, userInfo *GoogleUserInfo) {
	identity, err := a.admitLogin(ctx, userInfo)
	if errors.Is(err, errNotAdmitted) {
		logging.Warn(ctx, "[CLI Login] Access forbidden: user is not admitted", "email", identity.email, "error", err)
		redirectToCLILoopback(w, r, ticket, url.Values{"error": {cliErrorAccessDenied}})
		return
	}
	if err != nil {
		logging.Error(ctx, "[CLI Login] Authorization pipeline check failed", "error", err)
		redirectToCLILoopback(w, r, ticket, url.Values{"error": {cliErrorServerError}})
		return
	}
	code, err := a.issueCLILoginCode(ctx, identity, ticket.CodeChallenge, time.Now())
	if err != nil {
		logging.Error(ctx, "[CLI Login] Failed to issue login code", "email", identity.email, "error", err)
		redirectToCLILoopback(w, r, ticket, url.Values{"error": {cliErrorServerError}})
		return
	}
	redirectToCLILoopback(w, r, ticket, url.Values{"code": {code}})
}

// issueCLILoginCode stores a new one-time code for identity and returns it.
func (a *Authenticator) issueCLILoginCode(ctx context.Context, identity loginIdentity, codeChallenge string, now time.Time) (string, error) {
	code, hash, err := newCLILoginCode()
	if err != nil {
		return "", err
	}
	record := CLILoginCode{
		Hash:          hash,
		Email:         identity.email,
		Name:          identity.name,
		CodeChallenge: codeChallenge,
		ExpiresAt:     now.Add(cliLoopbackCodeTTL),
	}
	if err := a.cliCodes.Save(ctx, record); err != nil {
		return "", err
	}
	logging.Info(ctx, "[CLI Login] Issued login code", "email", identity.email, "code_hash_prefix", codeHashPrefix(hash))
	return code, nil
}

// redirectToCLILoopback sends the browser to `http://127.0.0.1:<port>/callback` with params and the
// CLI's state. The URL is built from the ticket only, never from the request.
func redirectToCLILoopback(w http.ResponseWriter, r *http.Request, ticket cliLoginTicket, params url.Values) {
	params.Set("state", ticket.State)
	target := url.URL{
		Scheme:   "http",
		Host:     net.JoinHostPort("127.0.0.1", strconv.Itoa(ticket.Port)),
		Path:     "/callback",
		RawQuery: params.Encode(),
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	http.Redirect(w, r, target.String(), http.StatusFound)
}
