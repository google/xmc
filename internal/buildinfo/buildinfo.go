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

// Package buildinfo exposes the Bazel-stamped application version to XMC Go binaries.
package buildinfo

import "strings"

// FallbackVersion is returned by Version when a binary was built without
// Bazel STABLE_APP_VERSION stamping (e.g., go build, go test, or --nostamp).
const FallbackVersion = "0.0.0-unknown"

// version is injected at link time via Bazel x_defs:
// "xmanager_cloud/internal/buildinfo.version": "{STABLE_APP_VERSION}".
var version string

// StampedVersion returns the stamped application version, or "" when the binary
// was built without a valid STABLE_APP_VERSION stamp.
func StampedVersion() string {
	return resolve(version)
}

// Version returns the stamped application version, falling back to
// FallbackVersion when the binary is unstamped.
func Version() string {
	if v := resolve(version); v != "" {
		return v
	}
	return FallbackVersion
}

func resolve(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || trimmed == "redacted" || trimmed == "{STABLE_APP_VERSION}" {
		return ""
	}
	return trimmed
}
