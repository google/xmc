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

func TestConfirmationPageEscapes(t *testing.T) {
	w := httptest.NewRecorder()
	writeCLITemplatePage(w, cliConfirmationPage, cliConfirmationPageData{Email: "<b>x</b>@mycompany.com", Confirmation: `"><script>alert(1)</script>`})
	got, body := readCLIPage(w.Result())

	if diff := cmp.Diff(cliPage(http.StatusOK, "Sign in to the XMC command-line tool as &lt;b&gt;x&lt;/b&gt;@mycompany.com?"), got); diff != "" {
		t.Errorf("confirmation page mismatch (-want +got):\n%s", diff)
	}
	if want := `value="&#34;&gt;&lt;script&gt;alert(1)&lt;/script&gt;"`; !strings.Contains(body, want) {
		t.Errorf("confirmation page does not escape the confirmation as %s:\n%s", want, body)
	}
}

func TestCodePage(t *testing.T) {
	w := httptest.NewRecorder()
	writeCLITemplatePage(w, cliCodePage, cliTestCode)
	got, body := readCLIPage(w.Result())
	if diff := cmp.Diff(cliPage(http.StatusOK, "Your sign-in code"), got); diff != "" {
		t.Errorf("code page mismatch (-want +got):\n%s", diff)
	}
	if match := cliCodePagePattern.FindStringSubmatch(body); match == nil || match[1] != cliTestCode {
		t.Errorf("code page does not show %q:\n%s", cliTestCode, body)
	}
}

func TestWriteCLIManualErrorPages(t *testing.T) {
	tests := []struct {
		name  string
		write func(http.ResponseWriter)
		want  cliPageResult
	}{
		{name: "access denied", write: func(w http.ResponseWriter) { writeCLIManualError(w, cliErrorAccessDenied) }, want: cliPage(http.StatusForbidden, "Sign-in failed")},
		{name: "server error", write: func(w http.ResponseWriter) { writeCLIManualError(w, cliErrorServerError) }, want: cliPage(http.StatusInternalServerError, "Sign-in failed")},
		{name: "expired", write: writeCLIManualExpired, want: cliPage(http.StatusBadRequest, "Sign-in expired")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			tt.write(w)
			got, _ := readCLIPage(w.Result())
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("error page mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
