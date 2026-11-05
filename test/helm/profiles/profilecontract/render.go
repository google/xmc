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
// and uses the exported building blocks for assertions that only hold for that profile. Each
// exported helper that takes a *testing.T wraps an unexported variant that returns an error, so
// the library's own tests can show that every check detects the violation it guards against.
package profilecontract

import (
	"errors"
	"fmt"
	"io"
	"maps"
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
	// "deploy/profiles/stable-2026a", or an absolute directory.
	ChartRelPath string
	// ProfileName is the expected profile identity, for example "stable-2026a".
	ProfileName string
	// Components lists the catalog components in the order the profile renders them.
	Components []string
}

// ChartDir locates any chart directory with the same resolution as the profile checks: rel itself
// when absolute, otherwise in Bazel runfiles or the closest ancestor of the working directory.
func ChartDir(rel string) (string, error) {
	return chartDir(Spec{ChartRelPath: rel})
}

// chartDir locates the profile chart: s.ChartRelPath itself when absolute, otherwise in Bazel
// runfiles (TEST_SRCDIR) or, under plain go test, in the closest ancestor of the working
// directory that contains it.
func chartDir(s Spec) (string, error) {
	rel := filepath.FromSlash(s.ChartRelPath)
	candidates := []string{rel}
	if !filepath.IsAbs(rel) {
		candidates = nil
		if srcDir := os.Getenv("TEST_SRCDIR"); srcDir != "" {
			candidates = append(candidates, filepath.Join(srcDir, "_main", rel), filepath.Join(srcDir, "xmanager_cloud", rel))
		}
		if wd, err := os.Getwd(); err == nil {
			for dir := wd; filepath.Dir(dir) != dir; dir = filepath.Dir(dir) {
				candidates = append(candidates, filepath.Join(dir, rel))
			}
		}
	}
	for _, dir := range candidates {
		if _, err := os.Stat(filepath.Join(dir, "Chart.yaml")); err == nil {
			return dir, nil
		}
	}
	return "", fmt.Errorf("profile chart %s not found in %v", s.ChartRelPath, candidates)
}

// fatalOnError fails the test with err when it is non-nil.
func fatalOnError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// loadChart loads the chart at dir through the Helm SDK, applying .helmignore.
func loadChart(dir string) (*chart.Chart, error) {
	ch, err := loader.Load(dir)
	if err != nil {
		return nil, fmt.Errorf("failed to load chart %s: %w", dir, err)
	}
	return ch, nil
}

// LoadProfile loads a fresh copy of the profile meta-chart, which tests may modify in memory.
func LoadProfile(t *testing.T, s Spec) *chart.Chart {
	t.Helper()
	ch, err := loadProfile(s)
	fatalOnError(t, err)
	return ch
}

func loadProfile(s Spec) (*chart.Chart, error) {
	dir, err := chartDir(s)
	if err != nil {
		return nil, err
	}
	return loadChart(dir)
}

// LoadProfileWithFileEdit loads the profile chart and, in memory only, replaces the content of
// the file at path with data, or removes the file when remove is true.
func LoadProfileWithFileEdit(t *testing.T, s Spec, path, data string, remove bool) *chart.Chart {
	t.Helper()
	ch, err := loadProfileWithFileEdit(s, path, data, remove)
	fatalOnError(t, err)
	return ch
}

func loadProfileWithFileEdit(s Spec, path, data string, remove bool) (*chart.Chart, error) {
	ch, err := loadProfile(s)
	if err != nil {
		return nil, err
	}
	i := slices.IndexFunc(ch.Files, func(f *chart.File) bool { return f.Name == path })
	if i < 0 {
		return nil, fmt.Errorf("profile chart has no file %q", path)
	}
	if remove {
		ch.Files = slices.Delete(ch.Files, i, i+1)
		return ch, nil
	}
	ch.Files[i] = &chart.File{Name: path, Data: []byte(data)}
	return ch, nil
}

// render renders ch with overrides through the Helm SDK, including values.schema.json
// validation, and returns rendered templates keyed by "<chart>/<path>".
func render(ch *chart.Chart, releaseName string, overrides map[string]any) (map[string]string, error) {
	opts := chartutil.ReleaseOptions{Name: releaseName, Namespace: "xmc", Revision: 1, IsInstall: true}
	vals, err := chartutil.ToRenderValues(ch, overrides, opts, nil)
	if err != nil {
		return nil, err
	}
	return engine.Render(ch, vals)
}

// RenderDocs renders a profile chart and returns the decoded documents of every rendered
// manifest: templates/components.yaml first, then the other templates in path order. Decoding
// every template, not just components.yaml, makes anything else the profile emits visible.
func RenderDocs(t *testing.T, ch *chart.Chart, overrides map[string]any) []map[string]any {
	t.Helper()
	docs, err := renderDocs(ch, overrides)
	fatalOnError(t, err)
	return docs
}

func renderDocs(ch *chart.Chart, overrides map[string]any) ([]map[string]any, error) {
	rendered, err := render(ch, DefaultRelease, overrides)
	if err != nil {
		return nil, fmt.Errorf("render(%s, %v) failed: %w", ch.Name(), overrides, err)
	}
	components := ch.Name() + "/templates/components.yaml"
	paths := []string{components}
	for _, p := range slices.Sorted(maps.Keys(rendered)) {
		if p != components && !strings.HasSuffix(p, "NOTES.txt") {
			paths = append(paths, p)
		}
	}
	var docs []map[string]any
	for _, p := range paths {
		d, err := decodeDocs(rendered[p])
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		docs = append(docs, d...)
	}
	return docs, nil
}

// DecodeDocs decodes every non-empty YAML document in manifest, such as one rendered template.
func DecodeDocs(manifest string) ([]map[string]any, error) {
	return decodeDocs(manifest)
}

// decodeDocs decodes every non-empty YAML document in manifest.
func decodeDocs(manifest string) ([]map[string]any, error) {
	var docs []map[string]any
	dec := yaml.NewDecoder(strings.NewReader(manifest))
	for {
		var doc map[string]any
		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			return docs, nil
		}
		if err != nil {
			return nil, fmt.Errorf("failed to decode rendered manifest: %w\n%s", err, manifest)
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

// docName returns metadata.name of a decoded document, or "" when it is absent.
func docName(doc map[string]any) string {
	name, _ := Lookup(doc, "metadata", "name").(string)
	return name
}

// DocByName returns the document named name, failing the test when there is none.
func DocByName(t *testing.T, docs []map[string]any, name string) map[string]any {
	t.Helper()
	doc, err := docByName(docs, name)
	fatalOnError(t, err)
	return doc
}

func docByName(docs []map[string]any, name string) (map[string]any, error) {
	for _, doc := range docs {
		if docName(doc) == name {
			return doc, nil
		}
	}
	return nil, fmt.Errorf("no rendered document named %q", name)
}

// ComponentOverrides builds admin values that override a single catalog component.
func ComponentOverrides(name string, vals map[string]any) map[string]any {
	return map[string]any{"components": map[string]any{name: vals}}
}

// errorContains returns an error unless err is non-nil and its message contains want. When err
// is nil, the returned message says the render succeeded so that callers matching on it cannot
// mistake the echoed want for a real render failure.
func errorContains(err error, want string) error {
	if err == nil {
		return fmt.Errorf("render succeeded, want error containing %q", want)
	}
	if !strings.Contains(err.Error(), want) {
		return fmt.Errorf("render error = %v, want error containing %q", err, want)
	}
	return nil
}
