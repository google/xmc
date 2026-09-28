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
	"fmt"
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
		writeTestManifest(t, dir, "c1.yaml", "apiVersion: xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: c1 }\nspec: { version: 1.0.0 }\n")
		writeTestManifest(t, dir, "c2.yaml", "apiVersion: xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: c2 }\nspec: { version: 2.0.0 }\n")
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
		writeTestManifest(t, dir, "components.yaml", "apiVersion: xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: comp-1 }\nspec: { version: 1.0.0 }\n---\napiVersion: xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: comp-2 }\nspec: { version: 1.2.0 }\n---\n# Comment only\n")
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
		writeTestManifest(t, dir, "invalid-group-kind.yaml", "apiVersion: xmc.dev/v1alpha1\nkind: InvalidKind\nmetadata: { name: invalid }\n")
		writeTestManifest(t, dir, "decode-error.yaml", "apiVersion: xmc.dev/v1alpha1\nkind: Component\nspec: [invalid_seq]\n")
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

	t.Run("end-to-end semver dependency constraints across files", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeTestManifest(t, dir, "comp-a.yaml", `
apiVersion: xmc.dev/v1alpha1
kind: Component
metadata: { name: comp-a }
spec:
  version: "1.0.0"
  dependencies:
    - name: comp-b
      version: ">=2.0.0 <3.0.0"
`)
		writeTestManifest(t, dir, "comp-b.yaml", `
apiVersion: xmc.dev/v1alpha1
kind: Component
metadata: { name: comp-b }
spec:
  version: "2.4.1"
`)
		l := linter.NewLinter(nil)
		res, err := l.ValidateDirectory(dir)
		if err != nil || !res.Valid {
			t.Fatalf("expected valid multi-component directory with semver constraints, got err=%v, errors=%v", err, res.Errors)
		}
	})

	t.Run("end-to-end semver version incompatibility across files", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeTestManifest(t, dir, "comp-a.yaml", `
apiVersion: xmc.dev/v1alpha1
kind: Component
metadata: { name: comp-a }
spec:
  version: "1.0.0"
  dependencies:
    - name: comp-b
      version: "^3.0.0"
`)
		writeTestManifest(t, dir, "comp-b.yaml", `
apiVersion: xmc.dev/v1alpha1
kind: Component
metadata: { name: comp-b }
spec:
  version: "2.4.1"
`)
		l := linter.NewLinter(nil)
		res, err := l.ValidateDirectory(dir)
		if err != nil {
			t.Fatalf("unexpected execution error: %v", err)
		}
		if res.Valid {
			t.Fatalf("expected validation failure for incompatible SemVer versions")
		}
		assertDiagnosticRule(t, res, "VERSION_INCOMPATIBILITY")
	})

	t.Run("mixed yaml yml and non-manifest file extensions", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeTestManifest(t, dir, "c1.yaml", "apiVersion: xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: c1 }\nspec: { version: 1.0.0 }\n")
		writeTestManifest(t, dir, "c2.yml", "apiVersion: xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: c2 }\nspec: { version: 1.0.0 }\n")
		writeTestManifest(t, dir, "notes.txt", "this is a text file")
		writeTestManifest(t, dir, "config.json", "{}")
		l := linter.NewLinter(nil)
		res, err := l.ValidateDirectory(dir)
		if err != nil || !res.Valid {
			t.Fatalf("expected valid directory ignoring non-yaml files, got err=%v, errors=%v", err, res.Errors)
		}
	})

	t.Run("nested subdirectories manifest discovery", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		subDir := filepath.Join(dir, "nested", "sub")
		if err := os.MkdirAll(subDir, 0755); err != nil {
			t.Fatalf("failed to create subDir: %v", err)
		}
		writeTestManifest(t, subDir, "c1.yaml", "apiVersion: xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: c1 }\nspec: { version: 1.0.0 }\n")
		writeTestManifest(t, dir, "c2.yaml", "apiVersion: xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: c2 }\nspec: { version: 1.0.0 }\n")
		l := linter.NewLinter(nil)
		res, err := l.ValidateDirectory(dir)
		if err != nil || !res.Valid {
			t.Fatalf("expected valid nested directories, got err=%v, errors=%v", err, res.Errors)
		}
	})
}

func TestValidateDirectory_NestedChartRoots(t *testing.T) {
	t.Parallel()

	const (
		chartYAML    = "apiVersion: v2\nname: %s\nversion: 0.1.0\n"
		validComp    = "apiVersion: xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: %s }\nspec: { version: 0.1.0 }\n"
		missingDepOf = "apiVersion: xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: %s }\nspec: { version: 0.1.0, enabled: true, dependencies: [missing-dep] }\n"
	)
	// Charts render invalid or conflicting Components, so the diagnostics reveal which charts the
	// walker rendered.
	tree := t.TempDir()
	writeTestTree(t, tree, map[string]string{
		// Profile meta-chart with a nested component chart it renders itself.
		"profiles/profile/Chart.yaml":                            fmt.Sprintf(chartYAML, "profile"),
		"profiles/profile/templates/components.yaml":             fmt.Sprintf(validComp, "a"),
		"profiles/profile/components/a/Chart.yaml":               fmt.Sprintf(chartYAML, "a"),
		"profiles/profile/components/a/templates/component.yaml": fmt.Sprintf(missingDepOf, "a"),
		// Chart with a vendored Helm subchart under charts/.
		"vendored/Chart.yaml":                          fmt.Sprintf(chartYAML, "vendored"),
		"vendored/templates/components.yaml":           fmt.Sprintf(validComp, "v"),
		"vendored/charts/sub/Chart.yaml":               fmt.Sprintf(chartYAML, "sub"),
		"vendored/charts/sub/templates/component.yaml": fmt.Sprintf(missingDepOf, "sub"),
		// Independent sibling charts under a chart-free directory, both defining "dup".
		"catalog/a/Chart.yaml":               fmt.Sprintf(chartYAML, "a"),
		"catalog/a/templates/component.yaml": fmt.Sprintf(validComp, "dup"),
		"catalog/b/Chart.yaml":               fmt.Sprintf(chartYAML, "b"),
		"catalog/b/templates/component.yaml": fmt.Sprintf(validComp, "dup"),
	})

	tests := []struct {
		name     string
		lintDir  string
		wantRule string // Empty means the directory must lint with zero diagnostics.
	}{
		{name: "outer chart root skips nested chart", lintDir: "profiles/profile"},
		{name: "directory above outer chart root skips nested chart", lintDir: "profiles"},
		{name: "nested chart linted on its own is rendered", lintDir: "profiles/profile/components/a", wantRule: "MISSING_DEPENDENCY"},
		{name: "chart-free directory renders charts whose parent is outside the walk", lintDir: "profiles/profile/components", wantRule: "MISSING_DEPENDENCY"},
		{name: "vendored charts/ subchart is still rendered", lintDir: "vendored", wantRule: "MISSING_DEPENDENCY"},
		{name: "sibling charts under chart-free directory are both rendered", lintDir: "catalog", wantRule: "DUPLICATE_COMPONENT_NAME"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			res, err := linter.NewLinter(nil).ValidateDirectory(filepath.Join(tree, tt.lintDir))
			if err != nil {
				t.Fatalf("ValidateDirectory(%q) error: %v", tt.lintDir, err)
			}
			if tt.wantRule == "" && len(res.Diagnostics) != 0 {
				t.Fatalf("ValidateDirectory(%q) diagnostics = %+v, want none", tt.lintDir, res.Diagnostics)
			}
			if tt.wantRule != "" && res.Valid {
				t.Fatalf("ValidateDirectory(%q) valid = true, want %s error", tt.lintDir, tt.wantRule)
			}
			assertDiagnosticRule(t, res, tt.wantRule)
		})
	}
}

// writeTestTree writes files (keyed by slash-separated path relative to root), creating parent directories.
func writeTestTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("failed to create parent of %s: %v", rel, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("failed to write %s: %v", rel, err)
		}
	}
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
