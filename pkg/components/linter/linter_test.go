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
	"os"
	"path/filepath"
	"testing"

	"xmanager_cloud/pkg/components/linter"
)

func TestValidateDirectory_E2E(t *testing.T) {
	t.Parallel()

	t.Run("valid directory with multiple valid component manifests", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()

		manifestA := `
apiVersion: components.xmc.dev/v1alpha1
kind: Component
metadata:
  name: cert-manager
spec:
  version: "1.12.4"
`
		manifestB := `
apiVersion: components.xmc.dev/v1alpha1
kind: Component
metadata:
  name: jobset
spec:
  version: "0.2.0"
`
		if err := os.WriteFile(filepath.Join(dir, "cert-manager.yaml"), []byte(manifestA), 0644); err != nil {
			t.Fatalf("failed to write cert-manager.yaml: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "jobset.yaml"), []byte(manifestB), 0644); err != nil {
			t.Fatalf("failed to write jobset.yaml: %v", err)
		}

		rules := &linter.RulesConfig{
			StaticChecks: linter.StaticChecksConfig{
				EnforceNamespaceScope:     false,
				EnforceDeletionProtection: false,
			},
		}

		l := linter.NewLinter(rules)
		res, err := l.ValidateDirectory(dir)
		if err != nil {
			t.Fatalf("ValidateDirectory() unexpected error: %v", err)
		}
		if !res.Valid {
			t.Errorf("expected valid directory result, got errors: %v", res.Errors)
		}
	})

	t.Run("directory with syntax error in yaml file", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		brokenFile := filepath.Join(dir, "broken.yaml")
		if err := os.WriteFile(brokenFile, []byte(":::invalid: yaml:"), 0644); err != nil {
			t.Fatalf("failed to write broken file: %v", err)
		}

		l := linter.NewLinter(nil)
		res, err := l.ValidateDirectory(dir)
		if err != nil {
			t.Fatalf("ValidateDirectory() unexpected error: %v", err)
		}
		if res.Valid {
			t.Errorf("expected validation failure for syntax error")
		}
		assertDiagnosticRule(t, res, "YAML_SYNTAX")
	})

	t.Run("non-existent directory returns error", func(t *testing.T) {
		t.Parallel()
		l := linter.NewLinter(nil)
		_, err := l.ValidateDirectory("/tmp/path/does/not/exist/998877")
		if err == nil {
			t.Fatalf("expected error for non-existent directory")
		}
	})

	t.Run("empty directory is valid", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		l := linter.NewLinter(nil)
		res, err := l.ValidateDirectory(dir)
		if err != nil {
			t.Fatalf("ValidateDirectory() unexpected error: %v", err)
		}
		if !res.Valid {
			t.Errorf("expected empty directory to be valid")
		}
	})

	t.Run("multi-document stream in single yaml file", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		multiDocFile := filepath.Join(dir, "components.yaml")
		content := `
apiVersion: components.xmc.dev/v1alpha1
kind: Component
metadata:
  name: comp-1
spec:
  version: "1.0.0"
---
apiVersion: components.xmc.dev/v1alpha1
kind: Component
metadata:
  name: comp-2
spec:
  version: "1.2.0"
---
# Empty document with comment only
---

`
		if err := os.WriteFile(multiDocFile, []byte(content), 0644); err != nil {
			t.Fatalf("failed to write multi-doc file: %v", err)
		}

		l := linter.NewLinter(nil)
		res, err := l.ValidateDirectory(dir)
		if err != nil {
			t.Fatalf("ValidateDirectory() unexpected error: %v", err)
		}
		if !res.Valid {
			t.Errorf("expected multi-doc directory to be valid, got errors: %v", res.Errors)
		}
	})
}
