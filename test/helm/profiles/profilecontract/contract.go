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
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// Run checks the generic profile meta-chart contract as parallel subtests:
//   - RendersOnlyComponents: only the catalog Component CRs are rendered, in s.Components order.
//   - MatchesStandaloneDefaults: each component renders like its standalone chart.
//   - RejectsInvalidInstalls: the values schema and the release-name guard reject invalid installs.
//   - ComponentFileErrors: missing or invalid component files fail with a message naming the file.
//   - CatalogIsConsistent: components/ directories, values.yaml, the schema and the render agree.
//   - MetadataIsConsistent: the profile name and profile.yaml metadata are consistent.
func Run(t *testing.T, s Spec) {
	t.Helper()
	checks := []struct {
		name  string
		check func(*testing.T, Spec)
	}{
		{name: "RendersOnlyComponents", check: checkRendersOnlyComponents},
		{name: "MatchesStandaloneDefaults", check: checkMatchesStandaloneDefaults},
		{name: "RejectsInvalidInstalls", check: checkRejectsInvalidInstalls},
		{name: "ComponentFileErrors", check: checkComponentFileErrors},
		{name: "CatalogIsConsistent", check: checkCatalogIsConsistent},
		{name: "MetadataIsConsistent", check: checkMetadataIsConsistent},
	}
	for _, c := range checks {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			c.check(t, s)
		})
	}
}

// AssertRendersComponents checks that rendering the profile with overrides produces exactly the
// Component CRs of s.Components, in order, and nothing else.
func AssertRendersComponents(t *testing.T, s Spec, overrides map[string]any) {
	t.Helper()
	var want, got []string
	for _, name := range s.Components {
		want = append(want, "Component/"+name)
	}
	for _, doc := range RenderDocs(t, LoadProfile(t, s), overrides) {
		got = append(got, fmt.Sprintf("%v/%s", doc["kind"], DocName(doc)))
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("rendered kind/name mismatch (-want +got):\n%s", diff)
	}
}

// AssertMatchesStandalone checks that the profile renders component, given the per-component
// overrides, exactly like the component's standalone chart rendered with the same values.
func AssertMatchesStandalone(t *testing.T, s Spec, component string, overrides map[string]any) {
	t.Helper()
	standalone, err := Render(LoadChart(t, filepath.Join(ChartDir(t, s), "components", component)), DefaultRelease, overrides)
	if err != nil {
		t.Fatalf("Render(%s) failed: %v", component, err)
	}
	want := DecodeDocs(t, standalone[component+"/templates/component.yaml"])
	got := DocByName(t, RenderDocs(t, LoadProfile(t, s), ComponentOverrides(component, overrides)), component)
	if diff := cmp.Diff(want, []map[string]any{got}); diff != "" {
		t.Errorf("profile-rendered %s differs from standalone chart (-standalone +profile):\n%s", component, diff)
	}
}

// AssertRenderFails checks that rendering the profile as releaseName with overrides fails with an
// error containing wantErr.
func AssertRenderFails(t *testing.T, s Spec, releaseName string, overrides map[string]any, wantErr string) {
	t.Helper()
	_, err := Render(LoadProfile(t, s), releaseName, overrides)
	AssertErrorContains(t, err, wantErr)
}

func checkRendersOnlyComponents(t *testing.T, s Spec) {
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
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			AssertRendersComponents(t, s, tt.overrides)
		})
	}
}

func checkMatchesStandaloneDefaults(t *testing.T, s Spec) {
	for _, name := range s.Components {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			AssertMatchesStandalone(t, s, name, nil)
		})
	}
}

// invalidInstall is a render that the profile must reject with an error containing wantErr.
type invalidInstall struct {
	name, releaseName, wantErr string
	overrides                  map[string]any
}

func checkRejectsInvalidInstalls(t *testing.T, s Spec) {
	tests := []invalidInstall{
		{name: "unknown component", overrides: ComponentOverrides("not-a-catalog-component", map[string]any{"enabled": true}), wantErr: "not-a-catalog-component"},
		{name: "wrong platformProfile", overrides: map[string]any{"global": map[string]any{"platformProfile": "not-" + s.ProfileName}}, wantErr: "platformProfile"},
		{name: "xmc-bridge component release name", releaseName: "xmc-cmp-" + s.Components[0], wantErr: "must not be installed as an xmc-cmp-* component release"},
	}
	for _, c := range s.Components {
		tests = append(tests, componentKeyRejections(c)...)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			releaseName := tt.releaseName
			if releaseName == "" {
				releaseName = DefaultRelease
			}
			AssertRenderFails(t, s, releaseName, tt.overrides, tt.wantErr)
		})
	}
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

func checkComponentFileErrors(t *testing.T, s Spec) {
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
	for _, c := range s.Components {
		for _, tt := range tests {
			t.Run(tt.name+"/"+c, func(t *testing.T) {
				t.Parallel()
				path := fmt.Sprintf("components/%s/%s", c, tt.file)
				_, err := Render(LoadProfileWithFileEdit(t, s, path, tt.data, tt.remove), DefaultRelease, nil)
				AssertErrorContains(t, err, tt.wantErr(path))
			})
		}
	}
}

func missingFileError(path string) string { return fmt.Sprintf("required file %q is missing", path) }

func invalidYAMLError(path string) string {
	return fmt.Sprintf("xmc-profile: %s: error converting YAML", path)
}
