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

func assertDiagnosticRules(t *testing.T, res *linter.ValidationResult, wantRules []string) {
	t.Helper()
	if len(wantRules) == 0 {
		if !res.Valid {
			t.Errorf("expected valid result, got errors: %v", res.Errors)
		}
		return
	}
	for _, wantRule := range wantRules {
		found := false
		for _, d := range res.Diagnostics {
			if d.Rule == wantRule {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected diagnostic with rule %q, got diagnostics: %+v", wantRule, res.Diagnostics)
		}
	}
}

func assertDiagnosticRule(t *testing.T, res *linter.ValidationResult, wantRule string) {
	t.Helper()
	if wantRule == "" {
		return
	}
	found := false
	for _, d := range res.Diagnostics {
		if d.Rule == wantRule {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected diagnostic with rule %q, got diagnostics: %+v", wantRule, res.Diagnostics)
	}
}
