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

func TestDAG_AcyclicAndCompletenessChecks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		manifests    map[string]string
		wantValid    bool
		wantRule     string
		wantCycleStr string
	}{
		{
			name: "valid multi-dependency acyclic DAG",
			manifests: map[string]string{
				"comp-a.yaml": `
apiVersion: xmc.dev/v1alpha1
kind: Component
metadata:
  name: comp-a
spec:
  dependencies:
    - comp-b
    - comp-c
`,
				"comp-b.yaml": `
apiVersion: xmc.dev/v1alpha1
kind: Component
metadata:
  name: comp-b
spec:
  dependencies:
    - comp-d
`,
				"comp-c.yaml": `
apiVersion: xmc.dev/v1alpha1
kind: Component
metadata:
  name: comp-c
spec:
  dependencies:
    - comp-d
`,
				"comp-d.yaml": `
apiVersion: xmc.dev/v1alpha1
kind: Component
metadata:
  name: comp-d
`,
			},
			wantValid: true,
		},
		{
			name: "missing required dependency",
			manifests: map[string]string{
				"comp-a.yaml": `
apiVersion: xmc.dev/v1alpha1
kind: Component
metadata:
  name: comp-a
spec:
  dependencies:
    - comp-nonexistent
`,
			},
			wantValid: false,
			wantRule:  "MISSING_DEPENDENCY",
		},
		{
			name: "disabled component with missing dependency is allowed",
			manifests: map[string]string{
				"comp-a.yaml": `
apiVersion: xmc.dev/v1alpha1
kind: Component
metadata:
  name: comp-a
spec:
  enabled: false
  dependencies:
    - comp-nonexistent
`,
			},
			wantValid: true,
		},
		{
			name: "missing optional dependency is allowed",
			manifests: map[string]string{
				"comp-a.yaml": `
apiVersion: xmc.dev/v1alpha1
kind: Component
metadata:
  name: comp-a
spec:
  dependencies:
    - name: comp-optional
      optional: true
`,
			},
			wantValid: true,
		},
		{
			name: "enabled dependent depends on disabled optional dependency is allowed",
			manifests: map[string]string{
				"comp-a.yaml": `
apiVersion: xmc.dev/v1alpha1
kind: Component
metadata:
  name: comp-a
spec:
  enabled: true
  dependencies:
    - name: comp-b
      optional: true
`,
				"comp-b.yaml": `
apiVersion: xmc.dev/v1alpha1
kind: Component
metadata:
  name: comp-b
spec:
  enabled: false
`,
			},
			wantValid: true,
		},
		{
			name: "enabled dependent depends on disabled dependency",
			manifests: map[string]string{
				"comp-a.yaml": `
apiVersion: xmc.dev/v1alpha1
kind: Component
metadata:
  name: comp-a
spec:
  enabled: true
  dependencies:
    - comp-b
`,
				"comp-b.yaml": `
apiVersion: xmc.dev/v1alpha1
kind: Component
metadata:
  name: comp-b
spec:
  enabled: false
`,
			},
			wantValid: false,
			wantRule:  "DISABLED_DEPENDENCY",
		},
		{
			name: "disabled dependent on disabled dependency is allowed",
			manifests: map[string]string{
				"comp-a.yaml": `
apiVersion: xmc.dev/v1alpha1
kind: Component
metadata:
  name: comp-a
spec:
  enabled: false
  dependencies:
    - comp-b
`,
				"comp-b.yaml": `
apiVersion: xmc.dev/v1alpha1
kind: Component
metadata:
  name: comp-b
spec:
  enabled: false
`,
			},
			wantValid: true,
		},
		{
			name: "self cycle comp-a -> comp-a",
			manifests: map[string]string{
				"comp-a.yaml": `
apiVersion: xmc.dev/v1alpha1
kind: Component
metadata:
  name: comp-a
spec:
  dependencies:
    - comp-a
`,
			},
			wantValid:    false,
			wantRule:     "CYCLIC_DEPENDENCY",
			wantCycleStr: "comp-a -> comp-a",
		},
		{
			name: "multi-node cycle comp-a -> comp-b -> comp-a",
			manifests: map[string]string{
				"comp-a.yaml": `
apiVersion: xmc.dev/v1alpha1
kind: Component
metadata:
  name: comp-a
spec:
  dependencies:
    - comp-b
`,
				"comp-b.yaml": `
apiVersion: xmc.dev/v1alpha1
kind: Component
metadata:
  name: comp-b
spec:
  dependencies:
    - comp-a
`,
			},
			wantValid:    false,
			wantRule:     "CYCLIC_DEPENDENCY",
			wantCycleStr: "comp-a -> comp-b -> comp-a",
		},
		{
			name: "duplicate component definition in directory",
			manifests: map[string]string{
				"comp-1.yaml": `
apiVersion: xmc.dev/v1alpha1
kind: Component
metadata:
  name: duplicated-comp
spec:
  version: "1.0.0"
`,
				"comp-2.yaml": `
apiVersion: xmc.dev/v1alpha1
kind: Component
metadata:
  name: duplicated-comp
spec:
  version: "1.2.0"
`,
			},
			wantValid: false,
			wantRule:  "DUPLICATE_COMPONENT_NAME",
		},
		{
			name: "duplicate component definition across distinct files",
			manifests: map[string]string{
				"comp-first.yaml": `
apiVersion: xmc.dev/v1alpha1
kind: Component
metadata:
  name: jobset-operator
spec:
  version: "1.0.0"
`,
				"comp-second.yaml": `
apiVersion: xmc.dev/v1alpha1
kind: Component
metadata:
  name: jobset-operator
spec:
  version: "1.2.0"
`,
			},
			wantValid: false,
			wantRule:  "DUPLICATE_COMPONENT_NAME",
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
				t.Fatalf("ValidateDirectory() unexpected execution error: %v", err)
			}
			if res.Valid != tc.wantValid {
				t.Errorf("res.Valid = %v, want %v (errors: %v)", res.Valid, tc.wantValid, res.Errors)
			}
			if tc.wantRule != "" {
				assertDiagnosticRule(t, res, tc.wantRule)
			}
			if tc.wantCycleStr != "" {
				foundCycle := false
				for _, d := range res.Diagnostics {
					if strings.Contains(d.Message, tc.wantCycleStr) {
						foundCycle = true
						break
					}
				}
				if !foundCycle {
					t.Errorf("expected cycle string %q in diagnostics, got: %v", tc.wantCycleStr, res.Errors)
				}
			}
		})
	}
}
