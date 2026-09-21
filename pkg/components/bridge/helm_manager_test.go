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
	"testing/synctest"
	"time"

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
	mu              sync.Mutex
	lastRelease     string
	lastNS          string
	lastChart       *chart.Chart
	lastValues      map[string]any
	upgradeErr      error
	upgradeCalls    int
	uninstallCalls  int
	uninstallErr    error
	lastUninstall   string
	lastUninstallNS string
	panicOnUpgrade  bool
}

func (m *mockHelmActionRunner) Upgrade(ctx context.Context, releaseName, namespace string, chartObj *chart.Chart, values map[string]any) (*release.Release, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.panicOnUpgrade {
		panic("simulated runner panic")
	}
	m.upgradeCalls++
	m.lastRelease = releaseName
	m.lastNS = namespace
	m.lastChart = chartObj
	m.lastValues = values
	if m.upgradeErr != nil {
		return nil, m.upgradeErr
	}
	return &release.Release{Name: releaseName, Namespace: namespace}, nil
}

func (m *mockHelmActionRunner) Uninstall(ctx context.Context, releaseName, namespace string) (*release.UninstallReleaseResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.uninstallCalls++
	m.lastUninstall = releaseName
	m.lastUninstallNS = namespace
	if m.uninstallErr != nil {
		return nil, m.uninstallErr
	}
	return &release.UninstallReleaseResponse{Release: &release.Release{Name: releaseName, Namespace: namespace}}, nil
}

func TestK8sHelmManager_SyncUpgrade(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()
	runner := &mockHelmActionRunner{}
	mgr := NewK8sHelmManager(client, "xmc", "xmanager-cloud",
		WithActionRunner(runner),
		WithSyncExecution(true),
		WithChartPath("deploy/helm"),
	)
	defer mgr.Close()

	ref := ComponentRef{Namespace: "xmc", Name: "rod-data-provider"}
	opts := HelmComponentOptions{
		Ref:      ref,
		Enabled:  true,
		ImageTag: "v1.2.3",
	}

	// 1. Ensure component triggers synchronous upgrade with per-component release name
	if err := mgr.Ensure1PComponent(ctx, ref, opts); err != nil {
		t.Fatalf("Ensure1PComponent failed: %v", err)
	}

	expectedRelease := ComponentReleaseName("rod-data-provider")
	runner.mu.Lock()
	if runner.upgradeCalls != 1 {
		t.Errorf("expected 1 upgrade call, got %d", runner.upgradeCalls)
	}
	if runner.lastRelease != expectedRelease {
		t.Errorf("expected release %q, got %q", expectedRelease, runner.lastRelease)
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

	// 2. Delete component triggers uninstallation of component release
	if err := mgr.Delete1PComponent(ctx, ref); err != nil {
		t.Fatalf("Delete1PComponent failed: %v", err)
	}

	runner.mu.Lock()
	if runner.uninstallCalls != 1 {
		t.Errorf("expected 1 uninstall call, got %d", runner.uninstallCalls)
	}
	if runner.lastUninstall != expectedRelease {
		t.Errorf("expected uninstall release %q, got %q", expectedRelease, runner.lastUninstall)
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

func TestK8sHelmManager_ConfigMapDeclarativeSourceOfTruth(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()
	runner := &mockHelmActionRunner{}
	mgr := NewK8sHelmManager(client, "xmc", "xmanager-cloud",
		WithActionRunner(runner),
		WithSyncExecution(true),
	)
	defer mgr.Close()

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

	// Verify ConfigMap persists both components declaratively
	cm, err := client.CoreV1().ConfigMaps("xmc").Get(ctx, ComponentValuesConfigMapName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("failed to get ConfigMap: %v", err)
	}
	if _, ok := cm.Data["rod-data-provider.json"]; !ok {
		t.Error("expected rod-data-provider.json in ConfigMap")
	}
	if _, ok := cm.Data["ingestion-gateway.json"]; !ok {
		t.Error("expected ingestion-gateway.json in ConfigMap")
	}

	// Verify that component B was actuated under its own isolated release
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if runner.upgradeCalls != 2 {
		t.Fatalf("expected 2 upgrade calls, got %d", runner.upgradeCalls)
	}
	expectedReleaseB := ComponentReleaseName("ingestion-gateway")
	if runner.lastRelease != expectedReleaseB {
		t.Errorf("expected last release %q, got %q", expectedReleaseB, runner.lastRelease)
	}
	_, hasIngestion := runner.lastValues["ingestionGatewayDeployment"]
	if !hasIngestion {
		t.Errorf("expected ingestionGatewayDeployment in upgrade values, got keys: %+v", runner.lastValues)
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

func TestK8sHelmManager_AsyncUpgrade(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()
	runner := &mockHelmActionRunner{}
	mgr := NewK8sHelmManager(client, "xmc", "xmanager-cloud",
		WithActionRunner(runner),
		WithSyncExecution(false),
	)
	defer mgr.Close()

	ref := ComponentRef{Namespace: "xmc", Name: "health-service"}
	opts := HelmComponentOptions{
		Ref:     ref,
		Enabled: true,
	}

	if err := mgr.Ensure1PComponent(ctx, ref, opts); err != nil {
		t.Fatalf("Ensure1PComponent async failed: %v", err)
	}

	// Wait up to 1 second for background worker execution
	var calls int
	for i := 0; i < 50; i++ {
		time.Sleep(10 * time.Millisecond)
		runner.mu.Lock()
		calls = runner.upgradeCalls
		runner.mu.Unlock()
		if calls > 0 {
			break
		}
	}
	if calls == 0 {
		t.Error("expected background worker to execute upgrade")
	}

	if err := mgr.GetUpgradeError(ref); err != nil {
		t.Errorf("expected nil upgrade error on success, got %v", err)
	}
}

func TestK8sHelmManager_AsyncUpgrade_Failure(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()
	runner := &mockHelmActionRunner{
		upgradeErr: errors.New("async upgrade network error"),
	}
	mgr := NewK8sHelmManager(client, "xmc", "xmanager-cloud",
		WithActionRunner(runner),
		WithSyncExecution(false),
	)
	defer mgr.Close()

	ref := ComponentRef{Namespace: "xmc", Name: "health-service"}
	opts := HelmComponentOptions{
		Ref:     ref,
		Enabled: true,
	}

	if err := mgr.Ensure1PComponent(ctx, ref, opts); err != nil {
		t.Fatalf("unexpected synchronous error on async Ensure1PComponent: %v", err)
	}

	// Wait up to 1 second for background worker failure to record
	var recordedErr error
	for i := 0; i < 50; i++ {
		time.Sleep(10 * time.Millisecond)
		recordedErr = mgr.GetUpgradeError(ref)
		if recordedErr != nil {
			break
		}
	}
	if recordedErr == nil || !errors.Is(recordedErr, runner.upgradeErr) {
		t.Errorf("expected recorded async error %v, got %v", runner.upgradeErr, recordedErr)
	}
}

func TestK8sHelmManager_Close(t *testing.T) {
	client := fake.NewClientset()
	runner := &mockHelmActionRunner{}
	mgr := NewK8sHelmManager(client, "xmc", "xmanager-cloud",
		WithActionRunner(runner),
		WithSyncExecution(false),
	)

	// Idempotent Close
	if err := mgr.Close(); err != nil {
		t.Fatalf("first Close failed: %v", err)
	}
	if err := mgr.Close(); err != nil {
		t.Fatalf("second Close failed: %v", err)
	}

	// Triggering upgrade after close should return error
	ref := ComponentRef{Namespace: "xmc", Name: "test-service"}
	opts := HelmComponentOptions{Ref: ref, Enabled: true}
	err := mgr.Ensure1PComponent(context.Background(), ref, opts)
	if err == nil || !strings.Contains(err.Error(), "helm manager is stopped") {
		t.Errorf("expected stopped manager error, got %v", err)
	}
}

func TestK8sHelmManager_CloseDrainsPendingUpgrades(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()
	runner := &mockHelmActionRunner{}
	mgr := NewK8sHelmManager(client, "xmc", "xmanager-cloud",
		WithActionRunner(runner),
		WithSyncExecution(false),
		WithDebounceDuration(100*time.Millisecond),
	)

	ref := ComponentRef{Namespace: "xmc", Name: "drained-service"}
	opts := HelmComponentOptions{Ref: ref, Enabled: true}
	if err := mgr.Ensure1PComponent(ctx, ref, opts); err != nil {
		t.Fatalf("Ensure1PComponent failed: %v", err)
	}

	// Immediate Close should drain pending upgrades without context.Canceled error
	if err := mgr.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	if err := mgr.GetUpgradeError(ref); err != nil {
		t.Errorf("expected no upgrade error after drainage, got %v", err)
	}
	if runner.upgradeCalls != 1 {
		t.Errorf("expected 1 upgrade call after drainage, got %d", runner.upgradeCalls)
	}
}

func TestK8sHelmManager_DebouncedBatchUpgrade(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		client := fake.NewClientset()
		runner := &mockHelmActionRunner{}
		mgr := NewK8sHelmManager(client, "xmc", "xmanager-cloud",
			WithActionRunner(runner),
			WithSyncExecution(false),
			WithDebounceDuration(30*time.Millisecond),
		)
		defer mgr.Close()

		refA := ComponentRef{Namespace: "xmc", Name: "component-a"}
		optsA1 := HelmComponentOptions{Ref: refA, Enabled: true, ImageTag: "v1"}
		optsA2 := HelmComponentOptions{Ref: refA, Enabled: true, ImageTag: "v2"}

		// Trigger rapid updates for component A within 30ms window
		if err := mgr.Ensure1PComponent(ctx, refA, optsA1); err != nil {
			t.Fatalf("Ensure1PComponent A1 failed: %v", err)
		}
		if err := mgr.Ensure1PComponent(ctx, refA, optsA2); err != nil {
			t.Fatalf("Ensure1PComponent A2 failed: %v", err)
		}

		// Advance virtual time past the debounce duration
		time.Sleep(100 * time.Millisecond)

		runner.mu.Lock()
		calls := runner.upgradeCalls
		runner.mu.Unlock()

		if calls != 1 {
			t.Errorf("expected 1 debounced upgrade call for component-a, got %d", calls)
		}
		if err := mgr.GetUpgradeError(refA); err != nil {
			t.Errorf("expected nil error for refA, got %v", err)
		}
	})
}

type panicHelmActionRunner struct {
	mockHelmActionRunner
}

func (p *panicHelmActionRunner) Upgrade(ctx context.Context, releaseName, namespace string, chartObj *chart.Chart, values map[string]any) (*release.Release, error) {
	panic("simulated upgrade panic")
}

func TestK8sHelmManager_ExecuteBatch_UpgradePanicUnlock(t *testing.T) {
	client := fake.NewClientset()
	runner := &panicHelmActionRunner{}
	mgr := NewK8sHelmManager(client, "xmc", "xmanager-cloud",
		WithActionRunner(runner),
		WithSyncExecution(true),
	)
	defer mgr.Close()

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic from panicHelmActionRunner")
		}
		// Verify m.upgradeMu is unlocked by successfully acquiring it with TryLock
		if !mgr.upgradeMu.TryLock() {
			t.Error("expected upgradeMu to be unlocked after panic")
		} else {
			mgr.upgradeMu.Unlock()
		}
	}()

	mgr.executeComponentUpgrade(upgradeRequest{
		ref:       ComponentRef{Namespace: "xmc", Name: "panic-comp"},
		namespace: "xmc",
		values:    map[string]any{"key": "val"},
	})
}

func TestK8sHelmManager_Close_SynchronizesWithTriggerUpgrade(t *testing.T) {
	client := fake.NewClientset()
	runner := &mockHelmActionRunner{}
	mgr := NewK8sHelmManager(client, "xmc", "xmanager-cloud",
		WithActionRunner(runner),
		WithSyncExecution(false),
		WithDebounceDuration(10*time.Millisecond),
	)

	var wg sync.WaitGroup
	ctx := context.Background()

	// Launch concurrent triggerUpgrade calls
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ref := ComponentRef{Namespace: "xmc", Name: "comp"}
			_ = mgr.triggerUpgrade(ctx, ref, map[string]any{"val": "test"})
		}()
	}

	// Close concurrently
	_ = mgr.Close()
	wg.Wait()

	// Any subsequent triggerUpgrade must return stopped error
	ref := ComponentRef{Namespace: "xmc", Name: "comp-after-close"}
	err := mgr.triggerUpgrade(ctx, ref, map[string]any{"val": 999})
	if err == nil {
		t.Error("expected error from triggerUpgrade after Close, got nil")
	}
}

func TestK8sHelmManager_TriggerUpgrade_ChannelSaturation_NoDeadlock(t *testing.T) {
	client := fake.NewClientset()
	runner := &mockHelmActionRunner{}
	mgr := NewK8sHelmManager(client, "xmc", "xmanager-cloud",
		WithActionRunner(runner),
		WithSyncExecution(false),
		WithDebounceDuration(5*time.Millisecond),
	)
	defer mgr.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Launch 150 concurrent triggerUpgrade calls exceeding channel buffer size (100)
	var wg sync.WaitGroup
	for i := 0; i < 150; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ref := ComponentRef{Namespace: "xmc", Name: "comp"}
			_ = mgr.triggerUpgrade(ctx, ref, map[string]any{"val": "saturated"})
		}()
	}

	doneCh := make(chan struct{})
	go func() {
		wg.Wait()
		close(doneCh)
	}()

	select {
	case <-doneCh:
		// Success, no deadlock occurred
	case <-ctx.Done():
		t.Fatal("deadlock detected: triggerUpgrade calls did not complete within timeout")
	}
}

func TestK8sHelmManager_DrainRemainingUpgrades_ClosedChannel(t *testing.T) {
	client := fake.NewClientset()
	runner := &mockHelmActionRunner{}
	mgr := NewK8sHelmManager(client, "xmc", "xmanager-cloud",
		WithActionRunner(runner),
		WithSyncExecution(true),
	)
	defer mgr.Close()

	// Explicitly close upgradeChan
	close(mgr.upgradeChan)

	// drainRemainingUpgrades must safely terminate and not loop indefinitely
	doneCh := make(chan struct{})
	go func() {
		mgr.drainRemainingUpgrades()
		close(doneCh)
	}()

	select {
	case <-doneCh:
		// Success, terminated cleanly on closed channel
	case <-time.After(1 * time.Second):
		t.Fatal("drainRemainingUpgrades hung indefinitely on closed channel")
	}
}

func TestK8sHelmManager_SyncExecution_StoppedManagerReturnsError(t *testing.T) {
	client := fake.NewClientset()
	runner := &mockHelmActionRunner{}
	mgr := NewK8sHelmManager(client, "xmc", "xmanager-cloud",
		WithActionRunner(runner),
		WithSyncExecution(true),
	)

	if err := mgr.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	ref := ComponentRef{Namespace: "xmc", Name: "sync-service"}
	opts := HelmComponentOptions{Ref: ref, Enabled: true}
	err := mgr.Ensure1PComponent(context.Background(), ref, opts)
	if err == nil || !strings.Contains(err.Error(), "helm manager is stopped") {
		t.Errorf("expected stopped manager error, got %v", err)
	}
}

func TestK8sHelmManager_AsyncUpgrade_DoesNotClearErrorOnEnqueue(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		client := fake.NewClientset()
		runner := &mockHelmActionRunner{}
		mgr := NewK8sHelmManager(client, "xmc", "xmanager-cloud",
			WithActionRunner(runner),
			WithSyncExecution(false),
			WithDebounceDuration(30*time.Millisecond),
		)
		defer mgr.Close()

		ref := ComponentRef{Namespace: "xmc", Name: "test-comp"}
		expectedErr := errors.New("previous upgrade failed")
		mgr.setUpgradeErr(ref, expectedErr)

		opts := HelmComponentOptions{Ref: ref, Enabled: true}
		if err := mgr.Ensure1PComponent(ctx, ref, opts); err != nil {
			t.Fatalf("Ensure1PComponent failed: %v", err)
		}

		// Verify error is NOT cleared on enqueue
		if gotErr := mgr.GetUpgradeError(ref); gotErr == nil || gotErr.Error() != expectedErr.Error() {
			t.Fatalf("expected error to remain %v immediately after enqueue, got %v", expectedErr, gotErr)
		}

		// Advance virtual time so worker processes batch and succeeds
		time.Sleep(100 * time.Millisecond)

		// Error should now be cleared by the successful upgrade
		if gotErr := mgr.GetUpgradeError(ref); gotErr != nil {
			t.Errorf("expected error cleared after worker execution, got %v", gotErr)
		}
	})
}

func TestK8sHelmManager_WorkerRecoversPanic(t *testing.T) {
	client := fake.NewClientset()
	runner := &mockHelmActionRunner{
		panicOnUpgrade: true,
	}
	mgr := NewK8sHelmManager(client, "xmc", "xmanager-cloud",
		WithActionRunner(runner),
		WithSyncExecution(false),
		WithDebounceDuration(10*time.Millisecond),
	)

	ref := ComponentRef{Namespace: "xmc", Name: "panicking-service"}
	opts := HelmComponentOptions{Ref: ref, Enabled: true}
	if err := mgr.Ensure1PComponent(context.Background(), ref, opts); err != nil {
		t.Fatalf("Ensure1PComponent failed: %v", err)
	}

	// Close waits for worker to exit via wg.Wait(). If panic wasn't recovered, Close or test would crash.
	if err := mgr.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
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
	mgr := NewK8sHelmManager(client, "xmc", "xmanager-cloud", WithActionRunner(runner))

	ref := ComponentRef{Namespace: "xmc", Name: ""}
	err := mgr.triggerUpgrade(ctx, ref, nil)
	if err == nil {
		t.Fatal("expected error on triggerUpgrade with empty component name, got nil")
	}
	if !strings.Contains(err.Error(), "helm release name must not be empty") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestK8sHelmManager_TriggerUpgrade_ContextCancellation_DuringLockAcquisition(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()
	runner := &mockHelmActionRunner{}
	mgr := NewK8sHelmManager(client, "xmc", "xmanager-cloud", WithActionRunner(runner), WithSyncExecution(true))

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

type panickingHelmActionRunner struct {
	mockHelmActionRunner
	panicOnUninstall bool
}

func (p *panickingHelmActionRunner) Uninstall(ctx context.Context, releaseName, namespace string) (*release.UninstallReleaseResponse, error) {
	if p.panicOnUninstall {
		panic("simulated uninstall panic")
	}
	return p.mockHelmActionRunner.Uninstall(ctx, releaseName, namespace)
}

func TestK8sHelmManager_UninstallRelease_NilRunner(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()
	// Manager without runner
	mgr := NewK8sHelmManager(client, "xmc", "xmanager-cloud")

	ref := ComponentRef{Namespace: "xmc", Name: "rod-data-provider"}
	// Delete1PComponent and Purge1PComponent should skip uninstallation safely without error
	if err := mgr.Delete1PComponent(ctx, ref); err != nil {
		t.Fatalf("expected Delete1PComponent with nil runner to succeed, got %v", err)
	}
	if err := mgr.Purge1PComponent(ctx, ref); err != nil {
		t.Fatalf("expected Purge1PComponent with nil runner to succeed, got %v", err)
	}
}

func TestK8sHelmManager_UninstallRelease_PanicSafeLockDefer(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()
	panickingRunner := &panickingHelmActionRunner{panicOnUninstall: true}
	mgr := NewK8sHelmManager(client, "xmc", "xmanager-cloud", WithActionRunner(panickingRunner))

	ref := ComponentRef{Namespace: "xmc", Name: "rod-data-provider"}

	// Verify that panic in runner.Uninstall is caught and lock is still released
	func() {
		defer func() {
			r := recover()
			if r == nil {
				t.Fatal("expected panic from panicking runner, got nil")
			}
		}()
		_ = mgr.Delete1PComponent(ctx, ref)
	}()

	// Since defer m.releaseUpgradeLock() ran during panic unwind, subsequent lock acquisition must not hang
	panickingRunner.panicOnUninstall = false
	if err := mgr.Delete1PComponent(ctx, ref); err != nil {
		t.Fatalf("expected Delete1PComponent to succeed after recovered panic, got: %v", err)
	}
}

func TestResolveComponentChartPath(t *testing.T) {
	baseDir := t.TempDir()
	compSubDir := filepath.Join(baseDir, "components", "ingestion-gateway")
	if err := os.MkdirAll(compSubDir, 0755); err != nil {
		t.Fatalf("failed to create component subdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(compSubDir, "Chart.yaml"), []byte("apiVersion: v2\nname: ingestion-gateway\nversion: 0.1.0\n"), 0644); err != nil {
		t.Fatalf("failed to write Chart.yaml: %v", err)
	}

	directSubDir := filepath.Join(baseDir, "rod-data-provider")
	if err := os.MkdirAll(directSubDir, 0755); err != nil {
		t.Fatalf("failed to create direct subdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directSubDir, "Chart.yaml"), []byte("apiVersion: v2\nname: rod-data-provider\nversion: 0.1.0\n"), 0644); err != nil {
		t.Fatalf("failed to write Chart.yaml: %v", err)
	}

	tgzPath := filepath.Join(baseDir, "health-service.tgz")
	if err := os.WriteFile(tgzPath, []byte("dummy-tgz"), 0644); err != nil {
		t.Fatalf("failed to write tgz file: %v", err)
	}

	tests := []struct {
		name         string
		basePath     string
		ref          ComponentRef
		overridePath string
		wantPath     string
	}{
		{
			name:         "ExplicitOverridePrecedence",
			basePath:     baseDir,
			ref:          ComponentRef{Namespace: "xmc", Name: "ingestion-gateway"},
			overridePath: "/custom/override/chart",
			wantPath:     "/custom/override/chart",
		},
		{
			name:     "ComponentsSubdirectoryResolution",
			basePath: baseDir,
			ref:      ComponentRef{Namespace: "xmc", Name: "ingestion-gateway"},
			wantPath: compSubDir,
		},
		{
			name:     "DirectSubdirectoryResolution",
			basePath: baseDir,
			ref:      ComponentRef{Namespace: "xmc", Name: "rod-data-provider"},
			wantPath: directSubDir,
		},
		{
			name:     "PackagedArchiveResolution",
			basePath: baseDir,
			ref:      ComponentRef{Namespace: "xmc", Name: "health-service"},
			wantPath: tgzPath,
		},
		{
			name:     "UmbrellaChartFallback",
			basePath: baseDir,
			ref:      ComponentRef{Namespace: "xmc", Name: "unknown-component"},
			wantPath: baseDir,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveComponentChartPath(tc.basePath, tc.ref, tc.overridePath)
			if got != tc.wantPath {
				t.Errorf("ResolveComponentChartPath() = %q, want %q", got, tc.wantPath)
			}
		})
	}
}

func TestK8sHelmManager_PerComponentChartLoading(t *testing.T) {
	ctx := context.Background()
	baseDir := t.TempDir()
	compChartDir := filepath.Join(baseDir, "components", "ingestion-gateway")
	if err := os.MkdirAll(compChartDir, 0755); err != nil {
		t.Fatalf("failed to create component chart dir: %v", err)
	}
	chartYAML := "apiVersion: v2\nname: ingestion-gateway-chart\nversion: 1.2.3\n"
	if err := os.WriteFile(filepath.Join(compChartDir, "Chart.yaml"), []byte(chartYAML), 0644); err != nil {
		t.Fatalf("failed to write Chart.yaml: %v", err)
	}

	client := fake.NewClientset()
	runner := &mockHelmActionRunner{}
	mgr := NewK8sHelmManager(
		client,
		"xmc",
		"xmanager-cloud",
		WithActionRunner(runner),
		WithChartPath(baseDir),
		WithSyncExecution(true),
	)
	defer mgr.Close()

	ref := ComponentRef{Namespace: "xmc", Name: "ingestion-gateway"}
	if err := mgr.Ensure1PComponent(ctx, ref, HelmComponentOptions{Ref: ref, Enabled: true}); err != nil {
		t.Fatalf("Ensure1PComponent failed: %v", err)
	}

	runner.mu.Lock()
	loadedChart := runner.lastChart
	runner.mu.Unlock()

	if loadedChart == nil || loadedChart.Metadata == nil || loadedChart.Metadata.Name != "ingestion-gateway-chart" {
		t.Fatalf("expected per-component chart 'ingestion-gateway-chart' to be loaded and passed to runner, got %+v", loadedChart)
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
