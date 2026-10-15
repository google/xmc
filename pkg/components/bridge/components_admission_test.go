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
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"text/template"
	"time"

	"github.com/google/go-cmp/cmp"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

type admissionClients struct {
	adminDynamic      dynamic.Interface
	nonAdminComp      dynamic.ResourceInterface
	platformAdminComp dynamic.ResourceInterface
	kubeClient        kubernetes.Interface
}

// TestComponentsAdmissionAndControllerSuite verifies the Component CRD OpenAPI/CEL schema validations
// and ValidatingAdmissionPolicy deletion protection against a local in-memory etcd and kube-apiserver (envtest).
func TestComponentsAdmissionAndControllerSuite(t *testing.T) {
	ctx := context.Background()
	clients := startAdmissionEnvtest(ctx, t)

	t.Run("CRDOpenAPIAndCELSchemaValidations", func(t *testing.T) {
		verifyCRDSchemaAndCELRules(ctx, t, clients.adminDynamic.Resource(ComponentGVR).Namespace("default"))
	})
	t.Run("ValidatingAdmissionPolicyDeletionProtection", func(t *testing.T) {
		waitForVAPActivation(ctx, t, clients.nonAdminComp)
		verifyDeletionProtectionVAP(ctx, t, clients)
	})
}

func startAdmissionEnvtest(ctx context.Context, t *testing.T) admissionClients {
	t.Helper()
	clients := buildAdmissionClients(t, startComponentEnvtest(t))
	grantComponentRBAC(ctx, t, clients.kubeClient)
	installDeletionProtectionVAP(ctx, t, clients)
	return clients
}

// startComponentEnvtest starts an envtest kube-apiserver and etcd with the chart's Component CRD
// installed, stopped on test cleanup, and returns its admin config.
func startComponentEnvtest(t *testing.T) *rest.Config {
	t.Helper()
	crdDir := t.TempDir()
	crdBytes := renderHelmManifestTemplate(t, "crd.yaml", true)
	if err := os.WriteFile(filepath.Join(crdDir, "crds.yaml"), crdBytes, 0600); err != nil {
		t.Fatalf("failed to write CRD manifest: %v", err)
	}
	testEnv := &envtest.Environment{
		BinaryAssetsDirectory:   resolveEnvtestAssets(t),
		ControlPlaneStopTimeout: 10 * time.Second,
		CRDInstallOptions:       envtest.CRDInstallOptions{Paths: []string{crdDir}, ErrorIfPathMissing: true, PollInterval: 10 * time.Millisecond, MaxTime: 5 * time.Second},
	}
	cfg, err := testEnv.Start()
	if err != nil {
		t.Fatalf("failed to start envtest: %v", err)
	}
	t.Cleanup(func() { _ = testEnv.Stop() })
	return cfg
}

func grantComponentRBAC(ctx context.Context, t *testing.T, kubeClient kubernetes.Interface) {
	t.Helper()
	role := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{Name: "xmc-components-test-operator"},
		Rules:      []rbacv1.PolicyRule{{APIGroups: []string{"xmc.dev"}, Resources: []string{"components", "components/status"}, Verbs: []string{"*"}}},
	}
	if _, err := kubeClient.RbacV1().ClusterRoles().Create(ctx, role, metav1.CreateOptions{}); err != nil {
		t.Fatalf("failed to create ClusterRole: %v", err)
	}
	binding := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "xmc-components-test-operator-binding"},
		RoleRef:    rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "ClusterRole", Name: role.Name},
		Subjects:   []rbacv1.Subject{{Kind: "User", Name: "system:serviceaccount:xmc:gitops-reconciler"}, {Kind: "Group", Name: "xmc-platform-admins"}},
	}
	if _, err := kubeClient.RbacV1().ClusterRoleBindings().Create(ctx, binding, metav1.CreateOptions{}); err != nil {
		t.Fatalf("failed to create ClusterRoleBinding: %v", err)
	}
}

func buildAdmissionClients(t *testing.T, baseCfg *rest.Config) admissionClients {
	t.Helper()
	adminDyn, err := dynamic.NewForConfig(baseCfg)
	if err != nil {
		t.Fatalf("dynamic.NewForConfig failed: %v", err)
	}
	kubeClient, err := kubernetes.NewForConfig(baseCfg)
	if err != nil {
		t.Fatalf("kubernetes.NewForConfig failed: %v", err)
	}
	impersonateComp := func(user string, groups ...string) dynamic.ResourceInterface {
		cfg := rest.CopyConfig(baseCfg)
		cfg.Impersonate = rest.ImpersonationConfig{UserName: user, Groups: groups}
		dyn, err := dynamic.NewForConfig(cfg)
		if err != nil {
			t.Fatalf("impersonated dynamic.NewForConfig(%s) failed: %v", user, err)
		}
		return dyn.Resource(ComponentGVR).Namespace("default")
	}
	return admissionClients{
		adminDynamic:      adminDyn,
		nonAdminComp:      impersonateComp("system:serviceaccount:xmc:gitops-reconciler", "system:authenticated"),
		platformAdminComp: impersonateComp("sre-oncall@example.com", "system:authenticated", "xmc-platform-admins"),
		kubeClient:        kubeClient,
	}
}

func installDeletionProtectionVAP(ctx context.Context, t *testing.T, clients admissionClients) {
	t.Helper()
	dec := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(renderHelmManifestTemplate(t, "deletion-vap.yaml", true)), 4096)
	var policy admissionregistrationv1.ValidatingAdmissionPolicy
	if err := dec.Decode(&policy); err != nil {
		t.Fatalf("failed to decode ValidatingAdmissionPolicy: %v", err)
	}
	var binding admissionregistrationv1.ValidatingAdmissionPolicyBinding
	if err := dec.Decode(&binding); err != nil {
		t.Fatalf("failed to decode ValidatingAdmissionPolicyBinding: %v", err)
	}
	admClient := clients.kubeClient.AdmissionregistrationV1()
	if _, err := admClient.ValidatingAdmissionPolicies().Create(ctx, &policy, metav1.CreateOptions{}); err != nil {
		t.Fatalf("failed to create ValidatingAdmissionPolicy: %v", err)
	}
	if _, err := admClient.ValidatingAdmissionPolicyBindings().Create(ctx, &binding, metav1.CreateOptions{}); err != nil {
		t.Fatalf("failed to create ValidatingAdmissionPolicyBinding: %v", err)
	}
}

func waitForVAPActivation(ctx context.Context, t *testing.T, compClient dynamic.ResourceInterface) {
	t.Helper()
	canary := newComponentManifest("vap-readiness-canary", map[string]any{"enabled": true, "componentType": "COMPONENT_TYPE_CUSTOM_1P_COMPONENT", "deletionProtection": true})
	if _, err := compClient.Create(ctx, canary, metav1.CreateOptions{}); err != nil {
		t.Fatalf("failed to create VAP canary component: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = compClient.Patch(cleanupCtx, canary.GetName(), types.MergePatchType, []byte(`{"spec":{"deletionProtection":false}}`), metav1.PatchOptions{})
		_ = compClient.Delete(cleanupCtx, canary.GetName(), metav1.DeleteOptions{})
	})
	err := wait.PollUntilContextTimeout(ctx, 50*time.Millisecond, 3*time.Second, true, func(ctx context.Context) (bool, error) {
		delErr := compClient.Delete(ctx, canary.GetName(), metav1.DeleteOptions{DryRun: []string{metav1.DryRunAll}})
		if delErr == nil {
			return false, nil
		}
		return strings.Contains(delErr.Error(), "Deletion rejected: Component has deletionProtection enabled"), nil
	})
	if err != nil {
		t.Fatalf("timed out waiting for ValidatingAdmissionPolicy activation: %v", err)
	}
}

// TestDeletionVAPIgnoresEnableComponentsFlag asserts the deletion admission policy and its binding
// render whatever componentsDeployment.enabled is set to, and survive helm uninstall, so disabling
// xmc-bridge never drops deletion protection for the CRs the kept CRD still holds.
func TestDeletionVAPIgnoresEnableComponentsFlag(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		enabled bool
	}{
		{name: "components disabled", enabled: false},
		{name: "components enabled", enabled: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dec := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(renderHelmManifestTemplate(t, "deletion-vap.yaml", tt.enabled)), 4096)
			var gotKinds []string
			for {
				var doc unstructured.Unstructured
				err := dec.Decode(&doc.Object)
				if errors.Is(err, io.EOF) {
					break
				}
				if err != nil {
					t.Fatalf("decoding deletion-vap.yaml: %v", err)
				}
				gotKinds = append(gotKinds, doc.GetKind())
				if got := doc.GetAnnotations()["helm.sh/resource-policy"]; got != "keep" {
					t.Errorf("%s helm.sh/resource-policy = %q, want %q", doc.GetKind(), got, "keep")
				}
			}
			wantKinds := []string{"ValidatingAdmissionPolicy", "ValidatingAdmissionPolicyBinding"}
			if diff := cmp.Diff(wantKinds, gotKinds); diff != "" {
				t.Errorf("deletion-vap.yaml rendered kinds mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestCRDIgnoresEnableComponentsFlag asserts the Component CRD renders whatever
// componentsDeployment.enabled is set to. It is a pure template check, so it runs outside the
// envtest suite.
func TestCRDIgnoresEnableComponentsFlag(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		enabled bool
	}{
		{name: "components disabled", enabled: false},
		{name: "components enabled", enabled: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var doc map[string]any
			if err := yaml.Unmarshal(renderHelmManifestTemplate(t, "crd.yaml", tt.enabled), &doc); err != nil || doc["kind"] == nil {
				t.Errorf("expected crd.yaml with componentsDeployment.enabled=%v to render K8s object, got %v (err=%v)", tt.enabled, doc, err)
			}
		})
	}
}

// TestCRDLifecycleAnnotations asserts the CRD survives helm uninstall, a componentsDeployment.enabled
// flip and an Argo CD prune, because deleting the CRD deletes every Component CR.
func TestCRDLifecycleAnnotations(t *testing.T) {
	t.Parallel()
	var crd struct {
		Metadata struct {
			Annotations map[string]string `json:"annotations"`
		} `json:"metadata"`
	}
	if err := yaml.Unmarshal(renderHelmManifestTemplate(t, "crd.yaml", false), &crd); err != nil {
		t.Fatalf("yaml.Unmarshal(crd.yaml) = %v", err)
	}
	want := map[string]string{
		"helm.sh/resource-policy":         "keep",
		"argocd.argoproj.io/sync-options": "Prune=false,Delete=false",
		"argocd.argoproj.io/sync-wave":    "-1",
	}
	if diff := cmp.Diff(want, crd.Metadata.Annotations); diff != "" {
		t.Errorf("CRD annotations mismatch (-want +got):\n%s", diff)
	}
}

func verifyCRDSchemaAndCELRules(ctx context.Context, t *testing.T, compClient dynamic.ResourceInterface) {
	t.Helper()
	wrapperRef := func(obsPath, readyPath string) map[string]any {
		m := map[string]any{"group": "clickhouse.altinity.com", "version": "v1", "kind": "ClickHouseInstallation", "name": "chi-1"}
		if obsPath != "" {
			m["observedGenerationPath"] = obsPath
		}
		if readyPath != "" {
			m["readyConditionPath"] = readyPath
		}
		return m
	}
	tests := []struct {
		name        string
		spec        map[string]any
		wantErr     bool
		errContains string
	}{
		{"rejects missing required spec.enabled", map[string]any{"componentType": "COMPONENT_TYPE_CUSTOM_1P_COMPONENT"}, true, "spec.enabled: Required value"},
		{"rejects invalid deletionPolicy value", map[string]any{"enabled": true, "deletionPolicy": "Retain"}, true, "deletionPolicy"},
		{"rejects negative lifecycleControl.drainTimeoutSeconds", map[string]any{"enabled": true, "lifecycleControl": map[string]any{"drainTimeoutSeconds": -5}}, true, "drainTimeoutSeconds"},
		{"rejects 3P wrapper without wrapperRef or parameters", map[string]any{"enabled": true, "componentType": "COMPONENT_TYPE_CUSTOM_3P_WRAPPER"}, true, "either wrapperRef or parameters must be specified when componentType is COMPONENT_TYPE_CUSTOM_3P_WRAPPER"},
		{"accepts operatorless 3P wrapper with parameters only", map[string]any{"enabled": true, "componentType": "COMPONENT_TYPE_CUSTOM_3P_WRAPPER", "deletionProtection": false, "parameters": map[string]any{"host": "clickhouse.xmc.svc.cluster.local"}}, false, ""},
		{"rejects wrapperRef observedGenerationPath outside status", map[string]any{"enabled": true, "componentType": "COMPONENT_TYPE_CUSTOM_3P_WRAPPER", "wrapperRef": wrapperRef("$.metadata.generation", "")}, true, "observedGenerationPath"},
		{"rejects wrapperRef readyConditionPath outside status", map[string]any{"enabled": true, "componentType": "COMPONENT_TYPE_CUSTOM_3P_WRAPPER", "wrapperRef": wrapperRef("", "$.spec.ready")}, true, "readyConditionPath"},
		{"accepts valid 3P wrapper with status JSONPaths and applies schema defaults", map[string]any{"enabled": true, "componentType": "COMPONENT_TYPE_CUSTOM_3P_WRAPPER", "wrapperRef": wrapperRef("$.status.observedGeneration", "$.status.conditions[?(@.type=='Ready')].status")}, false, ""},
	}
	for i, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			name := fmt.Sprintf("cel-case-%d", i)
			created, err := compClient.Create(ctx, newComponentManifest(name, tc.spec), metav1.CreateOptions{})
			if (err != nil) != tc.wantErr {
				t.Fatalf("Create(%s) error = %v, wantErr %v", name, err, tc.wantErr)
			}
			if tc.wantErr && !strings.Contains(err.Error(), tc.errContains) {
				t.Errorf("expected error containing %q, got %v", tc.errContains, err)
			}
			if !tc.wantErr && tc.spec["deletionProtection"] == nil {
				assertDefaultSchemaFields(t, created)
			}
		})
	}
}

func assertDefaultSchemaFields(t *testing.T, created *unstructured.Unstructured) {
	t.Helper()
	if delProt, found, _ := unstructured.NestedBool(created.Object, "spec", "deletionProtection"); !found || !delProt {
		t.Errorf("expected default spec.deletionProtection=true, got %v (found=%v)", delProt, found)
	}
	if delPol, _, _ := unstructured.NestedString(created.Object, "spec", "deletionPolicy"); delPol != "Delete" {
		t.Errorf("expected default spec.deletionPolicy=Delete, got %q", delPol)
	}
}

func verifyDeletionProtectionVAP(ctx context.Context, t *testing.T, clients admissionClients) {
	t.Helper()
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for _, name := range []string{"protected-ingestion", "platform-admin-protected"} {
			_, _ = clients.nonAdminComp.Patch(cleanupCtx, name, types.MergePatchType, []byte(`{"spec":{"deletionProtection":false}}`), metav1.PatchOptions{})
			_ = clients.nonAdminComp.Delete(cleanupCtx, name, metav1.DeleteOptions{})
		}
	})
	protectedSpec := map[string]any{"enabled": true, "componentType": "COMPONENT_TYPE_CUSTOM_1P_COMPONENT", "deletionProtection": true}
	comp := newComponentManifest("protected-ingestion", protectedSpec)
	if _, err := clients.nonAdminComp.Create(ctx, comp, metav1.CreateOptions{}); err != nil {
		t.Fatalf("Create protected component failed: %v", err)
	}
	if err := clients.nonAdminComp.Delete(ctx, comp.GetName(), metav1.DeleteOptions{}); err == nil || !strings.Contains(err.Error(), "Deletion rejected: Component has deletionProtection enabled") {
		t.Fatalf("expected VAP rejection on DELETE with deletionProtection=true, got: %v", err)
	}
	if _, err := clients.nonAdminComp.Patch(ctx, comp.GetName(), types.MergePatchType, []byte(`{"spec":{"deletionProtection":false}}`), metav1.PatchOptions{}); err != nil {
		t.Fatalf("Patch deletionProtection=false failed: %v", err)
	}
	if err := clients.nonAdminComp.Delete(ctx, comp.GetName(), metav1.DeleteOptions{}); err != nil {
		t.Fatalf("expected DELETE to succeed after setting deletionProtection=false, got: %v", err)
	}
	// xmc-platform-admins holds RBAC delete on components but has no admission exemption.
	platformAdminTarget := newComponentManifest("platform-admin-protected", protectedSpec)
	if _, err := clients.nonAdminComp.Create(ctx, platformAdminTarget, metav1.CreateOptions{}); err != nil {
		t.Fatalf("Create platform admin target failed: %v", err)
	}
	if err := clients.platformAdminComp.Delete(ctx, platformAdminTarget.GetName(), metav1.DeleteOptions{}); err == nil || !strings.Contains(err.Error(), "Deletion rejected: Component has deletionProtection enabled") {
		t.Fatalf("expected VAP rejection on xmc-platform-admins DELETE with deletionProtection=true, got: %v", err)
	}
}

func newComponentManifest(name string, spec map[string]any) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]any{"apiVersion": "xmc.dev/v1alpha1", "kind": "Component", "metadata": map[string]any{"name": name, "namespace": "default"}, "spec": spec},
	}
}

func renderHelmManifestTemplate(t *testing.T, fileName string, enabled bool) []byte {
	t.Helper()
	candidates := []string{
		filepath.Join("..", "..", "..", "deploy", "helm", "templates", "components", fileName),
		filepath.Join(os.Getenv("TEST_SRCDIR"), "_main", "deploy", "helm", "templates", "components", fileName),
	}
	for _, p := range candidates {
		raw, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		tmpl, err := template.New(fileName).Parse(string(raw))
		if err != nil {
			t.Fatalf("failed to parse template %s: %v", fileName, err)
		}
		var buf bytes.Buffer
		vals := map[string]any{"Values": map[string]any{"componentsDeployment": map[string]any{"enabled": enabled}}}
		if err := tmpl.Execute(&buf, vals); err != nil {
			t.Fatalf("failed to execute template %s: %v", fileName, err)
		}
		return buf.Bytes()
	}
	t.Fatalf("failed to locate deploy/helm/templates/components/%s", fileName)
	return nil
}

func resolveEnvtestAssets(t *testing.T) string {
	t.Helper()
	if envDir := os.Getenv("KUBEBUILDER_ASSETS"); envDir != "" {
		return envDir
	}
	srcDir := os.Getenv("TEST_SRCDIR")
	for _, d := range []string{srcDir, filepath.Join(srcDir, "_main", "external")} {
		entries, err := os.ReadDir(d)
		if err != nil {
			continue
		}
		for _, e := range entries {
			p := filepath.Join(d, e.Name())
			if _, err := os.Stat(filepath.Join(p, "kube-apiserver")); err == nil && strings.Contains(e.Name(), "kubebuilder_envtest") {
				return p
			}
		}
	}
	if home := os.Getenv("HOME"); home != "" {
		fallback := filepath.Join(home, ".local", "share", "kubebuilder-envtest", "k8s", fmt.Sprintf("1.30.3-%s-%s", runtime.GOOS, runtime.GOARCH))
		if _, err := os.Stat(filepath.Join(fallback, "kube-apiserver")); err == nil {
			return fallback
		}
	}
	t.Fatal("failed to locate kubebuilder envtest binaries (etcd, kube-apiserver)")
	return ""
}
