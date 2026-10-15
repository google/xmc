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

// Helm chart template contract test for xmc-bridge single-writer leader election and probes.
// Production targets: deploy/helm/templates/components/deployment.yaml and rbac.yaml
// (paired with the runtime options in component_manager.go).
// Test type: hermetic static manifest test (reads the Helm template runfiles directly without
// a cluster). Kept alongside components_rbac_test.go in package bridge so bridge runtime changes
// and their required Deployment/Lease RBAC manifest invariants are verified together.
package bridge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBridgeChartSingleWriter asserts that the chart lets xmc-bridge take and renew its Lease,
// stops the old bridge before a rollout starts the new one, gives a stopping leader time to
// release the Lease, and probes the endpoints the manager serves. Two bridges reconciling one
// namespace race on CMS intent, Helm releases and finalizers.
func TestBridgeChartSingleWriter(t *testing.T) {
	tests := []struct {
		template string
		want     []string
		dontWant []string
	}{
		{
			template: "deployment.yaml",
			want: []string{
				"strategy:\n    type: Recreate\n",
				"terminationGracePeriodSeconds: 45\n",
				"- name: health\n          containerPort: 8081\n",
				"livenessProbe:\n          httpGet:\n            path: /healthz\n            port: health\n",
				"readinessProbe:\n          httpGet:\n            path: /readyz\n            port: health\n",
			},
			// Metrics have no authentication and listen on loopback only.
			dontWant: []string{"containerPort: 8080"},
		},
		{
			template: "rbac.yaml",
			want: []string{
				"kind: Role\nmetadata:\n  name: xmc-bridge-leader-election\n",
				"rules:\n  - apiGroups: [\"coordination.k8s.io\"]\n    resources: [\"leases\"]\n    verbs: [\"create\"]\n" +
					"  - apiGroups: [\"coordination.k8s.io\"]\n    resources: [\"leases\"]\n    resourceNames: [\"" + ComponentLeaseName + "\"]\n    verbs: [\"get\", \"update\"]\n",
				"roleRef:\n  kind: Role\n  name: xmc-bridge-leader-election\n",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.template, func(t *testing.T) {
			raw := readComponentsTemplate(t, tt.template)
			for _, want := range tt.want {
				if !strings.Contains(raw, want) {
					t.Errorf("%s does not contain:\n%s", tt.template, want)
				}
			}
			for _, dontWant := range tt.dontWant {
				if strings.Contains(raw, dontWant) {
					t.Errorf("%s contains %q", tt.template, dontWant)
				}
			}
		})
	}
}

// TestBridgeChartLeaseRulesStayNamespaced asserts that the Lease rules are not part of
// xmc-bridge-role, which becomes a ClusterRole with rbac.clusterScope.
func TestBridgeChartLeaseRulesStayNamespaced(t *testing.T) {
	raw := readComponentsTemplate(t, "rbac.yaml")
	mainRole, _, _ := strings.Cut(raw, "\n---\n")
	if strings.Contains(mainRole, "coordination.k8s.io") {
		t.Errorf("xmc-bridge-role grants Lease access; want it only in the namespaced xmc-bridge-leader-election Role:\n%s", mainRole)
	}
}

// readComponentsTemplate returns the raw deploy/helm/templates/components/name template.
func readComponentsTemplate(t *testing.T, name string) string {
	t.Helper()
	repoRoot, err := findRepoRoot()
	if err != nil {
		t.Fatalf("could not locate repository root: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(repoRoot, "deploy", "helm", "templates", "components", name))
	if err != nil {
		t.Fatalf("failed to read template %s: %v", name, err)
	}
	return string(raw)
}
