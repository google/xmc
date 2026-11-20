// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import "testing"

func TestResolveBuildVersion(t *testing.T) {
	tests := []struct {
		name    string
		stamped string
		want    string
	}{
		{name: "semver_release", stamped: "1.0.0", want: "1.0.0"},
		{name: "nightly_tag", stamped: "nightly-2026-11-13.123", want: "nightly-2026-11-13.123"},
		{name: "trims_whitespace", stamped: " 1.0.0 ", want: "1.0.0"},
		{name: "ignores_unstamped_placeholder", stamped: "{STABLE_APP_VERSION}", want: ""},
		{name: "ignores_redacted_stamp", stamped: "redacted", want: ""},
		{name: "empty_stamp", stamped: "", want: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveBuildVersion(tc.stamped); got != tc.want {
				t.Errorf("resolveBuildVersion(%q) = %q, want %q", tc.stamped, got, tc.want)
			}
		})
	}
}

func TestLoadPopulatesAppVersion(t *testing.T) {
	origPort := port
	origSA := xmanagerServiceAccount
	origRedis := redisAddr
	origBuildVersion := buildVersion
	t.Cleanup(func() {
		port = origPort
		xmanagerServiceAccount = origSA
		redisAddr = origRedis
		buildVersion = origBuildVersion
	})

	port = "8080"
	xmanagerServiceAccount = "xmc@example.iam.gserviceaccount.com"
	redisAddr = "redis:6379"
	buildVersion = "1.0.0"

	cfg := Load()
	if cfg.RuntimeConfig.AppVersion != "1.0.0" {
		t.Errorf("Load().RuntimeConfig.AppVersion = %q, want %q", cfg.RuntimeConfig.AppVersion, "1.0.0")
	}
}
