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
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// realProfile is the profile chart the fixtures are copied from. Its own expectations are tested
// in //test/helm/profiles/stable2026a; here it only provides a known-good meta-chart.
var realProfile = Spec{
	ChartRelPath: "deploy/profiles/stable-2026a",
	ProfileName:  "stable-2026a",
	Components:   []string{"redpanda", "clickhouse", "ingestion-gateway"},
}

// copyProfile copies the real profile chart into a temporary directory and returns a Spec for
// the copy, so a test can break one file without affecting other tests.
func copyProfile(t *testing.T) Spec {
	t.Helper()
	src, err := chartDir(realProfile)
	fatalOnError(t, err)
	dst := t.TempDir()
	err = filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(dst, strings.TrimPrefix(path, src))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path) // Follows Bazel runfiles symlinks.
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	fatalOnError(t, err)
	s := realProfile
	s.ChartRelPath = dst
	s.Components = slices.Clone(realProfile.Components)
	return s
}

// replaceInFile replaces the first occurrence of old with new in the chart file rel, failing the
// test when old is absent so that a stale mutation cannot silently leave the chart valid.
func replaceInFile(t *testing.T, s Spec, rel, old, new string) {
	t.Helper()
	path := filepath.Join(s.ChartRelPath, rel)
	data, err := os.ReadFile(path)
	fatalOnError(t, err)
	if !strings.Contains(string(data), old) {
		t.Fatalf("%s does not contain %q", rel, old)
	}
	fatalOnError(t, os.WriteFile(path, []byte(strings.Replace(string(data), old, new, 1)), 0o644))
}

// writeFile creates the chart file rel with data.
func writeFile(t *testing.T, s Spec, rel, data string) {
	t.Helper()
	path := filepath.Join(s.ChartRelPath, rel)
	fatalOnError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	fatalOnError(t, os.WriteFile(path, []byte(data), 0o644))
}

// checkErrors runs every case of the named contract check against s and joins their errors.
func checkErrors(t *testing.T, s Spec, check string) error {
	t.Helper()
	i := slices.IndexFunc(contractChecks, func(c contractCheck) bool { return c.name == check })
	if i < 0 {
		t.Fatalf("unknown contract check %q", check)
	}
	var errs []error
	for _, c := range contractChecks[i].cases(s) {
		errs = append(errs, c.verify())
	}
	return errors.Join(errs...)
}

func TestRun_PassesOnUnmodifiedProfileCopy(t *testing.T) {
	t.Parallel()
	s := copyProfile(t)
	Run(t, s)
	AssertRendersComponents(t, s, nil)
	AssertMatchesStandalone(t, s, s.Components[0], map[string]any{"enabled": true})
	AssertRenderFails(t, s, "xmc-cmp-x", nil, "xmc-cmp-*")
}

const extraConfigMap = "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: extra\n"

func TestValidateSpec(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mutate  func(*Spec)
		wantErr string
	}{
		{name: "valid", mutate: func(*Spec) {}},
		{name: "empty Components", mutate: func(s *Spec) { s.Components = nil }, wantErr: "Spec.Components must not be empty"},
		{name: "empty ChartRelPath", mutate: func(s *Spec) { s.ChartRelPath = "" }, wantErr: "Spec.ChartRelPath must not be empty"},
		{name: "empty ProfileName", mutate: func(s *Spec) { s.ProfileName = "" }, wantErr: "Spec.ProfileName must not be empty"},
	}
	for _, tt := range tests {
		s := realProfile
		tt.mutate(&s)
		err := validateSpec(s)
		if tt.wantErr == "" && err != nil || tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
			t.Errorf("%s: validateSpec() error = %v, want %q", tt.name, err, tt.wantErr)
		}
	}
}

// TestContractChecks_DetectViolations breaks one property of a valid profile per case and checks
// that the contract check guarding that property reports it.
func TestContractChecks_DetectViolations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		mutate    func(*testing.T, *Spec)
		wantCheck string
		wantErr   string
	}{
		{name: "extra document in components.yaml", wantCheck: "RendersOnlyComponents", wantErr: "ConfigMap/extra", mutate: func(t *testing.T, s *Spec) {
			replaceInFile(t, *s, "templates/components.yaml", "{{- end }}", "{{- end }}\n---\n"+extraConfigMap)
		}},
		{name: "extra non-Component template file", wantCheck: "RendersOnlyComponents", wantErr: "ConfigMap/extra", mutate: func(t *testing.T, s *Spec) {
			writeFile(t, *s, "templates/extra.yaml", extraConfigMap)
		}},
		{name: "components rendered out of order", wantCheck: "RendersOnlyComponents", wantErr: "rendered kind/name mismatch", mutate: func(t *testing.T, s *Spec) {
			slices.Reverse(s.Components)
		}},
		{name: "profile default differs from standalone chart", wantCheck: "MatchesStandaloneDefaults", wantErr: "redpanda differs from standalone chart", mutate: func(t *testing.T, s *Spec) {
			replaceInFile(t, *s, "values.yaml", "redpanda:\n    enabled: false", "redpanda:\n    enabled: true")
		}},
		{name: "schema accepts reserved keys", wantCheck: "RejectsInvalidInstalls", wantErr: `want error containing "componentClass"`, mutate: func(t *testing.T, s *Spec) {
			replaceInFile(t, *s, "values.schema.json", `"enum": ["enabled",`, `"enum": ["componentClass", "chartVersion", "enabled",`)
		}},
		{name: "schema accepts any namespace", wantCheck: "RejectsInvalidInstalls", wantErr: `want error containing "namespace"`, mutate: func(t *testing.T, s *Spec) {
			replaceInFile(t, *s, "values.schema.json", `"pattern": "^[a-z0-9]([-a-z0-9]*[a-z0-9])?$"`, `"pattern": ".*"`)
		}},
		{name: "release-name guard removed", wantCheck: "RejectsInvalidInstalls", wantErr: "xmc-cmp-* component release", mutate: func(t *testing.T, s *Spec) {
			replaceInFile(t, *s, "templates/components.yaml", `hasPrefix "xmc-cmp-"`, `hasPrefix "never-"`)
		}},
		{name: "missing component file tolerated", wantCheck: "ComponentFileErrors", wantErr: "required file", mutate: func(t *testing.T, s *Spec) {
			replaceInFile(t, *s, "templates/_helpers.tpl", "{{- if not ($root.Files.Glob $path) -}}", "{{- if false -}}")
		}},
		{name: "component directory missing from catalog", wantCheck: "CatalogIsConsistent", wantErr: "catalog differs from components/ directories", mutate: func(t *testing.T, s *Spec) {
			writeFile(t, *s, "components/extra/Chart.yaml", "apiVersion: v2\nname: extra\nversion: 0.1.0\n")
		}},
		{name: "profile.yaml names another profile", wantCheck: "MetadataIsConsistent", wantErr: "is not used consistently", mutate: func(t *testing.T, s *Spec) {
			replaceInFile(t, *s, "profile.yaml", "name: stable-2026a", "name: other")
		}},
		{name: "Spec names another profile", wantCheck: "MetadataIsConsistent", wantErr: `profile name "other"`, mutate: func(t *testing.T, s *Spec) {
			s.ProfileName = "other"
		}},
		{name: "minXmcVersion not MAJOR.MINOR.PATCH", wantCheck: "MetadataIsConsistent", wantErr: "minXmcVersion", mutate: func(t *testing.T, s *Spec) {
			replaceInFile(t, *s, "profile.yaml", `minXmcVersion: "0.13.0"`, `minXmcVersion: "1.0"`)
		}},
		{name: "supportWindow ends before it starts", wantCheck: "MetadataIsConsistent", wantErr: "must be before end", mutate: func(t *testing.T, s *Spec) {
			replaceInFile(t, *s, "profile.yaml", `end: "2027-03"`, `end: "2026-01"`)
		}},
		{name: "supportWindow month malformed", wantCheck: "MetadataIsConsistent", wantErr: "want YYYY-MM months", mutate: func(t *testing.T, s *Spec) {
			replaceInFile(t, *s, "profile.yaml", `start: "2026-10"`, `start: "2026-13"`)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := copyProfile(t)
			tt.mutate(t, &s)
			err := checkErrors(t, s, tt.wantCheck)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("%s error = %v, want error containing %q", tt.wantCheck, err, tt.wantErr)
			}
		})
	}
}

// TestContractChecks_ReportUnreadableProfiles checks that every contract check reports a profile
// it cannot load instead of passing vacuously.
func TestContractChecks_ReportUnreadableProfiles(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*testing.T, Spec) Spec
	}{
		{name: "chart not found", mutate: func(t *testing.T, s Spec) Spec {
			s.ChartRelPath = filepath.Join(t.TempDir(), "missing")
			return s
		}},
		{name: "invalid Chart.yaml", mutate: func(t *testing.T, s Spec) Spec {
			writeFile(t, s, "Chart.yaml", "name: [x\n")
			return s
		}},
	}
	for _, tt := range tests {
		for _, c := range contractChecks {
			t.Run(tt.name+"/"+c.name, func(t *testing.T) {
				t.Parallel()
				if err := checkErrors(t, tt.mutate(t, copyProfile(t)), c.name); err == nil {
					t.Errorf("%s error = nil, want an error", c.name)
				}
			})
		}
	}
}
