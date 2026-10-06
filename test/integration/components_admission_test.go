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

package integration_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"text/template"
	"time"

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

	"xmanager_cloud/pkg/components/bridge"
)

type admissionClients struct {
	adminDynamic   dynamic.Interface
	nonAdminComp   dynamic.ResourceInterface
	breakglassComp dynamic.ResourceInterface
	kubeClient     kubernetes.Interface
}

// TestComponentsAdmissionAndControllerSuite verifies the Component CRD OpenAPI/CEL schema validations
// and ValidatingAdmissionPolicy deletion protection against a local in-memory etcd and kube-apiserver (envtest).
func TestComponentsAdmissionAndControllerSuite(t *testing.T) {
	ctx := context.Background()
	clients := startAdmissionEnvtest(ctx, t)

	t.Run("HelmTemplatesRespectEnableComponentsFlag", func(t *testing.T) {
		verifyEnableComponentsTemplateFlag(t)
	})
	t.Run("CRDOpenAPIAndCELSchemaValidations", func(t *testing.T) {
		verifyCRDSchemaAndCELRules(ctx, t, clients.adminDynamic.Resource(bridge.ComponentGVR).Namespace("default"))
	})
	t.Run("ValidatingAdmissionPolicyDeletionProtection", func(t *testing.T) {
		waitForVAPActivation(ctx, t, clients.nonAdminComp)
		verifyDeletionProtectionVAP(ctx, t, clients)
	})
}

func startAdmissionEnvtest(ctx context.Context, t *testing.T) admissionClients {
	t.Helper()
	crdDir := t.TempDir()
	crdBytes := renderHelmManifestTemplate(t, "crd.yaml", true)
	if err := os.WriteFile(filepath.Join(crdDir, "crds.yaml"), crdBytes, 0600); err != nil {
		t.Fatalf("failed to write CRD manifest: %v", err)
	}
	testEnv := &envtest.Environment{
		BinaryAssetsDirectory:   resolveEnvtestAssets(t),
		ControlPlaneStopTimeout: 200 * time.Millisecond,
		CRDInstallOptions: envtest.CRDInstallOptions{
			Paths:              []string{crdDir},
			ErrorIfPathMissing: true,
			PollInterval:       10 * time.Millisecond,
			MaxTime:            5 * time.Second,
		},
	}
	cfg, err := testEnv.Start()
	if err != nil {
		t.Fatalf("failed to start envtest: %v", err)
	}
	t.Cleanup(func() { _ = testEnv.Stop() })

	clients := buildAdmissionClients(t, cfg)
	grantComponentRBAC(ctx, t, clients.kubeClient)
	installDeletionProtectionVAP(ctx, t, clients)
	return clients
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
		Subjects: []rbacv1.Subject{
			{Kind: "User", Name: "system:serviceaccount:xmc:gitops-reconciler"},
			{Kind: "Group", Name: "xmc-platform-admins"},
		},
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
		return dyn.Resource(bridge.ComponentGVR).Namespace("default")
	}
	return admissionClients{
		adminDynamic:   adminDyn,
		nonAdminComp:   impersonateComp("system:serviceaccount:xmc:gitops-reconciler", "system:authenticated"),
		breakglassComp: impersonateComp("sre-oncall@example.com", "system:authenticated", "xmc-platform-admins"),
		kubeClient:     kubeClient,
	}
}

func installDeletionProtectionVAP(ctx context.Context, t *testing.T, clients admissionClients) {
	t.Helper()
	docs := strings.Split(string(renderHelmManifestTemplate(t, "deletion-vap.yaml", true)), "\n---")
	if len(docs) < 2 {
		t.Fatalf("expected ValidatingAdmissionPolicy and Binding documents in deletion-vap.yaml, got %d", len(docs))
	}
	var policy admissionregistrationv1.ValidatingAdmissionPolicy
	if err := yaml.Unmarshal([]byte(docs[0]), &policy); err != nil {
		t.Fatalf("failed to unmarshal ValidatingAdmissionPolicy: %v", err)
	}
	var binding admissionregistrationv1.ValidatingAdmissionPolicyBinding
	if err := yaml.Unmarshal([]byte(docs[1]), &binding); err != nil {
		t.Fatalf("failed to unmarshal ValidatingAdmissionPolicyBinding: %v", err)
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
	err := wait.PollUntilContextTimeout(ctx, 15*time.Millisecond, 3*time.Second, true, func(ctx context.Context) (bool, error) {
		delErr := compClient.Delete(ctx, canary.GetName(), metav1.DeleteOptions{})
		if delErr == nil || strings.Contains(delErr.Error(), "not found") {
			if _, createErr := compClient.Create(ctx, canary, metav1.CreateOptions{}); createErr != nil && !strings.Contains(createErr.Error(), "already exists") {
				return false, createErr
			}
			return false, nil
		}
		return strings.Contains(delErr.Error(), "Deletion rejected: Component has deletionProtection enabled"), nil
	})
	if err != nil {
		t.Fatalf("timed out waiting for ValidatingAdmissionPolicy activation: %v", err)
	}
}

func verifyEnableComponentsTemplateFlag(t *testing.T) {
	t.Helper()
	for _, tmplName := range []string{"crd.yaml", "deletion-vap.yaml"} {
		var disabledDoc, enabledDoc map[string]any
		if err := yaml.Unmarshal(renderHelmManifestTemplate(t, tmplName, false), &disabledDoc); err != nil || len(disabledDoc) != 0 {
			t.Fatalf("expected %s with componentsDeployment.enabled=false to render empty doc, got %v (err=%v)", tmplName, disabledDoc, err)
		}
		if err := yaml.Unmarshal(renderHelmManifestTemplate(t, tmplName, true), &enabledDoc); err != nil || enabledDoc["kind"] == nil {
			t.Fatalf("expected %s with componentsDeployment.enabled=true to render K8s object, got %v (err=%v)", tmplName, enabledDoc, err)
		}
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
			name := "cel-case-" + string(rune('a'+i))
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
	breakglassTarget := newComponentManifest("breakglass-protected", protectedSpec)
	if _, err := clients.nonAdminComp.Create(ctx, breakglassTarget, metav1.CreateOptions{}); err != nil {
		t.Fatalf("Create breakglass target failed: %v", err)
	}
	if err := clients.breakglassComp.Delete(ctx, breakglassTarget.GetName(), metav1.DeleteOptions{}); err != nil {
		t.Fatalf("expected xmc-platform-admins breakglass DELETE to succeed, got: %v", err)
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
		filepath.Join("..", "..", "deploy", "helm", "templates", "components", fileName),
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
	if srcDir := os.Getenv("TEST_SRCDIR"); srcDir != "" {
		var found string
		_ = filepath.WalkDir(srcDir, func(p string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() && d.Name() == "kube-apiserver" {
				found = filepath.Dir(p)
				return filepath.SkipAll
			}
			return nil
		})
		if found != "" {
			return found
		}
	}
	fallback := filepath.Join(os.Getenv("HOME"), ".local", "share", "kubebuilder-envtest", "k8s", fmt.Sprintf("1.30.3-%s-%s", runtime.GOOS, runtime.GOARCH))
	if _, err := os.Stat(filepath.Join(fallback, "kube-apiserver")); err == nil {
		return fallback
	}
	t.Fatal("failed to locate kubebuilder envtest binaries (etcd, kube-apiserver)")
	return ""
}
