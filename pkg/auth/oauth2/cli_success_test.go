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
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// successResult is the comparable outcome of a request to /api/cli/success.
type successResult struct {
	Status                                         int
	ContentType, CacheControl, ReferrerPolicy, CSP string
	// Title is the page heading; the error parameter's value must never appear in the page.
	Title string
}

func TestHandleCLISuccess(t *testing.T) {
	page := func(title string) successResult {
		return successResult{http.StatusOK, "text/html; charset=utf-8", "no-store", "no-referrer", "frame-ancestors 'none'; form-action 'self'", title}
	}
	tests := []struct {
		name      string
		method    string
		query     string
		fetchDest string
		disabled  bool
		want      successResult
	}{
		{name: "success", fetchDest: "document", want: page("Signed in")},
		{name: "error is not reflected", query: "?error=%3Cscript%3Ealert(1)%3C%2Fscript%3E", want: page("Sign-in failed")},
		{name: "disabled", disabled: true, want: successResult{Status: http.StatusNotFound}},
		{name: "POST", method: http.MethodPost, want: successResult{Status: http.StatusMethodNotAllowed}},
		{name: "embedded in an iframe", fetchDest: "iframe", want: successResult{Status: http.StatusBadRequest}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, _, _ := newCLITestAuthenticator(t, baseLoginConfig())
			if tt.disabled {
				a.cliCodes = nil
			}
			req := httptest.NewRequest(methodOrGet(tt.method), "https://xmc.example.com/api/cli/success"+tt.query, nil)
			if tt.fetchDest != "" {
				req.Header.Set("Sec-Fetch-Dest", tt.fetchDest)
			}
			w := httptest.NewRecorder()
			a.HandleCLISuccess(w, req)
			got := successResult{Status: w.Code}
			if w.Code == http.StatusOK {
				body := w.Body.String()
				got = successResult{w.Code, w.Header().Get("Content-Type"), w.Header().Get("Cache-Control"), w.Header().Get("Referrer-Policy"), w.Header().Get("Content-Security-Policy"), between(body, "<h1>", "</h1>")}
				if strings.Contains(body, "script") {
					t.Errorf("body reflects the error parameter: %q", body)
				}
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("HandleCLISuccess() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func methodOrGet(method string) string {
	if method == "" {
		return http.MethodGet
	}
	return method
}

// between returns the text of s between the first start and the following end.
func between(s, start, end string) string {
	_, after, _ := strings.Cut(s, start)
	before, _, _ := strings.Cut(after, end)
	return before
}
