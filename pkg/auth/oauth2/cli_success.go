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
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	fmt.Fprintf(w, cliResultPage, title, message)
}
