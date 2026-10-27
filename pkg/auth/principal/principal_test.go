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

package principal

import "testing"

func TestCanonicalEmail(t *testing.T) {
	testCases := []struct {
		name    string
		email   string
		want    string
		wantErr bool
	}{
		{name: "already canonical", email: "user@example.com", want: "user@example.com"},
		{name: "mixed case", email: "First.Last@Example.COM", want: "first.last@example.com"},
		{name: "apostrophe", email: "O'Brien@Example.com", want: "o'brien@example.com"},
		{name: "service account", email: "SA@project.iam.gserviceaccount.com", want: "sa@project.iam.gserviceaccount.com"},
		{name: "kelvin sign", email: "\u212Aate@corp.com", wantErr: true},
		{name: "non-ASCII letter", email: "jos\u00e9@corp.com", wantErr: true},
		{name: "leading whitespace", email: " a@corp.com", wantErr: true},
		{name: "trailing whitespace", email: "a@corp.com ", wantErr: true},
		{name: "display name", email: "Eve <a@corp.com>", wantErr: true},
		{name: "angle brackets only", email: "<a@corp.com>", wantErr: true},
		{name: "comment", email: "a@corp.com (Eve)", wantErr: true},
		{name: "quoted local part", email: `"x@evil.com"@corp.com`, wantErr: true},
		{name: "unsupported character", email: "a|b@corp.com", wantErr: true},
		{name: "dotless domain", email: "a@localhost", wantErr: true},
		{name: "no at sign", email: "1029384756", wantErr: true},
		{name: "empty", email: "", wantErr: true},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := CanonicalEmail(tc.email)
			if (err != nil) != tc.wantErr {
				t.Fatalf("CanonicalEmail(%q) error = %v, wantErr %v", tc.email, err, tc.wantErr)
			}
			if got != tc.want {
				t.Errorf("CanonicalEmail(%q) = %q, want %q", tc.email, got, tc.want)
			}
		})
	}
}

func TestNormalize(t *testing.T) {
	testCases := []struct {
		name      string
		principal string
		want      string
	}{
		{"already canonical", "user@example.com", "user@example.com"},
		{"mixed case", "First.Last@Example.COM", "first.last@example.com"},
		{"surrounding whitespace", " \tuser@example.com\n", "user@example.com"},
		{"domain", " Example.com ", "example.com"},
		{"non-ASCII is not folded", "\u212Aate@corp.com", "\u212Aate@corp.com"},
		{"empty", "", ""},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Normalize(tc.principal); got != tc.want {
				t.Errorf("Normalize(%q) = %q, want %q", tc.principal, got, tc.want)
			}
		})
	}
}
