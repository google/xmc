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

// Package profilecontract checks the contract that every XMC profile meta-chart under
// deploy/profiles satisfies, independently of the components it ships. A profile meta-chart
// renders one Component CR per catalog component by reading components/<name>/ through .Files.
//
// A profile-specific test describes its chart with a Spec, calls Run for the generic contract,
// and uses the exported building blocks for assertions that only hold for that profile.
package profilecontract

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/chartutil"
	"helm.sh/helm/v3/pkg/engine"
)

// DefaultRelease is the release name used when a check does not test release naming.
const DefaultRelease = "xmc-profile"

// Spec describes a profile meta-chart under test.
type Spec struct {
	// ChartRelPath is the profile chart directory relative to the repository root, for example
	// "deploy/profiles/stable-2026a".
	ChartRelPath string
	// ProfileName is the expected profile identity, for example "stable-2026a".
	ProfileName string
	// Components lists the catalog components in the order the profile renders them.
	Components []string
}

// ChartDir locates the profile chart in Bazel runfiles (TEST_SRCDIR) or, under plain go test, in
// the closest ancestor of the working directory that contains it.
func ChartDir(t *testing.T, s Spec) string {
	t.Helper()
	rel := filepath.FromSlash(s.ChartRelPath)
	var candidates []string
	if srcDir := os.Getenv("TEST_SRCDIR"); srcDir != "" {
		candidates = append(candidates, filepath.Join(srcDir, "_main", rel), filepath.Join(srcDir, "xmanager_cloud", rel))
	}
	if wd, err := os.Getwd(); err == nil {
		for dir := wd; filepath.Dir(dir) != dir; dir = filepath.Dir(dir) {
			candidates = append(candidates, filepath.Join(dir, rel))
		}
	}
	for _, dir := range candidates {
		if _, err := os.Stat(filepath.Join(dir, "Chart.yaml")); err == nil {
			return dir
		}
	}
	t.Fatalf("profile chart %s not found in %v", s.ChartRelPath, candidates)
	return ""
}

// LoadChart loads the chart at dir through the Helm SDK, applying .helmignore.
func LoadChart(t *testing.T, dir string) *chart.Chart {
	t.Helper()
	ch, err := loader.Load(dir)
	if err != nil {
		t.Fatalf("failed to load chart %s: %v", dir, err)
	}
	return ch
}

// LoadProfile loads a fresh copy of the profile meta-chart, which tests may modify in memory.
func LoadProfile(t *testing.T, s Spec) *chart.Chart {
	t.Helper()
	return LoadChart(t, ChartDir(t, s))
}

// LoadProfileWithFileEdit loads the profile chart and, in memory only, replaces the content of
// the file at path with data, or removes the file when remove is true.
func LoadProfileWithFileEdit(t *testing.T, s Spec, path, data string, remove bool) *chart.Chart {
	t.Helper()
	ch := LoadProfile(t, s)
	i := slices.IndexFunc(ch.Files, func(f *chart.File) bool { return f.Name == path })
	if i < 0 {
		t.Fatalf("profile chart has no file %q", path)
	}
	if remove {
		ch.Files = slices.Delete(ch.Files, i, i+1)
		return ch
	}
	ch.Files[i] = &chart.File{Name: path, Data: []byte(data)}
	return ch
}

// Render renders ch with overrides through the Helm SDK, including values.schema.json
// validation, and returns rendered templates keyed by "<chart>/<path>".
func Render(ch *chart.Chart, releaseName string, overrides map[string]any) (map[string]string, error) {
	opts := chartutil.ReleaseOptions{Name: releaseName, Namespace: "xmc", Revision: 1, IsInstall: true}
	vals, err := chartutil.ToRenderValues(ch, overrides, opts, nil)
	if err != nil {
		return nil, err
	}
	return engine.Render(ch, vals)
}

// RenderDocs renders a profile chart and returns the decoded documents of its
// templates/components.yaml.
func RenderDocs(t *testing.T, ch *chart.Chart, overrides map[string]any) []map[string]any {
	t.Helper()
	rendered, err := Render(ch, DefaultRelease, overrides)
	if err != nil {
		t.Fatalf("Render(%s, %v) failed: %v", ch.Name(), overrides, err)
	}
	return DecodeDocs(t, rendered[ch.Name()+"/templates/components.yaml"])
}

// DecodeDocs decodes every non-empty YAML document in manifest.
func DecodeDocs(t *testing.T, manifest string) []map[string]any {
	t.Helper()
	var docs []map[string]any
	dec := yaml.NewDecoder(strings.NewReader(manifest))
	for {
		var doc map[string]any
		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			return docs
		}
		if err != nil {
			t.Fatalf("failed to decode rendered manifest: %v\n%s", err, manifest)
		}
		if doc != nil {
			docs = append(docs, doc)
		}
	}
}

// Lookup returns the value at the key path in a decoded YAML document, or nil when any key is
// absent.
func Lookup(doc any, keys ...string) any {
	for _, key := range keys {
		m, _ := doc.(map[string]any)
		doc = m[key]
	}
	return doc
}

// DocName returns metadata.name of a decoded document, or "" when it is absent.
func DocName(doc map[string]any) string {
	name, _ := Lookup(doc, "metadata", "name").(string)
	return name
}

// DocByName returns the document named name, failing the test when there is none.
func DocByName(t *testing.T, docs []map[string]any, name string) map[string]any {
	t.Helper()
	for _, doc := range docs {
		if DocName(doc) == name {
			return doc
		}
	}
	t.Fatalf("no rendered document named %q", name)
	return nil
}

// ComponentOverrides builds admin values that override a single catalog component.
func ComponentOverrides(name string, vals map[string]any) map[string]any {
	return map[string]any{"components": map[string]any{name: vals}}
}

// AssertErrorContains fails the test unless err is non-nil and its message contains want.
func AssertErrorContains(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("Render() error = %v, want error containing %q", err, want)
	}
}
