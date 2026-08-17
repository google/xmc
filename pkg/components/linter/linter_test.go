// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package linter

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLinter_Pass1_Pass2_Pass3(t *testing.T) {
	tempDir := t.TempDir()

	// Create rules.yaml
	rulesContent := `
profile: "stable-2026a"
compatibilityMatrix:
  componentDependencies:
    - component: "ingestion-gateway"
      version: "1.0.0"
      requires:
        - component: "redpanda"
          versionRange: ">=1.0.0"
staticChecks:
  enforceDeletionProtection: true
  maxDrainTimeoutSeconds: 3600
  enforceNamespaceScope: true
`
	rulesPath := filepath.Join(tempDir, "rules.yaml")
	if err := os.WriteFile(rulesPath, []byte(rulesContent), 0644); err != nil {
		t.Fatalf("failed to write rules: %v", err)
	}

	rules, err := LoadRules(rulesPath)
	if err != nil {
		t.Fatalf("LoadRules failed: %v", err)
	}
	l := NewLinter(rules)

	// Valid manifests: Redpanda & Ingestion Gateway
	manifestDir := filepath.Join(tempDir, "manifests")
	if err := os.MkdirAll(manifestDir, 0755); err != nil {
		t.Fatalf("failed to mkdir: %v", err)
	}

	redpandaYAML := `
apiVersion: xmc.dev/v1alpha1
kind: Component
metadata:
  name: redpanda
  namespace: xmc
spec:
  enabled: true
  componentType: COMPONENT_TYPE_CUSTOM_3P_WRAPPER
  componentClass: "event-stream"
  deletionProtection: true
`
	if err := os.WriteFile(filepath.Join(manifestDir, "redpanda.yaml"), []byte(redpandaYAML), 0644); err != nil {
		t.Fatalf("failed to write redpanda manifest: %v", err)
	}

	gatewayYAML := `
apiVersion: xmc.dev/v1alpha1
kind: Component
metadata:
  name: ingestion-gateway
  namespace: xmc
spec:
  enabled: true
  componentType: COMPONENT_TYPE_CUSTOM_1P_COMPONENT
  componentClass: "metrics-ingestion"
  deletionProtection: true
  dependencies:
    - redpanda
`
	if err := os.WriteFile(filepath.Join(manifestDir, "gateway.yaml"), []byte(gatewayYAML), 0644); err != nil {
		t.Fatalf("failed to write gateway manifest: %v", err)
	}

	res, err := l.ValidateDirectory(manifestDir)
	if err != nil {
		t.Fatalf("ValidateDirectory failed: %v", err)
	}
	if !res.Valid || len(res.Errors) > 0 {
		t.Errorf("expected valid=true, got errors: %v", res.Errors)
	}
}

func TestLinter_CycleDetection(t *testing.T) {
	tempDir := t.TempDir()
	rules := RulesConfig{}
	l := NewLinter(rules)

	manifestDir := filepath.Join(tempDir, "cyclic")
	if err := os.MkdirAll(manifestDir, 0755); err != nil {
		t.Fatalf("failed to mkdir: %v", err)
	}

	compA := `
apiVersion: xmc.dev/v1alpha1
kind: Component
metadata:
  name: comp-a
spec:
  enabled: true
  dependencies:
    - comp-b
`
	compB := `
apiVersion: xmc.dev/v1alpha1
kind: Component
metadata:
  name: comp-b
spec:
  enabled: true
  dependencies:
    - comp-a
`
	_ = os.WriteFile(filepath.Join(manifestDir, "a.yaml"), []byte(compA), 0644)
	_ = os.WriteFile(filepath.Join(manifestDir, "b.yaml"), []byte(compB), 0644)

	res, err := l.ValidateDirectory(manifestDir)
	if err != nil {
		t.Fatalf("ValidateDirectory failed: %v", err)
	}
	if res.Valid || len(res.Errors) == 0 {
		t.Errorf("expected cycle detection failure, got valid=true")
	}
}

func TestLinter_SemverCompatibilityCheck(t *testing.T) {
	tempDir := t.TempDir()

	rulesContent := `
profile: "stable-2026a"
compatibilityMatrix:
  componentDependencies:
    - component: "ingestion-gateway"
      version: "1.0.0"
      requires:
        - component: "redpanda"
          versionRange: ">=2.0.0"
`
	rulesPath := filepath.Join(tempDir, "rules.yaml")
	_ = os.WriteFile(rulesPath, []byte(rulesContent), 0644)

	rules, err := LoadRules(rulesPath)
	if err != nil {
		t.Fatalf("LoadRules failed: %v", err)
	}
	l := NewLinter(rules)

	manifestDir := filepath.Join(tempDir, "manifests")
	_ = os.MkdirAll(manifestDir, 0755)

	// Redpanda has chartVersion: 1.2.0, which violates >=2.0.0 requirement
	redpandaYAML := `
apiVersion: xmc.dev/v1alpha1
kind: Component
metadata:
  name: redpanda
spec:
  enabled: true
  chartVersion: "1.2.0"
`
	gatewayYAML := `
apiVersion: xmc.dev/v1alpha1
kind: Component
metadata:
  name: ingestion-gateway
spec:
  enabled: true
  dependencies:
    - redpanda
`
	_ = os.WriteFile(filepath.Join(manifestDir, "redpanda.yaml"), []byte(redpandaYAML), 0644)
	_ = os.WriteFile(filepath.Join(manifestDir, "gateway.yaml"), []byte(gatewayYAML), 0644)

	res, err := l.ValidateDirectory(manifestDir)
	if err != nil {
		t.Fatalf("ValidateDirectory failed: %v", err)
	}
	if res.Valid || len(res.Errors) == 0 {
		t.Errorf("expected semver compatibility matrix violation for redpanda 1.2.0 vs >=2.0.0, got valid")
	}
}
