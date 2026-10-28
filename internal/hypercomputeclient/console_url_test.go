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

package hypercomputeclient

import "testing"

func TestEnsureProfilesTab(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "empty url returns empty string", raw: "", want: ""},
		{
			name: "url without query params appends tab=profiles",
			raw:  "https://console.cloud.google.com/kubernetes/aiml/run/us-central1/run-1",
			want: "https://console.cloud.google.com/kubernetes/aiml/run/us-central1/run-1?tab=profiles",
		},
		{
			name: "url with project param appends tab=profiles",
			raw:  "https://console.cloud.google.com/kubernetes/aiml/run/us-central1/run-1?project=test-project",
			want: "https://console.cloud.google.com/kubernetes/aiml/run/us-central1/run-1?project=test-project&tab=profiles",
		},
		{
			name: "url with multiple query params preserves params and appends tab=profiles",
			raw:  "https://console.cloud.google.com/kubernetes/aiml/run/us-central1/run-1?authuser=1&project=test-project",
			want: "https://console.cloud.google.com/kubernetes/aiml/run/us-central1/run-1?authuser=1&project=test-project&tab=profiles",
		},
		{
			name: "url already containing tab=profiles is idempotent",
			raw:  "https://console.cloud.google.com/kubernetes/aiml/run/us-central1/run-1?project=test-project&tab=profiles",
			want: "https://console.cloud.google.com/kubernetes/aiml/run/us-central1/run-1?project=test-project&tab=profiles",
		},
		{
			name: "url with different tab replaces tab with profiles",
			raw:  "https://console.cloud.google.com/kubernetes/aiml/run/us-central1/run-1?project=test-project&tab=overview",
			want: "https://console.cloud.google.com/kubernetes/aiml/run/us-central1/run-1?project=test-project&tab=profiles",
		},
		{
			name: "url with fragment preserves fragment",
			raw:  "https://console.cloud.google.com/kubernetes/aiml/run/us-central1/run-1?project=p#summary",
			want: "https://console.cloud.google.com/kubernetes/aiml/run/us-central1/run-1?project=p&tab=profiles#summary",
		},
		{name: "malformed url returns raw input unchanged", raw: "http://invalid domain with spaces", want: "http://invalid domain with spaces"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := EnsureProfilesTab(tc.raw); got != tc.want {
				t.Errorf("EnsureProfilesTab(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

func TestConsoleURL(t *testing.T) {
	tests := []struct {
		name     string
		location string
		mlRunID  string
		project  string
		want     string
	}{
		{
			name: "valid parameters construct url with profiles tab", location: "us-central1", mlRunID: "xmc-test-run-123",
			project: "test-project", want: "https://console.cloud.google.com/kubernetes/aiml/run/us-central1/xmc-test-run-123?project=test-project&tab=profiles",
		},
		{name: "empty location returns empty string", location: "", mlRunID: "xmc-test-run-123", project: "test-project", want: ""},
		{name: "empty mlRunID returns empty string", location: "us-central1", mlRunID: "", project: "test-project", want: ""},
		{name: "empty project returns empty string", location: "us-central1", mlRunID: "xmc-test-run-123", project: "", want: ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ConsoleURL(tc.location, tc.mlRunID, tc.project); got != tc.want {
				t.Errorf("ConsoleURL(%q, %q, %q) = %q, want %q", tc.location, tc.mlRunID, tc.project, got, tc.want)
			}
		})
	}
}

func TestMLRunURL(t *testing.T) {
	tests := []struct {
		name    string
		mlRun   map[string]any
		mlRunID string
		want    string
	}{
		{
			name: "nil resource falls back to console url", mlRun: nil, mlRunID: "run-1",
			want: "https://console.cloud.google.com/kubernetes/aiml/run/us-central1/run-1?project=test-project&tab=profiles",
		},
		{
			name: "resource without url falls back to console url", mlRun: map[string]any{"other": "field"}, mlRunID: "run-2",
			want: "https://console.cloud.google.com/kubernetes/aiml/run/us-central1/run-2?project=test-project&tab=profiles",
		},
		{
			name: "resource with empty url falls back to console url", mlRun: map[string]any{"url": ""}, mlRunID: "run-3",
			want: "https://console.cloud.google.com/kubernetes/aiml/run/us-central1/run-3?project=test-project&tab=profiles",
		},
		{
			name: "resource url is used with profiles tab", mlRun: map[string]any{"url": "https://console.cloud.google.com/aiml/run?project=p"},
			mlRunID: "run-4", want: "https://console.cloud.google.com/aiml/run?project=p&tab=profiles",
		},
		{
			name: "operation metadata url is not an MLRun field", mlRun: map[string]any{"metadata": map[string]any{"url": "https://other"}},
			mlRunID: "run-5", want: "https://console.cloud.google.com/kubernetes/aiml/run/us-central1/run-5?project=test-project&tab=profiles",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := MLRunURL(tc.mlRun, "us-central1", tc.mlRunID, "test-project"); got != tc.want {
				t.Errorf("MLRunURL(%v, %q) = %q, want %q", tc.mlRun, tc.mlRunID, got, tc.want)
			}
		})
	}
}
