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
apiVersion: xmc.dev/v1alpha1
kind: Component
metadata:
  name: test-component
spec:
  componentType: CUSTOM_1P_COMPONENT
  version: "1.0.0"
  deletionPolicy: DELETE
  lifecycleControl:
    drainTimeoutSeconds: 300s
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
			name:  "missing metadata.name in component manifest only triggers REQUIRED_FIELD",
			rules: &linter.RulesConfig{},
			manifests: map[string]string{
				"comp.yaml": `
apiVersion: xmc.dev/v1alpha1
kind: Component
spec:
  version: "1.0.0"
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
apiVersion: xmc.dev/v1alpha1
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
apiVersion: xmc.dev/v1alpha1
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
			name:  "invalid componentType",
			rules: &linter.RulesConfig{},
			manifests: map[string]string{
				"comp.yaml": `
apiVersion: xmc.dev/v1alpha1
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
apiVersion: xmc.dev/v1alpha1
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
			name:  "legacy deletionPolicy emits DEPRECATED_DELETION_POLICY warning",
			rules: &linter.RulesConfig{},
			manifests: map[string]string{
				"comp.yaml": `
apiVersion: xmc.dev/v1alpha1
kind: Component
metadata:
  name: legacy-policy
spec:
  componentType: COMPONENT_TYPE_CUSTOM_1P_COMPONENT
  deletionPolicy: DELETE
`,
			},
			wantValid: true,
			wantRules: []string{"DEPRECATED_DELETION_POLICY"},
		},
		{
			name:  "3P wrapper missing wrapperRef",
			rules: &linter.RulesConfig{},
			manifests: map[string]string{
				"comp.yaml": `
apiVersion: xmc.dev/v1alpha1
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
apiVersion: xmc.dev/v1alpha1
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
apiVersion: xmc.dev/v1alpha1
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
			name:  "3P wrapper without wrapperRef but with connection parameters",
			rules: &linter.RulesConfig{},
			manifests: map[string]string{
				"comp.yaml": `
apiVersion: xmc.dev/v1alpha1
kind: Component
metadata:
  name: clickhouse
spec:
  componentType: COMPONENT_TYPE_CUSTOM_3P_WRAPPER
  parameters:
    host: "clickhouse.xmc-metrics.svc.cluster.local"
    healthCheckEndpoint: "http://clickhouse.xmc-metrics.svc.cluster.local:8123/ping"
`,
			},
			wantValid: true,
		},
		{
			name:  "negative drain timeout rejected",
			rules: &linter.RulesConfig{},
			manifests: map[string]string{
				"comp.yaml": `
apiVersion: xmc.dev/v1alpha1
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
apiVersion: xmc.dev/v1alpha1
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
apiVersion: xmc.dev/v1alpha1
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
apiVersion: xmc.dev/v1alpha1
kind: Component
metadata:
  name: int-duration
spec:
  lifecycleControl:
    drainTimeoutSeconds: "300"
    gracePeriodSeconds: "60"
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
apiVersion: xmc.dev/v1alpha1
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
apiVersion: xmc.dev/v1alpha1
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
apiVersion: xmc.dev/v1alpha1
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
apiVersion: xmc.dev/v1alpha1
kind: Component
metadata:
  name: custom-comp
spec:
  componentType: CUSTOM_1P_COMPONENT
  wrapperRef:
    crdGroup: jobset.x-k8s.io
    crdKind: JobSet
    targetName: jobset-controller
`,
			},
			wantValid: false,
			wantRules: []string{"INVALID_COMPONENT_TYPE"},
		},
		{
			name:  "informal componentType 1P is rejected",
			rules: &linter.RulesConfig{},
			manifests: map[string]string{
				"comp.yaml": `
apiVersion: xmc.dev/v1alpha1
kind: Component
metadata:
  name: informal-type
spec:
  componentType: "1P"
`,
			},
			wantValid: false,
			wantRules: []string{"INVALID_COMPONENT_TYPE"},
		},
		{
			name:  "informal deletionPolicy is rejected",
			rules: &linter.RulesConfig{},
			manifests: map[string]string{
				"comp.yaml": `
apiVersion: xmc.dev/v1alpha1
kind: Component
metadata:
  name: informal-policy
spec:
  deletionPolicy: "DESTROY"
`,
			},
			wantValid: false,
			wantRules: []string{"INVALID_DELETION_POLICY"},
		},
		{
			name:  "proto-prefixed componentType COMPONENT_TYPE_CUSTOM_1P_COMPONENT is accepted",
			rules: &linter.RulesConfig{},
			manifests: map[string]string{
				"comp.yaml": `
apiVersion: xmc.dev/v1alpha1
kind: Component
metadata:
  name: proto-type
spec:
  componentType: "COMPONENT_TYPE_CUSTOM_1P_COMPONENT"
`,
			},
			wantValid: true,
		},
		{
			name:  "proto-prefixed and TitleCase deletionPolicy values are accepted",
			rules: &linter.RulesConfig{},
			manifests: map[string]string{
				"comp1.yaml": `
apiVersion: xmc.dev/v1alpha1
kind: Component
metadata:
  name: proto-policy
spec:
  deletionPolicy: "DELETION_POLICY_DELETE"
`,
				"comp2.yaml": `
apiVersion: xmc.dev/v1alpha1
kind: Component
metadata:
  name: crd-policy
spec:
  deletionPolicy: "Delete"
`,
			},
			wantValid: true,
		},
		{
			name:  "3P wrapper with invalid observedGenerationPath or readyConditionPath is rejected",
			rules: &linter.RulesConfig{},
			manifests: map[string]string{
				"comp.yaml": `
apiVersion: xmc.dev/v1alpha1
kind: Component
metadata:
  name: invalid-paths
spec:
  componentType: COMPONENT_TYPE_CUSTOM_3P_WRAPPER
  wrapperRef:
    group: redpanda.vectorized.io
    version: v1alpha1
    kind: Cluster
    name: redpanda
    observedGenerationPath: "$.spec.generation"
    readyConditionPath: "status.ready"
`,
			},
			wantValid: false,
			wantRules: []string{"INVALID_WRAPPER_REF"},
		},
		{
			name:  "3P wrapper with endpointJsonPath or connectionSecretJsonPath outside status is rejected",
			rules: &linter.RulesConfig{},
			manifests: map[string]string{
				"comp.yaml": `
apiVersion: xmc.dev/v1alpha1
kind: Component
metadata:
  name: invalid-endpoint-paths
spec:
  componentType: COMPONENT_TYPE_CUSTOM_3P_WRAPPER
  wrapperRef:
    group: redpanda.vectorized.io
    version: v1alpha1
    kind: Cluster
    name: redpanda
    endpointJsonPath: "http://169.254.169.254/computeMetadata/v1/"
    connectionSecretJsonPath: "$.spec.secretName"
`,
			},
			wantValid: false,
			wantRules: []string{"INVALID_WRAPPER_REF"},
		},
		{
			name:  "component with non-string parameter value is rejected",
			rules: &linter.RulesConfig{},
			manifests: map[string]string{
				"comp.yaml": `
apiVersion: xmc.dev/v1alpha1
kind: Component
metadata:
  name: non-string-param
spec:
  componentType: COMPONENT_TYPE_CUSTOM_1P_COMPONENT
  parameters:
    kafkaPort: 9092
`,
			},
			wantValid: false,
			wantRules: []string{"SCHEMA_ERROR"},
		},
		{
			name:  "explicit UNSPECIFIED componentType and deletionPolicy is valid",
			rules: &linter.RulesConfig{},
			manifests: map[string]string{
				"comp.yaml": `
apiVersion: xmc.dev/v1alpha1
kind: Component
metadata:
  name: unspecified-comp
spec:
  componentType: "UNSPECIFIED"
  deletionPolicy: "UNSPECIFIED"
`,
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
	writeTestManifest(t, dir, "comp.yaml", `
apiVersion: xmc.dev/v1alpha1
kind: Component
metadata:
  name: safe-component
spec:
  version: "1.0.0"
`)

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
	writeTestManifest(t, dir, "comp_wrapper.yaml", `
apiVersion: xmc.dev/v1alpha1
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
`)

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
