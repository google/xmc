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
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// contractCase is one verification of a contract check. verify returns nil when the profile
// satisfies it. An empty name runs the case directly in the check's subtest.
type contractCase struct {
	name   string
	verify func() error
}

// contractCheck is a named group of cases built from a Spec.
type contractCheck struct {
	name  string
	cases func(Spec) []contractCase
}

var contractChecks = []contractCheck{
	{name: "RendersOnlyComponents", cases: rendersOnlyComponentsCases},
	{name: "MatchesStandaloneDefaults", cases: matchesStandaloneDefaultsCases},
	{name: "RejectsInvalidInstalls", cases: rejectsInvalidInstallsCases},
	{name: "ComponentFileErrors", cases: componentFileErrorsCases},
	{name: "CatalogIsConsistent", cases: func(s Spec) []contractCase {
		return []contractCase{{verify: func() error { return catalogConsistency(s) }}}
	}},
	{name: "MetadataIsConsistent", cases: func(s Spec) []contractCase {
		return []contractCase{{verify: func() error { return metadataConsistency(s) }}}
	}},
}

// Run checks the generic profile meta-chart contract as parallel subtests:
//   - RendersOnlyComponents: only the catalog Component CRs are rendered, in s.Components order.
//   - MatchesStandaloneDefaults: each component renders like its standalone chart.
//   - RejectsInvalidInstalls: the values schema and the release-name guard reject invalid installs.
//   - ComponentFileErrors: missing or invalid component files fail with a message naming the file.
//   - CatalogIsConsistent: components/ directories, values.yaml, the schema and the render agree.
//   - MetadataIsConsistent: the profile name and profile.yaml metadata are consistent.
func Run(t *testing.T, s Spec) {
	t.Helper()
	fatalOnError(t, validateSpec(s))
	for _, c := range contractChecks {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			for _, cc := range c.cases(s) {
				if cc.name == "" {
					reportError(t, cc.verify())
					continue
				}
				t.Run(cc.name, func(t *testing.T) {
					t.Parallel()
					reportError(t, cc.verify())
				})
			}
		})
	}
}

func reportError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Error(err)
	}
}

// validateSpec rejects a Spec that the contract cannot meaningfully check: without a chart, a
// profile name or a catalog, the checks would fail obscurely or pass vacuously.
func validateSpec(s Spec) error {
	fields := []struct {
		name  string
		empty bool
	}{
		{name: "ChartRelPath", empty: s.ChartRelPath == ""},
		{name: "ProfileName", empty: s.ProfileName == ""},
		{name: "Components", empty: len(s.Components) == 0},
	}
	var errs []error
	for _, f := range fields {
		if f.empty {
			errs = append(errs, fmt.Errorf("profilecontract: Spec.%s must not be empty", f.name))
		}
	}
	return errors.Join(errs...)
}

// AssertRendersComponents checks that rendering the profile with overrides produces exactly the
// Component CRs of s.Components, in order, and nothing else.
func AssertRendersComponents(t *testing.T, s Spec, overrides map[string]any) {
	t.Helper()
	reportError(t, rendersComponents(s, overrides))
}

func rendersComponents(s Spec, overrides map[string]any) error {
	ch, err := loadProfile(s)
	if err != nil {
		return err
	}
	docs, err := renderDocs(ch, overrides)
	if err != nil {
		return err
	}
	var want, got []string
	for _, name := range s.Components {
		want = append(want, "Component/"+name)
	}
	for _, doc := range docs {
		got = append(got, fmt.Sprintf("%v/%s", doc["kind"], DocName(doc)))
	}
	if diff := cmp.Diff(want, got); diff != "" {
		return fmt.Errorf("rendered kind/name mismatch (-want +got):\n%s", diff)
	}
	return nil
}

// AssertMatchesStandalone checks that the profile renders component, given the per-component
// overrides, exactly like the component's standalone chart rendered with the same values.
func AssertMatchesStandalone(t *testing.T, s Spec, component string, overrides map[string]any) {
	t.Helper()
	reportError(t, matchesStandalone(s, component, overrides))
}

func matchesStandalone(s Spec, component string, overrides map[string]any) error {
	want, err := renderStandalone(s, component, overrides)
	if err != nil {
		return err
	}
	ch, err := loadProfile(s)
	if err != nil {
		return err
	}
	// Without overrides, render the profile as is: a nil component override would make Helm drop
	// the profile's own components.<name> defaults and hide drift from the standalone chart.
	var profileOverrides map[string]any
	if overrides != nil {
		profileOverrides = ComponentOverrides(component, overrides)
	}
	docs, err := renderDocs(ch, profileOverrides)
	if err != nil {
		return err
	}
	got, err := docByName(docs, component)
	if err != nil {
		return err
	}
	if diff := cmp.Diff(want, []map[string]any{got}); diff != "" {
		return fmt.Errorf("profile-rendered %s differs from standalone chart (-standalone +profile):\n%s", component, diff)
	}
	return nil
}

// renderStandalone renders the Component CR of the standalone chart components/<component>.
func renderStandalone(s Spec, component string, overrides map[string]any) ([]map[string]any, error) {
	dir, err := chartDir(s)
	if err != nil {
		return nil, err
	}
	ch, err := loadChart(filepath.Join(dir, "components", component))
	if err != nil {
		return nil, err
	}
	rendered, err := Render(ch, DefaultRelease, overrides)
	if err != nil {
		return nil, fmt.Errorf("Render(%s) failed: %w", component, err)
	}
	return decodeDocs(rendered[component+"/templates/component.yaml"])
}

// AssertRenderFails checks that rendering the profile as releaseName with overrides fails with an
// error containing wantErr.
func AssertRenderFails(t *testing.T, s Spec, releaseName string, overrides map[string]any, wantErr string) {
	t.Helper()
	reportError(t, renderFails(s, releaseName, overrides, wantErr))
}

func renderFails(s Spec, releaseName string, overrides map[string]any, wantErr string) error {
	ch, err := loadProfile(s)
	if err != nil {
		return err
	}
	_, err = Render(ch, releaseName, overrides)
	return errorContains(err, wantErr)
}

func rendersOnlyComponentsCases(s Spec) []contractCase {
	allEnabled := map[string]any{}
	for _, name := range s.Components {
		allEnabled[name] = map[string]any{"enabled": true}
	}
	tests := []struct {
		name      string
		overrides map[string]any
	}{
		{name: "default values"},
		{name: "all components enabled", overrides: map[string]any{"components": allEnabled}},
		{name: "wrapper-chart global key", overrides: map[string]any{"global": map[string]any{"foo": "bar"}}},
	}
	var cases []contractCase
	for _, tt := range tests {
		cases = append(cases, contractCase{name: tt.name, verify: func() error { return rendersComponents(s, tt.overrides) }})
	}
	return cases
}

func matchesStandaloneDefaultsCases(s Spec) []contractCase {
	var cases []contractCase
	for _, name := range s.Components {
		cases = append(cases, contractCase{name: name, verify: func() error { return matchesStandalone(s, name, nil) }})
	}
	return cases
}

// invalidInstall is a render that the profile must reject with an error containing wantErr.
type invalidInstall struct {
	name, releaseName, wantErr string
	overrides                  map[string]any
}

func rejectsInvalidInstallsCases(s Spec) []contractCase {
	tests := []invalidInstall{
		{name: "unknown component", overrides: ComponentOverrides("not-a-catalog-component", map[string]any{"enabled": true}), wantErr: "not-a-catalog-component"},
		{name: "wrong platformProfile", overrides: map[string]any{"global": map[string]any{"platformProfile": "not-" + s.ProfileName}}, wantErr: "platformProfile"},
		{name: "xmc-bridge component release name", releaseName: "xmc-cmp-" + DefaultRelease, wantErr: "must not be installed as an xmc-cmp-* component release"},
	}
	for _, c := range s.Components {
		tests = append(tests, componentKeyRejections(c)...)
	}
	var cases []contractCase
	for _, tt := range tests {
		releaseName := tt.releaseName
		if releaseName == "" {
			releaseName = DefaultRelease
		}
		cases = append(cases, contractCase{name: tt.name, verify: func() error { return renderFails(s, releaseName, tt.overrides, tt.wantErr) }})
	}
	return cases
}

// componentKeyRejections returns the invalid per-component values that every catalog component
// must reject: keys the profile reserves, and malformed deletionPolicy and namespace values.
func componentKeyRejections(c string) []invalidInstall {
	reject := func(name, key string, val any) invalidInstall {
		return invalidInstall{name: name + "/" + c, overrides: ComponentOverrides(c, map[string]any{key: val}), wantErr: key}
	}
	return []invalidInstall{
		reject("componentClass override", "componentClass", "x"),
		reject("chartVersion override", "chartVersion", "9.9.9"),
		reject("invalid deletionPolicy", "deletionPolicy", "Keep"),
		reject("invalid namespace", "namespace", "Bad_NS"),
		reject("empty namespace", "namespace", ""),
	}
}

func componentFileErrorsCases(s Spec) []contractCase {
	tests := []struct {
		name, file, data string
		remove           bool
		wantErr          func(path string) string
	}{
		{name: "missing Chart.yaml", file: "Chart.yaml", remove: true, wantErr: missingFileError},
		{name: "missing values.yaml", file: "values.yaml", remove: true, wantErr: missingFileError},
		{name: "missing component.yaml", file: "templates/component.yaml", remove: true, wantErr: missingFileError},
		{name: "invalid Chart.yaml", file: "Chart.yaml", data: "name: [x\n", wantErr: invalidYAMLError},
		{name: "invalid values.yaml", file: "values.yaml", data: "enabled: [true\n", wantErr: invalidYAMLError},
	}
	var cases []contractCase
	for _, c := range s.Components {
		for _, tt := range tests {
			path := fmt.Sprintf("components/%s/%s", c, tt.file)
			cases = append(cases, contractCase{name: tt.name + "/" + c, verify: func() error {
				ch, err := loadProfileWithFileEdit(s, path, tt.data, tt.remove)
				if err != nil {
					return err
				}
				_, err = Render(ch, DefaultRelease, nil)
				return errorContains(err, tt.wantErr(path))
			}})
		}
	}
	return cases
}

func missingFileError(path string) string { return fmt.Sprintf("required file %q is missing", path) }

func invalidYAMLError(path string) string {
	return fmt.Sprintf("xmc-profile: %s: error converting YAML", path)
}
