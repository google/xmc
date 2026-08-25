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

func TestSchema_StructuralAndSecurityChecks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		rules     *linter.RulesConfig
		manifests map[string]string
		wantValid bool
		wantRules []string
	}{
		{
			name:  "valid standard component",
			rules: &linter.RulesConfig{},
			manifests: map[string]string{
				"comp.yaml": `
apiVersion: components.xmc.dev/v1alpha1
kind: Component
metadata:
  name: test-component
spec:
  type: CUSTOM_1P_COMPONENT
  version: "1.0.0"
  deletionPolicy: DELETE
  lifecycleControl:
    drainingTimeout: 300s
`,
			},
			wantValid: true,
		},
		{
			name:  "missing metadata.name in raw manifest",
			rules: &linter.RulesConfig{},
			manifests: map[string]string{
				"service.yaml": `
apiVersion: v1
kind: Service
metadata:
  namespace: default
`,
			},
			wantValid: false,
			wantRules: []string{"REQUIRED_FIELD"},
		},
		{
			name:  "invalid component name with prefix slash rejected",
			rules: &linter.RulesConfig{},
			manifests: map[string]string{
				"comp.yaml": `
apiVersion: components.xmc.dev/v1alpha1
kind: Component
metadata:
  name: components/my-comp
spec:
  version: "1.0.0"
`,
			},
			wantValid: false,
			wantRules: []string{"INVALID_COMPONENT_NAME"},
		},
		{
			name:  "invalid component name DNS-1123 format",
			rules: &linter.RulesConfig{},
			manifests: map[string]string{
				"comp.yaml": `
apiVersion: components.xmc.dev/v1alpha1
kind: Component
metadata:
  name: Invalid_Component_Name!
spec:
  version: "1.0.0"
`,
			},
			wantValid: false,
			wantRules: []string{"INVALID_COMPONENT_NAME"},
		},
		{
			name: "enforce namespace scope rejects lowercase cluster-scoped kind clusterrole",
			rules: &linter.RulesConfig{
				StaticChecks: linter.StaticChecksConfig{
					EnforceNamespaceScope: true,
				},
			},
			manifests: map[string]string{
				"role.yaml": `
apiVersion: rbac.authorization.k8s.io/v1
kind: clusterrole
metadata:
  name: secret-reader
`,
			},
			wantValid: false,
			wantRules: []string{"CLUSTER_SCOPED_KIND_REJECTED"},
		},
		{
			name: "enforce namespace scope rejects ValidatingAdmissionPolicyBinding",
			rules: &linter.RulesConfig{
				StaticChecks: linter.StaticChecksConfig{
					EnforceNamespaceScope: true,
				},
			},
			manifests: map[string]string{
				"binding.yaml": `
apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicyBinding
metadata:
  name: demo-binding
`,
			},
			wantValid: false,
			wantRules: []string{"CLUSTER_SCOPED_KIND_REJECTED"},
		},
		{
			name: "enforce namespace scope rejects IngressClass",
			rules: &linter.RulesConfig{
				StaticChecks: linter.StaticChecksConfig{
					EnforceNamespaceScope: true,
				},
			},
			manifests: map[string]string{
				"ingress-class.yaml": `
apiVersion: networking.k8s.io/v1
kind: IngressClass
metadata:
  name: custom-ingress
`,
			},
			wantValid: false,
			wantRules: []string{"CLUSTER_SCOPED_KIND_REJECTED"},
		},
		{
			name: "enforce namespace scope rejects CustomResourceDefinition",
			rules: &linter.RulesConfig{
				StaticChecks: linter.StaticChecksConfig{
					EnforceNamespaceScope: true,
				},
			},
			manifests: map[string]string{
				"crd.yaml": `
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: crontabs.stable.example.com
`,
			},
			wantValid: false,
			wantRules: []string{"CLUSTER_SCOPED_KIND_REJECTED"},
		},
		{
			name: "enforce namespace scope allows namespaced Deployment",
			rules: &linter.RulesConfig{
				StaticChecks: linter.StaticChecksConfig{
					EnforceNamespaceScope: true,
				},
			},
			manifests: map[string]string{
				"deploy.yaml": `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: nginx-deployment
  namespace: prod
`,
			},
			wantValid: true,
		},
		{
			name:  "invalid componentType",
			rules: &linter.RulesConfig{},
			manifests: map[string]string{
				"comp.yaml": `
apiVersion: components.xmc.dev/v1alpha1
kind: Component
metadata:
  name: bad-type
spec:
  componentType: INVALID_UNKNOWN_TYPE
`,
			},
			wantValid: false,
			wantRules: []string{"INVALID_COMPONENT_TYPE"},
		},
		{
			name:  "invalid deletionPolicy",
			rules: &linter.RulesConfig{},
			manifests: map[string]string{
				"comp.yaml": `
apiVersion: components.xmc.dev/v1alpha1
kind: Component
metadata:
  name: bad-policy
spec:
  deletionPolicy: DESTROY_EVERYTHING
`,
			},
			wantValid: false,
			wantRules: []string{"INVALID_DELETION_POLICY"},
		},
		{
			name:  "3P wrapper missing wrapperRef",
			rules: &linter.RulesConfig{},
			manifests: map[string]string{
				"comp.yaml": `
apiVersion: components.xmc.dev/v1alpha1
kind: Component
metadata:
  name: wrapper-comp
spec:
  componentType: CUSTOM_3P_WRAPPER
`,
			},
			wantValid: false,
			wantRules: []string{"MISSING_WRAPPER_REF"},
		},
		{
			name:  "3P wrapper with incomplete wrapperRef fields",
			rules: &linter.RulesConfig{},
			manifests: map[string]string{
				"comp.yaml": `
apiVersion: components.xmc.dev/v1alpha1
kind: Component
metadata:
  name: wrapper-comp
spec:
  componentType: CUSTOM_3P_WRAPPER
  wrapperRef:
    crdGroup: jobset.x-k8s.io
    crdVersion: ""
    crdKind: JobSet
    targetName: ""
`,
			},
			wantValid: false,
			wantRules: []string{"INVALID_WRAPPER_REF"},
		},
		{
			name:  "3P wrapper with valid wrapperRef",
			rules: &linter.RulesConfig{},
			manifests: map[string]string{
				"comp.yaml": `
apiVersion: components.xmc.dev/v1alpha1
kind: Component
metadata:
  name: wrapper-comp
spec:
  componentType: CUSTOM_3P_WRAPPER
  wrapperRef:
    crdGroup: jobset.x-k8s.io
    crdVersion: v1alpha2
    crdKind: JobSet
    targetName: jobset-controller
`,
			},
			wantValid: true,
		},
		{
			name:  "negative drain timeout rejected",
			rules: &linter.RulesConfig{},
			manifests: map[string]string{
				"comp.yaml": `
apiVersion: components.xmc.dev/v1alpha1
kind: Component
metadata:
  name: neg-drain
spec:
  lifecycleControl:
    drainTimeoutSeconds: -10
`,
			},
			wantValid: false,
			wantRules: []string{"SCHEMA_ERROR"},
		},
		{
			name: "drain timeout exceeding max allowed",
			rules: &linter.RulesConfig{
				StaticChecks: linter.StaticChecksConfig{
					MaxDrainTimeoutSeconds: 120,
				},
			},
			manifests: map[string]string{
				"comp.yaml": `
apiVersion: components.xmc.dev/v1alpha1
kind: Component
metadata:
  name: long-drain
spec:
  lifecycleControl:
    drainTimeoutSeconds: 300
`,
			},
			wantValid: false,
			wantRules: []string{"DRAIN_TIMEOUT_EXCEEDED"},
		},
		{
			name:  "negative grace period seconds rejected",
			rules: &linter.RulesConfig{},
			manifests: map[string]string{
				"comp.yaml": `
apiVersion: components.xmc.dev/v1alpha1
kind: Component
metadata:
  name: bad-grace
spec:
  lifecycleControl:
    gracePeriodSeconds: -5
`,
			},
			wantValid: false,
			wantRules: []string{"SCHEMA_ERROR"},
		},
		{
			name:  "integer string duration in lifecycleControl is supported",
			rules: &linter.RulesConfig{},
			manifests: map[string]string{
				"comp.yaml": `
apiVersion: components.xmc.dev/v1alpha1
kind: Component
metadata:
  name: int-duration
spec:
  lifecycleControl:
    drainingTimeout: "300"
    gracePeriod: "60"
`,
			},
			wantValid: true,
		},
		{
			name: "enforce deletion protection missing or false",
			rules: &linter.RulesConfig{
				StaticChecks: linter.StaticChecksConfig{
					EnforceDeletionProtection: true,
				},
			},
			manifests: map[string]string{
				"comp.yaml": `
apiVersion: components.xmc.dev/v1alpha1
kind: Component
metadata:
  name: unprotected
spec:
  deletionProtection: false
`,
			},
			wantValid: false,
			wantRules: []string{"DELETION_PROTECTION_REQUIRED"},
		},
		{
			name: "enforce deletion protection satisfied",
			rules: &linter.RulesConfig{
				StaticChecks: linter.StaticChecksConfig{
					EnforceDeletionProtection: true,
				},
			},
			manifests: map[string]string{
				"comp.yaml": `
apiVersion: components.xmc.dev/v1alpha1
kind: Component
metadata:
  name: protected
spec:
  deletionProtection: true
`,
			},
			wantValid: true,
		},
		{
			name:  "missing apiVersion in raw manifest",
			rules: &linter.RulesConfig{},
			manifests: map[string]string{
				"comp.yaml": `
kind: Component
metadata:
  name: missing-apiversion
spec:
  version: "1.0.0"
`,
			},
			wantValid: false,
			wantRules: []string{"REQUIRED_FIELD"},
		},
		{
			name:  "missing kind in raw manifest",
			rules: &linter.RulesConfig{},
			manifests: map[string]string{
				"comp.yaml": `
apiVersion: components.xmc.dev/v1alpha1
metadata:
  name: missing-kind
spec:
  version: "1.0.0"
`,
			},
			wantValid: false,
			wantRules: []string{"REQUIRED_FIELD"},
		},
		{
			name:  "wrapperRef disallowed on 1P custom component",
			rules: &linter.RulesConfig{},
			manifests: map[string]string{
				"comp.yaml": `
apiVersion: components.xmc.dev/v1alpha1
kind: Component
metadata:
  name: custom-comp
spec:
  componentType: CUSTOM_1P_COMPONENT
  wrapperRef:
    crdGroup: jobset.x-k8s.io
    targetKind: JobSet
    targetName: jobset-controller
`,
			},
			wantValid: false,
			wantRules: []string{"INVALID_COMPONENT_TYPE"},
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
				t.Fatalf("ValidateDirectory() unexpected execution error: %v", err)
			}
			if res.Valid != tc.wantValid {
				t.Errorf("res.Valid = %v, want %v (errors: %v)", res.Valid, tc.wantValid, res.Errors)
			}
			assertDiagnosticRules(t, res, tc.wantRules)
		})
	}
}

func assertDiagnosticRules(t *testing.T, res *linter.ValidationResult, wantRules []string) {
	t.Helper()
	for _, rule := range wantRules {
		assertDiagnosticRule(t, res, rule)
	}
}

func TestLinter_ZeroValueRulesValidation(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	p := filepath.Join(dir, "comp.yaml")
	content := `
apiVersion: components.xmc.dev/v1alpha1
kind: Component
metadata:
  name: safe-component
spec:
  version: "1.0.0"
`
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test manifest: %v", err)
	}

	t.Run("zero value struct Linter does not panic", func(t *testing.T) {
		t.Parallel()
		var l linter.Linter
		res, err := l.ValidateDirectory(dir)
		if err != nil {
			t.Fatalf("ValidateDirectory() unexpected error: %v", err)
		}
		if !res.Valid {
			t.Errorf("expected valid result, got errors: %v", res.Errors)
		}
	})

	t.Run("NewLinter with nil rules does not panic", func(t *testing.T) {
		t.Parallel()
		l := linter.NewLinter(nil)
		res, err := l.ValidateDirectory(dir)
		if err != nil {
			t.Fatalf("ValidateDirectory() unexpected error: %v", err)
		}
		if !res.Valid {
			t.Errorf("expected valid result, got errors: %v", res.Errors)
		}
	})
}

func TestSchema_EdgeCaseCoverage(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	compFile := filepath.Join(dir, "comp_wrapper.yaml")
	content := `
apiVersion: components.xmc.dev/v1alpha1
kind: Component
metadata:
  name: wrapper-empty-fields
spec:
  componentType: CUSTOM_3P_WRAPPER
  wrapperRef:
    crdGroup: ""
    crdVersion: "v1"
    crdKind: ""
    targetName: "jobset"
`
	if err := os.WriteFile(compFile, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test manifest: %v", err)
	}

	l := linter.NewLinter(nil)
	res, err := l.ValidateDirectory(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Valid {
		t.Errorf("expected invalid result for empty wrapper fields")
	}
	assertDiagnosticRule(t, res, "INVALID_WRAPPER_REF")
}
