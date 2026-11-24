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

package buildinfo

import "testing"

func TestVersionAndStampedVersion(t *testing.T) {
	orig := version
	t.Cleanup(func() {
		version = orig
	})

	tests := []struct {
		name        string
		raw         string
		wantStamped string
		wantVersion string
	}{
		{name: "semver_release", raw: "1.1.0", wantStamped: "1.1.0", wantVersion: "1.1.0"},
		{name: "between_tags", raw: "1.1.0-14-g0aef3d886", wantStamped: "1.1.0-14-g0aef3d886", wantVersion: "1.1.0-14-g0aef3d886"},
		{name: "trims_whitespace", raw: " 1.1.0 ", wantStamped: "1.1.0", wantVersion: "1.1.0"},
		{name: "empty_stamp", raw: "", wantStamped: "", wantVersion: FallbackVersion},
		{name: "redacted_stamp", raw: "redacted", wantStamped: "", wantVersion: FallbackVersion},
		{name: "unstamped_placeholder", raw: "{STABLE_APP_VERSION}", wantStamped: "", wantVersion: FallbackVersion},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			version = tc.raw
			if got := StampedVersion(); got != tc.wantStamped {
				t.Errorf("StampedVersion() with %q = %q, want %q", tc.raw, got, tc.wantStamped)
			}
			if got := Version(); got != tc.wantVersion {
				t.Errorf("Version() with %q = %q, want %q", tc.raw, got, tc.wantVersion)
			}
		})
	}
}
