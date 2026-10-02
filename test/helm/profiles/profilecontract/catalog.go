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
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"gopkg.in/yaml.v3"
)

// checkCatalogIsConsistent guards against catalog drift: a component added to or removed from one
// place must be added to or removed from all of them.
func checkCatalogIsConsistent(t *testing.T, s Spec) {
	chartDir := ChartDir(t, s)
	chartFiles, err := filepath.Glob(filepath.Join(chartDir, "components", "*", "Chart.yaml"))
	if err != nil {
		t.Fatalf("failed to list component charts: %v", err)
	}
	var componentDirs, rendered []string
	for _, f := range chartFiles {
		componentDirs = append(componentDirs, filepath.Base(filepath.Dir(f)))
	}
	for _, doc := range RenderDocs(t, LoadProfile(t, s), nil) {
		rendered = append(rendered, DocName(doc))
	}
	src := readProfileSources(t, chartDir)
	catalogs := map[string][]string{
		"templates/components.yaml": slices.Sorted(slices.Values(rendered)),
		"values.yaml":               sortedKeys(Lookup(src, "values.yaml", "components")),
		"values.schema.json":        sortedKeys(Lookup(src, "values.schema.json", "properties", "components", "properties")),
	}
	for source, names := range catalogs {
		if diff := cmp.Diff(slices.Sorted(slices.Values(componentDirs)), names); diff != "" {
			t.Errorf("%s catalog differs from components/ directories (-dirs +%s):\n%s", source, source, diff)
		}
	}
}

// checkMetadataIsConsistent checks that s.ProfileName is used in every place that names the
// profile, and that profile.yaml minXmcVersion and supportWindow are well-formed.
func checkMetadataIsConsistent(t *testing.T, s Spec) {
	src := readProfileSources(t, ChartDir(t, s))
	want := map[string]any{"profile.yaml": s.ProfileName, "Chart.yaml annotation": s.ProfileName, "values.yaml": s.ProfileName, "values.schema.json": s.ProfileName}
	got := map[string]any{
		"profile.yaml":          Lookup(src, "profile.yaml", "name"),
		"Chart.yaml annotation": Lookup(src, "Chart.yaml", "annotations", "xmc.dev/profile"),
		"values.yaml":           Lookup(src, "values.yaml", "global", "platformProfile"),
		"values.schema.json":    Lookup(src, "values.schema.json", "properties", "global", "properties", "platformProfile", "const"),
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("profile name %q is not used consistently (-want +got):\n%s", s.ProfileName, diff)
	}
	if v := fmt.Sprint(Lookup(src, "profile.yaml", "minXmcVersion")); !regexp.MustCompile(`^\d+\.\d+\.\d+$`).MatchString(v) {
		t.Errorf("profile.yaml minXmcVersion = %q, want MAJOR.MINOR.PATCH", v)
	}
	window := Lookup(src, "profile.yaml", "supportWindow")
	assertSupportWindow(t, fmt.Sprint(Lookup(window, "start")), fmt.Sprint(Lookup(window, "end")))
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
