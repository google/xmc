// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package summary

import (
	"context"
	"testing"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/pkg/components/store"
)

func TestStoreComponentsProvider_Health_And_Config(t *testing.T) {
	ctx := context.Background()
	memStore := store.NewMemoryStore()
	provider, err := NewStoreComponentsProvider(memStore)
	if err != nil {
		t.Fatalf("NewStoreComponentsProvider failed: %v", err)
	}

	// Add 3P ClickHouse
	_, _ = memStore.SetComponentIntent(ctx, "clickhouse", &componentspb.ComponentSpecProto{
		Enabled:        true,
		ComponentType:  componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER,
		ComponentClass: "analytical-db",
		Parameters: map[string]string{
			"httpPort":   "8123",
			"nativePort": "9000",
		},
	}, 1)
	_ = memStore.UpdateObservedGeneration(ctx, "clickhouse", 1, nil, "Ready=True")

	// Add 1P Ingestion Gateway
	_, _ = memStore.SetComponentIntent(ctx, "ingestion-gateway", &componentspb.ComponentSpecProto{
		Enabled:        true,
		ComponentType:  componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		ComponentClass: "metrics-ingestion",
		Parameters: map[string]string{
			"port": "8080",
		},
	}, 1)

	// Check Health
	healthList, err := provider.GetComponentsHealth(ctx)
	if err != nil {
		t.Fatalf("GetComponentsHealth failed: %v", err)
	}
	if len(healthList) != 2 {
		t.Fatalf("got %d health items, want 2", len(healthList))
	}

	healthMap := make(map[string]ComponentHealth)
	for _, h := range healthList {
		healthMap[h.Name] = h
	}

	chHealth, ok := healthMap["clickhouse"]
	if !ok || chHealth.HealthState != HealthStateHealthy || chHealth.Type != ComponentTypeCustom3PWrapper {
		t.Errorf("unexpected clickhouse health: %+v", chHealth)
	}

	gwHealth, ok := healthMap["ingestion-gateway"]
	if !ok || gwHealth.Type != ComponentTypeCustom1PComponent {
		t.Errorf("unexpected ingestion-gateway health: %+v", gwHealth)
	}

	// Check Config
	configList, err := provider.GetComponentsConfig(ctx)
	if err != nil {
		t.Fatalf("GetComponentsConfig failed: %v", err)
	}
	if len(configList) != 2 {
		t.Fatalf("got %d config items, want 2", len(configList))
	}
}
