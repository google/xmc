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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"text/template"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
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

// helmTemplateFuncs returns the subset of Helm template functions used by the rendered chart templates.
func helmTemplateFuncs() template.FuncMap {
	return template.FuncMap{
		// toJson mirrors Helm's toJson, which renders an empty string on marshal errors.
		"toJson": func(v any) string {
			b, err := json.Marshal(v)
			if err != nil {
				return ""
			}
			return string(b)
		},
	}
}

func renderChartTemplateWithValues(t *testing.T, relTemplatePath string, releaseNS string, enabled, clusterScope bool) []byte {
	t.Helper()
	repoRoot, err := findRepoRoot()
	if err != nil {
		t.Fatalf("could not locate repository root: %v", err)
	}
	valuesRaw, err := os.ReadFile(filepath.Join(repoRoot, "deploy", "helm", "values.yaml"))
	if err != nil {
		t.Fatalf("failed to read values.yaml: %v", err)
	}
	var values map[string]any
	if err := yaml.Unmarshal(valuesRaw, &values); err != nil {
		t.Fatalf("failed to unmarshal values.yaml: %v", err)
	}
	compDeploy, ok := values["componentsDeployment"].(map[string]any)
	if !ok {
		t.Fatalf("values.yaml: componentsDeployment is missing or not a map: %T", values["componentsDeployment"])
	}
	compDeploy["enabled"] = enabled
	rbacCfg, ok := compDeploy["rbac"].(map[string]any)
	if !ok {
		t.Fatalf("values.yaml: componentsDeployment.rbac is missing or not a map: %T", compDeploy["rbac"])
	}
	rbacCfg["clusterScope"] = clusterScope

	tmplRaw, err := os.ReadFile(filepath.Join(repoRoot, "deploy", "helm", "templates", relTemplatePath))
	if err != nil {
		t.Fatalf("failed to read template %s: %v", relTemplatePath, err)
	}
	tmpl, err := template.New(filepath.Base(relTemplatePath)).Funcs(helmTemplateFuncs()).Parse(string(tmplRaw))
	if err != nil {
		t.Fatalf("failed to parse template %s: %v", relTemplatePath, err)
	}
	var buf bytes.Buffer
	data := map[string]any{
		"Values":  values,
		"Release": map[string]any{"Namespace": releaseNS},
	}
	if err := tmpl.Execute(&buf, data); err != nil {
		t.Fatalf("failed to execute template %s: %v", relTemplatePath, err)
	}
	return buf.Bytes()
}

func assertNoWildcardRules(t *testing.T, roleName string, rules []rbacv1.PolicyRule) {
	t.Helper()
	for _, r := range rules {
		if slices.Contains(r.APIGroups, "*") || slices.Contains(r.Resources, "*") || slices.Contains(r.Verbs, "*") {
			t.Errorf("role %q contains wildcard RBAC rule: %+v", roleName, r)
		}
	}
}

func TestComponentsRBAC_TemplateRendering(t *testing.T) {
	repoRoot, err := findRepoRoot()
	if err != nil {
		t.Fatalf("could not locate repository root: %v", err)
	}

	renderedDefault := string(renderChartTemplateWithValues(t, filepath.Join("components", "rbac.yaml"), "custom-ns", true, false))
	if !strings.Contains(renderedDefault, "kind: Role\nmetadata:\n  name: xmc-bridge-role") {
		t.Errorf("expected default rbac.yaml render to define Role xmc-bridge-role, got:\n%s", renderedDefault)
	}
	if strings.Contains(renderedDefault, `apiGroups: ["*"]`) || strings.Contains(renderedDefault, `resources: ["*"]`) {
		t.Errorf("expected rbac.yaml to contain no wildcard apiGroups/resources, got:\n%s", renderedDefault)
	}
	for _, wantGroup := range []string{`"clickhouse.altinity.com"`, `"cluster.redpanda.com"`, `"ray.io"`, `"jobset.x-k8s.io"`} {
		if !strings.Contains(renderedDefault, wantGroup) {
			t.Errorf("expected default rendered rbac.yaml to include 3P wrapper group %s", wantGroup)
		}
	}

	if notesDefault := strings.TrimSpace(string(renderChartTemplateWithValues(t, "NOTES.txt", "custom-ns", true, false))); notesDefault != "" {
		t.Errorf("expected empty NOTES.txt when clusterScope=false, got: %q", notesDefault)
	}
	if notesCluster := string(renderChartTemplateWithValues(t, "NOTES.txt", "custom-ns", true, true)); !strings.Contains(notesCluster, "WARNING: componentsDeployment.rbac.clusterScope=true") {
		t.Errorf("expected NOTES.txt warning when clusterScope=true, got: %q", notesCluster)
	}

	helmPath, err := exec.LookPath("helm")
	if err != nil {
		t.Log("helm binary not found on PATH; skipping live helm template execution")
		return
	}
	chartDir := filepath.Join(repoRoot, "deploy", "helm")
	cmd := exec.Command(helmPath, "template", "xmc", chartDir,
		"-s", "templates/components/rbac.yaml",
		"--set", "componentsDeployment.enabled=true",
		"--namespace", "custom-ns")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("helm template failed: %v\nOutput: %s", err, string(out))
	}
	rendered := string(out)
	if !strings.Contains(rendered, "kind: Role\nmetadata:\n  name: xmc-bridge-role") || strings.Contains(rendered, "kind: ClusterRole\nmetadata:\n  name: xmc-bridge-role") {
		t.Errorf("expected rendered output to contain 'kind: Role' for xmc-bridge-role, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "kind: ClusterRole\nmetadata:\n  name: xmc-bridge-cluster-identity-custom-ns") {
		t.Errorf("expected rendered output to contain namespace-suffixed 'kind: ClusterRole' xmc-bridge-cluster-identity-custom-ns, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "kind: ClusterRoleBinding\nmetadata:\n  name: xmc-bridge-cluster-identity-custom-ns") {
		t.Errorf("expected rendered output to contain namespace-suffixed 'kind: ClusterRoleBinding' xmc-bridge-cluster-identity-custom-ns, got:\n%s", rendered)
	}
}

func TestComponentsRBAC_LeastPrivilegeEnvtest(t *testing.T) {
	ctx := context.Background()
	adminCfg := startComponentEnvtest(t)
	adminKube, err := kubernetes.NewForConfig(adminCfg)
	if err != nil {
		t.Fatalf("kubernetes.NewForConfig failed: %v", err)
	}
	if _, err := adminKube.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "xmc"}}, metav1.CreateOptions{}); err != nil {
		t.Fatalf("failed to create xmc namespace: %v", err)
	}
	applyBridgeRBACManifests(ctx, t, adminKube, "xmc", false)
	if _, err := adminKube.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "xmc-cs"}}, metav1.CreateOptions{}); err != nil {
		t.Fatalf("failed to create xmc-cs namespace: %v", err)
	}
	applyBridgeRBACManifests(ctx, t, adminKube, "xmc-cs", true)

	bridgeKube, bridgeDyn := impersonateBridgeSA(t, adminCfg, "xmc")
	clusterScopeKube, clusterScopeDyn := impersonateBridgeSA(t, adminCfg, "xmc-cs")
	// The RayCluster CRD is not installed: an authorized request reaches the API server's
	// not-found handler, while an unauthorized one is rejected with Forbidden first.
	rayClusters := schema.GroupVersionResource{Group: "ray.io", Version: "v1", Resource: "rayclusters"}

	isAllowed := func(err error) bool { return err == nil }
	tests := []struct {
		name string
		call func(context.Context) error
		want func(error) bool
	}{
		{
			name: "GET kube-system namespace is allowed",
			call: func(ctx context.Context) error {
				_, err := bridgeKube.CoreV1().Namespaces().Get(ctx, "kube-system", metav1.GetOptions{})
				return err
			},
			want: isAllowed,
		},
		{
			name: "GET default namespace is Forbidden",
			call: func(ctx context.Context) error {
				_, err := bridgeKube.CoreV1().Namespaces().Get(ctx, "default", metav1.GetOptions{})
				return err
			},
			want: apierrors.IsForbidden,
		},
		{
			name: "LIST components in release namespace is allowed",
			call: func(ctx context.Context) error {
				_, err := bridgeDyn.Resource(ComponentGVR).Namespace("xmc").List(ctx, metav1.ListOptions{})
				return err
			},
			want: isAllowed,
		},
		{
			name: "LIST components in default namespace is Forbidden under Role scope",
			call: func(ctx context.Context) error {
				_, err := bridgeDyn.Resource(ComponentGVR).Namespace("default").List(ctx, metav1.ListOptions{})
				return err
			},
			want: apierrors.IsForbidden,
		},
		{
			name: "LIST validatingadmissionpolicies is Forbidden (no wildcard read)",
			call: func(ctx context.Context) error {
				_, err := bridgeKube.AdmissionregistrationV1().ValidatingAdmissionPolicies().List(ctx, metav1.ListOptions{})
				return err
			},
			want: apierrors.IsForbidden,
		},
		{
			name: "DELETE allowlisted ingestion-gateway-components-reader Role passes RBAC (NotFound)",
			call: func(ctx context.Context) error {
				return bridgeKube.RbacV1().Roles("xmc").Delete(ctx, "ingestion-gateway-components-reader", metav1.DeleteOptions{})
			},
			want: apierrors.IsNotFound,
		},
		{
			name: "DELETE non-allowlisted xmc-bridge-role Role is Forbidden",
			call: func(ctx context.Context) error {
				return bridgeKube.RbacV1().Roles("xmc").Delete(ctx, "xmc-bridge-role", metav1.DeleteOptions{})
			},
			want: apierrors.IsForbidden,
		},
		{
			name: "PATCH ConfigMap in release namespace passes RBAC (NotFound)",
			call: func(ctx context.Context) error {
				_, err := bridgeKube.CoreV1().ConfigMaps("xmc").Patch(ctx, "xmc-cmp-missing", types.MergePatchType, []byte(`{}`), metav1.PatchOptions{})
				return err
			},
			want: apierrors.IsNotFound,
		},
		{
			name: "DELETE ConfigMap in release namespace passes RBAC (NotFound)",
			call: func(ctx context.Context) error {
				return bridgeKube.CoreV1().ConfigMaps("xmc").Delete(ctx, "xmc-cmp-missing", metav1.DeleteOptions{})
			},
			want: apierrors.IsNotFound,
		},
		{
			name: "LIST wrapper resources in release namespace passes RBAC (NotFound)",
			call: func(ctx context.Context) error {
				_, err := bridgeDyn.Resource(rayClusters).Namespace("xmc").List(ctx, metav1.ListOptions{})
				return err
			},
			want: apierrors.IsNotFound,
		},
		{
			name: "LIST wrapper resources in default namespace is Forbidden without clusterScope",
			call: func(ctx context.Context) error {
				_, err := bridgeDyn.Resource(rayClusters).Namespace("default").List(ctx, metav1.ListOptions{})
				return err
			},
			want: apierrors.IsForbidden,
		},
		{
			name: "clusterScope: LIST wrapper resources in default namespace passes RBAC (NotFound)",
			call: func(ctx context.Context) error {
				_, err := clusterScopeDyn.Resource(rayClusters).Namespace("default").List(ctx, metav1.ListOptions{})
				return err
			},
			want: apierrors.IsNotFound,
		},
		{
			name: "clusterScope: LIST components in default namespace is Forbidden",
			call: func(ctx context.Context) error {
				_, err := clusterScopeDyn.Resource(ComponentGVR).Namespace("default").List(ctx, metav1.ListOptions{})
				return err
			},
			want: apierrors.IsForbidden,
		},
		{
			name: "clusterScope: CREATE ConfigMap in default namespace is Forbidden",
			call: func(ctx context.Context) error {
				cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "xmc-cmp-probe"}}
				_, err := clusterScopeKube.CoreV1().ConfigMaps("default").Create(ctx, cm, metav1.CreateOptions{})
				return err
			},
			want: apierrors.IsForbidden,
		},
		{
			name: "clusterScope: DELETE Deployment in default namespace is Forbidden",
			call: func(ctx context.Context) error {
				return clusterScopeKube.AppsV1().Deployments("default").Delete(ctx, "xmc-cmp-probe", metav1.DeleteOptions{})
			},
			want: apierrors.IsForbidden,
		},
		{
			name: "clusterScope: LIST components in release namespace is allowed",
			call: func(ctx context.Context) error {
				_, err := clusterScopeDyn.Resource(ComponentGVR).Namespace("xmc-cs").List(ctx, metav1.ListOptions{})
				return err
			},
			want: isAllowed,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(ctx); !tc.want(err) {
				t.Errorf("unexpected result for xmc-bridge-sa: %v", err)
			}
		})
	}
}

// TestComponentsRBAC_ScopeRendering asserts that xmc-bridge-role stays a namespaced Role and
// that rbac.clusterScope only adds read-only cluster-scoped rules.
func TestComponentsRBAC_ScopeRendering(t *testing.T) {
	tests := []struct {
		clusterScope     bool
		wantClusterRoles []string
	}{
		{
			clusterScope:     false,
			wantClusterRoles: []string{"xmc-bridge-cluster-identity-custom-ns"},
		},
		{
			clusterScope:     true,
			wantClusterRoles: []string{"xmc-bridge-cluster-identity-custom-ns", "xmc-bridge-wrapper-reader-custom-ns"},
		},
	}
	readVerbs := []string{"get", "list", "watch"}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("clusterScope=%t", tt.clusterScope), func(t *testing.T) {
			rendered := renderChartTemplateWithValues(t, filepath.Join("components", "rbac.yaml"), "custom-ns", true, tt.clusterScope)
			var roles, clusterRoles []string
			for _, doc := range decodeRBACDocs(t, rendered) {
				var obj rbacv1.ClusterRole // Role and ClusterRole share metadata and rules.
				if err := yaml.Unmarshal(doc.raw, &obj); err != nil {
					t.Fatalf("unmarshal %s: %v", doc.kind, err)
				}
				switch doc.kind {
				case "Role":
					roles = append(roles, obj.Name)
				case "ClusterRole":
					clusterRoles = append(clusterRoles, obj.Name)
					for _, r := range obj.Rules {
						if slices.ContainsFunc(r.Verbs, func(v string) bool { return !slices.Contains(readVerbs, v) }) {
							t.Errorf("ClusterRole %q grants non-read verbs: %+v", obj.Name, r)
						}
					}
				}
			}
			slices.Sort(roles)
			if want := []string{"xmc-bridge-leader-election", "xmc-bridge-role"}; !slices.Equal(roles, want) {
				t.Errorf("Roles = %v, want %v", roles, want)
			}
			slices.Sort(clusterRoles)
			if !slices.Equal(clusterRoles, tt.wantClusterRoles) {
				t.Errorf("ClusterRoles = %v, want %v", clusterRoles, tt.wantClusterRoles)
			}
		})
	}
}

// impersonateBridgeSA returns clients acting as xmc-bridge-sa in namespace ns.
func impersonateBridgeSA(t *testing.T, adminCfg *rest.Config, ns string) (kubernetes.Interface, dynamic.Interface) {
	t.Helper()
	cfg := rest.CopyConfig(adminCfg)
	cfg.Impersonate = rest.ImpersonationConfig{
		UserName: "system:serviceaccount:" + ns + ":xmc-bridge-sa",
		Groups:   []string{"system:serviceaccounts", "system:serviceaccounts:" + ns, "system:authenticated"},
	}
	kube, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatalf("impersonated kubernetes.NewForConfig failed: %v", err)
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		t.Fatalf("impersonated dynamic.NewForConfig failed: %v", err)
	}
	return kube, dyn
}

// rbacDoc is one non-empty document of a rendered RBAC manifest, re-encoded as JSON.
type rbacDoc struct {
	kind string
	raw  []byte
}

func decodeRBACDocs(t *testing.T, rendered []byte) []rbacDoc {
	t.Helper()
	var docs []rbacDoc
	dec := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(rendered), 4096)
	for {
		var raw map[string]any
		if err := dec.Decode(&raw); err != nil {
			if errors.Is(err, io.EOF) {
				return docs
			}
			t.Fatalf("failed to decode RBAC document: %v", err)
		}
		if len(raw) == 0 {
			continue
		}
		docBytes, err := json.Marshal(raw)
		if err != nil {
			t.Fatalf("failed to marshal RBAC document: %v", err)
		}
		kind, ok := raw["kind"].(string)
		if !ok {
			t.Fatalf("RBAC document missing kind or kind is not a string: %v", raw)
		}
		docs = append(docs, rbacDoc{kind: kind, raw: docBytes})
	}
}

func applyBridgeRBACManifests(ctx context.Context, t *testing.T, kubeClient kubernetes.Interface, ns string, clusterScope bool) {
	t.Helper()
	rendered := renderChartTemplateWithValues(t, filepath.Join("components", "rbac.yaml"), ns, true, clusterScope)
	for _, doc := range decodeRBACDocs(t, rendered) {
		applySingleRBACDoc(ctx, t, kubeClient, ns, doc.kind, doc.raw)
	}
}

func applySingleRBACDoc(ctx context.Context, t *testing.T, kubeClient kubernetes.Interface, ns, kind string, docBytes []byte) {
	t.Helper()
	switch kind {
	case "Role":
		var obj rbacv1.Role
		if err := yaml.Unmarshal(docBytes, &obj); err != nil {
			t.Fatalf("unmarshal Role: %v", err)
		}
		assertNoWildcardRules(t, obj.Name, obj.Rules)
		if _, err := kubeClient.RbacV1().Roles(ns).Create(ctx, &obj, metav1.CreateOptions{}); err != nil {
			t.Fatalf("create Role %s: %v", obj.Name, err)
		}
	case "RoleBinding":
		var obj rbacv1.RoleBinding
		if err := yaml.Unmarshal(docBytes, &obj); err != nil {
			t.Fatalf("unmarshal RoleBinding: %v", err)
		}
		if _, err := kubeClient.RbacV1().RoleBindings(ns).Create(ctx, &obj, metav1.CreateOptions{}); err != nil {
			t.Fatalf("create RoleBinding %s: %v", obj.Name, err)
		}
	case "ClusterRole":
		var obj rbacv1.ClusterRole
		if err := yaml.Unmarshal(docBytes, &obj); err != nil {
			t.Fatalf("unmarshal ClusterRole: %v", err)
		}
		assertNoWildcardRules(t, obj.Name, obj.Rules)
		if _, err := kubeClient.RbacV1().ClusterRoles().Create(ctx, &obj, metav1.CreateOptions{}); err != nil {
			t.Fatalf("create ClusterRole %s: %v", obj.Name, err)
		}
	case "ClusterRoleBinding":
		var obj rbacv1.ClusterRoleBinding
		if err := yaml.Unmarshal(docBytes, &obj); err != nil {
			t.Fatalf("unmarshal ClusterRoleBinding: %v", err)
		}
		if _, err := kubeClient.RbacV1().ClusterRoleBindings().Create(ctx, &obj, metav1.CreateOptions{}); err != nil {
			t.Fatalf("create ClusterRoleBinding %s: %v", obj.Name, err)
		}
	default:
		t.Fatalf("unexpected RBAC kind %q", kind)
	}
}
