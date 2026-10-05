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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"helm.sh/helm/v3/pkg/release"
	"helm.sh/helm/v3/pkg/storage/driver"
)

func TestK8sHelmManager_Lifecycle(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()
	runner := &mockHelmActionRunner{}
	mgr := NewK8sHelmManager(client, "xmc", "xmanager-cloud", WithActionRunner(runner))

	ref := ComponentRef{
		Namespace: "xmc",
		Name:      "ingestion-gateway",
	}
	opts := HelmComponentOptions{
		Ref:      ref,
		Enabled:  true,
		ImageTag: "v1.0.0",
		Parameters: map[string]string{
			"minPartitions": "128",
		},
	}

	// 1. Ensure 1P component
	if err := mgr.Ensure1PComponent(ctx, ref, opts); err != nil {
		t.Fatalf("Ensure1PComponent failed: %v", err)
	}

	if !mgr.IsComponentActive(ref) {
		t.Error("expected ingestion-gateway to be active")
	}

	// Verify ConfigMap was created with serialized Helm values
	cm, err := client.CoreV1().ConfigMaps("xmc").Get(ctx, ComponentValuesConfigMapName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("failed to get ConfigMap: %v", err)
	}

	rawJSON, ok := cm.Data["ingestion-gateway.json"]
	if !ok {
		t.Fatalf("missing ingestion-gateway.json in ConfigMap data: %+v", cm.Data)
	}

	var parsedValues map[string]any
	if err := json.Unmarshal([]byte(rawJSON), &parsedValues); err != nil {
		t.Fatalf("failed to unmarshal ConfigMap JSON: %v", err)
	}

	igMap, ok := parsedValues["ingestionGatewayDeployment"].(map[string]any)
	enabled, _ := igMap["enabled"].(bool)
	if !ok || !enabled {
		t.Errorf("expected ingestionGatewayDeployment.enabled = true, got %+v", parsedValues)
	}

	// 2. Disable 1P component via Delete1PComponent
	if err := mgr.Delete1PComponent(ctx, ref); err != nil {
		t.Fatalf("Delete1PComponent failed: %v", err)
	}

	if mgr.IsComponentActive(ref) {
		t.Error("expected ingestion-gateway to be inactive after deletion")
	}

	// Verify ConfigMap entry is retained with enabled = false
	cmDisabled, err := client.CoreV1().ConfigMaps("xmc").Get(ctx, ComponentValuesConfigMapName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("failed to get disabled ConfigMap: %v", err)
	}

	disabledJSON, exists := cmDisabled.Data["ingestion-gateway.json"]
	if !exists {
		t.Fatalf("expected ingestion-gateway.json to be retained in ConfigMap on disablement")
	}

	var disabledValues map[string]any
	if err := json.Unmarshal([]byte(disabledJSON), &disabledValues); err != nil {
		t.Fatalf("failed to unmarshal disabled values: %v", err)
	}
	igDisabledMap, ok := disabledValues["ingestionGatewayDeployment"].(map[string]any)
	disabledEnabled, isBool := igDisabledMap["enabled"].(bool)
	if !ok || !isBool || disabledEnabled {
		t.Errorf("expected ingestionGatewayDeployment.enabled = false after disablement, got %+v", disabledValues)
	}

	// 3. Purge 1P component permanently removes the key
	if err := mgr.Purge1PComponent(ctx, ref); err != nil {
		t.Fatalf("Purge1PComponent failed: %v", err)
	}

	cmPurged, err := client.CoreV1().ConfigMaps("xmc").Get(ctx, ComponentValuesConfigMapName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("failed to get purged ConfigMap: %v", err)
	}

	if _, exists := cmPurged.Data["ingestion-gateway.json"]; exists {
		t.Errorf("expected ingestion-gateway.json to be deleted from ConfigMap after Purge, but key still exists")
	}
}

func TestK8sHelmManager_NamespaceIsolation(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()
	runner := &mockHelmActionRunner{}
	mgr := NewK8sHelmManager(client, "xmc", "xmanager-cloud", WithActionRunner(runner))

	refXMC := ComponentRef{
		Namespace: "xmc",
		Name:      "rod-data-provider",
	}
	refOther := ComponentRef{
		Namespace: "analytics",
		Name:      "rod-data-provider",
	}
	optsXMC := HelmComponentOptions{
		Ref:     refXMC,
		Enabled: true,
	}
	optsOther := HelmComponentOptions{
		Ref:     refOther,
		Enabled: true,
	}

	if err := mgr.Ensure1PComponent(ctx, refXMC, optsXMC); err != nil {
		t.Fatalf("Ensure1PComponent xmc failed: %v", err)
	}
	if err := mgr.Ensure1PComponent(ctx, refOther, optsOther); err != nil {
		t.Fatalf("Ensure1PComponent analytics failed: %v", err)
	}

	// Verify ConfigMaps in both namespaces
	cmXMC, err := client.CoreV1().ConfigMaps("xmc").Get(ctx, ComponentValuesConfigMapName, metav1.GetOptions{})
	if err != nil || cmXMC.Data["rod-data-provider.json"] == "" {
		t.Fatalf("expected rod-data-provider.json in xmc ConfigMap: %v", err)
	}

	cmAnalytics, err := client.CoreV1().ConfigMaps("analytics").Get(ctx, ComponentValuesConfigMapName, metav1.GetOptions{})
	if err != nil || cmAnalytics.Data["rod-data-provider.json"] == "" {
		t.Fatalf("expected rod-data-provider.json in analytics ConfigMap: %v", err)
	}

	// Verify active states in both namespaces
	if !mgr.IsComponentActive(refXMC) {
		t.Error("expected refXMC to be active")
	}
	if !mgr.IsComponentActive(refOther) {
		t.Error("expected refOther to be active")
	}

	// Disable from analytics only
	if err := mgr.Delete1PComponent(ctx, refOther); err != nil {
		t.Fatalf("Delete1PComponent analytics failed: %v", err)
	}

	// Verify analytics is inactive and xmc remains active
	if mgr.IsComponentActive(refOther) {
		t.Error("expected refOther to be inactive after delete")
	}
	if !mgr.IsComponentActive(refXMC) {
		t.Error("expected refXMC to still be active")
	}

	// Verify analytics ConfigMap retains rod-data-provider.json with enabled = false
	cmAnalyticsDisabled, err := client.CoreV1().ConfigMaps("analytics").Get(ctx, ComponentValuesConfigMapName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("failed to get analytics ConfigMap: %v", err)
	}
	if _, exists := cmAnalyticsDisabled.Data["rod-data-provider.json"]; !exists {
		t.Errorf("expected rod-data-provider.json to be retained in analytics ConfigMap on disablement")
	}

	// Purge from analytics
	if err := mgr.Purge1PComponent(ctx, refOther); err != nil {
		t.Fatalf("Purge1PComponent analytics failed: %v", err)
	}

	// Verify analytics ConfigMap had rod-data-provider.json removed, while xmc remains intact
	cmAnalyticsUpdated, err := client.CoreV1().ConfigMaps("analytics").Get(ctx, ComponentValuesConfigMapName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("failed to get analytics ConfigMap: %v", err)
	}
	if _, exists := cmAnalyticsUpdated.Data["rod-data-provider.json"]; exists {
		t.Errorf("expected rod-data-provider.json to be removed from analytics ConfigMap after purge")
	}

	cmXMCUpdated, err := client.CoreV1().ConfigMaps("xmc").Get(ctx, ComponentValuesConfigMapName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("failed to get xmc ConfigMap: %v", err)
	}
	if _, exists := cmXMCUpdated.Data["rod-data-provider.json"]; !exists {
		t.Errorf("expected rod-data-provider.json to still exist in xmc ConfigMap")
	}
}

func TestK8sHelmManager_Guards(t *testing.T) {
	ctx := context.Background()
	var nilMgr *K8sHelmManager
	refTest := ComponentRef{Namespace: "xmc", Name: "test"}

	if err := nilMgr.Ensure1PComponent(ctx, refTest, HelmComponentOptions{}); err != nil {
		t.Errorf("expected nil error on nil receiver Ensure1PComponent, got %v", err)
	}
	if err := nilMgr.Delete1PComponent(ctx, refTest); err != nil {
		t.Errorf("expected nil error on nil receiver Delete1PComponent, got %v", err)
	}
	if err := nilMgr.Purge1PComponent(ctx, refTest); err != nil {
		t.Errorf("expected nil error on nil receiver Purge1PComponent, got %v", err)
	}
	if nilMgr.IsComponentActive(refTest) {
		t.Error("expected false on nil receiver IsComponentActive")
	}

	mgr := NewK8sHelmManager(nil, "xmc", "xmanager-cloud")

	emptyRef := ComponentRef{}
	if err := mgr.Ensure1PComponent(ctx, emptyRef, HelmComponentOptions{}); err == nil {
		t.Error("expected error on empty componentName for Ensure1PComponent")
	}
	if err := mgr.Delete1PComponent(ctx, emptyRef); err == nil {
		t.Error("expected error on empty componentName for Delete1PComponent")
	}
	if err := mgr.Purge1PComponent(ctx, emptyRef); err == nil {
		t.Error("expected error on empty componentName for Purge1PComponent")
	}
	if err := mgr.Orphan1PComponent(ctx, emptyRef); err == nil {
		t.Error("expected error on empty componentName for Orphan1PComponent")
	}

	emptyNsRef := ComponentRef{Namespace: "", Name: "test-comp"}
	if err := mgr.Ensure1PComponent(ctx, emptyNsRef, HelmComponentOptions{}); err == nil {
		t.Error("expected error on empty namespace for Ensure1PComponent")
	}
	if err := mgr.Delete1PComponent(ctx, emptyNsRef); err == nil {
		t.Error("expected error on empty namespace for Delete1PComponent")
	}
	if err := mgr.Purge1PComponent(ctx, emptyNsRef); err == nil {
		t.Error("expected error on empty namespace for Purge1PComponent")
	}
	if err := mgr.Orphan1PComponent(ctx, emptyNsRef); err == nil {
		t.Error("expected error on empty namespace for Orphan1PComponent")
	}

	if err := nilMgr.Orphan1PComponent(ctx, refTest); err != nil {
		t.Errorf("expected nil error on nil receiver Orphan1PComponent, got %v", err)
	}

	nonEmptyRef := ComponentRef{Namespace: "xmc", Name: "ingestion-gateway"}
	if err := mgr.Ensure1PComponent(ctx, nonEmptyRef, HelmComponentOptions{}); err == nil {
		t.Error("expected error on nil client for Ensure1PComponent")
	}
	if err := mgr.Delete1PComponent(ctx, nonEmptyRef); err == nil {
		t.Error("expected error on nil client for Delete1PComponent")
	}
	if err := mgr.Purge1PComponent(ctx, nonEmptyRef); err == nil {
		t.Error("expected error on nil client for Purge1PComponent")
	}
}

func TestK8sHelmManager_Orphan(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()
	runner := &mockHelmActionRunner{}
	mgr := NewK8sHelmManager(client, "xmc", "xmanager-cloud", WithActionRunner(runner))

	ref := ComponentRef{
		Namespace: "xmc",
		Name:      "rod-data-provider",
	}
	opts := HelmComponentOptions{
		Ref:     ref,
		Enabled: true,
	}

	if err := mgr.Ensure1PComponent(ctx, ref, opts); err != nil {
		t.Fatalf("Ensure1PComponent failed: %v", err)
	}

	if err := mgr.Orphan1PComponent(ctx, ref); err != nil {
		t.Fatalf("Orphan1PComponent failed: %v", err)
	}

	if mgr.IsComponentActive(ref) {
		t.Error("expected rod-data-provider to be inactive after orphaning")
	}

	// Verify ConfigMap data is retained after orphaning.
	cm, err := client.CoreV1().ConfigMaps("xmc").Get(ctx, ComponentValuesConfigMapName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("failed to get ConfigMap after orphan: %v", err)
	}
	if _, exists := cm.Data["rod-data-provider.json"]; !exists {
		t.Error("expected component values to be retained in ConfigMap after orphaning, but key was deleted")
	}
}

func TestK8sHelmManager_NoOpUpdateGuard(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()
	runner := &mockHelmActionRunner{}
	mgr := NewK8sHelmManager(client, "xmc", "xmanager-cloud", WithActionRunner(runner))

	ref := ComponentRef{
		Namespace: "xmc",
		Name:      "rod-data-provider",
	}
	opts := HelmComponentOptions{
		Ref:     ref,
		Enabled: true,
		Parameters: map[string]string{
			"replicas": "2",
		},
	}

	// 1. Initial ensure -> creates the ConfigMap
	if err := mgr.Ensure1PComponent(ctx, ref, opts); err != nil {
		t.Fatalf("first Ensure1PComponent failed: %v", err)
	}

	actionsAfterCreate := len(client.Actions())

	// 2. Second ensure with identical options -> no-op, must NOT issue an update
	if err := mgr.Ensure1PComponent(ctx, ref, opts); err != nil {
		t.Fatalf("second Ensure1PComponent failed: %v", err)
	}

	for _, action := range client.Actions()[actionsAfterCreate:] {
		if action.GetVerb() == "update" {
			t.Errorf("unexpected Update action on no-op Ensure1PComponent: %+v", action)
		}
	}

	// 3. Disable via Delete1PComponent -> issues update
	if err := mgr.Delete1PComponent(ctx, ref); err != nil {
		t.Fatalf("first Delete1PComponent failed: %v", err)
	}

	actionsAfterDisable := len(client.Actions())

	// 4. Second Delete1PComponent -> no-op, must NOT issue an update
	if err := mgr.Delete1PComponent(ctx, ref); err != nil {
		t.Fatalf("second Delete1PComponent failed: %v", err)
	}

	for _, action := range client.Actions()[actionsAfterDisable:] {
		if action.GetVerb() == "update" {
			t.Errorf("unexpected Update action on no-op Delete1PComponent: %+v", action)
		}
	}
}

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

func TestK8sHelmManager_OptionsAndGuards(t *testing.T) {
	mgr := NewK8sHelmManager(nil, "xmc", "xmanager-cloud",
		WithKubeconfig("/tmp/nonexistent-kubeconfig"),
		WithChartPath("deploy/helm"),
	)
	defer mgr.Close()
	if mgr.chartPath != "deploy/helm" {
		t.Errorf("expected chartPath deploy/helm, got %q", mgr.chartPath)
	}
	if mgr.runner == nil {
		t.Error("expected runner to be initialized by WithKubeconfig")
	}
	if sdkRunner, ok := mgr.runner.(*SDKHelmActionRunner); !ok || sdkRunner.chartPath != "deploy/helm" {
		t.Errorf("expected SDKHelmActionRunner with chartPath deploy/helm, got %+v", mgr.runner)
	}

	// Verify options order independence (WithChartPath before WithKubeconfig).
	mgrReversed := NewK8sHelmManager(nil, "xmc", "xmanager-cloud",
		WithChartPath("deploy/helm/reversed"),
		WithKubeconfig("/tmp/nonexistent-kubeconfig"),
	)
	if sdkRunner, ok := mgrReversed.runner.(*SDKHelmActionRunner); !ok || sdkRunner.chartPath != "deploy/helm/reversed" {
		t.Errorf("expected SDKHelmActionRunner with chartPath deploy/helm/reversed, got %+v", mgrReversed.runner)
	}

	// Verify explicit WithActionRunner takes precedence over default SDK runner creation.
	mockRunner := &mockHelmActionRunner{}
	mgrWithRunner := NewK8sHelmManager(nil, "xmc", "xmanager-cloud",
		WithActionRunner(mockRunner),
		WithKubeconfig("/tmp/nonexistent-kubeconfig"),
	)
	if mgrWithRunner.runner != mockRunner {
		t.Errorf("expected explicit action runner to take precedence, got %+v", mgrWithRunner.runner)
	}

	mgrGetter := NewK8sHelmManager(nil, "xmc", "xmanager-cloud",
		WithRESTClientGetter(nil),
		WithChartPath("deploy/helm/getter"),
	)
	defer mgrGetter.Close()
	if mgrGetter.runner == nil {
		t.Error("expected runner to be initialized by WithRESTClientGetter")
	}
	if sdkRunner, ok := mgrGetter.runner.(*SDKHelmActionRunner); !ok || sdkRunner.chartPath != "deploy/helm/getter" {
		t.Errorf("expected SDKHelmActionRunner with chartPath deploy/helm/getter, got %+v", mgrGetter.runner)
	}
}

func TestK8sHelmManager_RetryFailedUpgrade_Ensure(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()
	runner := &mockHelmActionRunner{
		upgradeErr: errors.New("transient Helm lock conflict"),
	}
	mgr := NewK8sHelmManager(client, "xmc", "xmanager-cloud",
		WithActionRunner(runner),
		WithSyncExecution(true),
	)
	defer mgr.Close()

	ref := ComponentRef{Namespace: "xmc", Name: "metrics"}
	opts := HelmComponentOptions{
		Ref:     ref,
		Enabled: true,
	}

	// First attempt fails during Helm upgrade
	err := mgr.Ensure1PComponent(ctx, ref, opts)
	if err == nil || !strings.Contains(err.Error(), "transient Helm lock conflict") {
		t.Fatalf("expected transient upgrade error, got %v", err)
	}

	// On second reconciliation, ConfigMap is not modified, but previous upgrade failed.
	// Ensure1PComponent must retry rather than exiting early.
	runner.upgradeErr = nil
	if err := mgr.Ensure1PComponent(ctx, ref, opts); err != nil {
		t.Fatalf("expected retry Ensure1PComponent to succeed, got %v", err)
	}
	if runner.upgradeCalls != 2 {
		t.Errorf("expected 2 runner calls after retry, got %d", runner.upgradeCalls)
	}
}

func TestK8sHelmManager_RetryFailedUpgrade_Delete(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()
	runner := &mockHelmActionRunner{}
	mgr := NewK8sHelmManager(client, "xmc", "xmanager-cloud",
		WithActionRunner(runner),
		WithSyncExecution(true),
	)
	defer mgr.Close()

	ref := ComponentRef{Namespace: "xmc", Name: "metrics"}
	opts := HelmComponentOptions{
		Ref:     ref,
		Enabled: true,
	}
	if err := mgr.Ensure1PComponent(ctx, ref, opts); err != nil {
		t.Fatalf("unexpected Ensure1PComponent error: %v", err)
	}

	// Inject error for delete uninstall
	runner.uninstallErr = errors.New("deletion lock error")
	err := mgr.Delete1PComponent(ctx, ref)
	if err == nil || !strings.Contains(err.Error(), "deletion lock error") {
		t.Fatalf("expected deletion upgrade error, got %v", err)
	}

	// On second reconciliation, previous uninstall failed.
	// Delete1PComponent must retry.
	runner.uninstallErr = nil
	if err := mgr.Delete1PComponent(ctx, ref); err != nil {
		t.Fatalf("expected retry Delete1PComponent to succeed, got %v", err)
	}
}

func TestK8sHelmManager_ActuationTracking(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()
	runner := &mockHelmActionRunner{}
	mgr := NewK8sHelmManager(client, "xmc", "xmanager-cloud",
		WithActionRunner(runner),
		WithSyncExecution(true),
	)
	defer mgr.Close()

	ref := ComponentRef{Namespace: "xmc", Name: "metrics"}
	opts := HelmComponentOptions{Ref: ref, Enabled: true}

	if mgr.IsComponentActuated(ref, 1) {
		t.Error("expected component not to be actuated before Ensure1PComponent")
	}

	if err := mgr.Ensure1PComponent(ctx, ref, opts); err != nil {
		t.Fatalf("Ensure1PComponent failed: %v", err)
	}
	mgr.RecordActuation(ref, 1)

	if !mgr.IsComponentActuated(ref, 1) {
		t.Error("expected component to be actuated at generation 1")
	}
	if mgr.IsComponentActuated(ref, 2) {
		t.Error("expected component not to be actuated at generation 2")
	}

	// After deletion, actuation record should be cleared
	if err := mgr.Delete1PComponent(ctx, ref); err != nil {
		t.Fatalf("Delete1PComponent failed: %v", err)
	}
	if mgr.IsComponentActuated(ref, 1) {
		t.Error("expected component actuation to be cleared after deletion")
	}
}

func TestK8sHelmManager_ColdStartHydration(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()
	runner := &mockHelmActionRunner{}

	// First manager persists active component values into xmc-component-values ConfigMap
	mgr1 := NewK8sHelmManager(client, "xmc", "xmanager-cloud", WithActionRunner(runner), WithSyncExecution(true))
	ref := ComponentRef{Namespace: "xmc", Name: "ingestion-gateway"}
	if err := mgr1.Ensure1PComponent(ctx, ref, HelmComponentOptions{Ref: ref, Enabled: true, ImageTag: "v1.0.0"}); err != nil {
		t.Fatalf("mgr1 Ensure1PComponent failed: %v", err)
	}
	_ = mgr1.Close()

	// Simulate controller restart with a new K8sHelmManager instance
	mgr2 := NewK8sHelmManager(client, "xmc", "xmanager-cloud", WithActionRunner(runner), WithSyncExecution(true))
	defer mgr2.Close()

	if err := mgr2.HydrateFromCluster(ctx); err != nil {
		t.Fatalf("HydrateFromCluster failed: %v", err)
	}
	if !mgr2.IsComponentActive(ref) {
		t.Fatal("expected ingestion-gateway to be active after HydrateFromCluster")
	}

	if err := mgr2.HydrateComponentState(ctx, ref, 3, true); err != nil {
		t.Fatalf("HydrateComponentState failed: %v", err)
	}
	if !mgr2.IsComponentActuated(ref, 3) {
		t.Fatal("expected ingestion-gateway to be marked actuated at generation 3 after HydrateComponentState")
	}
}

func TestK8sHelmManager_ComponentHealth(t *testing.T) {
	ref := ComponentRef{Namespace: "xmc", Name: "ingestion-gateway"}
	releaseKey := componentKey(ref.Namespace, ComponentReleaseName(ref.Name))
	type health struct {
		Healthy bool
		Reason  string
	}
	tests := []struct {
		name          string
		ref           ComponentRef
		noRunner      bool
		installed     bool
		releaseStatus release.Status
		statusErr     error
		upgradeErr    error
		pending       bool
		missing       []string
		missingErr    error
		want          health
	}{
		{name: "deployed release is healthy", ref: ref, installed: true, want: health{true, ReasonComponentHealthy}},
		{name: "deployed release missing objects is drifted", ref: ref, installed: true, missing: []string{"Role/ingestion-gateway-components-reader"}, want: health{false, ReasonHelmReleaseDrifted}},
		{name: "resource lookup error trusts deployed status", ref: ref, installed: true, missingErr: errors.New("connection refused"), want: health{true, ReasonComponentHealthy}},
		{name: "last upgrade failed", ref: ref, installed: true, upgradeErr: errors.New("image pull failed"), want: health{false, ReasonHelmUpgradeFailed}},
		{name: "upgrade in flight over previously deployed revision", ref: ref, installed: true, pending: true, want: health{false, ReasonHelmUpgradePending}},
		{name: "upgrade error takes precedence over pending retry", ref: ref, installed: true, pending: true, upgradeErr: errors.New("boom"), want: health{false, ReasonHelmUpgradeFailed}},
		{name: "release not found", ref: ref, want: health{false, ReasonHelmReleaseNotFound}},
		{name: "wrapped not found error", ref: ref, statusErr: fmt.Errorf("helm status failed: %w", driver.ErrReleaseNotFound), want: health{false, ReasonHelmReleaseNotFound}},
		{name: "non-sentinel not found text is transient", ref: ref, statusErr: errors.New(`namespaces "xmc" not found`), want: health{false, ReasonHelmStatusUnavailable}},
		{name: "release stuck in pending-install", ref: ref, installed: true, releaseStatus: release.StatusPendingInstall, want: health{false, ReasonHelmReleaseStuck}},
		{name: "release stuck in pending-upgrade", ref: ref, installed: true, releaseStatus: release.StatusPendingUpgrade, want: health{false, ReasonHelmReleaseStuck}},
		{name: "release failed", ref: ref, installed: true, releaseStatus: release.StatusFailed, want: health{false, ReasonHelmReleaseNotReady}},
		{name: "status query error", ref: ref, statusErr: errors.New("connection refused"), want: health{false, ReasonHelmStatusUnavailable}},
		{name: "runner uninitialized", ref: ref, noRunner: true, want: health{false, ReasonHelmRunnerUninitialized}},
		{name: "upgrade error takes precedence over missing runner", ref: ref, noRunner: true, upgradeErr: errors.New("helm action runner is uninitialized"), want: health{false, ReasonHelmUpgradeFailed}},
		{name: "empty namespace", ref: ComponentRef{Name: "ingestion-gateway"}, want: health{false, ReasonInvalidComponentRef}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := &mockHelmActionRunner{installed: map[string]bool{releaseKey: tt.installed}, releaseStatus: tt.releaseStatus, statusErr: tt.statusErr, missing: tt.missing, missingErr: tt.missingErr}
			opts := []HelmManagerOption{WithSyncExecution(true)}
			if !tt.noRunner {
				opts = append(opts, WithActionRunner(runner))
			}
			mgr := NewK8sHelmManager(fake.NewClientset(), "xmc", "xmanager-cloud", opts...)
			defer mgr.Close()
			mgr.setUpgradeErr(ref, tt.upgradeErr)
			if tt.pending {
				mgr.markUpgradePending(ref)
			}

			healthy, reason, msg := mgr.ComponentHealth(context.Background(), tt.ref)
			if diff := cmp.Diff(tt.want, health{healthy, reason}); diff != "" {
				t.Errorf("ComponentHealth() mismatch (-want +got):\n%s\nmessage: %s", diff, msg)
			}
			if msg == "" {
				t.Error("ComponentHealth() returned empty message")
			}
		})
	}

	var nilMgr *K8sHelmManager
	if healthy, reason, _ := nilMgr.ComponentHealth(context.Background(), ref); healthy || reason != ReasonHelmRunnerUninitialized {
		t.Errorf("nil manager ComponentHealth() = (%v, %q), want (false, %q)", healthy, reason, ReasonHelmRunnerUninitialized)
	}
}

// TestK8sHelmManager_ReleaseRepair verifies that an actuated component whose release drifts, disappears, or fails is
// re-upgraded at the same generation by the next Ensure1PComponent, while transient errors never trigger a re-upgrade.
func TestK8sHelmManager_ReleaseRepair(t *testing.T) {
	const generation = 3
	ref := ComponentRef{Namespace: "xmc", Name: "ingestion-gateway"}
	opts := HelmComponentOptions{Ref: ref, Enabled: true, ImageTag: "v1.0.0"}
	releaseKey := componentKey(ref.Namespace, ComponentReleaseName(ref.Name))
	type result struct {
		Reason      string
		NeedsRepair bool
		Reupgraded  bool
		FinalReason string
	}
	tests := []struct {
		name   string
		mutate func(r *mockHelmActionRunner)
		want   result
	}{
		{
			name:   "drifted release is re-upgraded and recovers",
			mutate: func(r *mockHelmActionRunner) { r.missing = []string{"Role/ingestion-gateway-components-reader"} },
			want:   result{Reason: ReasonHelmReleaseDrifted, NeedsRepair: true, Reupgraded: true, FinalReason: ReasonComponentHealthy},
		},
		{
			name:   "release deleted after actuation is re-installed",
			mutate: func(r *mockHelmActionRunner) { delete(r.installed, releaseKey) },
			want:   result{Reason: ReasonHelmReleaseNotFound, NeedsRepair: true, Reupgraded: true, FinalReason: ReasonComponentHealthy},
		},
		{
			name:   "terminally failed release is re-upgraded and recovers",
			mutate: func(r *mockHelmActionRunner) { r.releaseStatus = release.StatusFailed },
			want:   result{Reason: ReasonHelmReleaseNotReady, NeedsRepair: true, Reupgraded: true, FinalReason: ReasonComponentHealthy},
		},
		{
			name:   "transient status error keeps actuation",
			mutate: func(r *mockHelmActionRunner) { r.statusErr = errors.New("connection refused") },
			want:   result{Reason: ReasonHelmStatusUnavailable, FinalReason: ReasonHelmStatusUnavailable},
		},
		{
			name:   "transient resource lookup error keeps actuation",
			mutate: func(r *mockHelmActionRunner) { r.missingErr = errors.New("connection refused") },
			want:   result{Reason: ReasonComponentHealthy, FinalReason: ReasonComponentHealthy},
		},
		{
			name:   "stuck pending release keeps actuation",
			mutate: func(r *mockHelmActionRunner) { r.releaseStatus = release.StatusPendingUpgrade },
			want:   result{Reason: ReasonHelmReleaseStuck, FinalReason: ReasonHelmReleaseStuck},
		},
		{
			name:   "release uninstalled with kept history is re-upgraded and recovers",
			mutate: func(r *mockHelmActionRunner) { r.releaseStatus = release.StatusUninstalled },
			want:   result{Reason: ReasonHelmReleaseNotReady, NeedsRepair: true, Reupgraded: true, FinalReason: ReasonComponentHealthy},
		},
		{
			name:   "release being uninstalled is not repaired",
			mutate: func(r *mockHelmActionRunner) { r.releaseStatus = release.StatusUninstalling },
			want:   result{Reason: ReasonHelmReleaseNotReady, FinalReason: ReasonHelmReleaseNotReady},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			runner := &mockHelmActionRunner{}
			mgr := NewK8sHelmManager(fake.NewClientset(), "xmc", "xmanager-cloud", WithActionRunner(runner), WithSyncExecution(true))
			defer mgr.Close()
			if err := mgr.Ensure1PComponent(ctx, ref, opts); err != nil {
				t.Fatalf("initial Ensure1PComponent failed: %v", err)
			}
			mgr.RecordActuation(ref, generation)
			runner.mu.Lock()
			tt.mutate(runner)
			runner.mu.Unlock()

			var got result
			_, got.Reason, _ = mgr.ComponentHealth(ctx, ref)
			got.NeedsRepair = mgr.NeedsRepair(ref, generation)
			if !mgr.IsComponentActuated(ref, generation) {
				t.Error("IsComponentActuated() = false after health check, want true (repair is reported by NeedsRepair)")
			}
			if mgr.NeedsRepair(ref, generation+1) {
				t.Error("NeedsRepair() = true for a generation that was never actuated, want false")
			}
			if err := mgr.Ensure1PComponent(ctx, ref, opts); err != nil {
				t.Fatalf("repair Ensure1PComponent failed: %v", err)
			}
			mgr.RecordActuation(ref, generation)
			runner.mu.Lock()
			got.Reupgraded = runner.upgradeCalls > 1
			runner.mu.Unlock()
			_, got.FinalReason, _ = mgr.ComponentHealth(ctx, ref)

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("release repair mismatch (-want +got):\n%s", diff)
			}
			if mgr.NeedsRepair(ref, generation) {
				t.Error("NeedsRepair() = true after repair upgrade, want false")
			}
		})
	}
}

// TestReleaseNeedsRepair verifies that only missing, drifted, failed, or uninstalled releases trigger repair, and that
// in-flight states such as uninstalling never do.
func TestReleaseNeedsRepair(t *testing.T) {
	tests := []struct {
		reason string
		status release.Status
		want   bool
	}{
		{reason: ReasonHelmReleaseNotFound, status: release.StatusUnknown, want: true},
		{reason: ReasonHelmReleaseDrifted, status: release.StatusDeployed, want: true},
		{reason: ReasonHelmReleaseNotReady, status: release.StatusFailed, want: true},
		{reason: ReasonHelmReleaseNotReady, status: release.StatusUninstalled, want: true},
		{reason: ReasonHelmReleaseNotReady, status: release.StatusUninstalling, want: false},
		{reason: ReasonHelmReleaseNotReady, status: release.StatusUnknown, want: false},
		{reason: ReasonHelmReleaseNotReady, status: release.StatusSuperseded, want: false},
		{reason: ReasonHelmReleaseStuck, status: release.StatusPendingUpgrade, want: false},
		{reason: ReasonHelmStatusUnavailable, status: release.StatusUnknown, want: false},
		{reason: ReasonComponentHealthy, status: release.StatusDeployed, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.reason+"/"+tt.status.String(), func(t *testing.T) {
			if got := releaseNeedsRepair(tt.reason, tt.status); got != tt.want {
				t.Errorf("releaseNeedsRepair(%q, %q) = %v, want %v", tt.reason, tt.status, got, tt.want)
			}
		})
	}
}

// TestK8sHelmManager_RepairBackoff verifies that a release whose objects keep being deleted is not re-upgraded on
// every health sweep: repairs back off exponentially, and the backoff resets on a new generation or after the release
// stays healthy for repairBackoffMax.
func TestK8sHelmManager_RepairBackoff(t *testing.T) {
	const generation = 3
	ref := ComponentRef{Namespace: "xmc", Name: "ingestion-gateway"}
	key := componentKey(ref.Namespace, ref.Name)
	opts := HelmComponentOptions{Ref: ref, Enabled: true, ImageTag: "v1.0.0"}
	deleted := []string{"Role/ingestion-gateway-components-reader"}

	setup := func(t *testing.T) (*K8sHelmManager, *mockHelmActionRunner) {
		t.Helper()
		runner := &mockHelmActionRunner{}
		mgr := NewK8sHelmManager(fake.NewClientset(), "xmc", "xmanager-cloud", WithActionRunner(runner), WithSyncExecution(true))
		t.Cleanup(func() { mgr.Close() })
		mgr.jitter = func() float64 { return 0 }
		if err := mgr.Ensure1PComponent(context.Background(), ref, opts); err != nil {
			t.Fatalf("initial Ensure1PComponent failed: %v", err)
		}
		mgr.RecordActuation(ref, generation)
		return mgr, runner
	}
	// sweep simulates an actor that deletes an object before each health check, then runs one reconcile pass and
	// reports whether it re-upgraded the release.
	sweep := func(t *testing.T, mgr *K8sHelmManager, runner *mockHelmActionRunner) bool {
		t.Helper()
		ctx := context.Background()
		runner.mu.Lock()
		runner.missing = deleted
		before := runner.upgradeCalls
		runner.mu.Unlock()
		if _, reason, _ := mgr.ComponentHealth(ctx, ref); reason != ReasonHelmReleaseDrifted {
			t.Fatalf("ComponentHealth() reason = %q, want %q", reason, ReasonHelmReleaseDrifted)
		}
		if err := mgr.Ensure1PComponent(ctx, ref, opts); err != nil {
			t.Fatalf("Ensure1PComponent failed: %v", err)
		}
		mgr.RecordActuation(ref, generation)
		runner.mu.Lock()
		defer runner.mu.Unlock()
		return runner.upgradeCalls > before
	}
	attempts := func(mgr *K8sHelmManager) int {
		mgr.mu.RLock()
		defer mgr.mu.RUnlock()
		return mgr.repairBackoff[key].count
	}

	t.Run("backs off exponentially between repairs", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			mgr, runner := setup(t)
			// Each step waits, then sweeps; the first repair is immediate, later ones wait 30s, then 60s.
			steps := []struct {
				wait time.Duration
				want bool
			}{
				{0, true},
				{0, false},
				{29 * time.Second, false},
				{time.Second, true},
				{30 * time.Second, false},
				{30 * time.Second, true},
			}
			for i, s := range steps {
				time.Sleep(s.wait)
				if got := sweep(t, mgr, runner); got != s.want {
					t.Errorf("step %d (after %v): re-upgraded = %v, want %v", i, s.wait, got, s.want)
				}
			}
			if got := attempts(mgr); got != 3 {
				t.Errorf("repair attempts = %d, want 3", got)
			}
		})
	})

	t.Run("new generation resets backoff", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			mgr, runner := setup(t)
			sweep(t, mgr, runner)
			mgr.RecordActuation(ref, generation+1)
			if got := attempts(mgr); got != 0 {
				t.Errorf("repair attempts after new generation = %d, want 0", got)
			}
		})
	})

	t.Run("only a sustained healthy period resets backoff", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			ctx := context.Background()
			mgr, runner := setup(t)
			sweep(t, mgr, runner)
			runner.mu.Lock()
			runner.missing = nil
			runner.mu.Unlock()
			for _, tc := range []struct {
				wait time.Duration
				want int
			}{
				{healthCacheTTL, 1},
				{repairBackoffMax, 0},
			} {
				time.Sleep(tc.wait)
				if healthy, reason, _ := mgr.ComponentHealth(ctx, ref); !healthy {
					t.Fatalf("ComponentHealth() unhealthy: %s", reason)
				}
				if got := attempts(mgr); got != tc.want {
					t.Errorf("repair attempts after healthy check at +%v = %d, want %d", tc.wait, got, tc.want)
				}
			}
		})
	})
}

// TestK8sHelmManager_RepairDelay verifies the repair backoff schedule doubles from repairBackoffBase, caps at
// repairBackoffMax, and adds at most repairBackoffJitter of randomized delay.
func TestK8sHelmManager_RepairDelay(t *testing.T) {
	tests := []struct {
		attempt int
		jitter  float64
		want    time.Duration
	}{
		{attempt: 1, want: 30 * time.Second},
		{attempt: 2, want: time.Minute},
		{attempt: 3, want: 2 * time.Minute},
		{attempt: 5, want: 8 * time.Minute},
		{attempt: 6, want: 10 * time.Minute},
		{attempt: 100, want: 10 * time.Minute},
		{attempt: 1, jitter: 0.5, want: 31500 * time.Millisecond},
		{attempt: 100, jitter: 0.999, want: 10*time.Minute + time.Duration(float64(10*time.Minute)*0.1*0.999)},
	}
	for _, tt := range tests {
		mgr := &K8sHelmManager{jitter: func() float64 { return tt.jitter }}
		if got := mgr.repairDelay(tt.attempt); got != tt.want {
			t.Errorf("repairDelay(%d) with jitter %v = %v, want %v", tt.attempt, tt.jitter, got, tt.want)
		}
	}
}

// TestK8sHelmManager_TeardownClearsRepair verifies that uninstalling, purging, or orphaning a component drops any
// pending repair marker.
func TestK8sHelmManager_TeardownClearsRepair(t *testing.T) {
	ref := ComponentRef{Namespace: "xmc", Name: "ingestion-gateway"}
	tests := []struct {
		name     string
		teardown func(context.Context, *K8sHelmManager) error
	}{
		{name: "Delete1PComponent", teardown: func(ctx context.Context, m *K8sHelmManager) error { return m.Delete1PComponent(ctx, ref) }},
		{name: "Purge1PComponent", teardown: func(ctx context.Context, m *K8sHelmManager) error { return m.Purge1PComponent(ctx, ref) }},
		{name: "Orphan1PComponent", teardown: func(ctx context.Context, m *K8sHelmManager) error { return m.Orphan1PComponent(ctx, ref) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			runner := &mockHelmActionRunner{}
			mgr := NewK8sHelmManager(fake.NewClientset(), "xmc", "xmanager-cloud", WithActionRunner(runner), WithSyncExecution(true))
			defer mgr.Close()
			if err := mgr.Ensure1PComponent(ctx, ref, HelmComponentOptions{Ref: ref, Enabled: true}); err != nil {
				t.Fatalf("Ensure1PComponent failed: %v", err)
			}
			runner.mu.Lock()
			runner.missing = []string{"Service/ingestion-gateway-service"}
			runner.mu.Unlock()
			if _, reason, _ := mgr.ComponentHealth(ctx, ref); reason != ReasonHelmReleaseDrifted {
				t.Fatalf("ComponentHealth() reason = %q, want %q", reason, ReasonHelmReleaseDrifted)
			}
			key := componentKey(ref.Namespace, ref.Name)
			mgr.mu.Lock()
			mgr.repairBackoff[key] = repairAttempt{count: 1}
			mgr.mu.Unlock()
			if err := tt.teardown(ctx, mgr); err != nil {
				t.Fatalf("%s failed: %v", tt.name, err)
			}
			mgr.mu.RLock()
			defer mgr.mu.RUnlock()
			if mgr.repairNeeded[key] {
				t.Errorf("repairNeeded still set after %s", tt.name)
			}
			if _, ok := mgr.repairBackoff[key]; ok {
				t.Errorf("repairBackoff still set after %s", tt.name)
			}
		})
	}
}

// TestK8sHelmManager_ComponentHealth_CachesDeployedVerdict verifies Status is not queried on every poll while the
// release is deployed, that the cache expires after healthCacheTTL, and that release operations invalidate it.
func TestK8sHelmManager_ComponentHealth_CachesDeployedVerdict(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		ref := ComponentRef{Namespace: "xmc", Name: "ingestion-gateway"}
		runner := &mockHelmActionRunner{installed: map[string]bool{componentKey(ref.Namespace, ComponentReleaseName(ref.Name)): true}}
		mgr := NewK8sHelmManager(fake.NewClientset(), "xmc", "xmanager-cloud", WithActionRunner(runner), WithSyncExecution(true))
		defer mgr.Close()
		statusCalls := func() int {
			runner.mu.Lock()
			defer runner.mu.Unlock()
			return runner.statusCalls
		}

		for range 3 {
			if healthy, reason, _ := mgr.ComponentHealth(ctx, ref); !healthy {
				t.Fatalf("ComponentHealth() unhealthy: %s", reason)
			}
		}
		if got := statusCalls(); got != 1 {
			t.Errorf("Status calls within TTL = %d, want 1", got)
		}

		time.Sleep(healthCacheTTL)
		mgr.ComponentHealth(ctx, ref)
		if got := statusCalls(); got != 2 {
			t.Errorf("Status calls after TTL expiry = %d, want 2", got)
		}

		mgr.setUpgradeErr(ref, nil) // Any release operation boundary invalidates the cache.
		mgr.ComponentHealth(ctx, ref)
		if got := statusCalls(); got != 3 {
			t.Errorf("Status calls after invalidation = %d, want 3", got)
		}
	})
}

// TestK8sHelmManager_ColdStartHydration_VerifiesRelease covers the cold-start case where the values ConfigMap and a
// Ready=True CR survive a restart, but the component's Helm release was never successfully installed.
func TestK8sHelmManager_ColdStartHydration_VerifiesRelease(t *testing.T) {
	ref := ComponentRef{Namespace: "xmc", Name: "ingestion-gateway"}
	opts := HelmComponentOptions{Ref: ref, Enabled: true, ImageTag: "v1.0.0"}
	type result struct {
		Active           bool
		Actuated         bool
		StatusCalls      int
		UpgradeTriggered bool
	}
	tests := []struct {
		name               string
		initialUpgradeErr  error
		releaseStatus      release.Status
		statusErr          error
		observedGeneration int64
		crReady            bool
		want               result
	}{
		{
			name:               "deployed release trusts Ready CR without re-upgrade",
			observedGeneration: 3, crReady: true,
			want: result{Active: true, Actuated: true, StatusCalls: 1},
		},
		{
			name:               "missing release is not trusted and next Ensure installs it",
			initialUpgradeErr:  errors.New("install failed"),
			observedGeneration: 3, crReady: true,
			want: result{StatusCalls: 1, UpgradeTriggered: true},
		},
		{
			name:               "failed release is not trusted",
			releaseStatus:      release.StatusFailed,
			observedGeneration: 3, crReady: true,
			want: result{StatusCalls: 1, UpgradeTriggered: true},
		},
		{
			name:               "transient status error trusts Ready CR",
			statusErr:          errors.New("connection refused"),
			observedGeneration: 3, crReady: true,
			want: result{Active: true, Actuated: true, StatusCalls: 1},
		},
		{
			name:               "pending release left by a crashed bridge trusts Ready CR",
			releaseStatus:      release.StatusPendingUpgrade,
			observedGeneration: 3, crReady: true,
			want: result{Active: true, Actuated: true, StatusCalls: 1},
		},
		{
			name:               "unready CR skips release verification",
			observedGeneration: 3, crReady: false,
			want: result{UpgradeTriggered: true},
		},
		{
			name:               "deactivation hydration skips release verification",
			observedGeneration: 0, crReady: true,
			want: result{Active: true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			client := fake.NewClientset()
			runner := &mockHelmActionRunner{upgradeErr: tt.initialUpgradeErr}

			// Simulate the pre-restart controller persisting values (and possibly failing the install).
			mgr1 := NewK8sHelmManager(client, "xmc", "xmanager-cloud", WithActionRunner(runner), WithSyncExecution(true))
			_ = mgr1.Ensure1PComponent(ctx, ref, opts)
			_ = mgr1.Close()

			runner.mu.Lock()
			runner.upgradeErr, runner.releaseStatus, runner.statusErr = nil, tt.releaseStatus, tt.statusErr
			runner.statusCalls = 0
			upgradesBefore := runner.upgradeCalls
			runner.mu.Unlock()

			mgr2 := NewK8sHelmManager(client, "xmc", "xmanager-cloud", WithActionRunner(runner), WithSyncExecution(true))
			defer mgr2.Close()
			if err := mgr2.HydrateComponentState(ctx, ref, tt.observedGeneration, tt.crReady); err != nil {
				t.Fatalf("HydrateComponentState failed: %v", err)
			}
			// Actuation is always checked at the CR's current generation (3), independent of the hydrated observedGeneration.
			got := result{Active: mgr2.IsComponentActive(ref), Actuated: mgr2.IsComponentActuated(ref, 3)}
			if err := mgr2.Ensure1PComponent(ctx, ref, opts); err != nil {
				t.Fatalf("Ensure1PComponent failed: %v", err)
			}
			runner.mu.Lock()
			got.StatusCalls = runner.statusCalls
			got.UpgradeTriggered = runner.upgradeCalls > upgradesBefore
			runner.mu.Unlock()

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("cold-start hydration mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
