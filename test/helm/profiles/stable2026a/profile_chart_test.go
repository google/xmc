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
	"encoding/json"
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

// catalogOrder is the dependency order in which templates/components.yaml renders components.
var catalogOrder = []string{"redpanda", "clickhouse", "ingestion-gateway"}

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

// renderChart renders the chart at chartDir with overrides through the Helm SDK, including
// .helmignore filtering and values.schema.json validation, and returns rendered templates keyed
// by "<chart>/<path>".
func renderChart(chartDir, releaseName string, overrides map[string]any) (map[string]string, error) {
	ch, err := loader.Load(chartDir)
	if err != nil {
		return nil, fmt.Errorf("failed to load chart %s: %w", chartDir, err)
	}
	return renderLoadedChart(ch, releaseName, overrides)
}

// renderLoadedChart renders an already loaded chart, so tests can modify ch.Files in memory
// before rendering.
func renderLoadedChart(ch *chart.Chart, releaseName string, overrides map[string]any) (map[string]string, error) {
	opts := chartutil.ReleaseOptions{Name: releaseName, Namespace: "xmc", Revision: 1, IsInstall: true}
	vals, err := chartutil.ToRenderValues(ch, overrides, opts, nil)
	if err != nil {
		return nil, err
	}
	return engine.Render(ch, vals)
}

// renderProfileDocs renders the profile meta-chart and returns its decoded YAML documents.
func renderProfileDocs(t *testing.T, overrides map[string]any) []map[string]any {
	t.Helper()
	rendered, err := renderChart(profileChartDir(t), "xmc-profile", overrides)
	if err != nil {
		t.Fatalf("renderChart(profile, %v) failed: %v", overrides, err)
	}
	return decodeDocs(t, rendered[renderedComponentsKey])
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

func docName(doc map[string]any) string {
	meta, _ := doc["metadata"].(map[string]any)
	name, _ := meta["name"].(string)
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

	allEnabled := map[string]any{"components": map[string]any{
		"redpanda":          map[string]any{"enabled": true},
		"clickhouse":        map[string]any{"enabled": true},
		"ingestion-gateway": map[string]any{"enabled": true, "imageTag": "v1.2.3"},
	}}
	tests := []struct {
		name      string
		overrides map[string]any
	}{
		{name: "default values"},
		{name: "all components enabled", overrides: allEnabled},
		{name: "wrapper-chart global key", overrides: map[string]any{"global": map[string]any{"foo": "bar"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var got, want []string
			for _, doc := range renderProfileDocs(t, tt.overrides) {
				got = append(got, fmt.Sprintf("%v/%s", doc["kind"], docName(doc)))
			}
			for _, name := range catalogOrder {
				want = append(want, "Component/"+name)
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
		{name: "redpanda overrides", component: "redpanda", overrides: map[string]any{
			"enabled": true, "namespace": "data", "deletionProtection": false, "deletionPolicy": "Orphan",
			"parameters": map[string]any{"kafkaPort": 19092}, "dependencies": []any{"clickhouse"},
		}},
		{name: "clickhouse overrides", component: "clickhouse", overrides: map[string]any{
			"enabled": true, "parameters": map[string]any{"host": "ch.example.internal", "httpPort": 18123},
		}},
		{name: "ingestion-gateway overrides", component: "ingestion-gateway", overrides: map[string]any{
			"enabled": true, "imageTag": "v9", "dependencies": []any{"redpanda"},
			"parameters": map[string]any{"topicName": "custom", "extraKey": "extra"},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			componentDir := filepath.Join(profileChartDir(t), "components", tt.component)
			standalone, err := renderChart(componentDir, "xmc-profile", tt.overrides)
			if err != nil {
				t.Fatalf("renderChart(%s) failed: %v", tt.component, err)
			}
			want := decodeDocs(t, standalone[tt.component+"/templates/component.yaml"])
			got := docByName(t, renderProfileDocs(t, componentOverrides(tt.component, tt.overrides)), tt.component)
			if diff := cmp.Diff(want, []map[string]any{got}); diff != "" {
				t.Errorf("profile-rendered %s differs from standalone chart (-standalone +profile):\n%s", tt.component, diff)
			}
		})
	}
}

func TestProfileChart_HonorsExplicitFalsyOverrides(t *testing.T) {
	t.Parallel()

	docs := renderProfileDocs(t, map[string]any{
		"global": map[string]any{"imageTag": "v2"},
		"components": map[string]any{
			"redpanda":          map[string]any{"deletionProtection": false},
			"ingestion-gateway": map[string]any{"enabled": true, "dependencies": []any{}},
		},
	})
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
			spec, _ := docByName(t, docs, tt.component)["spec"].(map[string]any)
			if diff := cmp.Diff(tt.want, spec[tt.field]); diff != "" {
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
			_, err := renderChart(profileChartDir(t), releaseName, tt.overrides)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("renderChart() error = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}

// loadChartWithFileEdit loads the profile chart and, in memory only, replaces the content of the
// file at path with data, or removes the file when remove is true.
func loadChartWithFileEdit(t *testing.T, path, data string, remove bool) *chart.Chart {
	t.Helper()
	ch, err := loader.Load(profileChartDir(t))
	if err != nil {
		t.Fatalf("failed to load profile chart: %v", err)
	}
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

// redpandaSpecSummary extracts the redpanda spec fields asserted by the file presence tests.
func redpandaSpecSummary(t *testing.T, rendered map[string]string) map[string]any {
	t.Helper()
	spec, _ := docByName(t, decodeDocs(t, rendered[renderedComponentsKey]), "redpanda")["spec"].(map[string]any)
	params, _ := spec["parameters"].(map[string]any)
	return map[string]any{
		"enabled":            spec["enabled"],
		"deletionProtection": spec["deletionProtection"],
		"kafkaPort":          params["kafkaPort"],
	}
}

// TestProfileChart_ComponentFilePresence checks that empty component files are tolerated, while
// missing files and invalid YAML fail rendering with a message naming the file.
func TestProfileChart_ComponentFilePresence(t *testing.T) {
	t.Parallel()

	const chartPath = "components/redpanda/Chart.yaml"
	const valuesPath = "components/redpanda/values.yaml"
	const templatePath = "components/redpanda/templates/component.yaml"
	tests := []struct {
		name      string
		path      string
		data      string
		remove    bool
		overrides map[string]any
		want      map[string]any
		wantErr   string
	}{
		{name: "empty values.yaml renders template defaults", path: valuesPath,
			want: map[string]any{"enabled": false, "deletionProtection": true, "kafkaPort": "9092"}},
		{name: "empty values.yaml still applies overrides", path: valuesPath,
			overrides: componentOverrides("redpanda", map[string]any{"enabled": true, "deletionProtection": false, "parameters": map[string]any{"kafkaPort": 19092}}),
			want:      map[string]any{"enabled": true, "deletionProtection": false, "kafkaPort": "19092"}},
		{name: "missing Chart.yaml fails", path: chartPath, remove: true, wantErr: fmt.Sprintf("required file %q is missing", chartPath)},
		{name: "invalid Chart.yaml fails", path: chartPath, data: "name: [redpanda\n", wantErr: fmt.Sprintf("xmc-profile: %s: error converting YAML", chartPath)},
		{name: "invalid values.yaml fails", path: valuesPath, data: "enabled: [true\n", wantErr: fmt.Sprintf("xmc-profile: %s: error converting YAML", valuesPath)},
		{name: "missing values.yaml fails", path: valuesPath, remove: true, wantErr: fmt.Sprintf("required file %q is missing", valuesPath)},
		{name: "missing component.yaml fails", path: templatePath, remove: true, wantErr: fmt.Sprintf("required file %q is missing", templatePath)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rendered, err := renderLoadedChart(loadChartWithFileEdit(t, tt.path, tt.data, tt.remove), "xmc-profile", tt.overrides)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("renderLoadedChart() error = %v, want error containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("renderLoadedChart() failed: %v", err)
			}
			if diff := cmp.Diff(tt.want, redpandaSpecSummary(t, rendered)); diff != "" {
				t.Errorf("redpanda spec mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestProfileChart_CatalogIsConsistent guards against catalog drift: a component added to or
// removed from one place must be added to or removed from all of them.
func TestProfileChart_CatalogIsConsistent(t *testing.T) {
	t.Parallel()

	chartDir := profileChartDir(t)
	entries, err := os.ReadDir(filepath.Join(chartDir, "components"))
	if err != nil {
		t.Fatalf("failed to list components/: %v", err)
	}
	var componentDirs []string
	for _, e := range entries {
		if e.IsDir() {
			componentDirs = append(componentDirs, e.Name())
		}
	}
	var rendered []string
	for _, doc := range renderProfileDocs(t, nil) {
		rendered = append(rendered, docName(doc))
	}
	var values struct {
		Components map[string]any `yaml:"components"`
	}
	readYAML(t, filepath.Join(chartDir, "values.yaml"), &values)
	var schema struct {
		Properties struct {
			Components struct {
				Properties map[string]any `json:"properties"`
			} `json:"components"`
		} `json:"properties"`
	}
	readJSON(t, filepath.Join(chartDir, "values.schema.json"), &schema)

	wantDirs := slices.Sorted(slices.Values(componentDirs))
	catalogs := map[string][]string{
		"templates/components.yaml": slices.Sorted(slices.Values(rendered)),
		"values.yaml":               slices.Sorted(maps.Keys(values.Components)),
		"values.schema.json":        slices.Sorted(maps.Keys(schema.Properties.Components.Properties)),
	}
	for source, names := range catalogs {
		if diff := cmp.Diff(wantDirs, names); diff != "" {
			t.Errorf("%s catalog differs from components/ directories (-dirs +%s):\n%s", source, source, diff)
		}
	}
}

func TestProfileMetadata_IsConsistent(t *testing.T) {
	t.Parallel()

	chartDir := profileChartDir(t)
	var profile struct {
		Name          string `yaml:"name"`
		MinXmcVersion string `yaml:"minXmcVersion"`
		SupportWindow struct {
			Start string `yaml:"start"`
			End   string `yaml:"end"`
		} `yaml:"supportWindow"`
	}
	readYAML(t, filepath.Join(chartDir, "profile.yaml"), &profile)
	var chart struct {
		Annotations map[string]string `yaml:"annotations"`
	}
	readYAML(t, filepath.Join(chartDir, "Chart.yaml"), &chart)
	var values struct {
		Global struct {
			PlatformProfile string `yaml:"platformProfile"`
		} `yaml:"global"`
	}
	readYAML(t, filepath.Join(chartDir, "values.yaml"), &values)
	var schema struct {
		Properties struct {
			Global struct {
				Properties struct {
					PlatformProfile struct {
						Const string `json:"const"`
					} `json:"platformProfile"`
				} `json:"properties"`
			} `json:"global"`
		} `json:"properties"`
	}
	readJSON(t, filepath.Join(chartDir, "values.schema.json"), &schema)

	want := map[string]string{"Chart.yaml annotation": profile.Name, "values.yaml": profile.Name, "values.schema.json": profile.Name}
	got := map[string]string{
		"Chart.yaml annotation": chart.Annotations["xmc.dev/profile"],
		"values.yaml":           values.Global.PlatformProfile,
		"values.schema.json":    schema.Properties.Global.Properties.PlatformProfile.Const,
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("profile name %q is not used consistently (-want +got):\n%s", profile.Name, diff)
	}
	if !regexp.MustCompile(`^\d+\.\d+\.\d+$`).MatchString(profile.MinXmcVersion) {
		t.Errorf("profile.yaml minXmcVersion = %q, want MAJOR.MINOR.PATCH", profile.MinXmcVersion)
	}
	assertSupportWindow(t, profile.SupportWindow.Start, profile.SupportWindow.End)
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

func readYAML(t *testing.T, path string, out any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read %s: %v", path, err)
	}
	if err := yaml.Unmarshal(data, out); err != nil {
		t.Fatalf("failed to parse %s: %v", path, err)
	}
}

func readJSON(t *testing.T, path string, out any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read %s: %v", path, err)
	}
	if err := json.Unmarshal(data, out); err != nil {
		t.Fatalf("failed to parse %s: %v", path, err)
	}
}
