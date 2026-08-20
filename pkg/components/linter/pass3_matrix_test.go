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
	"os"
	"path/filepath"
	"testing"

	"xmanager_cloud/pkg/components/linter"
)

func TestPass3_SemVerCompatibility(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		rules     *linter.RulesConfig
		manifests map[string]string
		wantValid bool
		wantRule  string
	}{
		{
			name:  "satisfies manifest-level SemVer constraint",
			rules: &linter.RulesConfig{},
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
      version: ">=1.2.0 <2.0.0"
`,
				"comp-b.yaml": `
apiVersion: components.xmc.dev/v1alpha1
kind: Component
metadata:
  name: comp-b
spec:
  version: "1.4.2"
`,
			},
			wantValid: true,
		},
		{
			name:  "fails manifest-level SemVer constraint",
			rules: &linter.RulesConfig{},
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
			name: "satisfies matrix rules requirement with caret constraint",
			rules: &linter.RulesConfig{
				CompatibilityMatrix: linter.CompatibilityMatrixConfig{
					ComponentDependencies: []linter.ComponentDependencyRule{
						{
							Component: "jobset",
							Requires: []linter.DependencyRequirement{
								{
									Component: "cert-manager",
									Version:   "^1.12.0",
								},
							},
						},
					},
				},
			},
			manifests: map[string]string{
				"jobset.yaml": `
apiVersion: components.xmc.dev/v1alpha1
kind: Component
metadata:
  name: jobset
spec:
  version: "0.2.0"
`,
				"cert-manager.yaml": `
apiVersion: components.xmc.dev/v1alpha1
kind: Component
metadata:
  name: cert-manager
spec:
  version: "1.12.4"
`,
			},
			wantValid: true,
		},
		{
			name: "fails matrix rules requirement with caret constraint",
			rules: &linter.RulesConfig{
				CompatibilityMatrix: linter.CompatibilityMatrixConfig{
					ComponentDependencies: []linter.ComponentDependencyRule{
						{
							Component: "jobset",
							Requires: []linter.DependencyRequirement{
								{
									Component: "cert-manager",
									Version:   "^1.12.0",
								},
							},
						},
					},
				},
			},
			manifests: map[string]string{
				"jobset.yaml": `
apiVersion: components.xmc.dev/v1alpha1
kind: Component
metadata:
  name: jobset
spec:
  version: "0.2.0"
`,
				"cert-manager.yaml": `
apiVersion: components.xmc.dev/v1alpha1
kind: Component
metadata:
  name: cert-manager
spec:
  version: "2.0.0"
`,
			},
			wantValid: false,
			wantRule:  "VERSION_INCOMPATIBILITY",
		},
		{
			name: "missing required component in matrix rules",
			rules: &linter.RulesConfig{
				CompatibilityMatrix: linter.CompatibilityMatrixConfig{
					ComponentDependencies: []linter.ComponentDependencyRule{
						{
							Component: "jobset",
							Requires: []linter.DependencyRequirement{
								{
									Component: "cert-manager",
									Version:   ">=1.0.0",
									Optional:  false,
								},
							},
						},
					},
				},
			},
			manifests: map[string]string{
				"jobset.yaml": `
apiVersion: components.xmc.dev/v1alpha1
kind: Component
metadata:
  name: jobset
spec:
  version: "0.2.0"
`,
			},
			wantValid: false,
			wantRule:  "REQUIRED_COMPONENT_MISSING",
		},
		{
			name: "missing optional component in matrix rules is ignored",
			rules: &linter.RulesConfig{
				CompatibilityMatrix: linter.CompatibilityMatrixConfig{
					ComponentDependencies: []linter.ComponentDependencyRule{
						{
							Component: "jobset",
							Requires: []linter.DependencyRequirement{
								{
									Component: "cert-manager",
									Version:   ">=1.0.0",
									Optional:  true,
								},
							},
						},
					},
				},
			},
			manifests: map[string]string{
				"jobset.yaml": `
apiVersion: components.xmc.dev/v1alpha1
kind: Component
metadata:
  name: jobset
spec:
  version: "0.2.0"
`,
			},
			wantValid: true,
		},
		{
			name: "component version filtering in matrix rules",
			rules: &linter.RulesConfig{
				CompatibilityMatrix: linter.CompatibilityMatrixConfig{
					ComponentDependencies: []linter.ComponentDependencyRule{
						{
							Component:        "jobset",
							ComponentVersion: ">=2.0.0",
							Requires: []linter.DependencyRequirement{
								{
									Component: "kueue",
									Version:   ">=0.8.0",
								},
							},
						},
					},
				},
			},
			manifests: map[string]string{
				// jobset version is 1.0.0, so rule for >=2.0.0 should NOT trigger
				"jobset.yaml": `
apiVersion: components.xmc.dev/v1alpha1
kind: Component
metadata:
  name: jobset
spec:
  version: "1.0.0"
`,
			},
			wantValid: true,
		},
		{
			name: "disabled requiring component skips matrix rules",
			rules: &linter.RulesConfig{
				CompatibilityMatrix: linter.CompatibilityMatrixConfig{
					ComponentDependencies: []linter.ComponentDependencyRule{
						{
							Component: "jobset",
							Requires: []linter.DependencyRequirement{
								{
									Component: "kueue",
									Version:   ">=0.8.0",
								},
							},
						},
					},
				},
			},
			manifests: map[string]string{
				"jobset.yaml": `
apiVersion: components.xmc.dev/v1alpha1
kind: Component
metadata:
  name: jobset
spec:
  enabled: false
  version: "1.0.0"
`,
			},
			wantValid: true,
		},
		{
			name: "disabled required component triggers error in matrix rules",
			rules: &linter.RulesConfig{
				CompatibilityMatrix: linter.CompatibilityMatrixConfig{
					ComponentDependencies: []linter.ComponentDependencyRule{
						{
							Component: "jobset",
							Requires: []linter.DependencyRequirement{
								{
									Component: "kueue",
									Version:   ">=0.8.0",
								},
							},
						},
					},
				},
			},
			manifests: map[string]string{
				"jobset.yaml": `
apiVersion: components.xmc.dev/v1alpha1
kind: Component
metadata:
  name: jobset
spec:
  enabled: true
  version: "1.0.0"
`,
				"kueue.yaml": `
apiVersion: components.xmc.dev/v1alpha1
kind: Component
metadata:
  name: kueue
spec:
  enabled: false
  version: "0.8.0"
`,
			},
			wantValid: false,
			wantRule:  "REQUIRED_COMPONENT_MISSING",
		},
		{
			name:  "invalid SemVer version in component manifest",
			rules: &linter.RulesConfig{},
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
			name: "valid platform requirements in matrix rules",
			rules: &linter.RulesConfig{
				CompatibilityMatrix: linter.CompatibilityMatrixConfig{
					PlatformRequirements: []linter.PlatformIntegrationRule{
						{
							Component: "jobset",
							Platform: linter.PlatformRequirementConfig{
								MinK8sVersion:     "1.28.0",
								KubernetesVersion: ">=1.28.0",
							},
						},
					},
				},
			},
			manifests: map[string]string{
				"jobset.yaml": `
apiVersion: components.xmc.dev/v1alpha1
kind: Component
metadata:
  name: jobset
spec:
  version: "0.2.0"
`,
			},
			wantValid: true,
		},
		{
			name: "invalid platform requirements minK8sVersion",
			rules: &linter.RulesConfig{
				CompatibilityMatrix: linter.CompatibilityMatrixConfig{
					PlatformRequirements: []linter.PlatformIntegrationRule{
						{
							Component: "jobset",
							Platform: linter.PlatformRequirementConfig{
								MinK8sVersion: "invalid.k8s.version",
							},
						},
					},
				},
			},
			manifests: map[string]string{
				"jobset.yaml": `
apiVersion: components.xmc.dev/v1alpha1
kind: Component
metadata:
  name: jobset
spec:
  version: "0.2.0"
`,
			},
			wantValid: false,
			wantRule:  "INVALID_VERSION_CONSTRAINT",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			for fname, content := range tc.manifests {
				p := filepath.Join(dir, fname)
				if err := os.WriteFile(p, []byte(content), 0644); err != nil {
					t.Fatalf("failed to write %s: %v", fname, err)
				}
			}

			l := linter.NewLinter(tc.rules)
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
