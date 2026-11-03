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
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"

	"xmanager_cloud/pkg/components/linter"
)

func TestValidateDirectory_ScaffoldingE2E(t *testing.T) {
	t.Parallel()

	t.Run("valid directory with multiple component manifests", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeTestManifest(t, dir, "c1.yaml", "apiVersion: xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: c1 }\nspec: { version: 1.0.0, stability: STABLE }\n")
		writeTestManifest(t, dir, "c2.yaml", "apiVersion: xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: c2 }\nspec: { version: 2.0.0, stability: STABLE }\n")
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
		writeTestManifest(t, dir, "components.yaml", "apiVersion: xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: comp-1 }\nspec: { version: 1.0.0, stability: STABLE }\n---\napiVersion: xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: comp-2 }\nspec: { version: 1.2.0, stability: STABLE }\n---\n# Comment only\n")
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
  stability: STABLE
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
  stability: STABLE
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
		writeTestManifest(t, dir, "c1.yaml", "apiVersion: xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: c1 }\nspec: { version: 1.0.0, stability: STABLE }\n")
		writeTestManifest(t, dir, "c2.yml", "apiVersion: xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: c2 }\nspec: { version: 1.0.0, stability: STABLE }\n")
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
		writeTestManifest(t, subDir, "c1.yaml", "apiVersion: xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: c1 }\nspec: { version: 1.0.0, stability: STABLE }\n")
		writeTestManifest(t, dir, "c2.yaml", "apiVersion: xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: c2 }\nspec: { version: 1.0.0, stability: STABLE }\n")
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
		validComp    = "apiVersion: xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: %s }\nspec: { version: 0.1.0, stability: STABLE }\n"
		missingDepOf = "apiVersion: xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: %s }\nspec: { version: 0.1.0, stability: STABLE, enabled: true, dependencies: [missing-dep] }\n"
		// renderNestedA is a meta-chart template that renders nested chart a's Component itself.
		renderNestedA = "{{ tpl (.Files.Get \"components/a/templates/component.yaml\") . }}\n"
		// brokenTemplate fails to compile, so rendering its chart fails.
		brokenTemplate = "{{ end }}\n"
	)
	// Charts render invalid or conflicting Components, so the diagnostics reveal which charts the
	// walker rendered and which file each diagnostic is attributed to.
	tree := t.TempDir()
	writeTestTree(t, tree, map[string]string{
		// Profile meta-chart that renders its nested component chart a through .Files.
		"profiles/profile/Chart.yaml":                            fmt.Sprintf(chartYAML, "profile"),
		"profiles/profile/templates/components.yaml":             renderNestedA,
		"profiles/profile/components/a/Chart.yaml":               fmt.Sprintf(chartYAML, "a"),
		"profiles/profile/components/a/templates/component.yaml": fmt.Sprintf(missingDepOf, "a"),
		// Meta-chart that does not render its nested chart b.
		"partial/Chart.yaml":                            fmt.Sprintf(chartYAML, "partial"),
		"partial/templates/components.yaml":             fmt.Sprintf(validComp, "p"),
		"partial/components/b/Chart.yaml":               fmt.Sprintf(chartYAML, "b"),
		"partial/components/b/templates/component.yaml": fmt.Sprintf(missingDepOf, "b"),
		// Meta-chart whose nested chart c has a template that the meta-chart never reads.
		"broken/Chart.yaml":                             fmt.Sprintf(chartYAML, "broken"),
		"broken/templates/components.yaml":              fmt.Sprintf(validComp, "w"),
		"broken/components/c/Chart.yaml":                fmt.Sprintf(chartYAML, "c"),
		"broken/components/c/templates/deployment.yaml": brokenTemplate,
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

	const (
		outerA  = "MISSING_DEPENDENCY profiles/profile/templates/components.yaml"
		nestedA = "MISSING_DEPENDENCY profiles/profile/components/a/templates/component.yaml"
	)
	tests := []struct {
		name    string
		lintDir string
		want    []string // Every diagnostic as "<rule> <file relative to tree>", sorted.
	}{
		{name: "outer chart reports rendered nested Component once", lintDir: "profiles/profile", want: []string{outerA}},
		{name: "directory above outer chart reports rendered nested Component once", lintDir: "profiles", want: []string{outerA}},
		{name: "nested chart linted on its own", lintDir: "profiles/profile/components/a", want: []string{nestedA}},
		{name: "chart-free directory renders charts whose parent is outside the walk", lintDir: "profiles/profile/components", want: []string{nestedA}},
		{name: "nested Component not rendered by outer chart is still linted", lintDir: "partial", want: []string{"MISSING_DEPENDENCY partial/components/b/templates/component.yaml"}},
		{name: "nested template error surfaces", lintDir: "broken", want: []string{"YAML_SYNTAX broken/components/c/templates/deployment.yaml"}},
		{name: "vendored charts/ subchart is rendered", lintDir: "vendored", want: []string{"MISSING_DEPENDENCY vendored/charts/sub/templates/component.yaml"}},
		{name: "sibling charts under chart-free directory are both rendered", lintDir: "catalog", want: []string{"DUPLICATE_COMPONENT_NAME catalog/b/templates/component.yaml"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			res, err := linter.NewLinter(nil).ValidateDirectory(filepath.Join(tree, tt.lintDir))
			if err != nil {
				t.Fatalf("ValidateDirectory(%q) error: %v", tt.lintDir, err)
			}
			if diff := cmp.Diff(tt.want, diagnosticKeys(t, tree, res)); diff != "" {
				t.Errorf("ValidateDirectory(%q) diagnostics mismatch (-want +got):\n%s\nfull diagnostics: %+v", tt.lintDir, diff, res.Diagnostics)
			}
		})
	}
}

// diagnosticKeys returns every diagnostic in res as "<rule> <file relative to root>", sorted.
func diagnosticKeys(t *testing.T, root string, res *linter.ValidationResult) []string {
	t.Helper()
	keys := make([]string, 0, len(res.Diagnostics))
	for _, d := range res.Diagnostics {
		rel, err := filepath.Rel(root, d.File)
		if err != nil {
			t.Fatalf("filepath.Rel(%q, %q) error: %v", root, d.File, err)
		}
		keys = append(keys, d.Rule+" "+filepath.ToSlash(rel))
	}
	slices.Sort(keys)
	return keys
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
