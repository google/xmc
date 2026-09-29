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

// Package stable2026a_test holds contract tests for the deploy/profiles/stable-2026a meta-chart.
// They live outside the chart directory so the Bazel test binary never lands inside the chart
// that Helm's loader reads.
package stable2026a_test

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"gopkg.in/yaml.v3"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/chartutil"
	"helm.sh/helm/v3/pkg/engine"
)

const (
	profileRelPath        = "deploy/profiles/stable-2026a"
	renderedComponentsKey = "xmc-profile-stable-2026a/templates/components.yaml"
)

// profileChartDir locates the profile chart in Bazel runfiles (TEST_SRCDIR) or, under plain
// go test, relative to this package directory.
func profileChartDir(t *testing.T) string {
	t.Helper()
	candidates := []string{filepath.Join("..", "..", "..", "..", filepath.FromSlash(profileRelPath))}
	if srcDir := os.Getenv("TEST_SRCDIR"); srcDir != "" {
		candidates = append([]string{
			filepath.Join(srcDir, "_main", filepath.FromSlash(profileRelPath)),
			filepath.Join(srcDir, "xmanager_cloud", filepath.FromSlash(profileRelPath)),
		}, candidates...)
	}
	for _, dir := range candidates {
		if _, err := os.Stat(filepath.Join(dir, "Chart.yaml")); err == nil {
			return dir
		}
	}
	t.Fatalf("profile chart not found in %v", candidates)
	return ""
}

// loadChart loads the chart at dir through the Helm SDK, applying .helmignore.
func loadChart(t *testing.T, dir string) *chart.Chart {
	t.Helper()
	ch, err := loader.Load(dir)
	if err != nil {
		t.Fatalf("failed to load chart %s: %v", dir, err)
	}
	return ch
}

// profileChart loads a fresh copy of the profile meta-chart, which tests may modify in memory.
func profileChart(t *testing.T) *chart.Chart {
	t.Helper()
	return loadChart(t, profileChartDir(t))
}

// renderChart renders ch with overrides through the Helm SDK, including values.schema.json
// validation, and returns rendered templates keyed by "<chart>/<path>".
func renderChart(ch *chart.Chart, releaseName string, overrides map[string]any) (map[string]string, error) {
	opts := chartutil.ReleaseOptions{Name: releaseName, Namespace: "xmc", Revision: 1, IsInstall: true}
	vals, err := chartutil.ToRenderValues(ch, overrides, opts, nil)
	if err != nil {
		return nil, err
	}
	return engine.Render(ch, vals)
}

// renderProfileDocs renders a profile chart and returns its decoded Component documents.
func renderProfileDocs(t *testing.T, ch *chart.Chart, overrides map[string]any) []map[string]any {
	t.Helper()
	rendered, err := renderChart(ch, "xmc-profile", overrides)
	if err != nil {
		t.Fatalf("renderChart(profile, %v) failed: %v", overrides, err)
	}
	return decodeDocs(t, rendered[renderedComponentsKey])
}

// assertErrorContains fails the test unless err is non-nil and its message contains want.
func assertErrorContains(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("renderChart() error = %v, want error containing %q", err, want)
	}
}

// componentOverrides builds admin values that override a single catalog component.
func componentOverrides(name string, vals map[string]any) map[string]any {
	return map[string]any{"components": map[string]any{name: vals}}
}

func decodeDocs(t *testing.T, manifest string) []map[string]any {
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

// lookup returns the value at the key path in a decoded YAML document, or nil when any key is
// absent.
func lookup(doc any, keys ...string) any {
	for _, key := range keys {
		m, _ := doc.(map[string]any)
		doc = m[key]
	}
	return doc
}

func docName(doc map[string]any) string {
	name, _ := lookup(doc, "metadata", "name").(string)
	return name
}

func docByName(t *testing.T, docs []map[string]any, name string) map[string]any {
	t.Helper()
	for _, doc := range docs {
		if docName(doc) == name {
			return doc
		}
	}
	t.Fatalf("no rendered document named %q", name)
	return nil
}

func TestProfileChart_RendersOnlyCatalogComponentCRs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		overrides map[string]any
	}{
		{name: "default values"},
		{name: "all components enabled", overrides: map[string]any{"components": map[string]any{"redpanda": map[string]any{"enabled": true},
			"clickhouse": map[string]any{"enabled": true}, "ingestion-gateway": map[string]any{"enabled": true, "imageTag": "v1.2.3"}}}},
		{name: "wrapper-chart global key", overrides: map[string]any{"global": map[string]any{"foo": "bar"}}},
	}
	// templates/components.yaml renders the catalog in dependency order.
	want := []string{"Component/redpanda", "Component/clickhouse", "Component/ingestion-gateway"}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var got []string
			for _, doc := range renderProfileDocs(t, profileChart(t), tt.overrides) {
				got = append(got, fmt.Sprintf("%v/%s", doc["kind"], docName(doc)))
			}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("rendered kind/name mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestProfileChart_MatchesStandaloneComponentCharts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		component string
		overrides map[string]any
	}{
		{name: "redpanda defaults", component: "redpanda"},
		{name: "clickhouse defaults", component: "clickhouse"},
		{name: "ingestion-gateway defaults", component: "ingestion-gateway"},
		{name: "redpanda overrides", component: "redpanda", overrides: map[string]any{"enabled": true, "namespace": "data",
			"deletionProtection": false, "deletionPolicy": "Orphan", "parameters": map[string]any{"kafkaPort": 19092}, "dependencies": []any{"clickhouse"}}},
		{name: "clickhouse overrides", component: "clickhouse", overrides: map[string]any{"enabled": true,
			"parameters": map[string]any{"host": "ch.example.internal", "httpPort": 18123}}},
		{name: "ingestion-gateway overrides", component: "ingestion-gateway", overrides: map[string]any{"enabled": true, "imageTag": "v9",
			"dependencies": []any{"redpanda"}, "parameters": map[string]any{"topicName": "custom", "extraKey": "extra"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			componentDir := filepath.Join(profileChartDir(t), "components", tt.component)
			standalone, err := renderChart(loadChart(t, componentDir), "xmc-profile", tt.overrides)
			if err != nil {
				t.Fatalf("renderChart(%s) failed: %v", tt.component, err)
			}
			want := decodeDocs(t, standalone[tt.component+"/templates/component.yaml"])
			got := docByName(t, renderProfileDocs(t, profileChart(t), componentOverrides(tt.component, tt.overrides)), tt.component)
			if diff := cmp.Diff(want, []map[string]any{got}); diff != "" {
				t.Errorf("profile-rendered %s differs from standalone chart (-standalone +profile):\n%s", tt.component, diff)
			}
		})
	}
}

func TestProfileChart_HonorsExplicitFalsyOverrides(t *testing.T) {
	t.Parallel()

	docs := renderProfileDocs(t, profileChart(t), map[string]any{"global": map[string]any{"imageTag": "v2"}, "components": map[string]any{
		"redpanda": map[string]any{"deletionProtection": false}, "ingestion-gateway": map[string]any{"enabled": true, "dependencies": []any{}}}})
	tests := []struct {
		name      string
		component string
		field     string
		want      any
	}{
		{name: "deletionProtection false wins over default true", component: "redpanda", field: "deletionProtection", want: false},
		{name: "untouched component keeps default", component: "clickhouse", field: "deletionProtection", want: true},
		{name: "empty dependencies list replaces defaults", component: "ingestion-gateway", field: "dependencies", want: nil},
		{name: "global imageTag reaches component", component: "ingestion-gateway", field: "imageTag", want: "v2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if diff := cmp.Diff(tt.want, lookup(docByName(t, docs, tt.component), "spec", tt.field)); diff != "" {
				t.Errorf("%s spec.%s mismatch (-want +got):\n%s", tt.component, tt.field, diff)
			}
		})
	}
}

func TestProfileChart_RejectsInvalidInstalls(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		releaseName string
		overrides   map[string]any
		wantErr     string
	}{
		{name: "componentClass override", overrides: componentOverrides("redpanda", map[string]any{"componentClass": "x"}), wantErr: "componentClass"},
		{name: "chartVersion override", overrides: componentOverrides("clickhouse", map[string]any{"chartVersion": "9.9.9"}), wantErr: "chartVersion"},
		{name: "workload key override", overrides: componentOverrides("ingestion-gateway", map[string]any{"deployWorkload": true}), wantErr: "deployWorkload"},
		{name: "unknown component", overrides: componentOverrides("kafka", map[string]any{"enabled": true}), wantErr: "kafka"},
		{name: "unknown 3P parameter", overrides: componentOverrides("redpanda", map[string]any{"parameters": map[string]any{"bogus": "1"}}), wantErr: "bogus"},
		{name: "invalid deletionPolicy", overrides: componentOverrides("redpanda", map[string]any{"deletionPolicy": "Keep"}), wantErr: "deletionPolicy"},
		{name: "invalid namespace", overrides: componentOverrides("clickhouse", map[string]any{"namespace": "Bad_NS"}), wantErr: "namespace"},
		{name: "empty namespace", overrides: componentOverrides("clickhouse", map[string]any{"namespace": ""}), wantErr: "namespace"},
		{name: "wrong platformProfile", overrides: map[string]any{"global": map[string]any{"platformProfile": "stable-2026b"}}, wantErr: "platformProfile"},
		{name: "xmc-bridge component release name", releaseName: "xmc-cmp-redpanda", wantErr: "must not be installed as an xmc-cmp-* component release"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			releaseName := tt.releaseName
			if releaseName == "" {
				releaseName = "xmc-profile"
			}
			_, err := renderChart(profileChart(t), releaseName, tt.overrides)
			assertErrorContains(t, err, tt.wantErr)
		})
	}
}

// loadChartWithFileEdit loads the profile chart and, in memory only, replaces the content of the
// file at path with data, or removes the file when remove is true.
func loadChartWithFileEdit(t *testing.T, path, data string, remove bool) *chart.Chart {
	t.Helper()
	ch := profileChart(t)
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

// TestProfileChart_ComponentFilePresence checks that an empty component values.yaml is tolerated,
// while missing files and invalid YAML fail rendering with a message naming the file.
func TestProfileChart_ComponentFilePresence(t *testing.T) {
	t.Parallel()

	const dir = "components/redpanda/"
	missing := func(file string) string { return fmt.Sprintf("required file %q is missing", dir+file) }
	invalid := func(file string) string { return fmt.Sprintf("xmc-profile: %s: error converting YAML", dir+file) }
	tests := []struct {
		name      string
		file      string
		data      string
		remove    bool
		overrides map[string]any
		want      map[string]any
		wantErr   string
	}{
		{name: "empty values.yaml renders template defaults", file: "values.yaml",
			want: map[string]any{"enabled": false, "deletionProtection": true, "kafkaPort": "9092"}},
		{name: "empty values.yaml still applies overrides", file: "values.yaml",
			overrides: componentOverrides("redpanda", map[string]any{"enabled": true, "deletionProtection": false, "parameters": map[string]any{"kafkaPort": 19092}}),
			want:      map[string]any{"enabled": true, "deletionProtection": false, "kafkaPort": "19092"}},
		{name: "missing Chart.yaml", file: "Chart.yaml", remove: true, wantErr: missing("Chart.yaml")},
		{name: "missing values.yaml", file: "values.yaml", remove: true, wantErr: missing("values.yaml")},
		{name: "missing component.yaml", file: "templates/component.yaml", remove: true, wantErr: missing("templates/component.yaml")},
		{name: "invalid Chart.yaml", file: "Chart.yaml", data: "name: [redpanda\n", wantErr: invalid("Chart.yaml")},
		{name: "invalid values.yaml", file: "values.yaml", data: "enabled: [true\n", wantErr: invalid("values.yaml")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ch := loadChartWithFileEdit(t, dir+tt.file, tt.data, tt.remove)
			if tt.wantErr != "" {
				_, err := renderChart(ch, "xmc-profile", tt.overrides)
				assertErrorContains(t, err, tt.wantErr)
				return
			}
			spec := lookup(docByName(t, renderProfileDocs(t, ch, tt.overrides), "redpanda"), "spec")
			got := map[string]any{"enabled": lookup(spec, "enabled"), "deletionProtection": lookup(spec, "deletionProtection"), "kafkaPort": lookup(spec, "parameters", "kafkaPort")}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("redpanda spec mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// readProfileSources decodes profile.yaml, Chart.yaml, values.yaml and values.schema.json (JSON
// is a subset of YAML) from chartDir, keyed by file name.
func readProfileSources(t *testing.T, chartDir string) map[string]any {
	t.Helper()
	src := map[string]any{}
	for _, name := range []string{"profile.yaml", "Chart.yaml", "values.yaml", "values.schema.json"} {
		data, err := os.ReadFile(filepath.Join(chartDir, name))
		if err != nil {
			t.Fatalf("failed to read %s: %v", name, err)
		}
		var doc map[string]any
		if err := yaml.Unmarshal(data, &doc); err != nil {
			t.Fatalf("failed to parse %s: %v", name, err)
		}
		src[name] = doc
	}
	return src
}

// sortedKeys returns the sorted keys of v if it is a decoded YAML mapping, or nil otherwise.
func sortedKeys(v any) []string {
	m, _ := v.(map[string]any)
	return slices.Sorted(maps.Keys(m))
}

// TestProfileChart_CatalogIsConsistent guards against catalog drift: a component added to or
// removed from one place must be added to or removed from all of them.
func TestProfileChart_CatalogIsConsistent(t *testing.T) {
	t.Parallel()

	chartDir := profileChartDir(t)
	chartFiles, err := filepath.Glob(filepath.Join(chartDir, "components", "*", "Chart.yaml"))
	if err != nil {
		t.Fatalf("failed to list component charts: %v", err)
	}
	var componentDirs, rendered []string
	for _, f := range chartFiles {
		componentDirs = append(componentDirs, filepath.Base(filepath.Dir(f)))
	}
	for _, doc := range renderProfileDocs(t, profileChart(t), nil) {
		rendered = append(rendered, docName(doc))
	}
	src := readProfileSources(t, chartDir)
	catalogs := map[string][]string{
		"templates/components.yaml": slices.Sorted(slices.Values(rendered)),
		"values.yaml":               sortedKeys(lookup(src, "values.yaml", "components")),
		"values.schema.json":        sortedKeys(lookup(src, "values.schema.json", "properties", "components", "properties")),
	}
	for source, names := range catalogs {
		if diff := cmp.Diff(slices.Sorted(slices.Values(componentDirs)), names); diff != "" {
			t.Errorf("%s catalog differs from components/ directories (-dirs +%s):\n%s", source, source, diff)
		}
	}
}

func TestProfileMetadata_IsConsistent(t *testing.T) {
	t.Parallel()

	src := readProfileSources(t, profileChartDir(t))
	name, _ := lookup(src, "profile.yaml", "name").(string)
	if name == "" {
		t.Fatal("profile.yaml name is empty")
	}
	want := map[string]any{"Chart.yaml annotation": name, "values.yaml": name, "values.schema.json": name}
	got := map[string]any{
		"Chart.yaml annotation": lookup(src, "Chart.yaml", "annotations", "xmc.dev/profile"),
		"values.yaml":           lookup(src, "values.yaml", "global", "platformProfile"),
		"values.schema.json":    lookup(src, "values.schema.json", "properties", "global", "properties", "platformProfile", "const"),
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("profile name %q is not used consistently (-want +got):\n%s", name, diff)
	}
	if v := fmt.Sprint(lookup(src, "profile.yaml", "minXmcVersion")); !regexp.MustCompile(`^\d+\.\d+\.\d+$`).MatchString(v) {
		t.Errorf("profile.yaml minXmcVersion = %q, want MAJOR.MINOR.PATCH", v)
	}
	window := lookup(src, "profile.yaml", "supportWindow")
	assertSupportWindow(t, fmt.Sprint(lookup(window, "start")), fmt.Sprint(lookup(window, "end")))
}

// assertSupportWindow checks both bounds are YYYY-MM months and start precedes end.
func assertSupportWindow(t *testing.T, start, end string) {
	t.Helper()
	startMonth, startErr := time.Parse("2006-01", start)
	endMonth, endErr := time.Parse("2006-01", end)
	if startErr != nil || endErr != nil {
		t.Fatalf("profile.yaml supportWindow = {start: %q, end: %q}, want YYYY-MM months (errors: %v, %v)", start, end, startErr, endErr)
	}
	if !startMonth.Before(endMonth) {
		t.Errorf("profile.yaml supportWindow start %q must be before end %q", start, end)
	}
}
