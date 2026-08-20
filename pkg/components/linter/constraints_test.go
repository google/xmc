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

	"xmanager_cloud/pkg/components/linter"
)

func TestMatchConstraint_Comprehensive(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		version    string
		constraint string
		wantMatch  bool
		wantErr    bool
	}{
		// Basic comparisons
		{name: "exact match", version: "1.2.3", constraint: "1.2.3", wantMatch: true},
		{name: "exact mismatch", version: "1.2.4", constraint: "1.2.3", wantMatch: false},
		{name: "equal prefix ==", version: "1.2.3", constraint: "==1.2.3", wantMatch: true},
		{name: "equal prefix =", version: "1.2.3", constraint: "=1.2.3", wantMatch: true},
		{name: "greater than or equal match", version: "1.2.3", constraint: ">=1.2.0", wantMatch: true},
		{name: "greater than or equal boundary", version: "1.2.0", constraint: ">=1.2.0", wantMatch: true},
		{name: "greater than or equal fail", version: "1.1.9", constraint: ">=1.2.0", wantMatch: false},
		{name: "greater than match", version: "1.2.1", constraint: ">1.2.0", wantMatch: true},
		{name: "greater than boundary fail", version: "1.2.0", constraint: ">1.2.0", wantMatch: false},
		{name: "less than or equal match", version: "2.0.0", constraint: "<=2.0.0", wantMatch: true},
		{name: "less than or equal fail", version: "2.0.1", constraint: "<=2.0.0", wantMatch: false},
		{name: "less than match", version: "1.9.9", constraint: "<2.0.0", wantMatch: true},
		{name: "less than boundary fail", version: "2.0.0", constraint: "<2.0.0", wantMatch: false},

		// Caret ranges (^)
		{name: "caret standard major > 0 match", version: "1.5.2", constraint: "^1.2.0", wantMatch: true},
		{name: "caret standard major > 0 next major fail", version: "2.0.0", constraint: "^1.2.0", wantMatch: false},
		{name: "caret standard major > 0 lower version fail", version: "1.1.0", constraint: "^1.2.0", wantMatch: false},
		{name: "caret 0.x.y match patch", version: "0.2.5", constraint: "^0.2.3", wantMatch: true},
		{name: "caret 0.x.y next minor fail", version: "0.3.0", constraint: "^0.2.3", wantMatch: false},
		{name: "caret 0.0.x match exact", version: "0.0.3", constraint: "^0.0.3", wantMatch: true},
		{name: "caret 0.0.x next patch fail", version: "0.0.4", constraint: "^0.0.3", wantMatch: false},

		// Pre-release boundary matching
		{name: "caret pre-release of next major fails", version: "2.0.0-alpha.1", constraint: "^1.2.0", wantMatch: false},
		{name: "tilde pre-release of next minor fails", version: "1.3.0-alpha.1", constraint: "~1.2.0", wantMatch: false},
		{name: "caret pre-release within same major succeeds if >= base", version: "1.2.1-alpha.1", constraint: "^1.2.0", wantMatch: true},

		// Tilde ranges (~)
		{name: "tilde standard match patch", version: "1.0.5", constraint: "~1.0.0", wantMatch: true},
		{name: "tilde standard next minor fail", version: "1.1.0", constraint: "~1.0.0", wantMatch: false},
		{name: "tilde with 2 segments ~1.2 match", version: "1.2.8", constraint: "~1.2", wantMatch: true},
		{name: "tilde with 2 segments ~1.2 fail", version: "1.3.0", constraint: "~1.2", wantMatch: false},

		// Hyphen ranges (X.Y.Z - A.B.C)
		{name: "hyphen range inside", version: "1.5.0", constraint: "1.0.0 - 2.0.0", wantMatch: true},
		{name: "hyphen range lower bound", version: "1.0.0", constraint: "1.0.0 - 2.0.0", wantMatch: true},
		{name: "hyphen range upper bound", version: "2.0.0", constraint: "1.0.0 - 2.0.0", wantMatch: true},
		{name: "hyphen range below", version: "0.9.9", constraint: "1.0.0 - 2.0.0", wantMatch: false},
		{name: "hyphen range above", version: "2.0.1", constraint: "1.0.0 - 2.0.0", wantMatch: false},

		// Wildcards
		{name: "wildcard star", version: "3.4.5", constraint: "*", wantMatch: true},
		{name: "wildcard empty", version: "3.4.5", constraint: "", wantMatch: true},
		{name: "wildcard x", version: "3.4.5", constraint: "x", wantMatch: true},
		{name: "wildcard 1.* match", version: "1.99.99", constraint: "1.*", wantMatch: true},
		{name: "wildcard 1.* fail", version: "2.0.0", constraint: "1.*", wantMatch: false},
		{name: "wildcard 1.2.* match", version: "1.2.99", constraint: "1.2.*", wantMatch: true},
		{name: "wildcard 1.2.* fail", version: "1.3.0", constraint: "1.2.*", wantMatch: false},

		// Conjunctions and Disjunctions
		{name: "conjunction match", version: "1.5.0", constraint: ">=1.0.0 <=2.0.0", wantMatch: true},
		{name: "conjunction fail", version: "2.5.0", constraint: ">=1.0.0 <=2.0.0", wantMatch: false},
		{name: "disjunction branch 1 match", version: "0.9.0", constraint: "<1.0.0 || >=2.0.0", wantMatch: true},
		{name: "disjunction branch 2 match", version: "2.1.0", constraint: "<1.0.0 || >=2.0.0", wantMatch: true},
		{name: "disjunction neither match", version: "1.5.0", constraint: "<1.0.0 || >=2.0.0", wantMatch: false},

		// Invalid inputs
		{name: "invalid version string", version: "bad.version", constraint: ">=1.0.0", wantErr: true},
		{name: "invalid constraint string", version: "1.0.0", constraint: ">>bad", wantErr: true},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := linter.MatchConstraint(tc.version, tc.constraint)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("MatchConstraint(%q, %q) expected error, got nil", tc.version, tc.constraint)
				}
				return
			}
			if err != nil {
				t.Fatalf("MatchConstraint(%q, %q) unexpected error: %v", tc.version, tc.constraint, err)
			}
			if got != tc.wantMatch {
				t.Errorf("MatchConstraint(%q, %q) = %v, want %v", tc.version, tc.constraint, got, tc.wantMatch)
			}
		})
	}
}
