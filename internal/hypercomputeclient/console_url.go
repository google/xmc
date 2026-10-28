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

import (
	"fmt"
	"net/url"
)

// EnsureProfilesTab guarantees that an MLRun URL targets the profiles view in
// Google Cloud Console / Diagon. The control plane API returns the base URL of the
// run (defaulting to the overview tab), so the tab query parameter is stamped
// explicitly. Pre-existing tab parameters are replaced, other query parameters are
// preserved, and malformed URLs fall back to the original string.
func EnsureProfilesTab(rawURL string) string {
	if rawURL == "" {
		return ""
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	q := parsed.Query()
	q.Set("tab", "profiles")
	parsed.RawQuery = q.Encode()
	return parsed.String()
}

// ConsoleURL constructs the ML Diagnostics run detail URL in Google Cloud Console
// with the profiles tab preselected. Returns "" when any input is empty, because a
// partial URL would not resolve to a run.
func ConsoleURL(location, mlRunID, project string) string {
	if location == "" || mlRunID == "" || project == "" {
		return ""
	}
	return fmt.Sprintf("https://console.cloud.google.com/kubernetes/aiml/run/%s/%s?project=%s&tab=profiles", location, mlRunID, project)
}

// MLRunURL returns the URL to surface for an MLRun: the API-provided top-level
// "url" field of the MLRun resource (with the profiles tab preselected) when
// present, otherwise the deterministic ConsoleURL for the id.
func MLRunURL(mlRun map[string]any, location, mlRunID, project string) string {
	if u, ok := mlRun["url"].(string); ok && u != "" {
		return EnsureProfilesTab(u)
	}
	return ConsoleURL(location, mlRunID, project)
}
