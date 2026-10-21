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
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"xmanager_cloud/internal/logging"
)

const (
	cliLoginCodeBytes    = 32
	cliLoopbackCodeTTL   = 60 * time.Second
	cliErrorAccessDenied = "access_denied"
	cliErrorServerError  = "server_error"
)

// CLILoginCode is a one-time CLI login code issued by the callback and redeemed by the token endpoint.
type CLILoginCode struct {
	Hash                       []byte
	Email, Name, CodeChallenge string
	ExpiresAt                  time.Time
}

// CLICodeStore persists one-time CLI login codes.
type CLICodeStore interface {
	Save(ctx context.Context, code CLILoginCode) error
}

// takeCLILoginTicket deletes any cli_login cookie and returns its ticket when authentic, unexpired, and bound to oauthState.
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
	if errors.Is(err, errExpiredCLILoginTicket) {
		logging.Info(r.Context(), "[CLI Login] Ignoring expired cli_login cookie")
		return cliLoginTicket{}, false
	}
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

func (a *Authenticator) issueCLILoginCode(ctx context.Context, identity loginIdentity, codeChallenge string, now time.Time) (string, error) {
	code, hash, err := newCLILoginCode()
	if err != nil {
		return "", err
	}
	record := CLILoginCode{Hash: hash, Email: identity.email, Name: identity.name, CodeChallenge: codeChallenge, ExpiresAt: now.Add(cliLoopbackCodeTTL)}
	if err := a.cliCodes.Save(ctx, record); err != nil {
		return "", err
	}
	logging.Info(ctx, "[CLI Login] Issued login code", "email", identity.email, "code_hash_prefix", codeHashPrefix(hash))
	return code, nil
}

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

func codeHashPrefix(hash []byte) string {
	return hex.EncodeToString(hash[:min(len(hash), 4)])
}

func redirectToCLILoopback(w http.ResponseWriter, r *http.Request, ticket cliLoginTicket, params url.Values) {
	params.Set("state", ticket.State)
	target := url.URL{Scheme: "http", Host: net.JoinHostPort("127.0.0.1", strconv.Itoa(ticket.Port)), Path: "/callback", RawQuery: params.Encode()}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	http.Redirect(w, r, target.String(), http.StatusFound)
}
