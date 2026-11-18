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
	"net/url"
	"strings"
	"unicode"
)

// Bound the path so the base64-encoded state fits in the `oauth_state` cookie.
const maxReturnPathBytes = 2048

func stateWithReturnPath(nonce, returnPath string) string {
	if !isLocalReturnPath(returnPath) {
		return nonce
	}
	return nonce + "." + base64.RawURLEncoding.EncodeToString([]byte(returnPath))
}

// The caller must validate the full state against the `oauth_state` cookie first.
func returnPathFromState(state string) string {
	_, encodedPath, _ := strings.Cut(state, ".")
	path, err := base64.RawURLEncoding.DecodeString(encodedPath)
	if err != nil || !isLocalReturnPath(string(path)) {
		return "/experiments"
	}
	return string(path)
}

func isLocalReturnPath(path string) bool {
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") {
		return false
	}
	if len(path) > maxReturnPathBytes || strings.Contains(path, "\\") || strings.ContainsFunc(path, unicode.IsControl) {
		return false
	}
	u, err := url.Parse(path)
	if err != nil || u.IsAbs() || u.Host != "" || u.User != nil {
		return false
	}
	return strings.HasPrefix(u.Path, "/") && !strings.HasPrefix(u.Path, "//") &&
		!strings.Contains(u.Path, "\\") && !strings.ContainsFunc(u.Path, unicode.IsControl)
}
