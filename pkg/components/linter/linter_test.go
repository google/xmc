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
		writeTestManifest(t, dir, "c1.yaml", "apiVersion: components.xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: c1 }\nspec: { version: 1.0.0 }\n")
		writeTestManifest(t, dir, "c2.yaml", "apiVersion: components.xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: c2 }\nspec: { version: 2.0.0 }\n")
		l := linter.NewLinter(nil)
		res, err := l.ValidateDirectory(dir)
		if err != nil || !res.Valid {
			t.Fatalf("ValidateDirectory() failed: err=%v, errors=%v", err, res.Errors)
		}
	})

	t.Run("directory with syntax error in yaml file", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeTestManifest(t, dir, "broken.yaml", ":::invalid: yaml:")
		l := linter.NewLinter(nil)
		res, err := l.ValidateDirectory(dir)
		if err != nil || res.Valid {
			t.Fatalf("expected validation failure for syntax error, got err=%v, valid=%v", err, res.Valid)
		}
		assertDiagnosticRule(t, res, "YAML_SYNTAX")
	})

	t.Run("edge directories", func(t *testing.T) {
		t.Parallel()
		l := linter.NewLinter(nil)
		if _, err := l.ValidateDirectory(""); err == nil {
			t.Errorf("expected error for empty directory path")
		}
		if _, err := l.ValidateDirectory("/tmp/path/does/not/exist/998877"); err == nil {
			t.Errorf("expected error for non-existent directory")
		}
		dir := t.TempDir()
		filePath := filepath.Join(dir, "single.yaml")
		writeTestManifest(t, dir, "single.yaml", "apiVersion: v1\nkind: ConfigMap\nmetadata: { name: test }\n")
		if _, err := l.ValidateDirectory(filePath); err == nil {
			t.Errorf("expected error when passing a file path instead of directory")
		}
		if res, err := l.ValidateDirectory(dir); err != nil || !res.Valid {
			t.Errorf("expected valid directory with non-component yaml to pass, got: %v", res.Errors)
		}
	})

	t.Run("multi-document stream in single yaml file", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeTestManifest(t, dir, "components.yaml", "apiVersion: components.xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: comp-1 }\nspec: { version: 1.0.0 }\n---\napiVersion: components.xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: comp-2 }\nspec: { version: 1.2.0 }\n---\n# Comment only\n")
		l := linter.NewLinter(nil)
		if res, err := l.ValidateDirectory(dir); err != nil || !res.Valid {
			t.Fatalf("expected multi-doc directory to be valid, got err=%v, errors=%v", err, res.Errors)
		}
	})

	t.Run("non-mapping root and third-party manifests skipped safely", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeTestManifest(t, dir, "list.yaml", "- item1\n- item2\n")
		writeTestManifest(t, dir, "dapr-comp.yaml", "apiVersion: dapr.io/v1alpha1\nkind: Component\nmetadata: { name: statestore }\nspec: { type: state.redis }\n")
		l := linter.NewLinter(nil)
		if res, err := l.ValidateDirectory(dir); err != nil || !res.Valid {
			t.Fatalf("expected non-mapping and 3P components to be skipped safely, got err=%v, errors=%v", err, res.Errors)
		}
	})

	t.Run("empty and schema malformed mapping documents", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeTestManifest(t, dir, "empty.yaml", "foo: bar\n---\nmetadata: 123\n")
		writeTestManifest(t, dir, "invalid-group-kind.yaml", "apiVersion: components.xmc.dev/v1alpha1\nkind: InvalidKind\nmetadata: { name: invalid }\n")
		writeTestManifest(t, dir, "decode-error.yaml", "apiVersion: components.xmc.dev/v1alpha1\nkind: Component\nspec: [invalid_seq]\n")
		l := linter.NewLinter(nil)
		res, err := l.ValidateDirectory(dir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assertDiagnosticRule(t, res, "SCHEMA_ERROR")
	})

	t.Run("unreadable file reports error", func(t *testing.T) {
		t.Parallel()
		if os.Geteuid() == 0 {
			t.Skip("skipping unreadable file test under root user")
		}
		dir := t.TempDir()
		filePath := filepath.Join(dir, "unreadable.yaml")
		if err := os.WriteFile(filePath, []byte("apiVersion: v1\n"), 0000); err != nil {
			t.Fatalf("write failed: %v", err)
		}
		t.Cleanup(func() { _ = os.Chmod(filePath, 0644) })
		l := linter.NewLinter(nil)
		res, err := l.ValidateDirectory(dir)
		if err != nil || res.Valid {
			t.Fatalf("expected read error on unreadable file, got valid=%v, err=%v", res.Valid, err)
		}
		assertDiagnosticRule(t, res, "FILE_READ_ERROR")
	})
}

func writeTestManifest(t *testing.T, dir, filename, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, filename), []byte(content), 0644); err != nil {
		t.Fatalf("failed to write %s: %v", filename, err)
	}
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
