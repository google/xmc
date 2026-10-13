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
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestComponentReleaseName(t *testing.T) {
	tests := []struct {
		component string
		release   string
	}{
		{component: "", release: ""},
		{component: "ray-operator", release: "xmc-cmp-ray-operator"},
	}
	for _, tt := range tests {
		if got := ComponentReleaseName(tt.component); got != tt.release {
			t.Errorf("ComponentReleaseName(%q) = %q, want %q", tt.component, got, tt.release)
		}
		if got := componentNameFromRelease(tt.release); got != tt.component {
			t.Errorf("componentNameFromRelease(%q) = %q, want %q", tt.release, got, tt.component)
		}
	}
}

// TestComponentNameFromRelease_UnprefixedRelease documents that a release name without the component prefix is
// returned unchanged. It is one-way: ComponentReleaseName always adds the prefix, so no component maps back to it.
func TestComponentNameFromRelease_UnprefixedRelease(t *testing.T) {
	if got := componentNameFromRelease("custom-release"); got != "custom-release" {
		t.Errorf("componentNameFromRelease(%q) = %q, want %q", "custom-release", got, "custom-release")
	}
}

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

func TestComponentReleaseNameForRef(t *testing.T) {
	tests := []struct {
		name        string
		ref         ComponentRef
		wantRelease string
		wantCanon   string
	}{
		{
			name:        "empty UID preserves canonical release name",
			ref:         ComponentRef{Namespace: "xmc", Name: "ingestion"},
			wantRelease: "xmc-cmp-ingestion",
			wantCanon:   "ingestion",
		},
		{
			name:        "UUID v4 preserves canonical release name for live upgrades and orphan adoption",
			ref:         ComponentRef{Namespace: "xmc", Name: "ray-operator", UID: "12345678-9abc-4def-8000-112233445566"},
			wantRelease: "xmc-cmp-ray-operator",
			wantCanon:   "ray-operator",
		},
		{
			name:        "synthetic UID preserves canonical release name",
			ref:         ComponentRef{Namespace: "xmc", Name: "rod-data-provider", UID: "uid-owner-a"},
			wantRelease: "xmc-cmp-rod-data-provider",
			wantCanon:   "rod-data-provider",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotRelease := ComponentReleaseNameForRef(tc.ref)
			if gotRelease != tc.wantRelease {
				t.Errorf("ComponentReleaseNameForRef(%+v) = %q, want %q", tc.ref, gotRelease, tc.wantRelease)
			}
			gotCanon := componentNameFromRelease(gotRelease)
			if gotCanon != tc.wantCanon {
				t.Errorf("componentNameFromRelease(%q) = %q, want %q", gotRelease, gotCanon, tc.wantCanon)
			}
		})
	}
}

func TestK8sHelmManager_PerCRReleaseIsolation(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()
	runner := &mockHelmActionRunner{}
	mgr := NewK8sHelmManager(client, "xmc", "xmanager-cloud",
		WithActionRunner(runner),
		WithSyncExecution(true),
	)
	defer mgr.Close()

	ownerRef := ComponentRef{Namespace: "ns-a", Name: "ingestion", UID: "11111111-aaaa-4bbb-8ccc-000000000001"}
	dupeRef := ComponentRef{Namespace: "ns-b", Name: "ingestion", UID: "22222222-dddd-4eee-8fff-000000000002"}

	if err := mgr.Ensure1PComponent(ctx, ownerRef, HelmComponentOptions{Ref: ownerRef, Enabled: true}); err != nil {
		t.Fatalf("Ensure1PComponent(owner) failed: %v", err)
	}
	if err := mgr.Ensure1PComponent(ctx, dupeRef, HelmComponentOptions{Ref: dupeRef, Enabled: true}); err != nil {
		t.Fatalf("Ensure1PComponent(dupe) failed: %v", err)
	}

	ownerKey := componentKey(ownerRef.Namespace, ComponentReleaseNameForRef(ownerRef))
	dupeKey := componentKey(dupeRef.Namespace, ComponentReleaseNameForRef(dupeRef))
	if !runner.installed[ownerKey] || !runner.installed[dupeKey] {
		t.Fatalf("expected both %q and %q to be installed", ownerKey, dupeKey)
	}

	// Deleting the cross-namespace duplicate CR uninstalls only dupeKey and preserves ownerKey.
	if err := mgr.Delete1PComponent(ctx, dupeRef); err != nil {
		t.Fatalf("Delete1PComponent(dupe) failed: %v", err)
	}
	if runner.installed[dupeKey] {
		t.Errorf("expected duplicate release %q to be uninstalled", dupeKey)
	}
	if !runner.installed[ownerKey] {
		t.Errorf("expected owner release %q to remain installed when duplicate is deleted", ownerKey)
	}

	// Same-namespace recreate: when newRef is active in ns-a, tearing down superseded ownerRef must not
	// uninstall ns-a/xmc-cmp-ingestion or clobber newRef's active state.
	newRef := ComponentRef{Namespace: "ns-a", Name: "ingestion", UID: "33333333-eeee-4fff-8000-000000000003"}
	if err := mgr.Ensure1PComponent(ctx, newRef, HelmComponentOptions{Ref: newRef, Enabled: true}); err != nil {
		t.Fatalf("Ensure1PComponent(newRef) failed: %v", err)
	}
	mgr.RecordActuation(ownerRef, 1)
	mgr.RecordActuation(newRef, 2)
	if err := mgr.Purge1PComponent(ctx, ownerRef); err != nil {
		t.Fatalf("Purge1PComponent(superseded ownerRef) failed: %v", err)
	}
	if !runner.installed[ownerKey] {
		t.Errorf("expected active release %q to remain installed when superseded incarnation is purged", ownerKey)
	}
	if mgr.IsComponentActuated(ownerRef, 1) {
		t.Errorf("expected superseded ownerRef actuation state to be forgotten")
	}
	if !mgr.IsComponentActuated(newRef, 2) || !mgr.IsComponentActive(newRef) {
		t.Errorf("expected active newRef state to remain intact")
	}

	// Cold-start / fenced-cluster Purge1PComponent (inactive in memory, no other active incarnation) still uninstalls.
	fencedRef := ComponentRef{Namespace: "ns-c", Name: "ingestion", UID: "44444444-0000-4000-8000-000000000004"}
	fencedKey := componentKey(fencedRef.Namespace, ComponentReleaseNameForRef(fencedRef))
	runner.installed[fencedKey] = true
	if err := mgr.Purge1PComponent(ctx, fencedRef); err != nil {
		t.Fatalf("Purge1PComponent(fencedRef) failed: %v", err)
	}
	if runner.installed[fencedKey] {
		t.Errorf("expected cold-start Purge1PComponent to uninstall %q", fencedKey)
	}
}

// TestK8sHelmManager_Delete1PComponent_ColdStartUninstallsLeakedRelease verifies that a namespace-level disabled entry
// hydrated from the values ConfigMap on cold start does not suppress uninstalling a CR incarnation's leaked release,
// while a teardown already recorded for that incarnation keeps later Delete calls idempotent.
func TestK8sHelmManager_Delete1PComponent_ColdStartUninstallsLeakedRelease(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()
	runner := &mockHelmActionRunner{}
	ref := ComponentRef{Namespace: "xmc", Name: "ingestion", UID: "55555555-0000-4000-8000-000000000005"}
	releaseKey := componentKey(ref.Namespace, ComponentReleaseNameForRef(ref))

	first := NewK8sHelmManager(client, "xmc", "xmanager-cloud", WithActionRunner(runner), WithSyncExecution(true))
	if err := first.Ensure1PComponent(ctx, ref, HelmComponentOptions{Ref: ref, Enabled: true}); err != nil {
		t.Fatalf("Ensure1PComponent() failed: %v", err)
	}
	if err := first.Delete1PComponent(ctx, ref); err != nil {
		t.Fatalf("Delete1PComponent() failed: %v", err)
	}
	first.Close()

	// Simulate a release that survived the previous controller's teardown (e.g. an interrupted uninstall).
	runner.installed[releaseKey] = true
	restarted := NewK8sHelmManager(client, "xmc", "xmanager-cloud", WithActionRunner(runner), WithSyncExecution(true))
	defer restarted.Close()
	if err := restarted.HydrateFromCluster(ctx); err != nil {
		t.Fatalf("HydrateFromCluster() failed: %v", err)
	}
	if restarted.IsComponentActive(ref) {
		t.Fatalf("IsComponentActive() = true after hydrating a disabled ConfigMap entry, want false")
	}

	if err := restarted.Delete1PComponent(ctx, ref); err != nil {
		t.Fatalf("Delete1PComponent() after restart failed: %v", err)
	}
	if runner.installed[releaseKey] {
		t.Errorf("expected cold-start Delete1PComponent to uninstall leaked release %q", releaseKey)
	}

	callsBefore := runner.uninstallCalls
	if err := restarted.Delete1PComponent(ctx, ref); err != nil {
		t.Fatalf("repeated Delete1PComponent() failed: %v", err)
	}
	if runner.uninstallCalls != callsBefore {
		t.Errorf("repeated Delete1PComponent() uninstall calls = %d, want %d", runner.uninstallCalls, callsBefore)
	}
}

// TestK8sHelmManager_EnsureRetiresStaleIncarnation verifies that actuating a new CR incarnation drops the state of an
// older incarnation whose teardown never ran, so the stale entry cannot suppress the new incarnation's teardown.
func TestK8sHelmManager_EnsureRetiresStaleIncarnation(t *testing.T) {
	ctx := context.Background()
	runner := &mockHelmActionRunner{}
	mgr := NewK8sHelmManager(fake.NewClientset(), "xmc", "xmanager-cloud", WithActionRunner(runner), WithSyncExecution(true))
	defer mgr.Close()
	staleRef := ComponentRef{Namespace: "xmc", Name: "ingestion", UID: "66666666-0000-4000-8000-000000000006"}
	liveRef := ComponentRef{Namespace: "xmc", Name: "ingestion", UID: "77777777-0000-4000-8000-000000000007"}
	releaseKey := componentKey(liveRef.Namespace, ComponentReleaseNameForRef(liveRef))

	if err := mgr.Ensure1PComponent(ctx, staleRef, HelmComponentOptions{Ref: staleRef, Enabled: true}); err != nil {
		t.Fatalf("Ensure1PComponent(staleRef) failed: %v", err)
	}
	mgr.RecordActuation(staleRef, 1)
	// staleRef's CR is gone without a teardown; its recreation liveRef is actuated next.
	if err := mgr.Ensure1PComponent(ctx, liveRef, HelmComponentOptions{Ref: liveRef, Enabled: true}); err != nil {
		t.Fatalf("Ensure1PComponent(liveRef) failed: %v", err)
	}
	if mgr.IsComponentActuated(staleRef, 1) {
		t.Errorf("IsComponentActuated(staleRef) = true after liveRef was actuated, want false")
	}

	if err := mgr.Delete1PComponent(ctx, liveRef); err != nil {
		t.Fatalf("Delete1PComponent(liveRef) failed: %v", err)
	}
	if runner.installed[releaseKey] {
		t.Errorf("expected Delete1PComponent(liveRef) to uninstall %q despite the stale incarnation", releaseKey)
	}
}
