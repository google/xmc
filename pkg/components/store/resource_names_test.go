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

package store_test

import (
	"strings"
	"testing"

	"xmanager_cloud/pkg/components/store"
)

func TestCanonicalComponentName(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "plain identifier",
			input: "my-component",
			want:  "my-component",
		},
		{
			name:  "with components prefix",
			input: "components/my-component",
			want:  "my-component",
		},
		{
			name:  "with leading and trailing slashes",
			input: "/components/my-component/",
			want:  "my-component",
		},
		{
			name:  "with whitespace",
			input: "  components/my-component/  ",
			want:  "my-component",
		},
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
		{
			name:  "whitespace only",
			input: "   ",
			want:  "",
		},
		{
			name:  "slashes only",
			input: "///",
			want:  "",
		},
		{
			name:  "collection name only",
			input: "components",
			want:  "components",
		},
		{
			name:  "collection name with trailing slash",
			input: "components/",
			want:  "components",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := store.CanonicalComponentName(tt.input)
			if got != tt.want {
				t.Errorf("CanonicalComponentName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestFullComponentName(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "canonical identifier",
			input: "jobset-controller",
			want:  "components/jobset-controller",
		},
		{
			name:  "already full name",
			input: "components/jobset-controller",
			want:  "components/jobset-controller",
		},
		{
			name:  "with slashes and whitespace",
			input: " /components/jobset-controller/ ",
			want:  "components/jobset-controller",
		},
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
		{
			name:  "whitespace only",
			input: "   ",
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := store.FullComponentName(tt.input)
			if got != tt.want {
				t.Errorf("FullComponentName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestIsValidComponentName(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{name: "valid standard name", input: "kueue-operator", want: true},
		{name: "valid single char", input: "a", want: true},
		{name: "valid single digit", input: "1", want: true},
		{name: "valid alphanumeric with hyphens", input: "comp-1-v2", want: true},
		{name: "valid max 63 chars", input: strings.Repeat("a", 63), want: true},
		{name: "invalid empty string", input: "", want: false},
		{name: "invalid exceeds 63 chars", input: strings.Repeat("a", 64), want: false},
		{name: "invalid leading hyphen", input: "-invalid", want: false},
		{name: "invalid trailing hyphen", input: "invalid-", want: false},
		{name: "invalid uppercase", input: "InvalidName", want: false},
		{name: "invalid underscore", input: "invalid_name", want: false},
		{name: "invalid dot", input: "invalid.name", want: false},
		{name: "invalid slash", input: "invalid/name", want: false},
		{name: "invalid special chars", input: "comp@123", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := store.IsValidComponentName(tt.input)
			if got != tt.want {
				t.Errorf("IsValidComponentName(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}
