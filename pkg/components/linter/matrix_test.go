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
	"strings"
	"testing"

	"xmanager_cloud/pkg/components/linter"
)

func TestMatrix_SemVerCompatibility(t *testing.T) {
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
				"comp-a.yaml": "apiVersion: xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: comp-a }\nspec: { stability: STABLE, version: \"1.0.0\", dependencies: [{ name: comp-b, version: \">=1.2.0 <2.0.0\" }] }\n",
				"comp-b.yaml": "apiVersion: xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: comp-b }\nspec: { stability: STABLE, version: \"1.4.2\" }\n",
			},
			wantValid: true,
		},
		{
			name:  "fails manifest-level SemVer constraint",
			rules: &linter.RulesConfig{},
			manifests: map[string]string{
				"comp-a.yaml": `
apiVersion: xmc.dev/v1alpha1
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
apiVersion: xmc.dev/v1alpha1
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
apiVersion: xmc.dev/v1alpha1
kind: Component
metadata:
  name: jobset
spec:
  stability: STABLE
  version: "0.2.0"
`,
				"cert-manager.yaml": `
apiVersion: xmc.dev/v1alpha1
kind: Component
metadata:
  name: cert-manager
spec:
  stability: STABLE
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
apiVersion: xmc.dev/v1alpha1
kind: Component
metadata:
  name: jobset
spec:
  version: "0.2.0"
`,
				"cert-manager.yaml": `
apiVersion: xmc.dev/v1alpha1
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
apiVersion: xmc.dev/v1alpha1
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
apiVersion: xmc.dev/v1alpha1
kind: Component
metadata:
  name: jobset
spec:
  stability: STABLE
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
apiVersion: xmc.dev/v1alpha1
kind: Component
metadata:
  name: jobset
spec:
  stability: STABLE
  version: "1.0.0"
`,
			},
			wantValid: true,
		},
		{
			name: "component with empty version skips version-specific matrix rule",
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
				// jobset has no version declared, so version-specific rule for >=2.0.0 should NOT trigger
				"jobset.yaml": `
apiVersion: xmc.dev/v1alpha1
kind: Component
metadata:
  name: jobset
spec:
  stability: STABLE
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
apiVersion: xmc.dev/v1alpha1
kind: Component
metadata:
  name: jobset
spec:
  stability: STABLE
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
apiVersion: xmc.dev/v1alpha1
kind: Component
metadata:
  name: jobset
spec:
  enabled: true
  version: "1.0.0"
`,
				"kueue.yaml": `
apiVersion: xmc.dev/v1alpha1
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
apiVersion: xmc.dev/v1alpha1
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
			name:  "invalid version constraint in dependency manifest",
			rules: &linter.RulesConfig{},
			manifests: map[string]string{
				"comp-a.yaml": "apiVersion: xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: comp-a }\nspec: { version: \"1.0.0\", dependencies: [{ name: comp-b, version: \">>bad\" }] }\n",
				"comp-b.yaml": "apiVersion: xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: comp-b }\nspec: { version: \"1.0.0\" }\n",
			},
			wantValid: false,
			wantRule:  "INVALID_VERSION_CONSTRAINT",
		},
		{
			name: "invalid componentVersion in matrix rules",
			rules: &linter.RulesConfig{
				CompatibilityMatrix: linter.CompatibilityMatrixConfig{
					ComponentDependencies: []linter.ComponentDependencyRule{
						{
							Component:        "jobset",
							ComponentVersion: ">>bad",
						},
					},
				},
			},
			manifests: map[string]string{
				"jobset.yaml": "apiVersion: xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: jobset }\nspec: { version: \"1.0.0\" }\n",
			},
			wantValid: false,
			wantRule:  "INVALID_VERSION_CONSTRAINT",
		},
		{
			name: "invalid requires version constraint in matrix rules",
			rules: &linter.RulesConfig{
				CompatibilityMatrix: linter.CompatibilityMatrixConfig{
					ComponentDependencies: []linter.ComponentDependencyRule{
						{
							Component: "jobset",
							Requires: []linter.DependencyRequirement{
								{
									Component: "cert-manager",
									Version:   ">>bad",
								},
							},
						},
					},
				},
			},
			manifests: map[string]string{
				"jobset.yaml":       "apiVersion: xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: jobset }\nspec: { version: \"1.0.0\" }\n",
				"cert-manager.yaml": "apiVersion: xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: cert-manager }\nspec: { version: \"1.0.0\" }\n",
			},
			wantValid: false,
			wantRule:  "INVALID_VERSION_CONSTRAINT",
		},
		{
			name:  "dependency component has empty version string",
			rules: &linter.RulesConfig{},
			manifests: map[string]string{
				"comp-a.yaml": "apiVersion: xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: comp-a }\nspec: { stability: STABLE, version: \"1.0.0\", dependencies: [{ name: comp-b, version: \">=1.0.0\" }] }\n",
				"comp-b.yaml": "apiVersion: xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: comp-b }\nspec: { stability: STABLE }\n",
			},
			wantValid: true,
		},
		{
			name: "matrix rule requires component with empty version string",
			rules: &linter.RulesConfig{
				CompatibilityMatrix: linter.CompatibilityMatrixConfig{
					ComponentDependencies: []linter.ComponentDependencyRule{
						{
							Component: "jobset",
							Requires: []linter.DependencyRequirement{
								{
									Component: "cert-manager",
									Version:   ">=1.0.0",
								},
							},
						},
					},
				},
			},
			manifests: map[string]string{
				"jobset.yaml":       "apiVersion: xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: jobset }\nspec: { stability: STABLE, version: \"1.0.0\" }\n",
				"cert-manager.yaml": "apiVersion: xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: cert-manager }\nspec: { stability: STABLE }\n",
			},
			wantValid: true,
		},
		{
			name:  "disabled requiring component in manifest dependencies is skipped",
			rules: &linter.RulesConfig{},
			manifests: map[string]string{
				"comp-a.yaml": "apiVersion: xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: comp-a }\nspec: { stability: STABLE, enabled: false, version: \"1.0.0\", dependencies: [{ name: comp-b, version: \"^2.0.0\" }] }\n",
				"comp-b.yaml": "apiVersion: xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: comp-b }\nspec: { stability: STABLE, version: \"1.8.0\" }\n",
			},
			wantValid: true,
		},
		{
			name:  "disabled target dependency component in manifest dependencies is skipped",
			rules: &linter.RulesConfig{},
			manifests: map[string]string{
				"comp-a.yaml": "apiVersion: xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: comp-a }\nspec: { stability: STABLE, enabled: true, version: \"1.0.0\", dependencies: [{ name: comp-b, version: \"^2.0.0\", optional: true }] }\n",
				"comp-b.yaml": "apiVersion: xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: comp-b }\nspec: { stability: STABLE, enabled: false, version: \"1.8.0\" }\n",
			},
			wantValid: true,
		},
		{
			name:  "invalid version on target dependency emits INVALID_VERSION and skips constraint evaluation",
			rules: &linter.RulesConfig{},
			manifests: map[string]string{
				"comp-a.yaml": "apiVersion: xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: comp-a }\nspec: { version: \"1.0.0\", dependencies: [{ name: comp-b, version: \">=1.0.0\" }] }\n",
				"comp-b.yaml": "apiVersion: xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: comp-b }\nspec: { version: \"invalid-semver\" }\n",
			},
			wantValid: false,
			wantRule:  "INVALID_VERSION",
		},
		{
			name: "invalid version on component in matrix rules emits INVALID_VERSION and skips rule matching",
			rules: &linter.RulesConfig{
				CompatibilityMatrix: linter.CompatibilityMatrixConfig{
					ComponentDependencies: []linter.ComponentDependencyRule{
						{
							Component:        "jobset",
							ComponentVersion: ">=1.0.0",
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
				"jobset.yaml": "apiVersion: xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: jobset }\nspec: { version: \"invalid-semver\" }\n",
			},
			wantValid: false,
			wantRule:  "INVALID_VERSION",
		},
		{
			name: "invalid version on required component in matrix rules emits INVALID_VERSION and skips constraint evaluation",
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
				"jobset.yaml":       "apiVersion: xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: jobset }\nspec: { version: \"1.0.0\" }\n",
				"cert-manager.yaml": "apiVersion: xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: cert-manager }\nspec: { version: \"invalid-semver\" }\n",
			},
			wantValid: false,
			wantRule:  "INVALID_VERSION",
		},
		{
			name: "missing component requirement with empty version and reason formats message cleanly",
			rules: &linter.RulesConfig{
				CompatibilityMatrix: linter.CompatibilityMatrixConfig{
					ComponentDependencies: []linter.ComponentDependencyRule{
						{
							Component: "jobset",
							Requires: []linter.DependencyRequirement{
								{
									Component: "cert-manager",
									Reason:    "certificate generation required",
								},
							},
						},
					},
				},
			},
			manifests: map[string]string{
				"jobset.yaml": "apiVersion: xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: jobset }\nspec: { version: \"1.0.0\" }\n",
			},
			wantValid: false,
			wantRule:  "REQUIRED_COMPONENT_MISSING",
		},
		{
			name: "version incompatibility reports custom reason",
			rules: &linter.RulesConfig{
				CompatibilityMatrix: linter.CompatibilityMatrixConfig{
					ComponentDependencies: []linter.ComponentDependencyRule{
						{
							Component: "jobset",
							Requires: []linter.DependencyRequirement{
								{
									Component: "cert-manager",
									Version:   ">=2.0.0",
									Reason:    "API v2 is required for webhook support",
								},
							},
						},
					},
				},
			},
			manifests: map[string]string{
				"jobset.yaml":       "apiVersion: xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: jobset }\nspec: { version: \"1.0.0\" }\n",
				"cert-manager.yaml": "apiVersion: xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: cert-manager }\nspec: { version: \"1.4.0\" }\n",
			},
			wantValid: false,
			wantRule:  "VERSION_INCOMPATIBILITY",
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
apiVersion: xmc.dev/v1alpha1
kind: Component
metadata:
  name: jobset
spec:
  stability: STABLE
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
apiVersion: xmc.dev/v1alpha1
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
		{
			name: "invalid platform kubernetesVersion constraint",
			rules: &linter.RulesConfig{
				CompatibilityMatrix: linter.CompatibilityMatrixConfig{
					PlatformRequirements: []linter.PlatformIntegrationRule{
						{
							Component: "jobset",
							Platform: linter.PlatformRequirementConfig{
								KubernetesVersion: ">>bad",
							},
						},
					},
				},
			},
			manifests: map[string]string{
				"jobset.yaml": "apiVersion: xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: jobset }\nspec: { version: \"1.0.0\" }\n",
			},
			wantValid: false,
			wantRule:  "INVALID_VERSION_CONSTRAINT",
		},
		{
			name: "platform requirements for disabled component is skipped",
			rules: &linter.RulesConfig{
				CompatibilityMatrix: linter.CompatibilityMatrixConfig{
					PlatformRequirements: []linter.PlatformIntegrationRule{
						{
							Component: "jobset",
							Platform: linter.PlatformRequirementConfig{
								KubernetesVersion: ">=1.28.0",
							},
						},
					},
				},
			},
			manifests: map[string]string{
				"jobset.yaml": "apiVersion: xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: jobset }\nspec: { stability: STABLE, enabled: false, version: \"1.0.0\" }\n",
			},
			wantValid: true,
		},
		{
			name: "valid platform requirements with maxK8sVersion and requiredCRDs",
			rules: &linter.RulesConfig{
				CompatibilityMatrix: linter.CompatibilityMatrixConfig{
					PlatformRequirements: []linter.PlatformIntegrationRule{
						{
							Component: "jobset",
							Platform: linter.PlatformRequirementConfig{
								MinK8sVersion:     "1.28.0",
								MaxK8sVersion:     "1.32.0",
								KubernetesVersion: ">=1.28.0 <1.32.0",
								RequiredCRDs:      []string{"jobsets.jobset.x-k8s.io"},
							},
						},
					},
				},
			},
			manifests: map[string]string{
				"jobset.yaml": `
apiVersion: xmc.dev/v1alpha1
kind: Component
metadata:
  name: jobset
spec:
  stability: STABLE
  version: "0.2.0"
`,
			},
			wantValid: true,
		},
		{
			name: "invalid platform requirements maxK8sVersion",
			rules: &linter.RulesConfig{
				CompatibilityMatrix: linter.CompatibilityMatrixConfig{
					PlatformRequirements: []linter.PlatformIntegrationRule{
						{
							Component: "jobset",
							Platform: linter.PlatformRequirementConfig{
								MaxK8sVersion: "invalid.k8s.version",
							},
						},
					},
				},
			},
			manifests: map[string]string{
				"jobset.yaml": `
apiVersion: xmc.dev/v1alpha1
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
		{
			name: "minK8sVersion greater than maxK8sVersion in platform requirements",
			rules: &linter.RulesConfig{
				CompatibilityMatrix: linter.CompatibilityMatrixConfig{
					PlatformRequirements: []linter.PlatformIntegrationRule{
						{
							Component: "jobset",
							Platform: linter.PlatformRequirementConfig{
								MinK8sVersion: "1.32.0",
								MaxK8sVersion: "1.28.0",
							},
						},
					},
				},
			},
			manifests: map[string]string{
				"jobset.yaml": `
apiVersion: xmc.dev/v1alpha1
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
		{
			name: "empty requiredCRD in platform requirements",
			rules: &linter.RulesConfig{
				CompatibilityMatrix: linter.CompatibilityMatrixConfig{
					PlatformRequirements: []linter.PlatformIntegrationRule{
						{
							Component: "jobset",
							Platform: linter.PlatformRequirementConfig{
								RequiredCRDs: []string{""},
							},
						},
					},
				},
			},
			manifests: map[string]string{
				"jobset.yaml": `
apiVersion: xmc.dev/v1alpha1
kind: Component
metadata:
  name: jobset
spec:
  version: "0.2.0"
`,
			},
			wantValid: false,
			wantRule:  "INVALID_CRD_REQUIREMENT",
		},
		{
			name: "whitespace requiredCRD in platform requirements",
			rules: &linter.RulesConfig{
				CompatibilityMatrix: linter.CompatibilityMatrixConfig{
					PlatformRequirements: []linter.PlatformIntegrationRule{
						{
							Component: "jobset",
							Platform: linter.PlatformRequirementConfig{
								RequiredCRDs: []string{"   "},
							},
						},
					},
				},
			},
			manifests: map[string]string{
				"jobset.yaml": "apiVersion: xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: jobset }\nspec: { version: \"0.2.0\" }\n",
			},
			wantValid: false,
			wantRule:  "INVALID_CRD_REQUIREMENT",
		},
		{
			name: "requiredCRD without dot in platform requirements",
			rules: &linter.RulesConfig{
				CompatibilityMatrix: linter.CompatibilityMatrixConfig{
					PlatformRequirements: []linter.PlatformIntegrationRule{
						{
							Component: "jobset",
							Platform: linter.PlatformRequirementConfig{
								RequiredCRDs: []string{"jobsets"},
							},
						},
					},
				},
			},
			manifests: map[string]string{
				"jobset.yaml": "apiVersion: xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: jobset }\nspec: { version: \"0.2.0\" }\n",
			},
			wantValid: false,
			wantRule:  "INVALID_CRD_REQUIREMENT",
		},
		{
			name: "requiredCRD with uppercase characters in platform requirements",
			rules: &linter.RulesConfig{
				CompatibilityMatrix: linter.CompatibilityMatrixConfig{
					PlatformRequirements: []linter.PlatformIntegrationRule{
						{
							Component: "jobset",
							Platform: linter.PlatformRequirementConfig{
								RequiredCRDs: []string{"Jobsets.x-k8s.io"},
							},
						},
					},
				},
			},
			manifests: map[string]string{
				"jobset.yaml": "apiVersion: xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: jobset }\nspec: { version: \"0.2.0\" }\n",
			},
			wantValid: false,
			wantRule:  "INVALID_CRD_REQUIREMENT",
		},
		{
			name: "requiredCRD with invalid special characters in platform requirements",
			rules: &linter.RulesConfig{
				CompatibilityMatrix: linter.CompatibilityMatrixConfig{
					PlatformRequirements: []linter.PlatformIntegrationRule{
						{
							Component: "jobset",
							Platform: linter.PlatformRequirementConfig{
								RequiredCRDs: []string{"jobsets@x-k8s.io"},
							},
						},
					},
				},
			},
			manifests: map[string]string{
				"jobset.yaml": "apiVersion: xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: jobset }\nspec: { version: \"0.2.0\" }\n",
			},
			wantValid: false,
			wantRule:  "INVALID_CRD_REQUIREMENT",
		},
		{
			name: "valid platform requirements with 2-segment k8s versions",
			rules: &linter.RulesConfig{
				CompatibilityMatrix: linter.CompatibilityMatrixConfig{
					PlatformRequirements: []linter.PlatformIntegrationRule{
						{
							Component: "jobset",
							Platform: linter.PlatformRequirementConfig{
								MinK8sVersion: "1.28",
								MaxK8sVersion: "1.32",
							},
						},
					},
				},
			},
			manifests: map[string]string{
				"jobset.yaml": "apiVersion: xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: jobset }\nspec: { stability: STABLE, version: \"0.2.0\" }\n",
			},
			wantValid: true,
		},
		{
			name: "invalid platform requirements 2-segment minK8s greater than maxK8s",
			rules: &linter.RulesConfig{
				CompatibilityMatrix: linter.CompatibilityMatrixConfig{
					PlatformRequirements: []linter.PlatformIntegrationRule{
						{
							Component: "jobset",
							Platform: linter.PlatformRequirementConfig{
								MinK8sVersion: "1.32",
								MaxK8sVersion: "1.28",
							},
						},
					},
				},
			},
			manifests: map[string]string{
				"jobset.yaml": "apiVersion: xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: jobset }\nspec: { version: \"0.2.0\" }\n",
			},
			wantValid: false,
			wantRule:  "INVALID_VERSION_CONSTRAINT",
		},
		{
			name: "invalid requiredCRD with leading dot or trailing hyphen",
			rules: &linter.RulesConfig{
				CompatibilityMatrix: linter.CompatibilityMatrixConfig{
					PlatformRequirements: []linter.PlatformIntegrationRule{
						{
							Component: "jobset",
							Platform: linter.PlatformRequirementConfig{
								RequiredCRDs: []string{".invalid.group", "jobsets.-k8s.io", "foo.bar_baz"},
							},
						},
					},
				},
			},
			manifests: map[string]string{
				"jobset.yaml": "apiVersion: xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: jobset }\nspec: { version: \"0.2.0\" }\n",
			},
			wantValid: false,
			wantRule:  "INVALID_CRD_REQUIREMENT",
		},
		{
			name: "invalid requiredCRD segment exceeding 63 characters",
			rules: &linter.RulesConfig{
				CompatibilityMatrix: linter.CompatibilityMatrixConfig{
					PlatformRequirements: []linter.PlatformIntegrationRule{
						{
							Component: "jobset",
							Platform: linter.PlatformRequirementConfig{
								// 64 'a' characters in first segment
								RequiredCRDs: []string{"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.k8s.io"},
							},
						},
					},
				},
			},
			manifests: map[string]string{
				"jobset.yaml": "apiVersion: xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: jobset }\nspec: { version: \"0.2.0\" }\n",
			},
			wantValid: false,
			wantRule:  "INVALID_CRD_REQUIREMENT",
		},
		{
			name: "invalid requiredCRD total length exceeding 253 characters",
			rules: &linter.RulesConfig{
				CompatibilityMatrix: linter.CompatibilityMatrixConfig{
					PlatformRequirements: []linter.PlatformIntegrationRule{
						{
							Component: "jobset",
							Platform: linter.PlatformRequirementConfig{
								// 4 segments of 63 'a' characters separated by dots = 255 characters total
								RequiredCRDs: []string{
									"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa." +
										"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa." +
										"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa." +
										"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
								},
							},
						},
					},
				},
			},
			manifests: map[string]string{
				"jobset.yaml": "apiVersion: xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: jobset }\nspec: { version: \"0.2.0\" }\n",
			},
			wantValid: false,
			wantRule:  "INVALID_CRD_REQUIREMENT",
		},
		{
			name: "valid requiredCRD with maximum allowed RFC-1123 lengths",
			rules: &linter.RulesConfig{
				CompatibilityMatrix: linter.CompatibilityMatrixConfig{
					PlatformRequirements: []linter.PlatformIntegrationRule{
						{
							Component: "jobset",
							Platform: linter.PlatformRequirementConfig{
								// 3 segments of 63 chars + 1 segment of 61 chars separated by 3 dots = exactly 253 characters
								RequiredCRDs: []string{
									"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa." +
										"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa." +
										"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa." +
										"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
								},
							},
						},
					},
				},
			},
			manifests: map[string]string{
				"jobset.yaml": "apiVersion: xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: jobset }\nspec: { stability: STABLE, version: \"0.2.0\" }\n",
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

func TestMatrix_OrRequirements(t *testing.T) {
	t.Parallel()

	orRule := func(reason string, optional bool, alts ...linter.DependencyRequirement) *linter.RulesConfig {
		return &linter.RulesConfig{
			CompatibilityMatrix: linter.CompatibilityMatrixConfig{
				ComponentDependencies: []linter.ComponentDependencyRule{
					{
						Component: "ingestion-gateway",
						Requires: []linter.DependencyRequirement{
							{
								Or:       alts,
								Reason:   reason,
								Optional: optional,
							},
						},
					},
				},
			},
		}
	}

	gatewayManifest := "apiVersion: xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: ingestion-gateway }\nspec: { stability: STABLE, version: \"0.1.0\" }\n"

	tests := []struct {
		name      string
		rules     *linter.RulesConfig
		manifests map[string]string
		wantValid bool
		wantRule  string
		wantMsgIn string
	}{
		{
			name: "satisfied when first alternative in or is enabled and matches version",
			rules: orRule("needs message bus", false,
				linter.DependencyRequirement{Component: "redpanda", Version: ">=0.1.0"},
				linter.DependencyRequirement{Component: "pubsub", Version: ">=0.1.0"},
			),
			manifests: map[string]string{
				"ingestion-gateway.yaml": gatewayManifest,
				"redpanda.yaml":          "apiVersion: xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: redpanda }\nspec: { stability: STABLE, version: \"0.1.0\" }\n",
			},
			wantValid: true,
		},
		{
			name: "satisfied when second alternative in or is enabled and first is disabled",
			rules: orRule("needs message bus", false,
				linter.DependencyRequirement{Component: "redpanda", Version: ">=0.1.0"},
				linter.DependencyRequirement{Component: "pubsub", Version: ">=0.1.0"},
			),
			manifests: map[string]string{
				"ingestion-gateway.yaml": gatewayManifest,
				"redpanda.yaml":          "apiVersion: xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: redpanda }\nspec: { stability: STABLE, enabled: false, version: \"0.1.0\" }\n",
				"pubsub.yaml":            "apiVersion: xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: pubsub }\nspec: { stability: STABLE, enabled: true, version: \"0.2.0\" }\n",
			},
			wantValid: true,
		},
		{
			name: "pure logical OR satisfied when one enabled alternative is incompatible and another is compatible",
			rules: orRule("needs message bus", false,
				linter.DependencyRequirement{Component: "redpanda", Version: ">=1.0.0"},
				linter.DependencyRequirement{Component: "pubsub", Version: ">=0.1.0"},
			),
			manifests: map[string]string{
				"ingestion-gateway.yaml": gatewayManifest,
				"redpanda.yaml":          "apiVersion: xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: redpanda }\nspec: { stability: STABLE, enabled: true, version: \"0.1.0\" }\n",
				"pubsub.yaml":            "apiVersion: xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: pubsub }\nspec: { stability: STABLE, enabled: true, version: \"0.2.0\" }\n",
			},
			wantValid: true,
		},
		{
			name: "satisfied when enabled alternative has empty version constraint or empty target version",
			rules: orRule("", false,
				linter.DependencyRequirement{Component: "redpanda", Version: ""},
				linter.DependencyRequirement{Component: "pubsub", Version: ">=0.1.0"},
			),
			manifests: map[string]string{
				"ingestion-gateway.yaml": gatewayManifest,
				"redpanda.yaml":          "apiVersion: xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: redpanda }\nspec: { stability: STABLE }\n",
			},
			wantValid: true,
		},
		{
			name: "fails with REQUIRED_COMPONENT_MISSING when all alternatives are missing or disabled",
			rules: orRule("Redpanda or Pub/Sub required", false,
				linter.DependencyRequirement{Component: "redpanda", Version: ">=0.1.0"},
				linter.DependencyRequirement{Component: "pubsub"},
			),
			manifests: map[string]string{
				"ingestion-gateway.yaml": gatewayManifest,
				"redpanda.yaml":          "apiVersion: xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: redpanda }\nspec: { enabled: false, version: \"0.1.0\" }\n",
			},
			wantValid: false,
			wantRule:  "REQUIRED_COMPONENT_MISSING",
			wantMsgIn: `requires at least one of ["redpanda" (>=0.1.0), "pubsub"], but all are missing or disabled; reason: Redpanda or Pub/Sub required`,
		},
		{
			name: "optional or requirement is skipped when all alternatives are missing",
			rules: orRule("", true,
				linter.DependencyRequirement{Component: "redpanda", Version: ">=0.1.0"},
				linter.DependencyRequirement{Component: "pubsub", Version: ">=0.1.0"},
			),
			manifests: map[string]string{
				"ingestion-gateway.yaml": gatewayManifest,
			},
			wantValid: true,
		},
		{
			name: "or requirement where all alternatives are marked optional is skipped when missing",
			rules: orRule("", false,
				linter.DependencyRequirement{Component: "redpanda", Optional: true},
				linter.DependencyRequirement{Component: "pubsub", Optional: true},
			),
			manifests: map[string]string{
				"ingestion-gateway.yaml": gatewayManifest,
			},
			wantValid: true,
		},
		{
			name: "fails with VERSION_INCOMPATIBILITY and inherits group reason when enabled alternative fails version check",
			rules: orRule("broker >=1.0.0 needed", false,
				linter.DependencyRequirement{Component: "redpanda", Version: ">=1.0.0"},
				linter.DependencyRequirement{Component: "pubsub", Version: ">=1.0.0"},
			),
			manifests: map[string]string{
				"ingestion-gateway.yaml": gatewayManifest,
				"redpanda.yaml":          "apiVersion: xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: redpanda }\nspec: { enabled: true, version: \"0.1.0\" }\n",
			},
			wantValid: false,
			wantRule:  "VERSION_INCOMPATIBILITY",
			wantMsgIn: "reason: broker >=1.0.0 needed",
		},
		{
			name: "fails with VERSION_INCOMPATIBILITY when all enabled alternatives fail version checks",
			rules: orRule("group reason", false,
				linter.DependencyRequirement{Component: "redpanda", Version: ">=1.0.0", Reason: "redpanda specific reason"},
				linter.DependencyRequirement{Component: "pubsub", Version: ">=1.0.0"},
			),
			manifests: map[string]string{
				"ingestion-gateway.yaml": gatewayManifest,
				"redpanda.yaml":          "apiVersion: xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: redpanda }\nspec: { enabled: true, version: \"0.1.0\" }\n",
				"pubsub.yaml":            "apiVersion: xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: pubsub }\nspec: { enabled: true, version: \"0.2.0\" }\n",
			},
			wantValid: false,
			wantRule:  "VERSION_INCOMPATIBILITY",
			wantMsgIn: "reason: redpanda specific reason",
		},
		{
			name: "invalid version constraint in or alternative emits INVALID_VERSION_CONSTRAINT even when missing",
			rules: orRule("", false,
				linter.DependencyRequirement{Component: "redpanda", Version: ">=0.1.0"},
				linter.DependencyRequirement{Component: "pubsub", Version: ">>invalid"},
			),
			manifests: map[string]string{
				"ingestion-gateway.yaml": gatewayManifest,
				"redpanda.yaml":          "apiVersion: xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: redpanda }\nspec: { enabled: true, version: \"0.1.0\" }\n",
			},
			wantValid: false,
			wantRule:  "INVALID_VERSION_CONSTRAINT",
		},
		{
			name: "invalid semver on enabled alternative in or emits INVALID_VERSION without REQUIRED_COMPONENT_MISSING",
			rules: orRule("", false,
				linter.DependencyRequirement{Component: "redpanda", Version: ">=0.1.0"},
				linter.DependencyRequirement{Component: "pubsub", Version: ">=0.1.0"},
			),
			manifests: map[string]string{
				"ingestion-gateway.yaml": gatewayManifest,
				"redpanda.yaml":          "apiVersion: xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: redpanda }\nspec: { enabled: true, version: \"not-a-semver\" }\n",
			},
			wantValid: false,
			wantRule:  "INVALID_VERSION",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			for fname, content := range tc.manifests {
				writeTestManifest(t, dir, fname, content)
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
			if tc.wantMsgIn != "" {
				joined := strings.Join(res.Errors, "\n")
				if !strings.Contains(joined, tc.wantMsgIn) {
					t.Errorf("errors %q do not contain %q", joined, tc.wantMsgIn)
				}
			}
		})
	}
}
