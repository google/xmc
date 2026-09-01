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

package linter_test

import (
	"testing"

	"xmanager_cloud/pkg/components/linter"
)

func TestMatrix_SemVerCompatibility(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		manifests map[string]string
		wantValid bool
		wantRule  string
	}{
		{
			name: "satisfies manifest-level SemVer constraint",
			manifests: map[string]string{
				"comp-a.yaml": "apiVersion: components.xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: comp-a }\nspec: { version: \"1.0.0\", dependencies: [{ name: comp-b, version: \">=1.2.0 <2.0.0\" }] }\n",
				"comp-b.yaml": "apiVersion: components.xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: comp-b }\nspec: { version: \"1.4.2\" }\n",
			},
			wantValid: true,
		},
		{
			name: "fails manifest-level SemVer constraint",
			manifests: map[string]string{
				"comp-a.yaml": `
apiVersion: components.xmc.dev/v1alpha1
kind: Component
metadata:
  name: comp-a
spec:
  version: "1.0.0"
  dependencies:
    - name: comp-b
      version: "^2.0.0"
`,
				"comp-b.yaml": `
apiVersion: components.xmc.dev/v1alpha1
kind: Component
metadata:
  name: comp-b
spec:
  version: "1.8.0"
`,
			},
			wantValid: false,
			wantRule:  "VERSION_INCOMPATIBILITY",
		},
		{
			name: "invalid SemVer version in component manifest",
			manifests: map[string]string{
				"comp.yaml": `
apiVersion: components.xmc.dev/v1alpha1
kind: Component
metadata:
  name: comp
spec:
  version: "not-a-valid-semver"
`,
			},
			wantValid: false,
			wantRule:  "INVALID_VERSION",
		},
		{
			name: "invalid version constraint in dependency manifest",
			manifests: map[string]string{
				"comp-a.yaml": "apiVersion: components.xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: comp-a }\nspec: { version: \"1.0.0\", dependencies: [{ name: comp-b, version: \">>bad\" }] }\n",
				"comp-b.yaml": "apiVersion: components.xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: comp-b }\nspec: { version: \"1.0.0\" }\n",
			},
			wantValid: false,
			wantRule:  "INVALID_VERSION_CONSTRAINT",
		},
		{
			name: "dependency component has empty version string",
			manifests: map[string]string{
				"comp-a.yaml": "apiVersion: components.xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: comp-a }\nspec: { version: \"1.0.0\", dependencies: [{ name: comp-b, version: \">=1.0.0\" }] }\n",
				"comp-b.yaml": "apiVersion: components.xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: comp-b }\nspec: {}\n",
			},
			wantValid: true,
		},
		{
			name: "disabled requiring component in manifest dependencies is skipped",
			manifests: map[string]string{
				"comp-a.yaml": "apiVersion: components.xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: comp-a }\nspec: { enabled: false, version: \"1.0.0\", dependencies: [{ name: comp-b, version: \"^2.0.0\" }] }\n",
				"comp-b.yaml": "apiVersion: components.xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: comp-b }\nspec: { version: \"1.8.0\" }\n",
			},
			wantValid: true,
		},
		{
			name: "disabled target dependency component in manifest dependencies is skipped",
			manifests: map[string]string{
				"comp-a.yaml": "apiVersion: components.xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: comp-a }\nspec: { enabled: true, version: \"1.0.0\", dependencies: [{ name: comp-b, version: \"^2.0.0\", optional: true }] }\n",
				"comp-b.yaml": "apiVersion: components.xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: comp-b }\nspec: { enabled: false, version: \"1.8.0\" }\n",
			},
			wantValid: true,
		},
		{
			name: "dependency with unversioned requirement passes",
			manifests: map[string]string{
				"comp-a.yaml": "apiVersion: components.xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: comp-a }\nspec: { enabled: true, version: \"1.0.0\", dependencies: [{ name: comp-b }] }\n",
				"comp-b.yaml": "apiVersion: components.xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: comp-b }\nspec: { enabled: true, version: \"1.0.0\" }\n",
			},
			wantValid: true,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			for fname, content := range tc.manifests {
				writeTestManifest(t, dir, fname, content)
			}
			l := linter.NewLinter(nil)
			res, err := l.ValidateDirectory(dir)
			if err != nil {
				t.Fatalf("ValidateDirectory() unexpected error: %v", err)
			}
			if res.Valid != tc.wantValid {
				t.Errorf("res.Valid = %v, want %v (errors: %v)", res.Valid, tc.wantValid, res.Errors)
			}
			assertDiagnosticRule(t, res, tc.wantRule)
		})
	}
}
