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

package cms

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/pkg/components/store"
)

func TestCMS_SetComponentIntent_And_Snapshot(t *testing.T) {
	ctx := context.Background()
	memStore := store.NewMemoryStore()
	server, err := NewServer(memStore)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	// 1. Register ClickHouse
	chResp, err := server.SetComponentIntent(ctx, &componentspb.SetComponentIntentRequest{
		ComponentName: "clickhouse",
		Intent: &componentspb.ComponentSpecProto{
			Enabled:            true,
			ComponentType:      componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER,
			ComponentClass:     "analytical-db",
			DeletionProtection: true,
		},
		DesiredK8SGeneration: 1,
	})
	if err != nil {
		t.Fatalf("SetComponentIntent(clickhouse) failed: %v", err)
	}
	if chResp.CommittedGeneration != 1 {
		t.Errorf("got committed generation %d, want 1", chResp.CommittedGeneration)
	}

	// 2. Update Observed Generation
	_, err = server.UpdateObservedGeneration(ctx, &componentspb.UpdateObservedGenerationRequest{
		ComponentName:      "clickhouse",
		ObservedGeneration: 1,
		AccessStatus: &componentspb.ComponentAccessStatusProto{
			EnvVars: map[string]string{"CLICKHOUSE_HOST": "clickhouse:9000"},
		},
		StatusCondition: "Ready=True",
	})
	if err != nil {
		t.Fatalf("UpdateObservedGeneration failed: %v", err)
	}

	// 3. Validate Requirement
	valResp, err := server.ValidateComponentRequirement(ctx, &componentspb.ValidateComponentRequirementRequest{
		RequiredComponentClass: "analytical-db",
	})
	if err != nil {
		t.Fatalf("ValidateComponentRequirement failed: %v", err)
	}
	if !valResp.IsSatisfied || valResp.SatisfyingComponentName != "clickhouse" {
		t.Errorf("got valResp %+v, want satisfied by clickhouse", valResp)
	}

	// 4. Resolve Snapshot for Experiment
	snapResp, err := server.ResolveComponentSnapshot(ctx, &componentspb.ResolveComponentSnapshotRequest{
		ExperimentXid:             999,
		RequestedComponentClasses: []string{"analytical-db"},
	})
	if err != nil {
		t.Fatalf("ResolveComponentSnapshot failed: %v", err)
	}
	if len(snapResp.SnapshotBindings) != 1 {
		t.Fatalf("got %d snapshot bindings, want 1", len(snapResp.SnapshotBindings))
	}
	if snapResp.SnapshotBindings[0].ComponentName != "clickhouse" {
		t.Errorf("got component name %q, want clickhouse", snapResp.SnapshotBindings[0].ComponentName)
	}

	// 5. Get Platform Health
	healthResp, err := server.GetPlatformHealth(ctx, &componentspb.GetPlatformHealthRequest{})
	if err != nil {
		t.Fatalf("GetPlatformHealth failed: %v", err)
	}
	if healthResp.IsPlatformDegraded {
		t.Errorf("expected platform not degraded, got %+v", healthResp)
	}
}

func TestCMS_DeactivationSafety_Validation(t *testing.T) {
	ctx := context.Background()
	memStore := store.NewMemoryStore()
	server, err := NewServer(memStore)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	// Setup Redpanda -> Ingestion Gateway
	_, _ = server.SetComponentIntent(ctx, &componentspb.SetComponentIntentRequest{
		ComponentName: "redpanda",
		Intent: &componentspb.ComponentSpecProto{
			Enabled:        true,
			ComponentClass: "event-stream",
		},
		DesiredK8SGeneration: 1,
	})
	_, _ = server.SetComponentIntent(ctx, &componentspb.SetComponentIntentRequest{
		ComponentName: "ingestion-gateway",
		Intent: &componentspb.ComponentSpecProto{
			Enabled:        true,
			ComponentClass: "metrics-ingestion",
			Dependencies:   []string{"redpanda"},
		},
		DesiredK8SGeneration: 1,
	})

	// Prepare deactivation on redpanda without cascade
	prepResp, err := server.PrepareDeactivation(ctx, &componentspb.PrepareDeactivationRequest{
		ComponentName:  "redpanda",
		RequestCascade: false,
	})
	if err != nil {
		t.Fatalf("PrepareDeactivation failed: %v", err)
	}
	if prepResp.IsSafeToDeactivate {
		t.Errorf("expected safe=false due to active ingestion-gateway dependent")
	}

	// Complete deactivation on non-existent component should return NotFound
	_, err = server.CompleteDeactivation(ctx, &componentspb.CompleteDeactivationRequest{
		ComponentName: "ghost-component",
	})
	if status.Code(err) != codes.NotFound {
		t.Errorf("got code %v, want NotFound", status.Code(err))
	}
}
