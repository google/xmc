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

	"github.com/google/go-cmp/cmp"
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

	configList, err := provider.GetComponentsConfig(ctx)
	if err != nil {
		t.Fatalf("GetComponentsConfig failed: %v", err)
	}
	if len(configList) != 0 {
		t.Errorf("got %d config items, want 0", len(configList))
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

func TestStoreComponentsProvider_ConfigExtraction(t *testing.T) {
	ctx := context.Background()
	memStore := store.NewMemoryStore()
	provider, err := NewStoreComponentsProvider(memStore)
	if err != nil {
		t.Fatalf("NewStoreComponentsProvider failed: %v", err)
	}

	_, _ = memStore.SetComponentIntent(ctx, "redpanda", &componentspb.ComponentSpec{
		Type:        componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version:     "v2.1.0",
		Description: "Streaming engine",
		WrapperRef: &componentspb.WrapperRef{
			CrdKind:         "RedpandaCluster",
			CrdGroup:        "redpanda.vectorized.io",
			CrdVersion:      "v1alpha1",
			TargetNamespace: "xmc-redpanda",
			TargetName:      "rp-cluster",
		},
	}, map[string]string{
		"tier": "storage",
	}, 2)

	_, _ = memStore.UpdateObservedGeneration(ctx, "redpanda", 2, &componentspb.ComponentAccessStatus{
		Ready: true,
		Coordinates: &componentspb.ResolvedComponentCoordinates{
			ServiceName: "redpanda-service",
			Port:        9092,
			Scheme:      "http",
			Host:        "redpanda.xmc-redpanda.svc.cluster.local",
		},
	}, componentspb.ComponentState_COMPONENT_STATE_ENABLED, "Ready")

	configList, err := provider.GetComponentsConfig(ctx)
	if err != nil {
		t.Fatalf("GetComponentsConfig failed: %v", err)
	}
	if len(configList) != 1 {
		t.Fatalf("got %d configs, want 1", len(configList))
	}

	cfg := configList[0]
	if cfg.Name != "redpanda" || cfg.Type != ComponentTypeCustom1PComponent {
		t.Errorf("unexpected name/type: name=%s type=%v", cfg.Name, cfg.Type)
	}

	expectedParams := []ConfigParameter{
		{Key: "description", Value: "Streaming engine"},
		{Key: "endpoint", Value: "http://redpanda.xmc-redpanda.svc.cluster.local:9092"},
		{Key: "generation", Value: "2"},
		{Key: "label.tier", Value: "storage"},
		{Key: "state", Value: "COMPONENT_STATE_ENABLED"},
		{Key: "type", Value: "COMPONENT_TYPE_CUSTOM_1P_COMPONENT"},
		{Key: "version", Value: "v2.1.0"},
		{Key: "wrapper.crd_group", Value: "redpanda.vectorized.io"},
		{Key: "wrapper.crd_kind", Value: "RedpandaCluster"},
		{Key: "wrapper.crd_version", Value: "v1alpha1"},
		{Key: "wrapper.target_name", Value: "rp-cluster"},
		{Key: "wrapper.target_namespace", Value: "xmc-redpanda"},
	}

	if diff := cmp.Diff(expectedParams, cfg.ConfigParameters); diff != "" {
		t.Errorf("ConfigParameters mismatch (-want +got):\n%s", diff)
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

	configList, err := provider.GetComponentsConfig(ctx)
	if err != nil {
		t.Fatalf("GetComponentsConfig failed: %v", err)
	}
	if len(configList) != 1 {
		t.Fatalf("got %d configs, want 1", len(configList))
	}

	cfgMap := make(map[string]string)
	for _, p := range configList[0].ConfigParameters {
		cfgMap[p.Key] = p.Value
	}

	if cfgMap["lifecycle.grace_period"] != "30s" {
		t.Errorf("got grace_period %q, want %q", cfgMap["lifecycle.grace_period"], "30s")
	}
	if cfgMap["lifecycle.draining_timeout"] != "2m0s" {
		t.Errorf("got draining_timeout %q, want %q", cfgMap["lifecycle.draining_timeout"], "2m0s")
	}
	if cfgMap["state"] != "COMPONENT_STATE_DISABLING" {
		t.Errorf("got state %q, want COMPONENT_STATE_DISABLING", cfgMap["state"])
	}
}

func TestFormatCoordinatesEndpoint_IPv6(t *testing.T) {
	tests := []struct {
		name     string
		coord    *componentspb.ResolvedComponentCoordinates
		expected string
	}{
		{
			name: "IPv6 without port gets wrapped in brackets",
			coord: &componentspb.ResolvedComponentCoordinates{
				Scheme: "http",
				Host:   "2001:db8::1",
			},
			expected: "http://[2001:db8::1]",
		},
		{
			name: "IPv6 with port uses JoinHostPort",
			coord: &componentspb.ResolvedComponentCoordinates{
				Scheme: "https",
				Host:   "2001:db8::1",
				Port:   8443,
			},
			expected: "https://[2001:db8::1]:8443",
		},
		{
			name: "IPv6 already wrapped without port is not double bracketed",
			coord: &componentspb.ResolvedComponentCoordinates{
				Scheme: "http",
				Host:   "[2001:db8::1]",
			},
			expected: "http://[2001:db8::1]",
		},
		{
			name: "IPv4 without port remains unbracketed",
			coord: &componentspb.ResolvedComponentCoordinates{
				Scheme: "http",
				Host:   "192.168.1.1",
			},
			expected: "http://192.168.1.1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatCoordinatesEndpoint(tt.coord)
			if got != tt.expected {
				t.Errorf("formatCoordinatesEndpoint() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestExtractComponentConfig_SortedDependencies(t *testing.T) {
	record := &store.ComponentRecord{
		Name:         "multi-dep-comp",
		State:        componentspb.ComponentState_COMPONENT_STATE_ENABLED,
		Generation:   1,
		Dependencies: []string{"zebra", "alpha", "charlie", "beta"},
	}

	cfg := extractComponentConfig(record)
	var depsVal string
	for _, p := range cfg.ConfigParameters {
		if p.Key == "dependencies" {
			depsVal = p.Value
			break
		}
	}

	want := "alpha, beta, charlie, zebra"
	if depsVal != want {
		t.Errorf("dependencies = %q, want %q", depsVal, want)
	}
}
