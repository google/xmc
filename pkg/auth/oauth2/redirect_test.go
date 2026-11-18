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
	"encoding/base64"
	"strings"
	"testing"
)

func TestIsLocalReturnPath(t *testing.T) {
	for _, tc := range []struct {
		name string
		path string
		want bool
	}{
		{name: "experiment", path: "/experiments/1", want: true},
		{name: "query and fragment", path: "/experiments/1?filter=a%2Fb&tab=logs#work-units", want: true},
		{name: "encoded path", path: "/experiments/1?name=caf%C3%A9", want: true},
		{name: "root", path: "/", want: true},
		{name: "maximum length", path: "/" + strings.Repeat("a", maxReturnPathBytes-1), want: true},
		{name: "empty"},
		{name: "absolute URL", path: "https://example.com/experiments/1"},
		{name: "scheme-relative URL", path: "//example.com/experiments/1"},
		{name: "triple-slash path", path: "///example.com/experiments/1"},
		{name: "quadruple-slash path", path: "////example.com/experiments/1"},
		{name: "encoded multi-slash path", path: "/%2f/example.com/experiments/1"},
		{name: "relative path", path: "experiments/1"},
		{name: "script URL", path: "javascript:alert(1)"},
		{name: "backslash", path: "/\\example.com"},
		{name: "encoded backslash", path: "/%5cexample.com"},
		{name: "encoded authority", path: "/%2fexample.com"},
		{name: "encoded leading slash", path: "%2f@example.com"},
		{name: "control character", path: "/\texample.com"},
		{name: "encoded control character", path: "/%0d%0aLocation:evil"},
		{name: "invalid escape", path: "/%zz"},
		{name: "oversized", path: "/" + strings.Repeat("a", maxReturnPathBytes)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := isLocalReturnPath(tc.path); got != tc.want {
				t.Errorf("isLocalReturnPath(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

func TestStateWithReturnPath(t *testing.T) {
	const nonce = "random-nonce"
	for _, path := range []string{"", "https://example.com", "/experiments/1?filter=a%2Fb#logs"} {
		t.Run(path, func(t *testing.T) {
			got := stateWithReturnPath(nonce, path)
			want := nonce
			if strings.HasPrefix(path, "/") {
				want += "." + base64.RawURLEncoding.EncodeToString([]byte(path))
			}
			if got != want {
				t.Errorf("stateWithReturnPath() = %q, want %q", got, want)
			}
		})
	}
}

func TestReturnPathFromState(t *testing.T) {
	const path = "/experiments/1?filter=a%2Fb#logs"
	for _, tc := range []struct {
		name  string
		state string
		want  string
	}{
		{name: "return path", state: stateWithReturnPath("nonce", path), want: path},
		{name: "nonce only", state: "nonce", want: "/experiments"},
		{name: "invalid base64", state: "nonce.!", want: "/experiments"},
		{name: "empty path", state: "nonce.", want: "/experiments"},
		{name: "external path", state: "nonce." + base64.RawURLEncoding.EncodeToString([]byte("//example.com")), want: "/experiments"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := returnPathFromState(tc.state); got != tc.want {
				t.Errorf("returnPathFromState() = %q, want %q", got, tc.want)
			}
		})
	}
}
