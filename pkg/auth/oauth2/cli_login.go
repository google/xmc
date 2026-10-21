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
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"time"

	"golang.org/x/oauth2"
	"xmanager_cloud/internal/logging"
)

const (
	cliLoginCookieName = "cli_login"
	cliLoginCookiePath = "/api/callback"
	cliLoginTicketTTL  = 5 * time.Minute
	minCLILoopbackPort = 1024
	maxCLILoopbackPort = 65535
	maxLoginHintLength = 254
)

var (
	cliStatePattern        = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
	base64URL256BitPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
)

type cliLoginRequest struct {
	// mode is cliLoginModeLoopback or cliLoginModeManual.
	mode string
	// port is the loopback listener's port; it is zero in manual mode.
	port          int
	state         string
	codeChallenge string
	loginHint     string
}

// EnableCLILogin turns on the CLI login endpoints. Call it once during initialization before serving requests.
func (a *Authenticator) EnableCLILogin(codes CLICodeStore) error {
	if codes == nil {
		return errors.New("cli login code store must not be nil")
	}
	if a.cliCodes != nil {
		return errors.New("cli login is already enabled")
	}
	a.cliCodes = codes
	return nil
}

// HandleCLILogin starts a CLI login: it records how the code reaches the CLI (its loopback port,
// or manual mode), the CLI's state and its PKCE challenge in the MAC'd cli_login cookie and
// redirects the browser to the identity provider. The callback then delivers a one-time code to
// `http://127.0.0.1:<port>/callback`, or in manual mode shows it in the browser once the user
// confirms.
func (a *Authenticator) HandleCLILogin(w http.ResponseWriter, r *http.Request) {
	if !a.checkCLIBrowserRequest(w, r) {
		return
	}
	if !headerAbsentOrEqual(r, "Sec-Fetch-Site", "none") {
		http.Error(w, "CLI login must be opened by the CLI", http.StatusForbidden)
		return
	}
	req, err := parseCLILoginRequest(r.URL.Query())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	oauthState, err := GenerateRandomString()
	if err != nil {
		http.Error(w, "Failed to generate secure random state", http.StatusInternalServerError)
		return
	}
	if err := a.setCLILoginCookie(w, r, req, oauthState, time.Now()); err != nil {
		logging.Error(r.Context(), "[CLI Login] Failed to seal cli_login ticket", "error", err)
		http.Error(w, "Failed to start CLI login", http.StatusInternalServerError)
		return
	}
	a.setOAuthStateCookie(w, r, oauthState)
	var opts []oauth2.AuthCodeOption
	if req.loginHint != "" {
		opts = []oauth2.AuthCodeOption{oauth2.SetAuthURLParam("login_hint", req.loginHint)}
	}
	http.Redirect(w, r, a.OAuthProvider.AuthCodeURL(oauthState, r, opts...), http.StatusTemporaryRedirect)
}

func (a *Authenticator) checkCLIBrowserRequest(w http.ResponseWriter, r *http.Request) bool {
	if a.cliCodes == nil {
		http.NotFound(w, r)
		return false
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return false
	}
	if !headerAbsentOrEqual(r, "Sec-Fetch-Dest", "document") {
		http.Error(w, "CLI login must be opened as a top-level page", http.StatusBadRequest)
		return false
	}
	return true
}

func headerAbsentOrEqual(r *http.Request, name, want string) bool {
	values := r.Header.Values(name)
	return len(values) == 0 || (len(values) == 1 && values[0] == want)
}

func parseCLILoginRequest(q url.Values) (cliLoginRequest, error) {
	params, err := singleValues(q, "port", "mode", "state", "code_challenge", "code_challenge_method", "login_hint")
	if err != nil {
		return cliLoginRequest{}, err
	}
	mode, port, err := parseCLIDelivery(params["port"], params["mode"])
	if err != nil {
		return cliLoginRequest{}, err
	}
	if !cliStatePattern.MatchString(params["state"]) {
		return cliLoginRequest{}, errors.New("state must be 1-128 characters of [A-Za-z0-9_-]")
	}
	if !base64URL256BitPattern.MatchString(params["code_challenge"]) {
		return cliLoginRequest{}, errors.New("code_challenge must be 43 base64url characters")
	}
	if params["code_challenge_method"] != "S256" {
		return cliLoginRequest{}, errors.New("code_challenge_method must be S256")
	}
	if err := validateLoginHint(params["login_hint"]); err != nil {
		return cliLoginRequest{}, err
	}
	return cliLoginRequest{mode: mode, port: port, state: params["state"], codeChallenge: params["code_challenge"], loginHint: params["login_hint"]}, nil
}

// parseCLIDelivery reads how the code reaches the CLI: a loopback port, or mode=manual for the user
// to paste it. Exactly one of the two must be given. It returns the mode and the port, which is
// zero in manual mode.
func parseCLIDelivery(rawPort, mode string) (string, int, error) {
	if (rawPort == "") == (mode == "") {
		return "", 0, errors.New("exactly one of port and mode=manual is required")
	}
	if mode == "" {
		port, err := parseCLILoopbackPort(rawPort)
		return cliLoginModeLoopback, port, err
	}
	if mode != cliLoginModeManual {
		return "", 0, errors.New("mode must be manual")
	}
	return cliLoginModeManual, 0, nil
}

func singleValues(q url.Values, names ...string) (map[string]string, error) {
	params := make(map[string]string, len(names))
	for _, name := range names {
		if len(q[name]) > 1 {
			return nil, fmt.Errorf("%s must not be repeated", name)
		}
		params[name] = q.Get(name)
	}
	return params, nil
}

func parseCLILoopbackPort(raw string) (int, error) {
	port, err := strconv.Atoi(raw)
	if err != nil || raw[0] < '1' || port < minCLILoopbackPort || port > maxCLILoopbackPort {
		return 0, fmt.Errorf("port must be an integer between %d and %d", minCLILoopbackPort, maxCLILoopbackPort)
	}
	return port, nil
}

func validateLoginHint(hint string) error {
	if len(hint) > maxLoginHintLength {
		return errors.New("login_hint must be at most 254 printable ASCII characters")
	}
	for i := 0; i < len(hint); i++ {
		if hint[i] < 0x20 || hint[i] > 0x7e {
			return errors.New("login_hint must be printable ASCII")
		}
	}
	return nil
}

func (a *Authenticator) setCLILoginCookie(w http.ResponseWriter, r *http.Request, req cliLoginRequest, oauthState string, now time.Time) error {
	sealed, err := a.sealCLILoginTicket(cliLoginTicket{
		Port:           req.port,
		State:          req.state,
		CodeChallenge:  req.codeChallenge,
		Mode:           req.mode,
		OAuthStateHash: hashOAuthState(oauthState),
		ExpiresAt:      now.Add(cliLoginTicketTTL).Unix(),
	})
	if err != nil {
		return err
	}
	a.writeCLILoginCookie(w, r, sealed, int(cliLoginTicketTTL/time.Second))
	return nil
}

func (a *Authenticator) clearCLILoginCookie(w http.ResponseWriter, r *http.Request) {
	a.writeCLILoginCookie(w, r, "", -1)
}

func (a *Authenticator) writeCLILoginCookie(w http.ResponseWriter, r *http.Request, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     cliLoginCookieName,
		Value:    value,
		Path:     cliLoginCookiePath,
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   a.isCookieSecure(r),
		SameSite: http.SameSiteLaxMode,
	})
}
