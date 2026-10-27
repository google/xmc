// Copyright 2025 Google LLC
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

func TestSanitizeIdentifier(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"my-run-123", "my-run-123"},
		{"My_Run-123!", "my-run-123"},
		{"--run--name--", "run-name"},
		{"123#ABC", "123-abc"},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			if got := SanitizeIdentifier(tc.input); got != tc.want {
				t.Errorf("SanitizeIdentifier(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}
