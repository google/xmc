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

func TestValidationResult_Diagnostics(t *testing.T) {
	t.Parallel()

	t.Run("new validation result starts clean", func(t *testing.T) {
		t.Parallel()
		res := linter.NewValidationResult()
		if !res.Valid {
			t.Errorf("new ValidationResult should be valid")
		}
		if res.HasErrors() {
			t.Errorf("new ValidationResult should not have errors")
		}
		if len(res.Diagnostics) != 0 || len(res.Errors) != 0 || len(res.Warnings) != 0 {
			t.Errorf("expected empty slices, got diagnostics: %v, errors: %v, warnings: %v", res.Diagnostics, res.Errors, res.Warnings)
		}
	})

	t.Run("add warning preserves valid status", func(t *testing.T) {
		t.Parallel()
		res := linter.NewValidationResult()
		res.AddWarning("manifest.yaml", 15, "DEPRECATED_SYNTAX", "field is deprecated")
		if !res.Valid {
			t.Errorf("expected valid to remain true after warning")
		}
		if res.HasErrors() {
			t.Errorf("expected HasErrors() to return false")
		}
		if len(res.Warnings) != 1 || res.Warnings[0] != "field is deprecated" {
			t.Errorf("unexpected warnings: %v", res.Warnings)
		}
	})

	t.Run("add error sets valid to false and records error message", func(t *testing.T) {
		t.Parallel()
		res := linter.NewValidationResult()
		res.AddError("manifest.yaml", 42, "SCHEMA_ERROR", "invalid schema definition")
		if res.Valid {
			t.Errorf("expected valid to be false after error")
		}
		if !res.HasErrors() {
			t.Errorf("expected HasErrors() to return true")
		}
		if len(res.Errors) != 1 || res.Errors[0] != "invalid schema definition" {
			t.Errorf("unexpected errors: %v", res.Errors)
		}
	})

	t.Run("add diagnostic with info severity", func(t *testing.T) {
		t.Parallel()
		res := linter.NewValidationResult()
		res.AddDiagnostic(linter.Diagnostic{
			File:     "info.yaml",
			Line:     1,
			Severity: linter.SeverityInfo,
			Rule:     "INFO_RULE",
			Message:  "info message",
		})
		if !res.Valid || res.HasErrors() {
			t.Errorf("info diagnostic should not invalidate result")
		}
		if len(res.Diagnostics) != 1 {
			t.Errorf("expected 1 diagnostic, got %d", len(res.Diagnostics))
		}
	})

	t.Run("concurrent diagnostic recording is thread-safe", func(t *testing.T) {
		t.Parallel()
		res := linter.NewValidationResult()
		const n = 100
		done := make(chan bool, n)
		for i := 0; i < n; i++ {
			go func(idx int) {
				if idx%2 == 0 {
					res.AddError("file.yaml", idx, "RULE_ERROR", "err")
				} else {
					res.AddWarning("file.yaml", idx, "RULE_WARN", "warn")
				}
				_ = res.HasErrors()
				done <- true
			}(i)
		}
		for i := 0; i < n; i++ {
			<-done
		}
		if len(res.Diagnostics) != n {
			t.Errorf("expected %d diagnostics, got %d", n, len(res.Diagnostics))
		}
		if !res.HasErrors() {
			t.Errorf("expected HasErrors() to return true")
		}
	})
}
