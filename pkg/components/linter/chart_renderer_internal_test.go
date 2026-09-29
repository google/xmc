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

package linter

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHasChartFile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		setup func(t *testing.T, dir string)
		want  bool
	}{
		{name: "Chart.yaml", setup: writeFileIn("Chart.yaml"), want: true},
		{name: "Chart.yml", setup: writeFileIn("Chart.yml"), want: true},
		{name: "directory named Chart.yaml", setup: func(t *testing.T, dir string) {
			if err := os.Mkdir(filepath.Join(dir, "Chart.yaml"), 0o755); err != nil {
				t.Fatalf("failed to create directory: %v", err)
			}
		}},
		{name: "no chart file", setup: writeFileIn("values.yaml")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			tt.setup(t, dir)
			if got := hasChartFile(dir); got != tt.want {
				t.Errorf("hasChartFile() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsNestedChartRoot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	for _, rel := range []string{
		"outer/Chart.yaml",
		"outer/components/a/Chart.yaml",
		"outer/charts/sub/Chart.yaml",
		"standalone/Chart.yaml",
		"plain/values.yaml",
	} {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("failed to create directory for %s: %v", rel, err)
		}
		writeFileIn(filepath.Base(path))(t, filepath.Dir(path))
	}

	tests := []struct {
		name     string
		walkRoot string
		dir      string
		want     bool
	}{
		{name: "not a chart root", walkRoot: ".", dir: "plain"},
		{name: "no enclosing chart", walkRoot: ".", dir: "standalone"},
		{name: "enclosed by chart below walk root", walkRoot: ".", dir: "outer/components/a", want: true},
		{name: "enclosed by chart at walk root", walkRoot: "outer", dir: "outer/components/a", want: true},
		{name: "vendored subchart", walkRoot: ".", dir: "outer/charts/sub"},
		{name: "enclosing chart outside walk root", walkRoot: "outer/components", dir: "outer/components/a"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := isNestedChartRoot(filepath.Join(root, tt.walkRoot), filepath.Join(root, tt.dir))
			if got != tt.want {
				t.Errorf("isNestedChartRoot(%q, %q) = %v, want %v", tt.walkRoot, tt.dir, got, tt.want)
			}
		})
	}
}

// writeFileIn returns a setup step writing an empty file with the given name.
func writeFileIn(name string) func(t *testing.T, dir string) {
	return func(t *testing.T, dir string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
			t.Fatalf("failed to write %s: %v", name, err)
		}
	}
}
