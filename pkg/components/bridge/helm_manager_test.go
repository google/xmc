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
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestK8sHelmManager_Lifecycle(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()
	mgr := NewK8sHelmManager(client, "xmc", "xmanager-cloud")

	opts := HelmComponentOptions{
		ComponentName: "ingestion-gateway",
		Namespace:     "xmc",
		Enabled:       true,
		ImageTag:      "v1.0.0",
		Parameters: map[string]string{
			"minPartitions": "128",
		},
	}

	// 1. Ensure 1P component
	if err := mgr.Ensure1PComponent(ctx, "xmc", "ingestion-gateway", opts); err != nil {
		t.Fatalf("Ensure1PComponent failed: %v", err)
	}

	if !mgr.IsComponentActive("ingestion-gateway") {
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
	if !ok || igMap["enabled"] != true {
		t.Errorf("expected ingestionGatewayDeployment.enabled = true, got %+v", parsedValues)
	}

	// 2. Delete / disable 1P component
	if err := mgr.Delete1PComponent(ctx, "xmc", "ingestion-gateway"); err != nil {
		t.Fatalf("Delete1PComponent failed: %v", err)
	}

	if mgr.IsComponentActive("ingestion-gateway") {
		t.Error("expected ingestion-gateway to be inactive after deletion")
	}

	// Verify ConfigMap was updated with enabled = false
	cmUpdated, err := client.CoreV1().ConfigMaps("xmc").Get(ctx, ComponentValuesConfigMapName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("failed to get updated ConfigMap: %v", err)
	}

	if err := json.Unmarshal([]byte(cmUpdated.Data["ingestion-gateway.json"]), &parsedValues); err != nil {
		t.Fatalf("failed to unmarshal updated JSON: %v", err)
	}

	igMapUpdated, ok := parsedValues["ingestionGatewayDeployment"].(map[string]any)
	if !ok || igMapUpdated["enabled"] != false {
		t.Errorf("expected ingestionGatewayDeployment.enabled = false after delete, got %+v", parsedValues)
	}
}

func TestK8sHelmManager_Guards(t *testing.T) {
	ctx := context.Background()
	var nilMgr *K8sHelmManager

	if err := nilMgr.Ensure1PComponent(ctx, "xmc", "test", HelmComponentOptions{}); err != nil {
		t.Errorf("expected nil error on nil receiver Ensure1PComponent, got %v", err)
	}
	if err := nilMgr.Delete1PComponent(ctx, "xmc", "test"); err != nil {
		t.Errorf("expected nil error on nil receiver Delete1PComponent, got %v", err)
	}
	if nilMgr.IsComponentActive("test") {
		t.Error("expected false on nil receiver IsComponentActive")
	}

	mgr := NewK8sHelmManager(nil, "", "")
	if err := mgr.Ensure1PComponent(ctx, "", "", HelmComponentOptions{}); err == nil {
		t.Error("expected error on empty componentName for Ensure1PComponent")
	}
	if err := mgr.Delete1PComponent(ctx, "", ""); err == nil {
		t.Error("expected error on empty componentName for Delete1PComponent")
	}
	if err := mgr.Orphan1PComponent(ctx, "", ""); err == nil {
		t.Error("expected error on empty componentName for Orphan1PComponent")
	}
	if err := nilMgr.Orphan1PComponent(ctx, "xmc", "test"); err != nil {
		t.Errorf("expected nil error on nil receiver Orphan1PComponent, got %v", err)
	}
}

func TestK8sHelmManager_Orphan(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()
	mgr := NewK8sHelmManager(client, "xmc", "xmanager-cloud")

	opts := HelmComponentOptions{
		ComponentName: "rod",
		Namespace:     "xmc",
		Enabled:       true,
	}

	if err := mgr.Ensure1PComponent(ctx, "xmc", "rod", opts); err != nil {
		t.Fatalf("Ensure1PComponent failed: %v", err)
	}

	if err := mgr.Orphan1PComponent(ctx, "xmc", "rod"); err != nil {
		t.Fatalf("Orphan1PComponent failed: %v", err)
	}

	if mgr.IsComponentActive("rod") {
		t.Error("expected rod to be inactive after orphaning")
	}
}
