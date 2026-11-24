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

func TestLoadPopulatesAppVersion(t *testing.T) {
	origPort := port
	origSA := xmanagerServiceAccount
	origRedis := redisAddr
	origStampedVersion := stampedVersion
	t.Cleanup(func() {
		port = origPort
		xmanagerServiceAccount = origSA
		redisAddr = origRedis
		stampedVersion = origStampedVersion
	})

	port = "8080"
	xmanagerServiceAccount = "xmc@example.iam.gserviceaccount.com"
	redisAddr = "redis:6379"
	stampedVersion = func() string { return "1.1.0" }

	cfg := Load()
	if cfg.RuntimeConfig.AppVersion != "1.1.0" {
		t.Errorf("Load().RuntimeConfig.AppVersion = %q, want %q", cfg.RuntimeConfig.AppVersion, "1.1.0")
	}
}
