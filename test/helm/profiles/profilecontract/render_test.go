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

package profilecontract

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// miniChart is a minimal chart with one profile-shaped component file, components/demo/values.yaml,
// that exercises the building blocks without a profile.
var miniChart = Spec{ChartRelPath: "test/helm/profiles/profilecontract/testdata/minichart"}

// TestChartDir resolves charts through every lookup strategy. It cannot run in parallel because
// it sets TEST_SRCDIR and changes the working directory.
func TestChartDir(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"_main/a", "xmanager_cloud/b", "c", "nested/d"} {
		writeFile(t, Spec{ChartRelPath: root}, filepath.Join(dir, "Chart.yaml"), "name: x\n")
	}
	tests := []struct {
		name, srcDir, wd, rel string
		want                  string
	}{
		{name: "Bazel runfiles _main", srcDir: root, rel: "a", want: filepath.Join(root, "_main", "a")},
		{name: "Bazel runfiles workspace name", srcDir: root, rel: "b", want: filepath.Join(root, "xmanager_cloud", "b")},
		{name: "ancestor of working directory", wd: filepath.Join(root, "nested", "d"), rel: "c", want: filepath.Join(root, "c")},
		{name: "absolute path", rel: filepath.Join(root, "c"), want: filepath.Join(root, "c")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("TEST_SRCDIR", tt.srcDir)
			if tt.wd != "" {
				t.Chdir(tt.wd)
			}
			got, err := chartDir(Spec{ChartRelPath: tt.rel})
			if err != nil || got != tt.want {
				t.Errorf("chartDir(%q) = %q, %v, want %q", tt.rel, got, err, tt.want)
			}
		})
	}
	t.Run("not found", func(t *testing.T) {
		if _, err := chartDir(Spec{ChartRelPath: "no/such/chart"}); err == nil || !strings.Contains(err.Error(), "profile chart no/such/chart not found") {
			t.Errorf("chartDir() error = %v, want not found", err)
		}
	})
}

func TestLoadProfileWithFileEdit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, path, data string
		remove           bool
		want             []string
	}{
		{name: "replace", path: "components/demo/values.yaml", data: "enabled: true\n", want: []string{"components/demo/values.yaml=enabled: true\n"}},
		{name: "remove", path: "components/demo/values.yaml", remove: true, want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var got []string
			for _, f := range LoadProfileWithFileEdit(t, miniChart, tt.path, tt.data, tt.remove).Files {
				got = append(got, f.Name+"="+string(f.Data))
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("chart files mismatch (-want +got):\n%s", diff)
			}
		})
	}
	t.Run("unknown file", func(t *testing.T) {
		t.Parallel()
		if _, err := loadProfileWithFileEdit(miniChart, "components/demo/missing.yaml", "", true); err == nil {
			t.Error("loadProfileWithFileEdit() error = nil, want unknown file error")
		}
	})
}

func TestRenderDocs_DecodesEveryTemplate(t *testing.T) {
	t.Parallel()
	ch := LoadProfile(t, miniChart)
	var got []string
	for _, doc := range RenderDocs(t, ch, map[string]any{"name": "custom"}) {
		got = append(got, docName(doc))
	}
	if diff := cmp.Diff([]string{"custom"}, got); diff != "" {
		t.Errorf("rendered names mismatch (-want +got):\n%s", diff)
	}
	if _, err := renderDocs(ch, map[string]any{"name": "a: [b"}); err == nil {
		t.Error("renderDocs() error = nil, want a decode error for invalid rendered YAML")
	}
}

func TestDecodeDocs(t *testing.T) {
	t.Parallel()
	got, err := decodeDocs("a: 1\n---\n---\nb: 2\n")
	fatalOnError(t, err)
	if diff := cmp.Diff([]map[string]any{{"a": 1}, {"b": 2}}, got); diff != "" {
		t.Errorf("decodeDocs() mismatch (-want +got):\n%s", diff)
	}
	if _, err := decodeDocs("a: [b"); err == nil {
		t.Error("decodeDocs() error = nil, want a decode error")
	}
}

func TestLookup(t *testing.T) {
	t.Parallel()
	doc := map[string]any{"metadata": map[string]any{"name": "x"}, "list": []any{1}}
	tests := []struct {
		name string
		keys []string
		want any
	}{
		{name: "nested key", keys: []string{"metadata", "name"}, want: "x"},
		{name: "missing key", keys: []string{"spec", "name"}, want: nil},
		{name: "non-map intermediate", keys: []string{"list", "name"}, want: nil},
		{name: "no keys", want: doc},
	}
	for _, tt := range tests {
		if diff := cmp.Diff(tt.want, Lookup(doc, tt.keys...)); diff != "" {
			t.Errorf("%s: Lookup(%v) mismatch (-want +got):\n%s", tt.name, tt.keys, diff)
		}
	}
}

func TestDocByName(t *testing.T) {
	t.Parallel()
	docs := []map[string]any{{"kind": "A"}, {"metadata": map[string]any{"name": "b"}}}
	if got := DocByName(t, docs, "b"); docName(got) != "b" {
		t.Errorf("DocByName(b) = %v, want the document named b", got)
	}
	if _, err := docByName(docs, "c"); err == nil {
		t.Error("docByName(c) error = nil, want not found")
	}
}

func TestErrorContains(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		err     error
		wantErr string
	}{
		{name: "nil error", err: nil, wantErr: `render succeeded, want error containing "want-this"`},
		{name: "unrelated error", err: errors.New("boom"), wantErr: `render error = boom, want error containing "want-this"`},
		{name: "matching error", err: errors.New("render: want-this failed")},
	}
	for _, tt := range tests {
		got := errorContains(tt.err, "want-this")
		gotMsg := ""
		if got != nil {
			gotMsg = got.Error()
		}
		if gotMsg != tt.wantErr {
			t.Errorf("%s: errorContains() = %q, want %q", tt.name, gotMsg, tt.wantErr)
		}
	}
}

func TestComponentOverrides(t *testing.T) {
	t.Parallel()
	want := map[string]any{"components": map[string]any{"a": map[string]any{"enabled": true}}}
	if diff := cmp.Diff(want, ComponentOverrides("a", map[string]any{"enabled": true})); diff != "" {
		t.Errorf("ComponentOverrides() mismatch (-want +got):\n%s", diff)
	}
}

func TestReadProfileSources_ReportsMissingFiles(t *testing.T) {
	t.Parallel()
	if _, err := readProfileSources(t.TempDir()); err == nil || !os.IsNotExist(errors.Unwrap(err)) {
		t.Errorf("readProfileSources() error = %v, want a not-exist error", err)
	}
}
