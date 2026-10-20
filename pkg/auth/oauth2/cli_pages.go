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
	"fmt"
	"html/template"
	"net/http"
)

// cliResultPage is the static page shown at the end of a CLI login. It takes only constant
// strings, so no request data is ever reflected into it.
const cliResultPage = `<!DOCTYPE html>
<html lang="en">
<head><meta charset="utf-8"><title>XManager CLI login</title></head>
<body style="font-family: sans-serif; margin: 4em auto; max-width: 32em; text-align: center">
<h1>%s</h1>
<p>%s</p>
</body>
</html>
`

// cliManualPageStyle is the inline style of the manual-mode pages. They use no script, so they
// work under the deployment's Content-Security-Policy.
const cliManualPageStyle = "font-family: sans-serif; margin: 4em auto; max-width: 36em"

// cliConfirmationPage asks the signed-in user to confirm a manual CLI login. html/template escapes
// the email and the sealed confirmation.
var cliConfirmationPage = template.Must(template.New("confirmation").Parse(`<!DOCTYPE html>
<html lang="en">
<head><meta charset="utf-8"><title>XManager CLI login</title></head>
<body style="` + cliManualPageStyle + `">
<h1>Sign in to the XMC command-line tool as {{.Email}}?</h1>
<p><strong>Only continue if you started <code>xmc login --no-browser</code> yourself, in your own terminal, just now.</strong>
If someone sent you this link or asked you to open it, select Cancel.</p>
<p>The next page shows a sign-in code. Never share the code with anyone.</p>
<form method="post" action="/api/cli/code">
<input type="hidden" name="confirmation" value="{{.Confirmation}}">
<button type="submit" name="action" value="continue">Continue</button>
<button type="submit" name="action" value="cancel">Cancel</button>
</form>
</body>
</html>
`))

// cliCodePage shows a one-time code for the user to paste into the CLI.
var cliCodePage = template.Must(template.New("code").Parse(`<!DOCTYPE html>
<html lang="en">
<head><meta charset="utf-8"><title>XManager CLI login</title></head>
<body style="` + cliManualPageStyle + `">
<h1>Your sign-in code</h1>
<p>Copy this code and paste it into the terminal where you ran <code>xmc login --no-browser</code>.
It works once and expires in 2 minutes.</p>
<p><code style="display: block; font-size: 1.6em; padding: 0.6em; border: 1px solid #888; user-select: all; word-break: break-all">{{.}}</code></p>
<p><strong>Never share this code with anyone or paste it anywhere else.</strong>
Whoever enters it into their terminal is signed in to XManager as you. XManager never asks for it by email, chat or phone.</p>
</body>
</html>
`))

type cliConfirmationPageData struct {
	Email        string
	Confirmation string
}

// HandleCLISuccess serves the page the CLI sends the browser to once its loopback listener has the
// result, so the one-time code leaves the address bar. Any `error` parameter selects the failure
// variant; its value is not displayed.
func (a *Authenticator) HandleCLISuccess(w http.ResponseWriter, r *http.Request) {
	if !a.checkCLIBrowserRequest(w, r) {
		return
	}
	title, message := "Signed in", "You can close this tab and return to the terminal."
	if r.URL.Query().Has("error") {
		title, message = "Sign-in failed", "Return to the terminal for details."
	}
	writeCLIResultPage(w, http.StatusOK, title, message)
}

// writeCLIResultPage writes cliResultPage with a constant title and message.
func writeCLIResultPage(w http.ResponseWriter, status int, title, message string) {
	writeCLIPageHeaders(w, status)
	fmt.Fprintf(w, cliResultPage, title, message)
}

// writeCLITemplatePage writes page, executed with data, as a CLI login HTML page.
func writeCLITemplatePage(w http.ResponseWriter, page *template.Template, data any) {
	writeCLIPageHeaders(w, http.StatusOK)
	// The status is already sent; an execution error only means the client went away.
	_ = page.Execute(w, data)
}

// writeCLIManualError shows a failed manual login in the browser, where the user is: there is no
// loopback listener to report it to. The page names neither the user nor the cause.
func writeCLIManualError(w http.ResponseWriter, errCode string) {
	if errCode == cliErrorAccessDenied {
		writeCLIResultPage(w, http.StatusForbidden, "Sign-in failed",
			"Sign-in was cancelled, or this account may not use this XManager deployment. Run <code>xmc login --no-browser</code> again to retry.")
		return
	}
	writeCLIResultPage(w, http.StatusInternalServerError, "Sign-in failed",
		"Something went wrong. Run <code>xmc login --no-browser</code> again to retry.")
}

// writeCLIManualExpired rejects a confirmation that is forged, expired or already used.
func writeCLIManualExpired(w http.ResponseWriter) {
	writeCLIResultPage(w, http.StatusBadRequest, "Sign-in expired",
		"This page has expired or was already used. Run <code>xmc login --no-browser</code> again to get a new code.")
}

// writeCLIPageHeaders writes the status and the headers shared by the CLI login HTML pages: they
// are never cached, never leak the URL, which may carry an authorization code, as a referrer, and
// cannot be framed or post forms elsewhere. The added CSP is enforced alongside the global one.
func writeCLIPageHeaders(w http.ResponseWriter, status int) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Add("Content-Security-Policy", "frame-ancestors 'none'; form-action 'self'")
	w.WriteHeader(status)
}
