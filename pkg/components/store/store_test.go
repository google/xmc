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

package store

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
)

func TestStore_SetComponentIntent_And_Get(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()

	// 1. Create Redpanda component
	redpandaIntent := &componentspb.ComponentSpecProto{
		Enabled:            true,
		ComponentType:      componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER,
		ComponentClass:     "event-stream",
		DeletionProtection: true,
		Parameters: map[string]string{
			"brokerEndpoints": "redpanda:9092",
		},
	}

	rec, err := s.SetComponentIntent(ctx, "redpanda", redpandaIntent, 1)
	if err != nil {
		t.Fatalf("SetComponentIntent(redpanda) unexpected error: %v", err)
	}
	if rec.Name != "redpanda" || rec.Generation != 1 {
		t.Errorf("got rec %+v, want name redpanda and gen 1", rec)
	}

	// 2. Create Ingestion Gateway with dependency on Redpanda
	gwIntent := &componentspb.ComponentSpecProto{
		Enabled:            true,
		ComponentType:      componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		ComponentClass:     "metrics-ingestion",
		DeletionProtection: true,
		Dependencies:       []string{"redpanda"},
		Parameters: map[string]string{
			"port": "8080",
		},
	}

	gwRec, err := s.SetComponentIntent(ctx, "ingestion-gateway", gwIntent, 1)
	if err != nil {
		t.Fatalf("SetComponentIntent(ingestion-gateway) unexpected error: %v", err)
	}
	if gwRec.Name != "ingestion-gateway" {
		t.Errorf("got name %q, want ingestion-gateway", gwRec.Name)
	}

	// 3. Attempt to create a component with missing dependency -> should fail
	invalidIntent := &componentspb.ComponentSpecProto{
		Enabled:      true,
		Dependencies: []string{"non-existent-comp"},
	}
	_, err = s.SetComponentIntent(ctx, "invalid-comp", invalidIntent, 1)
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("got code %v, want FailedPrecondition for missing dep", status.Code(err))
	}
}

func TestStore_DeactivationSafety_And_Cascade(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()

	// Redpanda -> Ingestion Gateway
	_, _ = s.SetComponentIntent(ctx, "redpanda", &componentspb.ComponentSpecProto{
		Enabled:        true,
		ComponentClass: "event-stream",
	}, 1)
	_ = s.UpdateObservedGeneration(ctx, "redpanda", 1, nil, "Ready=True")

	_, _ = s.SetComponentIntent(ctx, "ingestion-gateway", &componentspb.ComponentSpecProto{
		Enabled:        true,
		ComponentClass: "metrics-ingestion",
		Dependencies:   []string{"redpanda"},
	}, 1)
	_ = s.UpdateObservedGeneration(ctx, "ingestion-gateway", 1, nil, "Ready=True")

	// 1. PrepareDeactivation on redpanda without cascade -> should be blocked by ingestion-gateway
	prep, err := s.PrepareDeactivation(ctx, "redpanda", false)
	if err != nil {
		t.Fatalf("PrepareDeactivation failed: %v", err)
	}
	if prep.IsSafeToDeactivate {
		t.Errorf("expected deactivation to be blocked, got safe=true")
	}
	if len(prep.BlockingDependents) == 0 || prep.BlockingDependents[0] != "ingestion-gateway" {
		t.Errorf("expected blocking dependent ingestion-gateway, got %v", prep.BlockingDependents)
	}

	// 2. PrepareDeactivation with cascade=true -> should be safe and list cascaded deactivations
	prepCascade, err := s.PrepareDeactivation(ctx, "redpanda", true)
	if err != nil {
		t.Fatalf("PrepareDeactivation(cascade) failed: %v", err)
	}
	if !prepCascade.IsSafeToDeactivate {
		t.Errorf("expected deactivation with cascade to be safe")
	}
	if len(prepCascade.CascadedDeactivations) == 0 {
		t.Errorf("expected cascaded deactivations to contain ingestion-gateway")
	}
}

func TestStore_ResolveComponentSnapshot_Flattening(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()

	// Set up ClickHouse, Redpanda, Ingestion Gateway
	_, _ = s.SetComponentIntent(ctx, "clickhouse", &componentspb.ComponentSpecProto{
		Enabled:        true,
		ComponentClass: "analytical-db",
	}, 1)
	_ = s.UpdateObservedGeneration(ctx, "clickhouse", 1, &componentspb.ComponentAccessStatusProto{
		EnvVars: map[string]string{"CLICKHOUSE_HOST": "clickhouse.xmc-metrics.svc.cluster.local"},
	}, "Ready=True")

	_, _ = s.SetComponentIntent(ctx, "redpanda", &componentspb.ComponentSpecProto{
		Enabled:        true,
		ComponentClass: "event-stream",
	}, 2)
	_ = s.UpdateObservedGeneration(ctx, "redpanda", 2, &componentspb.ComponentAccessStatusProto{
		EnvVars: map[string]string{"KAFKA_BROKERS": "redpanda:9092"},
	}, "Ready=True")

	_, _ = s.SetComponentIntent(ctx, "ingestion-gateway", &componentspb.ComponentSpecProto{
		Enabled:        true,
		ComponentClass: "metrics-ingestion",
		Dependencies:   []string{"redpanda"},
	}, 3)
	_ = s.UpdateObservedGeneration(ctx, "ingestion-gateway", 3, &componentspb.ComponentAccessStatusProto{
		EnvVars: map[string]string{"GATEWAY_URL": "http://ingestion-gateway:8080"},
	}, "Ready=True")

	// Resolve snapshot for an experiment requesting ["analytical-db", "metrics-ingestion"]
	coords, err := s.ResolveComponentSnapshot(ctx, 12345, []string{"analytical-db", "metrics-ingestion"})
	if err != nil {
		t.Fatalf("ResolveComponentSnapshot failed: %v", err)
	}

	// Should contain 3 flattened components: clickhouse, ingestion-gateway, and transitive redpanda!
	if len(coords) != 3 {
		t.Fatalf("got %d resolved coords, want 3 (including transitive dependency redpanda)", len(coords))
	}

	coordMap := make(map[string]*componentspb.ResolvedComponentCoordinates)
	for _, c := range coords {
		coordMap[c.ComponentName] = c
	}

	if _, ok := coordMap["clickhouse"]; !ok {
		t.Errorf("missing clickhouse in resolved coordinates")
	}
	if _, ok := coordMap["ingestion-gateway"]; !ok {
		t.Errorf("missing ingestion-gateway in resolved coordinates")
	}
	if _, ok := coordMap["redpanda"]; !ok {
		t.Errorf("missing transitive redpanda in resolved coordinates")
	}
}

func TestStore_DeletionProtection(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()

	_, _ = s.SetComponentIntent(ctx, "protected-comp", &componentspb.ComponentSpecProto{
		Enabled:            true,
		DeletionProtection: true,
	}, 1)

	// Attempt deletion when deletionProtection: true -> should fail
	err := s.ExecuteDeletion(ctx, "protected-comp")
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("got code %v, want FailedPrecondition when deletionProtection is true", status.Code(err))
	}

	// Update deletionProtection to false
	_, _ = s.SetComponentIntent(ctx, "protected-comp", &componentspb.ComponentSpecProto{
		Enabled:            true,
		DeletionProtection: false,
	}, 2)

	// Deletion should now succeed
	err = s.ExecuteDeletion(ctx, "protected-comp")
	if err != nil {
		t.Errorf("ExecuteDeletion unexpected error: %v", err)
	}

	// Fetching deleted component should return NotFound
	_, err = s.GetComponent(ctx, "protected-comp")
	if status.Code(err) != codes.NotFound {
		t.Errorf("got code %v, want NotFound after soft-deletion", status.Code(err))
	}
}
