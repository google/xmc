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

package config

import "testing"

func TestConfig_RegisterConfigs_Defaults(t *testing.T) {
	var c Config
	c.registerConfigs()

	if c.ProjectID != "xmc-project" {
		t.Errorf("c.ProjectID = %q, want %q", c.ProjectID, "xmc-project")
	}
	if c.QuotaProjectID != "xmc-project" {
		t.Errorf("c.QuotaProjectID = %q, want %q", c.QuotaProjectID, "xmc-project")
	}
}

func TestConfig_RegisterConfigs_EnvOverride(t *testing.T) {
	t.Setenv("ess_gcp_project_id", "custom-proj")
	t.Setenv("ess_quota_project_id", "custom-quota-proj")

	var c Config
	c.registerConfigs()

	if c.ProjectID != "custom-proj" {
		t.Errorf("c.ProjectID = %q, want %q", c.ProjectID, "custom-proj")
	}
	if c.QuotaProjectID != "custom-quota-proj" {
		t.Errorf("c.QuotaProjectID = %q, want %q", c.QuotaProjectID, "custom-quota-proj")
	}
}
