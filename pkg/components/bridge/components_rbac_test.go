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

package bridge

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func findRepoRoot() (string, error) {
	candidates := []string{
		".",
		"../..",
		"../../..",
	}
	if srcDir := os.Getenv("TEST_SRCDIR"); srcDir != "" {
		workspace := os.Getenv("TEST_WORKSPACE")
		if workspace != "" {
			candidates = append([]string{filepath.Join(srcDir, workspace)}, candidates...)
		}
	}
	for _, c := range candidates {
		if _, err := os.Stat(filepath.Join(c, "deploy", "helm", "Chart.yaml")); err == nil {
			return c, nil
		}
	}
	return "", os.ErrNotExist
}

func TestComponentsRBAC_TemplateRendering(t *testing.T) {
	repoRoot, err := findRepoRoot()
	if err != nil {
		t.Fatalf("could not locate repository root: %v", err)
	}

	rbacPath := filepath.Join(repoRoot, "deploy", "helm", "templates", "components", "rbac.yaml")
	rawContent, err := os.ReadFile(rbacPath)
	if err != nil {
		t.Fatalf("failed to read rbac template %q: %v", rbacPath, err)
	}

	contentStr := string(rawContent)

	// Verify ClusterRole and ClusterRoleBinding are structurally present as options
	if !strings.Contains(contentStr, "kind: ClusterRole\n") {
		t.Errorf("expected rbac.yaml to define kind: ClusterRole as an option")
	}
	if !strings.Contains(contentStr, "kind: Role\n") {
		t.Errorf("expected rbac.yaml to define kind: Role as an option")
	}

	if !strings.Contains(contentStr, "resources: [\"configmaps\"]") {
		t.Errorf("expected rbac.yaml to grant configmaps permissions")
	}
	if !strings.Contains(contentStr, "namespace: {{ .Release.Namespace }}") {
		t.Errorf("expected subject in ClusterRoleBinding to specify release namespace")
	}

	// If helm is available on PATH, execute helm template and verify rendered manifests
	helmPath, err := exec.LookPath("helm")
	if err != nil {
		t.Log("helm binary not found on PATH; skipping live helm template execution")
		return
	}

	chartDir := filepath.Join(repoRoot, "deploy", "helm")

	// 1. Render when enabled
	cmd := exec.Command(helmPath, "template", "xmc", chartDir,
		"-s", "templates/components/rbac.yaml",
		"--set", "componentsDeployment.enabled=true",
		"--namespace", "custom-ns")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("helm template failed: %v\nOutput: %s", err, string(out))
	}

	rendered := string(out)
	if !strings.Contains(rendered, "kind: Role") || strings.Contains(rendered, "kind: ClusterRole") {
		t.Errorf("expected rendered output to contain 'kind: Role' (default scope), got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "name: xmc-bridge-role") {
		t.Errorf("expected rendered output to contain 'name: xmc-bridge-role', got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "kind: RoleBinding") || strings.Contains(rendered, "kind: ClusterRoleBinding") {
		t.Errorf("expected rendered output to contain 'kind: RoleBinding', got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "name: xmc-bridge-rolebinding") {
		t.Errorf("expected rendered output to contain 'name: xmc-bridge-rolebinding', got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "namespace: custom-ns") {
		t.Errorf("expected rendered output to contain 'namespace: custom-ns', got:\n%s", rendered)
	}

	// 2. Render when disabled -> output should be empty or comment-only
	cmdDisabled := exec.Command(helmPath, "template", "xmc", chartDir,
		"-s", "templates/components/rbac.yaml",
		"--set", "componentsDeployment.enabled=false")
	outDisabled, err := cmdDisabled.CombinedOutput()
	if err != nil {
		t.Fatalf("helm template (disabled) failed: %v\nOutput: %s", err, string(outDisabled))
	}
	renderedDisabled := strings.TrimSpace(string(outDisabled))
	if strings.Contains(renderedDisabled, "ClusterRole") || strings.Contains(renderedDisabled, "ClusterRoleBinding") {
		t.Errorf("expected no RBAC resources rendered when disabled, got:\n%s", renderedDisabled)
	}
}
