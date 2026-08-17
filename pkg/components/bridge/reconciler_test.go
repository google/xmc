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

package bridge

import (
	"context"
	"testing"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/pkg/components/cms"
	"xmanager_cloud/pkg/components/store"
)

type mockProber struct {
	healthy bool
	message string
}

func (m *mockProber) ProbeHealth(ctx context.Context, spec *componentspb.ComponentSpecProto) (bool, string) {
	return m.healthy, m.message
}

func TestReconciler_EnableComponent_Workflow(t *testing.T) {
	ctx := context.Background()
	memStore := store.NewMemoryStore()
	cmsServer, _ := cms.NewServer(memStore)
	prober := &mockProber{healthy: true, message: "Endpoints reachable"}

	reconciler, err := NewReconciler(NewCMSClientAdapter(cmsServer), prober, nil)
	if err != nil {
		t.Fatalf("NewReconciler failed: %v", err)
	}

	spec := &componentspb.ComponentSpecProto{
		Enabled:            true,
		ComponentType:      componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER,
		ComponentClass:     "analytical-db",
		DeletionProtection: true,
		Parameters: map[string]string{
			"clusterEndpoints": "clickhouse:9000",
			"nativePort":       "9000",
		},
	}

	// 1. First reconciliation pass -> Should request adding finalizer and transition to ENABLED
	res, err := reconciler.ReconcileComponent(ctx, "xmc", "clickhouse", 1, spec, false, false)
	if err != nil {
		t.Fatalf("ReconcileComponent failed: %v", err)
	}

	if !res.AddFinalizer {
		t.Errorf("expected AddFinalizer=true on first pass when deletionProtection=true")
	}
	if res.State != componentspb.ComponentState_COMPONENT_STATE_ENABLED {
		t.Errorf("got state %v, want ENABLED", res.State)
	}
	if res.Access == nil || res.Access.EnvVars["CLICKHOUSE_HOST"] != "clickhouse:9000" {
		t.Errorf("unexpected access payload: %+v", res.Access)
	}

	// 2. Verify Spanner store was updated
	comp, err := memStore.GetComponent(ctx, "clickhouse")
	if err != nil {
		t.Fatalf("GetComponent failed: %v", err)
	}
	if comp.ObservedGeneration == nil || *comp.ObservedGeneration != 1 {
		t.Errorf("got observed generation %v, want 1", comp.ObservedGeneration)
	}
}

func TestReconciler_DeletionProtection_Interception(t *testing.T) {
	ctx := context.Background()
	memStore := store.NewMemoryStore()
	cmsServer, _ := cms.NewServer(memStore)
	prober := &mockProber{healthy: true, message: "OK"}

	reconciler, _ := NewReconciler(NewCMSClientAdapter(cmsServer), prober, nil)

	spec := &componentspb.ComponentSpecProto{
		Enabled:            true,
		DeletionProtection: true,
	}
	_, _ = reconciler.ReconcileComponent(ctx, "xmc", "clickhouse", 1, spec, false, true)

	// Attempt deletion while deletionProtection is true
	res, err := reconciler.ReconcileComponent(ctx, "xmc", "clickhouse", 1, spec, true, true)
	if err != nil {
		t.Fatalf("ReconcileComponent failed: %v", err)
	}

	if res.RemoveFinalizer {
		t.Errorf("expected RemoveFinalizer=false when deletionProtection=true")
	}

	hasBlockedCond := false
	for _, c := range res.Conditions {
		if c.Type == ConditionDeletionBlocked && c.Status == "True" {
			hasBlockedCond = true
			break
		}
	}
	if !hasBlockedCond {
		t.Errorf("expected DeletionBlocked=True condition, got %+v", res.Conditions)
	}
}

func TestReconciler_1PComponent_WorkloadLifecycle(t *testing.T) {
	ctx := context.Background()
	memStore := store.NewMemoryStore()
	cmsServer, _ := cms.NewServer(memStore)
	prober := &mockProber{healthy: true, message: "OK"}
	fakeWM := NewFakeWorkloadManager()

	reconciler, err := NewReconciler(NewCMSClientAdapter(cmsServer), prober, fakeWM)
	if err != nil {
		t.Fatalf("NewReconciler failed: %v", err)
	}

	spec := &componentspb.ComponentSpecProto{
		Enabled:        true,
		ComponentType:  componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		ComponentClass: "metrics-ingestion",
		ImageTag:       "latest",
		Parameters: map[string]string{
			"port": "8080",
		},
	}

	// 1. Enable 1P component -> Ensure1PWorkload must be called
	res, err := reconciler.ReconcileComponent(ctx, "xmc", "ingestion-gateway", 1, spec, false, false)
	if err != nil {
		t.Fatalf("ReconcileComponent failed: %v", err)
	}
	if res.State != componentspb.ComponentState_COMPONENT_STATE_ENABLED {
		t.Errorf("got state %v, want ENABLED", res.State)
	}
	if !fakeWM.IsWorkloadActive("ingestion-gateway") {
		t.Errorf("expected 1P workload to be active after enabling")
	}

	// 2. Disable 1P component -> Delete1PWorkload must be called
	disabledSpec := &componentspb.ComponentSpecProto{
		Enabled:        false,
		ComponentType:  componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		ComponentClass: "metrics-ingestion",
	}
	res, err = reconciler.ReconcileComponent(ctx, "xmc", "ingestion-gateway", 2, disabledSpec, false, false)
	if err != nil {
		t.Fatalf("ReconcileComponent failed: %v", err)
	}
	if res.State != componentspb.ComponentState_COMPONENT_STATE_DISABLED {
		t.Errorf("got state %v, want DISABLED", res.State)
	}
	if fakeWM.IsWorkloadActive("ingestion-gateway") {
		t.Errorf("expected 1P workload to be deleted after disabling")
	}
}
