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

import (
	"os"
	"testing"
)

func TestConfig_RegisterConfigs(t *testing.T) {
	tests := []struct {
		name               string
		env                map[string]string
		wantProjectID      string
		wantQuotaProjectID string
	}{
		{
			name:               "defaults to empty when env vars are unset",
			env:                nil,
			wantProjectID:      "",
			wantQuotaProjectID: "",
		},
		{
			name: "loads explicit environment overrides",
			env: map[string]string{
				"ess_gcp_project_id":   "custom-proj",
				"ess_quota_project_id": "custom-quota-proj",
			},
			wantProjectID:      "custom-proj",
			wantQuotaProjectID: "custom-quota-proj",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for _, k := range []string{"ess_gcp_project_id", "ess_quota_project_id"} {
				t.Setenv(k, "")
				if err := os.Unsetenv(k); err != nil {
					t.Fatalf("os.Unsetenv(%q) failed: %v", k, err)
				}
			}
			for k, v := range tc.env {
				t.Setenv(k, v)
			}

			var c Config
			c.registerConfigs()

			if c.ProjectID != tc.wantProjectID {
				t.Errorf("c.ProjectID = %q, want %q", c.ProjectID, tc.wantProjectID)
			}
			if c.QuotaProjectID != tc.wantQuotaProjectID {
				t.Errorf("c.QuotaProjectID = %q, want %q", c.QuotaProjectID, tc.wantQuotaProjectID)
			}
		})
	}
}
