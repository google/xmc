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
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"time"

	"github.com/google/go-cmp/cmp"
	"gopkg.in/yaml.v3"
)

// catalogConsistency guards against catalog drift: a component added to or removed from one
// place must be added to or removed from all of them.
func catalogConsistency(s Spec) error {
	chartDir, err := chartDir(s)
	if err != nil {
		return err
	}
	chartFiles, _ := filepath.Glob(filepath.Join(chartDir, "components", "*", "Chart.yaml")) // The pattern is valid.
	var componentDirs, rendered []string
	for _, f := range chartFiles {
		componentDirs = append(componentDirs, filepath.Base(filepath.Dir(f)))
	}
	ch, err := loadChart(chartDir)
	if err != nil {
		return err
	}
	docs, err := renderDocs(ch, nil)
	if err != nil {
		return err
	}
	for _, doc := range docs {
		rendered = append(rendered, DocName(doc))
	}
	src, err := readProfileSources(chartDir)
	if err != nil {
		return err
	}
	catalogs := map[string][]string{
		"templates/components.yaml": slices.Sorted(slices.Values(rendered)),
		"values.yaml":               sortedKeys(Lookup(src, "values.yaml", "components")),
		"values.schema.json":        sortedKeys(Lookup(src, "values.schema.json", "properties", "components", "properties")),
	}
	var errs []error
	for _, source := range slices.Sorted(maps.Keys(catalogs)) {
		if diff := cmp.Diff(slices.Sorted(slices.Values(componentDirs)), catalogs[source]); diff != "" {
			errs = append(errs, fmt.Errorf("%s catalog differs from components/ directories (-dirs +%s):\n%s", source, source, diff))
		}
	}
	return errors.Join(errs...)
}

// metadataConsistency checks that s.ProfileName is used in every place that names the profile,
// and that profile.yaml minXmcVersion and supportWindow are well-formed.
func metadataConsistency(s Spec) error {
	dir, err := chartDir(s)
	if err != nil {
		return err
	}
	src, err := readProfileSources(dir)
	if err != nil {
		return err
	}
	var errs []error
	want := map[string]any{"profile.yaml": s.ProfileName, "Chart.yaml annotation": s.ProfileName, "values.yaml": s.ProfileName, "values.schema.json": s.ProfileName}
	got := map[string]any{
		"profile.yaml":          Lookup(src, "profile.yaml", "name"),
		"Chart.yaml annotation": Lookup(src, "Chart.yaml", "annotations", "xmc.dev/profile"),
		"values.yaml":           Lookup(src, "values.yaml", "global", "platformProfile"),
		"values.schema.json":    Lookup(src, "values.schema.json", "properties", "global", "properties", "platformProfile", "const"),
	}
	if diff := cmp.Diff(want, got); diff != "" {
		errs = append(errs, fmt.Errorf("profile name %q is not used consistently (-want +got):\n%s", s.ProfileName, diff))
	}
	if v := fmt.Sprint(Lookup(src, "profile.yaml", "minXmcVersion")); !regexp.MustCompile(`^\d+\.\d+\.\d+$`).MatchString(v) {
		errs = append(errs, fmt.Errorf("profile.yaml minXmcVersion = %q, want MAJOR.MINOR.PATCH", v))
	}
	window := Lookup(src, "profile.yaml", "supportWindow")
	errs = append(errs, supportWindowError(fmt.Sprint(Lookup(window, "start")), fmt.Sprint(Lookup(window, "end"))))
	return errors.Join(errs...)
}

// readProfileSources decodes profile.yaml, Chart.yaml, values.yaml and values.schema.json (JSON
// is a subset of YAML) from chartDir, keyed by file name.
func readProfileSources(chartDir string) (map[string]any, error) {
	src := map[string]any{}
	for _, name := range []string{"profile.yaml", "Chart.yaml", "values.yaml", "values.schema.json"} {
		data, err := os.ReadFile(filepath.Join(chartDir, name))
		if err != nil {
			return nil, fmt.Errorf("failed to read %s: %w", name, err)
		}
		var doc map[string]any
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return nil, fmt.Errorf("failed to parse %s: %w", name, err)
		}
		src[name] = doc
	}
	return src, nil
}

// sortedKeys returns the sorted keys of v if it is a decoded YAML mapping, or nil otherwise.
func sortedKeys(v any) []string {
	m, _ := v.(map[string]any)
	return slices.Sorted(maps.Keys(m))
}

// supportWindowError checks both bounds are YYYY-MM months and start precedes end.
func supportWindowError(start, end string) error {
	startMonth, startErr := time.Parse("2006-01", start)
	endMonth, endErr := time.Parse("2006-01", end)
	if startErr != nil || endErr != nil {
		return fmt.Errorf("profile.yaml supportWindow = {start: %q, end: %q}, want YYYY-MM months (errors: %v, %v)", start, end, startErr, endErr)
	}
	if !startMonth.Before(endMonth) {
		return fmt.Errorf("profile.yaml supportWindow start %q must be before end %q", start, end)
	}
	return nil
}
