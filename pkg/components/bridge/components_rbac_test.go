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
	"encoding/base64"
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

	"github.com/google/go-cmp/cmp"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/cli-runtime/pkg/genericclioptions"
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

// loadBridgeChartValues returns the chart's values.yaml with componentsDeployment.enabled set to enabled.
func loadBridgeChartValues(t *testing.T, repoRoot string, enabled bool) map[string]any {
	t.Helper()
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
	return values
}

func renderChartTemplateWithValues(t *testing.T, relTemplatePath string, releaseNS string, enabled bool) []byte {
	t.Helper()
	repoRoot, err := findRepoRoot()
	if err != nil {
		t.Fatalf("could not locate repository root: %v", err)
	}
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
		"Values":  loadBridgeChartValues(t, repoRoot, enabled),
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

	renderedDefault := string(renderChartTemplateWithValues(t, filepath.Join("components", "rbac.yaml"), "custom-ns", true))
	if !strings.Contains(renderedDefault, "kind: Role\nmetadata:\n  name: xmc-bridge-role") {
		t.Errorf("expected default rbac.yaml render to define Role xmc-bridge-role, got:\n%s", renderedDefault)
	}
	if strings.Contains(renderedDefault, `apiGroups: ["*"]`) || strings.Contains(renderedDefault, `resources: ["*"]`) {
		t.Errorf("expected rbac.yaml to contain no wildcard apiGroups/resources, got:\n%s", renderedDefault)
	}
	for _, wantGroup := range []string{`"clickhouse.altinity.com"`, `"cluster.redpanda.com"`, `"ray.io"`} {
		if !strings.Contains(renderedDefault, wantGroup) {
			t.Errorf("expected default rendered rbac.yaml to include 3P wrapper group %s", wantGroup)
		}
	}

	if docs := decodeRBACDocs(t, renderChartTemplateWithValues(t, filepath.Join("components", "rbac.yaml"), "custom-ns", false)); len(docs) != 0 {
		t.Errorf("expected rbac.yaml to render no objects when componentsDeployment.enabled=false, got %d: %+v", len(docs), docs)
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

// rbacCase is one request issued as xmc-bridge-sa and the outcome RBAC must produce for it.
type rbacCase struct {
	name string
	call func(context.Context) error
	want func(error) bool
}

func isAllowed(err error) bool { return err == nil }

func TestComponentsRBAC_LeastPrivilegeEnvtest(t *testing.T) {
	ctx := context.Background()
	adminCfg, _ := startBridgeRBACEnvtest(ctx, t, "xmc")
	kube, dyn := impersonateBridgeSA(t, adminCfg, "xmc")
	cases := slices.Concat(componentRBACCases(kube, dyn), configStorageRBACCases(kube), workloadRBACCases(kube), wrapperRBACCases(dyn))
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(ctx); !tc.want(err) {
				t.Errorf("unexpected result for xmc-bridge-sa: %v", err)
			}
		})
	}
}

// componentRBACCases covers the cluster identity read and Component CR access: read, finalizer patch, no
// create, update (PUT) or delete, and nothing outside the release namespace.
func componentRBACCases(kube kubernetes.Interface, dyn dynamic.Interface) []rbacCase {
	components := dyn.Resource(ComponentGVR).Namespace("xmc")
	missing := newComponentObject()
	missing.SetName("missing")
	missing.SetNamespace("xmc")
	return []rbacCase{
		{"GET kube-system namespace is allowed", func(ctx context.Context) error {
			_, err := kube.CoreV1().Namespaces().Get(ctx, "kube-system", metav1.GetOptions{})
			return err
		}, isAllowed},
		{"GET default namespace is Forbidden", func(ctx context.Context) error {
			_, err := kube.CoreV1().Namespaces().Get(ctx, "default", metav1.GetOptions{})
			return err
		}, apierrors.IsForbidden},
		{"LIST components in release namespace is allowed", func(ctx context.Context) error {
			_, err := components.List(ctx, metav1.ListOptions{})
			return err
		}, isAllowed},
		{"LIST components in default namespace is Forbidden", func(ctx context.Context) error {
			_, err := dyn.Resource(ComponentGVR).Namespace("default").List(ctx, metav1.ListOptions{})
			return err
		}, apierrors.IsForbidden},
		{"PATCH component passes RBAC (NotFound)", func(ctx context.Context) error {
			_, err := components.Patch(ctx, "missing", types.MergePatchType, []byte(`{}`), metav1.PatchOptions{})
			return err
		}, apierrors.IsNotFound},
		{"UPDATE component is Forbidden", func(ctx context.Context) error {
			_, err := components.Update(ctx, missing, metav1.UpdateOptions{})
			return err
		}, apierrors.IsForbidden},
		{"CREATE component is Forbidden", func(ctx context.Context) error {
			_, err := components.Create(ctx, missing, metav1.CreateOptions{})
			return err
		}, apierrors.IsForbidden},
		{"DELETE component is Forbidden", func(ctx context.Context) error {
			return components.Delete(ctx, "missing", metav1.DeleteOptions{})
		}, apierrors.IsForbidden},
		{"LIST validatingadmissionpolicies is Forbidden (no wildcard read)", func(ctx context.Context) error {
			_, err := kube.AdmissionregistrationV1().ValidatingAdmissionPolicies().List(ctx, metav1.ListOptions{})
			return err
		}, apierrors.IsForbidden},
	}
}

// configStorageRBACCases covers the xmc-component-values ConfigMap and the Helm release Secrets.
func configStorageRBACCases(kube kubernetes.Interface) []rbacCase {
	configMaps := kube.CoreV1().ConfigMaps("xmc")
	return []rbacCase{
		{"UPDATE ConfigMap xmc-component-values passes RBAC (NotFound)", func(ctx context.Context) error {
			cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: ComponentValuesConfigMapName}}
			_, err := configMaps.Update(ctx, cm, metav1.UpdateOptions{})
			return err
		}, apierrors.IsNotFound},
		{"DELETE ConfigMap xmc-bridge-config is Forbidden", func(ctx context.Context) error {
			return configMaps.Delete(ctx, "xmc-bridge-config", metav1.DeleteOptions{})
		}, apierrors.IsForbidden},
		{"PATCH non-allowlisted ConfigMap is Forbidden", func(ctx context.Context) error {
			_, err := configMaps.Patch(ctx, "xmc-cmp-missing", types.MergePatchType, []byte(`{}`), metav1.PatchOptions{})
			return err
		}, apierrors.IsForbidden},
		{"GET ConfigMap in default namespace is Forbidden", func(ctx context.Context) error {
			_, err := kube.CoreV1().ConfigMaps("default").Get(ctx, ComponentValuesConfigMapName, metav1.GetOptions{})
			return err
		}, apierrors.IsForbidden},
		{"LIST Secrets in release namespace is allowed (Helm release storage)", func(ctx context.Context) error {
			_, err := kube.CoreV1().Secrets("xmc").List(ctx, metav1.ListOptions{})
			return err
		}, isAllowed},
		{"PATCH Secret is Forbidden", func(ctx context.Context) error {
			_, err := kube.CoreV1().Secrets("xmc").Patch(ctx, "missing", types.MergePatchType, []byte(`{}`), metav1.PatchOptions{})
			return err
		}, apierrors.IsForbidden},
		{"LIST Secrets in default namespace is Forbidden", func(ctx context.Context) error {
			_, err := kube.CoreV1().Secrets("default").List(ctx, metav1.ListOptions{})
			return err
		}, apierrors.IsForbidden},
	}
}

// workloadRBACCases covers the objects 1P component charts render: only allowlisted names may be read or
// mutated, so control-plane workloads in the release namespace stay out of reach.
func workloadRBACCases(kube kubernetes.Interface) []rbacCase {
	return []rbacCase{
		{"GET allowlisted ingestion-gateway-deployment passes RBAC (NotFound)", func(ctx context.Context) error {
			_, err := kube.AppsV1().Deployments("xmc").Get(ctx, "ingestion-gateway-deployment", metav1.GetOptions{})
			return err
		}, apierrors.IsNotFound},
		{"PATCH non-allowlisted xmc-bridge-deployment is Forbidden", func(ctx context.Context) error {
			_, err := kube.AppsV1().Deployments("xmc").Patch(ctx, "xmc-bridge-deployment", types.MergePatchType, []byte(`{}`), metav1.PatchOptions{})
			return err
		}, apierrors.IsForbidden},
		{"GET non-allowlisted grpc-service is Forbidden", func(ctx context.Context) error {
			_, err := kube.CoreV1().Services("xmc").Get(ctx, "grpc-service", metav1.GetOptions{})
			return err
		}, apierrors.IsForbidden},
		{"LIST pods is Forbidden", func(ctx context.Context) error {
			_, err := kube.CoreV1().Pods("xmc").List(ctx, metav1.ListOptions{})
			return err
		}, apierrors.IsForbidden},
		{"DELETE allowlisted ingestion-gateway-components-reader Role passes RBAC (NotFound)", func(ctx context.Context) error {
			return kube.RbacV1().Roles("xmc").Delete(ctx, "ingestion-gateway-components-reader", metav1.DeleteOptions{})
		}, apierrors.IsNotFound},
		{"DELETE non-allowlisted xmc-bridge-role Role is Forbidden", func(ctx context.Context) error {
			return kube.RbacV1().Roles("xmc").Delete(ctx, "xmc-bridge-role", metav1.DeleteOptions{})
		}, apierrors.IsForbidden},
		{"LIST roles is Forbidden", func(ctx context.Context) error {
			_, err := kube.RbacV1().Roles("xmc").List(ctx, metav1.ListOptions{})
			return err
		}, apierrors.IsForbidden},
	}
}

// wrapperRBACCases covers 3P wrapper resources, which the bridge only GETs by name in its own namespace.
func wrapperRBACCases(dyn dynamic.Interface) []rbacCase {
	// The RayCluster CRD is not installed: an authorized request reaches the API server's
	// not-found handler, while an unauthorized one is rejected with Forbidden first.
	rayClusters := schema.GroupVersionResource{Group: "ray.io", Version: "v1", Resource: "rayclusters"}
	return []rbacCase{
		{"GET wrapper resource in release namespace passes RBAC (NotFound)", func(ctx context.Context) error {
			_, err := dyn.Resource(rayClusters).Namespace("xmc").Get(ctx, "ray", metav1.GetOptions{})
			return err
		}, apierrors.IsNotFound},
		{"LIST wrapper resources in release namespace is Forbidden", func(ctx context.Context) error {
			_, err := dyn.Resource(rayClusters).Namespace("xmc").List(ctx, metav1.ListOptions{})
			return err
		}, apierrors.IsForbidden},
		{"GET wrapper resource in default namespace is Forbidden", func(ctx context.Context) error {
			_, err := dyn.Resource(rayClusters).Namespace("default").Get(ctx, "ray", metav1.GetOptions{})
			return err
		}, apierrors.IsForbidden},
	}
}

// TestComponentsRBAC_HelmLifecycleEnvtest installs, upgrades, inspects and uninstalls the embedded
// ingestion-gateway chart as xmc-bridge-sa, so xmc-bridge-role is checked against the requests Helm
// actually makes (release Secrets, chart objects by name, RBAC escalation checks) rather than a verb list.
func TestComponentsRBAC_HelmLifecycleEnvtest(t *testing.T) {
	ctx := context.Background()
	adminCfg, adminKube := startBridgeRBACEnvtest(ctx, t, "xmc")
	runner := NewSDKHelmActionRunner(impersonatedHelmGetter(t, adminCfg, "xmc"))
	const releaseName = "xmc-cmp-ingestion-gateway"
	// The first call installs (GET, then CREATE); the second changes the HPA, so Helm PATCHes it.
	for _, values := range []map[string]any{
		{"deployWorkload": true},
		{"deployWorkload": true, "minReplicas": 3},
	} {
		if _, err := runner.Upgrade(ctx, releaseName, "xmc", nil, values); err != nil {
			t.Fatalf("Upgrade(%v) as xmc-bridge-sa: %v", values, err)
		}
	}
	hpa, err := adminKube.AutoscalingV2().HorizontalPodAutoscalers("xmc").Get(ctx, "ingestion-gateway-hpa", metav1.GetOptions{})
	if err != nil || hpa.Spec.MinReplicas == nil || *hpa.Spec.MinReplicas != 3 {
		t.Fatalf("HPA ingestion-gateway-hpa after upgrade = %+v, %v; want minReplicas 3", hpa, err)
	}
	rel, err := runner.Status(ctx, releaseName, "xmc")
	if err != nil {
		t.Fatalf("Status() as xmc-bridge-sa: %v", err)
	}
	if missing, err := runner.MissingResources(ctx, rel); err != nil || len(missing) != 0 {
		t.Fatalf("MissingResources() as xmc-bridge-sa = %v, %v; want none", missing, err)
	}
	if _, err := runner.Uninstall(ctx, releaseName, "xmc"); err != nil {
		t.Fatalf("Uninstall() as xmc-bridge-sa: %v", err)
	}
	if _, err := adminKube.AppsV1().Deployments("xmc").Get(ctx, "ingestion-gateway-deployment", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Errorf("Deployment ingestion-gateway-deployment after Uninstall: err = %v, want NotFound", err)
	}
}

// TestComponentsRBAC_ScopeRendering asserts that xmc-bridge-role is a namespaced Role and that the
// only cluster-scoped rules are the read-only cluster identity ones.
func TestComponentsRBAC_ScopeRendering(t *testing.T) {
	rendered := renderChartTemplateWithValues(t, filepath.Join("components", "rbac.yaml"), "custom-ns", true)
	readVerbs := []string{"get", "list", "watch"}
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
	if diff := cmp.Diff([]string{"xmc-bridge-leader-election", "xmc-bridge-role"}, roles); diff != "" {
		t.Errorf("Roles mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"xmc-bridge-cluster-identity-custom-ns"}, clusterRoles); diff != "" {
		t.Errorf("ClusterRoles mismatch (-want +got):\n%s", diff)
	}
}

// startBridgeRBACEnvtest starts envtest, creates namespace ns and applies the rendered xmc-bridge RBAC
// manifests there. It returns the admin config and client.
func startBridgeRBACEnvtest(ctx context.Context, t *testing.T, ns string) (*rest.Config, kubernetes.Interface) {
	t.Helper()
	adminCfg := startComponentEnvtest(t)
	adminKube, err := kubernetes.NewForConfig(adminCfg)
	if err != nil {
		t.Fatalf("kubernetes.NewForConfig failed: %v", err)
	}
	if _, err := adminKube.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}, metav1.CreateOptions{}); err != nil {
		t.Fatalf("failed to create %s namespace: %v", ns, err)
	}
	applyBridgeRBACManifests(ctx, t, adminKube, ns)
	return adminCfg, adminKube
}

// bridgeSAIdentity returns the user name and groups of xmc-bridge-sa in namespace ns.
func bridgeSAIdentity(ns string) (string, []string) {
	return "system:serviceaccount:" + ns + ":xmc-bridge-sa", []string{"system:serviceaccounts", "system:serviceaccounts:" + ns, "system:authenticated"}
}

// impersonateBridgeSA returns clients acting as xmc-bridge-sa in namespace ns.
func impersonateBridgeSA(t *testing.T, adminCfg *rest.Config, ns string) (kubernetes.Interface, dynamic.Interface) {
	t.Helper()
	cfg := rest.CopyConfig(adminCfg)
	user, groups := bridgeSAIdentity(ns)
	cfg.Impersonate = rest.ImpersonationConfig{UserName: user, Groups: groups}
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

// impersonatedHelmGetter returns a Helm RESTClientGetter for the envtest API server that authenticates
// with the admin client certificate of adminCfg and acts as xmc-bridge-sa in namespace ns.
func impersonatedHelmGetter(t *testing.T, adminCfg *rest.Config, ns string) genericclioptions.RESTClientGetter {
	t.Helper()
	b64 := base64.StdEncoding.EncodeToString
	content := fmt.Sprintf("apiVersion: v1\nkind: Config\nclusters:\n- name: envtest\n  cluster:\n    server: %s\n    certificate-authority-data: %s\n"+
		"contexts:\n- name: envtest\n  context:\n    cluster: envtest\n    user: admin\ncurrent-context: envtest\n"+
		"users:\n- name: admin\n  user:\n    client-certificate-data: %s\n    client-key-data: %s\n",
		adminCfg.Host, b64(adminCfg.CAData), b64(adminCfg.CertData), b64(adminCfg.KeyData))
	kubeconfig := filepath.Join(t.TempDir(), "kubeconfig")
	if err := os.WriteFile(kubeconfig, []byte(content), 0600); err != nil {
		t.Fatalf("failed to write kubeconfig: %v", err)
	}
	// Like the bridge's Helm CLI settings, the getter carries the release namespace, which Helm applies to
	// chart objects that set none.
	user, groups := bridgeSAIdentity(ns)
	cacheDir := t.TempDir()
	flags := genericclioptions.NewConfigFlags(false)
	flags.KubeConfig = &kubeconfig
	flags.CacheDir = &cacheDir
	flags.Namespace = &ns
	flags.Impersonate = &user
	flags.ImpersonateGroup = &groups
	return flags
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

func applyBridgeRBACManifests(ctx context.Context, t *testing.T, kubeClient kubernetes.Interface, ns string) {
	t.Helper()
	rendered := renderChartTemplateWithValues(t, filepath.Join("components", "rbac.yaml"), ns, true)
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
