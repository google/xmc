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

package summary

import (
	"context"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"
	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/pkg/components/store"
)

func TestNewStoreComponentsProvider_NilStore(t *testing.T) {
	provider, err := NewStoreComponentsProvider(nil)
	if err == nil {
		t.Fatalf("expected error for nil store, got nil")
	}
	if provider != nil {
		t.Fatalf("expected nil provider, got %v", provider)
	}
}

func TestStoreComponentsProvider_EmptyStore(t *testing.T) {
	ctx := context.Background()
	memStore := store.NewMemoryStore()
	provider, err := NewStoreComponentsProvider(memStore)
	if err != nil {
		t.Fatalf("NewStoreComponentsProvider failed: %v", err)
	}

	healthList, err := provider.GetComponentsHealth(ctx)
	if err != nil {
		t.Fatalf("GetComponentsHealth failed: %v", err)
	}
	if len(healthList) != 0 {
		t.Errorf("got %d health items, want 0", len(healthList))
	}
}

func TestStoreComponentsProvider_HealthEvaluation(t *testing.T) {
	ctx := context.Background()
	memStore := store.NewMemoryStore()
	provider, err := NewStoreComponentsProvider(memStore)
	if err != nil {
		t.Fatalf("NewStoreComponentsProvider failed: %v", err)
	}

	// 1. Healthy 1P Component
	_, _ = memStore.SetComponentIntent(ctx, "gateway-1p", &componentspb.ComponentSpec{
		Type:        componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version:     "v1.0.0",
		Description: "Ingestion gateway",
	}, map[string]string{"env": "prod"}, 1)
	_, _ = memStore.UpdateObservedGeneration(ctx, "gateway-1p", 1, &componentspb.ComponentAccessStatus{
		Ready:         true,
		HealthMessage: "Ready=True",
	}, componentspb.ComponentState_COMPONENT_STATE_ENABLED, "Healthy")

	// 2. 3P Wrapper Pending Reconciliation
	_, _ = memStore.SetComponentIntent(ctx, "clickhouse-3p", &componentspb.ComponentSpec{
		Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER,
		WrapperRef: &componentspb.WrapperRef{
			CrdGroup:        "clickhouse.altinity.com",
			CrdVersion:      "v1",
			CrdKind:         "ClickHouseInstallation",
			TargetNamespace: "xmc-system",
			TargetName:      "chi-cluster",
		},
	}, nil, 1)
	_, _ = memStore.UpdateObservedGeneration(ctx, "clickhouse-3p", 1, nil, componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION, "Pending")

	// 3. Enabled but Not Ready Component (with health message)
	_, _ = memStore.SetComponentIntent(ctx, "unready-comp", &componentspb.ComponentSpec{
		Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
	}, nil, 1)
	_, _ = memStore.UpdateObservedGeneration(ctx, "unready-comp", 1, &componentspb.ComponentAccessStatus{
		Ready:         false,
		HealthMessage: "CrashLoopBackOff",
	}, componentspb.ComponentState_COMPONENT_STATE_ENABLED, "CrashLoopBackOff")

	// 4. Enabled Component with Nil Status (unobserved)
	_, _ = memStore.SetComponentIntent(ctx, "unobserved-comp", &componentspb.ComponentSpec{
		Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
	}, nil, 1)
	_, _ = memStore.UpdateObservedGeneration(ctx, "unobserved-comp", 1, nil, componentspb.ComponentState_COMPONENT_STATE_ENABLED, "")

	// 5. Disabled Component
	_, _ = memStore.SetComponentIntent(ctx, "disabled-comp", &componentspb.ComponentSpec{
		Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
	}, nil, 1)
	_, _ = memStore.UpdateObservedGeneration(ctx, "disabled-comp", 1, nil, componentspb.ComponentState_COMPONENT_STATE_DISABLED, "Disabled")

	healthList, err := provider.GetComponentsHealth(ctx)
	if err != nil {
		t.Fatalf("GetComponentsHealth failed: %v", err)
	}

	healthMap := make(map[string]ComponentHealth, len(healthList))
	for _, h := range healthList {
		healthMap[h.Name] = h
	}

	tests := []struct {
		name        string
		target      string
		wantType    ComponentType
		wantState   HealthState
		wantMessage string
	}{
		{
			name:        "healthy 1p component",
			target:      "gateway-1p",
			wantType:    ComponentTypeCustom1PComponent,
			wantState:   HealthStateHealthy,
			wantMessage: "Healthy and Operational",
		},
		{
			name:        "pending reconciliation 3p wrapper",
			target:      "clickhouse-3p",
			wantType:    ComponentTypeCustom3PWrapper,
			wantState:   HealthStateUnhealthy,
			wantMessage: "Pending GKE reconciliation",
		},
		{
			name:        "unready component with health message",
			target:      "unready-comp",
			wantType:    ComponentTypeCustom1PComponent,
			wantState:   HealthStateUnhealthy,
			wantMessage: "CrashLoopBackOff",
		},
		{
			name:        "enabled component with nil status is unready",
			target:      "unobserved-comp",
			wantType:    ComponentTypeCustom1PComponent,
			wantState:   HealthStateUnhealthy,
			wantMessage: "Component enabled but not ready",
		},
		{
			name:        "disabled component",
			target:      "disabled-comp",
			wantType:    ComponentTypeCustom1PComponent,
			wantState:   HealthStateDisabled,
			wantMessage: "Component disabled",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, ok := healthMap[tt.target]
			if !ok {
				t.Fatalf("component %q not found in health results", tt.target)
			}
			if h.HealthState != tt.wantState {
				t.Errorf("got healthState %s, want %s", h.HealthState, tt.wantState)
			}
			if h.Type != tt.wantType {
				t.Errorf("got type %s, want %s", h.Type, tt.wantType)
			}
			if h.StatusMessage != tt.wantMessage {
				t.Errorf("got statusMessage %q, want %q", h.StatusMessage, tt.wantMessage)
			}
		})
	}
}

func TestEnums_StringMethods(t *testing.T) {
	if got := ComponentTypeCustom1PComponent.String(); got != "CUSTOM_1P_COMPONENT" {
		t.Errorf("unexpected ComponentType string: %s", got)
	}
	if got := ComponentTypeCustom3PWrapper.String(); got != "CUSTOM_3P_WRAPPER" {
		t.Errorf("unexpected ComponentType string: %s", got)
	}
	if got := ComponentTypeUnspecified.String(); got != "UNSPECIFIED" {
		t.Errorf("unexpected ComponentType string: %s", got)
	}

	if got := HealthStateHealthy.String(); got != "HEALTHY" {
		t.Errorf("unexpected HealthState string: %s", got)
	}
	if got := HealthStateUnhealthy.String(); got != "UNHEALTHY" {
		t.Errorf("unexpected HealthState string: %s", got)
	}
	if got := HealthStateDisabled.String(); got != "DISABLED" {
		t.Errorf("unexpected HealthState string: %s", got)
	}
	if got := HealthStateUnspecified.String(); got != "UNSPECIFIED" {
		t.Errorf("unexpected HealthState string: %s", got)
	}
}

func TestStoreComponentsProvider_LifecycleAndDependencies(t *testing.T) {
	ctx := context.Background()
	memStore := store.NewMemoryStore()
	provider, err := NewStoreComponentsProvider(memStore)
	if err != nil {
		t.Fatalf("NewStoreComponentsProvider failed: %v", err)
	}

	_, _ = memStore.SetComponentIntent(ctx, "draining-comp", &componentspb.ComponentSpec{
		Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER,
		Version: "v1.5.0",
		LifecycleControl: &componentspb.LifecycleControl{
			GracePeriod:     durationpb.New(30 * time.Second),
			DrainingTimeout: durationpb.New(120 * time.Second),
		},
	}, nil, 1)

	// Update dependencies directly on record or via store
	_, _ = memStore.UpdateObservedGeneration(ctx, "draining-comp", 1, nil, componentspb.ComponentState_COMPONENT_STATE_DISABLING, "Draining")

	healthList, err := provider.GetComponentsHealth(ctx)
	if err != nil {
		t.Fatalf("GetComponentsHealth failed: %v", err)
	}
	if len(healthList) != 1 {
		t.Fatalf("got %d health records, want 1", len(healthList))
	}
	if healthList[0].HealthState != HealthStateUnhealthy || healthList[0].StatusMessage != "Component draining" {
		t.Errorf("unexpected draining health: %+v", healthList[0])
	}
}
