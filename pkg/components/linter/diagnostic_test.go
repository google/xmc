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

	t.Run("basic operations and severities", func(t *testing.T) {
		t.Parallel()
		res := linter.NewValidationResult()
		if !res.Valid || res.HasErrors() || len(res.Diagnostics) != 0 {
			t.Fatalf("new ValidationResult should start clean and valid")
		}
		res.AddWarning("warn.yaml", 10, "WARN_RULE", "warning msg")
		res.AddDiagnostic(linter.Diagnostic{File: "custom.yaml", Line: 1, Severity: "CUSTOM", Rule: "CUSTOM", Message: "custom"})
		if !res.Valid || res.HasErrors() || len(res.Warnings) != 1 {
			t.Errorf("expected valid=true with 1 warning, got valid=%v, warnings=%v", res.Valid, res.Warnings)
		}
		res.AddError("err.yaml", 20, "ERR_RULE", "error msg")
		if res.Valid || !res.HasErrors() || len(res.Errors) != 1 {
			t.Errorf("expected valid=false with 1 error, got valid=%v, errors=%v", res.Valid, res.Errors)
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
		if len(res.Diagnostics) != n || !res.HasErrors() {
			t.Errorf("expected %d diagnostics and HasErrors()=true", n)
		}
	})

	t.Run("marshal json serialization", func(t *testing.T) {
		t.Parallel()
		res := linter.NewValidationResult()
		res.AddError("manifest.yaml", 15, "SCHEMA_ERROR", "field missing")
		res.AddWarning("manifest.yaml", 25, "DEPRECATION", "version deprecated")
		data, err := res.MarshalJSON()
		if err != nil {
			t.Fatalf("MarshalJSON() failed: %v", err)
		}
		if len(data) == 0 {
			t.Errorf("expected non-empty JSON data")
		}
	})
}
