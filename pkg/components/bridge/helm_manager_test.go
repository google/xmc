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
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/release"
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

type mockHelmActionRunner struct {
	mu           sync.Mutex
	lastRelease  string
	lastNS       string
	lastValues   map[string]any
	upgradeErr   error
	upgradeCalls int
}

func (m *mockHelmActionRunner) Upgrade(ctx context.Context, releaseName, namespace string, chartObj *chart.Chart, values map[string]any) (*release.Release, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.upgradeCalls++
	m.lastRelease = releaseName
	m.lastNS = namespace
	m.lastValues = values
	if m.upgradeErr != nil {
		return nil, m.upgradeErr
	}
	return &release.Release{Name: releaseName, Namespace: namespace}, nil
}

func TestK8sHelmManager_SyncUpgrade(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()
	runner := &mockHelmActionRunner{}
	mgr := NewK8sHelmManager(client, "xmc", "xmanager-cloud",
		WithActionRunner(runner),
		WithChartPath("deploy/helm"),
	)

	ref := ComponentRef{Namespace: "xmc", Name: "rod-data-provider"}
	opts := HelmComponentOptions{
		Ref:      ref,
		Enabled:  true,
		ImageTag: "v1.2.3",
	}

	// 1. Ensure component triggers synchronous upgrade
	if err := mgr.Ensure1PComponent(ctx, ref, opts); err != nil {
		t.Fatalf("Ensure1PComponent failed: %v", err)
	}

	runner.mu.Lock()
	if runner.upgradeCalls != 1 {
		t.Errorf("expected 1 upgrade call, got %d", runner.upgradeCalls)
	}
	if runner.lastRelease != "xmanager-cloud" {
		t.Errorf("expected release xmanager-cloud, got %q", runner.lastRelease)
	}
	if runner.lastNS != "xmc" {
		t.Errorf("expected namespace xmc, got %q", runner.lastNS)
	}
	compMap, ok := runner.lastValues["rodDeployment"].(map[string]any)
	enabled, _ := compMap["enabled"].(bool)
	if !ok || !enabled {
		t.Errorf("expected rodDeployment.enabled=true in upgrade values, got %v", runner.lastValues)
	}
	runner.mu.Unlock()

	// 2. Delete component triggers upgrade with enabled=false
	if err := mgr.Delete1PComponent(ctx, ref); err != nil {
		t.Fatalf("Delete1PComponent failed: %v", err)
	}

	runner.mu.Lock()
	if runner.upgradeCalls != 2 {
		t.Errorf("expected 2 upgrade calls, got %d", runner.upgradeCalls)
	}
	compMapDel, ok := runner.lastValues["rodDeployment"].(map[string]any)
	enabledDel, _ := compMapDel["enabled"].(bool)
	if !ok || enabledDel {
		t.Errorf("expected rodDeployment.enabled=false on delete, got %v", runner.lastValues)
	}
	runner.mu.Unlock()

	// 3. Error propagation during upgrade
	runner.mu.Lock()
	runner.upgradeErr = errors.New("simulated helm failure")
	runner.mu.Unlock()

	err := mgr.Ensure1PComponent(ctx, ref, opts)
	if err == nil || !errors.Is(err, runner.upgradeErr) {
		t.Errorf("expected simulated helm failure error, got %v", err)
	}
	if gotErr := mgr.GetUpgradeError(ref); gotErr == nil {
		t.Error("expected GetUpgradeError to return non-nil error")
	}
}

func TestK8sHelmManager_OptionsAndGuards(t *testing.T) {
	var nilMgr *K8sHelmManager
	ref := ComponentRef{Namespace: "xmc", Name: "test"}
	if err := nilMgr.GetUpgradeError(ref); err != nil {
		t.Errorf("expected nil error on nil receiver GetUpgradeError, got %v", err)
	}

	mgr := NewK8sHelmManager(nil, "xmc", "xmanager-cloud",
		WithKubeconfig("/tmp/nonexistent-kubeconfig"),
		WithChartPath("deploy/helm"),
	)
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
	if mgrGetter.runner == nil {
		t.Error("expected runner to be initialized by WithRESTClientGetter")
	}
	if sdkRunner, ok := mgrGetter.runner.(*SDKHelmActionRunner); !ok || sdkRunner.chartPath != "deploy/helm/getter" {
		t.Errorf("expected SDKHelmActionRunner with chartPath deploy/helm/getter, got %+v", mgrGetter.runner)
	}

	if err := mgr.GetUpgradeError(ComponentRef{}); err == nil {
		t.Error("expected error on empty componentName in GetUpgradeError")
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
	)

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
	if gotErr := mgr.GetUpgradeError(ref); gotErr == nil {
		t.Fatalf("expected recorded upgrade error, got nil")
	}

	// On second reconciliation, ConfigMap is not modified, but previous upgrade failed.
	// Ensure1PComponent must retry rather than exiting early.
	runner.upgradeErr = nil
	if err := mgr.Ensure1PComponent(ctx, ref, opts); err != nil {
		t.Fatalf("expected retry Ensure1PComponent to succeed, got %v", err)
	}
	if gotErr := mgr.GetUpgradeError(ref); gotErr != nil {
		t.Errorf("expected upgrade error cleared after successful retry, got %v", gotErr)
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
	)

	ref := ComponentRef{Namespace: "xmc", Name: "metrics"}
	opts := HelmComponentOptions{
		Ref:     ref,
		Enabled: true,
	}
	if err := mgr.Ensure1PComponent(ctx, ref, opts); err != nil {
		t.Fatalf("unexpected Ensure1PComponent error: %v", err)
	}

	// Inject error for delete upgrade
	runner.upgradeErr = errors.New("deletion lock error")
	err := mgr.Delete1PComponent(ctx, ref)
	if err == nil || !strings.Contains(err.Error(), "deletion lock error") {
		t.Fatalf("expected deletion upgrade error, got %v", err)
	}
	if gotErr := mgr.GetUpgradeError(ref); gotErr == nil {
		t.Fatalf("expected recorded upgrade error on delete, got nil")
	}

	// On second reconciliation, key is already deleted from ConfigMap, but previous upgrade failed.
	// Delete1PComponent must retry.
	runner.upgradeErr = nil
	if err := mgr.Delete1PComponent(ctx, ref); err != nil {
		t.Fatalf("expected retry Delete1PComponent to succeed, got %v", err)
	}
	if gotErr := mgr.GetUpgradeError(ref); gotErr != nil {
		t.Errorf("expected upgrade error cleared after successful delete retry, got %v", gotErr)
	}
}

func TestK8sHelmManager_ConfigMapDeclarativeSourceOfTruth(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()
	runner := &mockHelmActionRunner{}
	mgr := NewK8sHelmManager(client, "xmc", "xmanager-cloud",
		WithActionRunner(runner),
	)

	refA := ComponentRef{Namespace: "xmc", Name: "rod-data-provider"}
	optsA := HelmComponentOptions{Ref: refA, Enabled: true}
	if err := mgr.Ensure1PComponent(ctx, refA, optsA); err != nil {
		t.Fatalf("Ensure1PComponent A failed: %v", err)
	}

	refB := ComponentRef{Namespace: "xmc", Name: "ingestion-gateway"}
	optsB := HelmComponentOptions{Ref: refB, Enabled: true}
	if err := mgr.Ensure1PComponent(ctx, refB, optsB); err != nil {
		t.Fatalf("Ensure1PComponent B failed: %v", err)
	}

	// Verify that the second upgrade call for component B included component A's values from ConfigMap
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if runner.upgradeCalls != 2 {
		t.Fatalf("expected 2 upgrade calls, got %d", runner.upgradeCalls)
	}
	_, hasRod := runner.lastValues["rodDeployment"]
	_, hasIngestion := runner.lastValues["ingestionGatewayDeployment"]
	if !hasRod || !hasIngestion {
		t.Errorf("expected both rodDeployment and ingestionGatewayDeployment in merged upgrade values, got keys: %+v", runner.lastValues)
	}
}

func TestK8sHelmManager_Close(t *testing.T) {
	mgr := NewK8sHelmManager(fake.NewClientset(), "xmc", "xmanager-cloud")
	if err := mgr.Close(); err != nil {
		t.Fatalf("expected nil error on Close, got: %v", err)
	}
}

func TestK8sHelmManager_TriggerUpgrade_UninitializedRunner_ReturnsError(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()
	mgr := NewK8sHelmManager(client, "xmc", "xmanager-cloud")

	ref := ComponentRef{Namespace: "xmc", Name: "ingestion-gateway"}
	opts := HelmComponentOptions{Ref: ref, Enabled: true}

	err := mgr.Ensure1PComponent(ctx, ref, opts)
	if err == nil {
		t.Fatal("expected error on Ensure1PComponent with uninitialized runner, got nil")
	}
	if !strings.Contains(err.Error(), "helm action runner is uninitialized") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestK8sHelmManager_TriggerUpgrade_EmptyReleaseName_ReturnsError(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()
	runner := &mockHelmActionRunner{}
	mgr := NewK8sHelmManager(client, "xmc", "", WithActionRunner(runner))

	ref := ComponentRef{Namespace: "xmc", Name: "ingestion-gateway"}
	opts := HelmComponentOptions{Ref: ref, Enabled: true}

	err := mgr.Ensure1PComponent(ctx, ref, opts)
	if err == nil {
		t.Fatal("expected error on Ensure1PComponent with empty releaseName, got nil")
	}
	if !strings.Contains(err.Error(), "helm release name must not be empty") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestK8sHelmManager_TriggerUpgrade_ContextCancellation_DuringLockAcquisition(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()
	runner := &mockHelmActionRunner{}
	mgr := NewK8sHelmManager(client, "xmc", "xmanager-cloud", WithActionRunner(runner))

	if err := mgr.acquireUpgradeLock(ctx); err != nil {
		t.Fatalf("failed to acquire initial upgrade lock: %v", err)
	}

	cancelCtx, cancel := context.WithCancel(ctx)
	cancel()

	ref := ComponentRef{Namespace: "xmc", Name: "rod-data-provider"}
	err := mgr.triggerUpgrade(cancelCtx, ref, map[string]any{"foo": "bar"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled when acquiring lock with canceled context, got: %v", err)
	}

	mgr.releaseUpgradeLock()

	if err := mgr.triggerUpgrade(ctx, ref, map[string]any{"foo": "bar"}); err != nil {
		t.Fatalf("expected upgrade to succeed after releasing lock, got: %v", err)
	}
}
