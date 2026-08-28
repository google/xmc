// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package linter_test

import (
	"testing"

	"gopkg.in/yaml.v3"

	"xmanager_cloud/pkg/components/linter"
)

func TestComponentManifest_IsEnabled(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		rawYAML   string
		wantState bool
	}{
		{
			name:      "defaults to true when enabled field is omitted",
			rawYAML:   "spec: { version: \"1.0.0\" }",
			wantState: true,
		},
		{
			name:      "explicitly enabled true",
			rawYAML:   "spec: { enabled: true, version: \"1.0.0\" }",
			wantState: true,
		},
		{
			name:      "explicitly disabled false",
			rawYAML:   "spec: { enabled: false, version: \"1.0.0\" }",
			wantState: false,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var comp linter.ComponentManifest
			if err := yaml.Unmarshal([]byte(tc.rawYAML), &comp); err != nil {
				t.Fatalf("unexpected unmarshal error: %v", err)
			}
			if got := comp.IsEnabled(); got != tc.wantState {
				t.Errorf("IsEnabled() = %v, want %v", got, tc.wantState)
			}
		})
	}
}
