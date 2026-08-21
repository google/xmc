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

func TestValidateDirectory_ScaffoldingE2E(t *testing.T) {
	t.Parallel()

	t.Run("valid directory with multiple component manifests", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		manifestA := "apiVersion: components.xmc.dev/v1alpha1\nkind: Component\nmetadata:\n  name: cert-manager\nspec:\n  version: \"1.12.4\"\n"
		manifestB := "apiVersion: components.xmc.dev/v1alpha1\nkind: Component\nmetadata:\n  name: jobset\nspec:\n  version: \"0.2.0\"\n"
		if err := os.WriteFile(filepath.Join(dir, "cert-manager.yaml"), []byte(manifestA), 0644); err != nil {
			t.Fatalf("failed to write cert-manager.yaml: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "jobset.yaml"), []byte(manifestB), 0644); err != nil {
			t.Fatalf("failed to write jobset.yaml: %v", err)
		}

		l := linter.NewLinter()
		res, err := l.ValidateDirectory(dir)
		if err != nil || !res.Valid {
			t.Fatalf("ValidateDirectory() failed: err=%v, errors=%v", err, res.Errors)
		}
	})

	t.Run("directory with syntax error in yaml file", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "broken.yaml"), []byte(":::invalid: yaml:"), 0644); err != nil {
			t.Fatalf("failed to write broken file: %v", err)
		}

		l := linter.NewLinter()
		res, err := l.ValidateDirectory(dir)
		if err != nil || res.Valid {
			t.Fatalf("expected validation failure for syntax error, got err=%v, valid=%v", err, res.Valid)
		}
		assertDiagnosticRule(t, res, "YAML_SYNTAX")
	})

	t.Run("non-existent directory returns error", func(t *testing.T) {
		t.Parallel()
		l := linter.NewLinter()
		if _, err := l.ValidateDirectory("/tmp/path/does/not/exist/998877"); err == nil {
			t.Fatalf("expected error for non-existent directory")
		}
	})

	t.Run("empty directory is valid", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		l := linter.NewLinter()
		res, err := l.ValidateDirectory(dir)
		if err != nil || !res.Valid {
			t.Fatalf("expected empty directory to be valid, got err=%v, valid=%v", err, res.Valid)
		}
	})

	t.Run("multi-document stream in single yaml file", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		content := `apiVersion: components.xmc.dev/v1alpha1
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
		if err := os.WriteFile(filepath.Join(dir, "components.yaml"), []byte(content), 0644); err != nil {
			t.Fatalf("failed to write multi-doc file: %v", err)
		}

		l := linter.NewLinter()
		res, err := l.ValidateDirectory(dir)
		if err != nil || !res.Valid {
			t.Fatalf("expected multi-doc directory to be valid, got err=%v, errors=%v", err, res.Errors)
		}
	})

	t.Run("non-mapping root yaml document is skipped without schema error", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "list.yaml"), []byte("- item1\n- item2\n"), 0644); err != nil {
			t.Fatalf("failed to write list file: %v", err)
		}

		l := linter.NewLinter()
		res, err := l.ValidateDirectory(dir)
		if err != nil || !res.Valid {
			t.Fatalf("expected non-mapping yaml to be skipped, got err=%v, errors=%v", err, res.Errors)
		}
	})

	t.Run("third-party Component kind without xmc apiVersion is skipped from compManifests", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		content := `apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: statestore
spec:
  type: state.redis
`
		if err := os.WriteFile(filepath.Join(dir, "dapr-comp.yaml"), []byte(content), 0644); err != nil {
			t.Fatalf("failed to write dapr file: %v", err)
		}

		l := linter.NewLinter()
		res, err := l.ValidateDirectory(dir)
		if err != nil || !res.Valid {
			t.Fatalf("expected 3P Component to be safely decoded as RawManifest only, got err=%v, errors=%v", err, res.Errors)
		}
	})
}

func assertDiagnosticRule(t *testing.T, res *linter.ValidationResult, wantRule string) {
	t.Helper()
	if wantRule == "" {
		return
	}
	for _, d := range res.Diagnostics {
		if d.Rule == wantRule {
			return
		}
	}
	t.Errorf("expected diagnostic with rule %q, got diagnostics: %+v", wantRule, res.Diagnostics)
}
