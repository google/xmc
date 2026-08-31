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

package store_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/storage/data"
	spannerdata "xmanager_cloud/internal/storage/spanner/data"
	"xmanager_cloud/internal/storage/spanner/emulator"
	"xmanager_cloud/pkg/components/store"
)

func setupTestStore(ctx context.Context, t *testing.T) (*store.SpannerStore, *spanner.Client) {
	t.Helper()
	_, essClient := emulator.Setup(ctx, t)
	return store.NewSpannerStore(essClient), essClient
}

func TestSpannerStore_SetComponentIntent_Success(t *testing.T) {
	ctx := context.Background()
	s, _ := setupTestStore(ctx, t)

	spec := &componentspb.ComponentSpec{
		Type:        componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version:     "v1.0.0",
		Description: "Primary test component",
	}
	labels := map[string]string{"env": "test", "tier": "backend"}

	rec, err := s.SetComponentIntent(ctx, "components/test-comp", spec, labels, 1)
	if err != nil {
		t.Fatalf("SetComponentIntent failed: %v", err)
	}

	if rec.Name != "test-comp" {
		t.Errorf("expected Name 'test-comp', got %q", rec.Name)
	}
	if rec.Generation != 1 {
		t.Errorf("expected Generation 1, got %d", rec.Generation)
	}
	if rec.State != componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION {
		t.Errorf("expected state PENDING_RECONCILIATION, got %v", rec.State)
	}
	if rec.Etag == "" {
		t.Errorf("expected non-empty Etag")
	}
	if rec.CreateTime.IsZero() || rec.UpdateTime.IsZero() {
		t.Errorf("expected non-zero timestamps, got CreateTime=%v, UpdateTime=%v", rec.CreateTime, rec.UpdateTime)
	}
	if diff := cmp.Diff(labels, rec.Labels); diff != "" {
		t.Errorf("Labels mismatch (-want +got):\n%s", diff)
	}

	// Update the existing component intent
	updatedSpec := &componentspb.ComponentSpec{
		Type:        componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version:     "v1.1.0",
		Description: "Updated test component",
	}
	updatedLabels := map[string]string{"env": "prod"}

	updatedRec, err := s.SetComponentIntent(ctx, "test-comp", updatedSpec, updatedLabels, 0)
	if err != nil {
		t.Fatalf("SetComponentIntent update failed: %v", err)
	}

	if updatedRec.Generation != 2 {
		t.Errorf("expected auto-incremented Generation 2, got %d", updatedRec.Generation)
	}
	if updatedRec.ComponentID != rec.ComponentID {
		t.Errorf("expected same ComponentID %d, got %d", rec.ComponentID, updatedRec.ComponentID)
	}
	if updatedRec.Spec.GetVersion() != "v1.1.0" {
		t.Errorf("expected version v1.1.0, got %s", updatedRec.Spec.GetVersion())
	}
	if diff := cmp.Diff(updatedLabels, updatedRec.Labels); diff != "" {
		t.Errorf("Updated Labels mismatch (-want +got):\n%s", diff)
	}

	// Explicit desired generation bump
	explicitRec, err := s.SetComponentIntent(ctx, "test-comp", updatedSpec, updatedLabels, 10)
	if err != nil {
		t.Fatalf("SetComponentIntent explicit gen failed: %v", err)
	}
	if explicitRec.Generation != 10 {
		t.Errorf("expected Generation 10, got %d", explicitRec.Generation)
	}
}

func TestSpannerStore_SetComponentIntent_Dependencies(t *testing.T) {
	ctx := context.Background()
	s, _ := setupTestStore(ctx, t)

	// Create dependency component dep-a
	specA := &componentspb.ComponentSpec{
		Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version: "v1.0.0",
	}
	_, err := s.SetComponentIntent(ctx, "dep-a", specA, nil, 1)
	if err != nil {
		t.Fatalf("failed to create dep-a: %v", err)
	}

	// Create component comp-b depending on dep-a via annotations in labels
	specB := &componentspb.ComponentSpec{
		Type:        componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version:     "v1.0.0",
		Description: "Depends on dep-a",
	}
	labelsB := map[string]string{"xmc.dev/depends-on": "dep-a"}
	recB, err := s.SetComponentIntent(ctx, "comp-b", specB, labelsB, 1)
	if err != nil {
		t.Fatalf("failed to create comp-b: %v", err)
	}

	expectedDeps := []string{"dep-a"}
	if diff := cmp.Diff(expectedDeps, recB.Dependencies); diff != "" {
		t.Errorf("Dependencies mismatch (-want +got):\n%s", diff)
	}

	// Verify GetComponent also returns the dependencies
	fetchedB, err := s.GetComponent(ctx, "comp-b")
	if err != nil {
		t.Fatalf("GetComponent comp-b failed: %v", err)
	}
	if diff := cmp.Diff(expectedDeps, fetchedB.Dependencies); diff != "" {
		t.Errorf("Fetched Dependencies mismatch (-want +got):\n%s", diff)
	}
}

func TestSpannerStore_SetComponentIntent_MissingDependencyRejection(t *testing.T) {
	ctx := context.Background()
	s, _ := setupTestStore(ctx, t)

	spec := &componentspb.ComponentSpec{
		Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version: "v1.0.0",
	}
	labels := map[string]string{"xmc.dev/depends-on": "non-existent-comp"}

	_, err := s.SetComponentIntent(ctx, "comp-missing", spec, labels, 1)
	if err == nil {
		t.Fatalf("expected error for missing dependency, got nil")
	}
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("expected FailedPrecondition, got %v", status.Code(err))
	}
}

func TestSpannerStore_SetComponentIntent_CycleDetectionRejection(t *testing.T) {
	ctx := context.Background()
	s, _ := setupTestStore(ctx, t)

	spec := &componentspb.ComponentSpec{
		Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version: "v1.0.0",
	}

	// 1. Direct self-cycle
	selfCycleLabels := map[string]string{"xmc.dev/depends-on": "node-self"}
	_, err := s.SetComponentIntent(ctx, "node-self", spec, selfCycleLabels, 1)
	if err == nil {
		t.Fatalf("expected error for self-cycle, got nil")
	}
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("expected FailedPrecondition, got %v", status.Code(err))
	}

	// 2. 2-node cycle: node-1 -> node-2 -> node-1
	_, err = s.SetComponentIntent(ctx, "node-1", spec, nil, 1)
	if err != nil {
		t.Fatalf("failed to create node-1: %v", err)
	}
	_, err = s.SetComponentIntent(ctx, "node-2", spec, map[string]string{"xmc.dev/depends-on": "node-1"}, 1)
	if err != nil {
		t.Fatalf("failed to create node-2: %v", err)
	}
	// Try updating node-1 to depend on node-2
	_, err = s.SetComponentIntent(ctx, "node-1", spec, map[string]string{"xmc.dev/depends-on": "node-2"}, 0)
	if err == nil {
		t.Fatalf("expected cycle error for node-1 -> node-2 -> node-1, got nil")
	}
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("expected FailedPrecondition, got %v", status.Code(err))
	}

	// 3. 3-node cycle: a -> b -> c -> a
	_, err = s.SetComponentIntent(ctx, "c-node", spec, nil, 1)
	if err != nil {
		t.Fatalf("failed to create c-node: %v", err)
	}
	_, err = s.SetComponentIntent(ctx, "b-node", spec, map[string]string{"xmc.dev/depends-on": "c-node"}, 1)
	if err != nil {
		t.Fatalf("failed to create b-node: %v", err)
	}
	_, err = s.SetComponentIntent(ctx, "a-node", spec, map[string]string{"xmc.dev/depends-on": "b-node"}, 1)
	if err != nil {
		t.Fatalf("failed to create a-node: %v", err)
	}
	// Try updating c-node to depend on a-node
	_, err = s.SetComponentIntent(ctx, "c-node", spec, map[string]string{"xmc.dev/depends-on": "a-node"}, 0)
	if err == nil {
		t.Fatalf("expected cycle error for 3-node cycle, got nil")
	}
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("expected FailedPrecondition, got %v", status.Code(err))
	}
}

func TestSpannerStore_GetComponent_And_GetComponentByID(t *testing.T) {
	ctx := context.Background()
	s, _ := setupTestStore(ctx, t)

	spec := &componentspb.ComponentSpec{
		Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version: "v1.0.0",
	}
	created, err := s.SetComponentIntent(ctx, "lookup-comp", spec, nil, 1)
	if err != nil {
		t.Fatalf("SetComponentIntent failed: %v", err)
	}

	// Lookup by canonical name
	byCanonical, err := s.GetComponent(ctx, "lookup-comp")
	if err != nil {
		t.Fatalf("GetComponent by canonical failed: %v", err)
	}
	if byCanonical.ComponentID != created.ComponentID {
		t.Errorf("expected ComponentID %d, got %d", created.ComponentID, byCanonical.ComponentID)
	}

	// Lookup by full AIP-122 name
	byFullName, err := s.GetComponent(ctx, "components/lookup-comp")
	if err != nil {
		t.Fatalf("GetComponent by full name failed: %v", err)
	}
	if byFullName.ComponentID != created.ComponentID {
		t.Errorf("expected ComponentID %d, got %d", created.ComponentID, byFullName.ComponentID)
	}

	// Lookup by ID
	byID, err := s.GetComponentByID(ctx, created.ComponentID)
	if err != nil {
		t.Fatalf("GetComponentByID failed: %v", err)
	}
	if byID.Name != "lookup-comp" {
		t.Errorf("expected name 'lookup-comp', got %q", byID.Name)
	}

	// Non-existent lookups
	if _, err := s.GetComponent(ctx, "unknown-comp"); status.Code(err) != codes.NotFound {
		t.Errorf("expected NotFound for unknown name, got %v", err)
	}
	if _, err := s.GetComponentByID(ctx, 999999999); status.Code(err) != codes.NotFound {
		t.Errorf("expected NotFound for unknown ID, got %v", err)
	}
	if _, err := s.GetComponentByID(ctx, 0); status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument for ID 0, got %v", err)
	}
}

func TestSpannerStore_ListComponents(t *testing.T) {
	ctx := context.Background()
	s, _ := setupTestStore(ctx, t)

	spec := &componentspb.ComponentSpec{
		Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version: "v1.0.0",
	}

	names := []string{"charlie", "alpha", "bravo"}
	for _, n := range names {
		if _, err := s.SetComponentIntent(ctx, n, spec, nil, 1); err != nil {
			t.Fatalf("failed to create component %s: %v", n, err)
		}
	}

	list, err := s.ListComponents(ctx)
	if err != nil {
		t.Fatalf("ListComponents failed: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("expected 3 components, got %d", len(list))
	}

	expectedOrder := []string{"alpha", "bravo", "charlie"}
	var actualOrder []string
	for _, r := range list {
		actualOrder = append(actualOrder, r.Name)
	}
	if diff := cmp.Diff(expectedOrder, actualOrder); diff != "" {
		t.Errorf("Alphabetical ordering mismatch (-want +got):\n%s", diff)
	}
}

func TestSpannerStore_UpdateObservedGeneration(t *testing.T) {
	ctx := context.Background()
	s, _ := setupTestStore(ctx, t)

	spec := &componentspb.ComponentSpec{
		Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version: "v1.0.0",
	}
	_, err := s.SetComponentIntent(ctx, "obs-comp", spec, nil, 2)
	if err != nil {
		t.Fatalf("SetComponentIntent failed: %v", err)
	}

	access := &componentspb.ComponentAccessStatus{
		State: componentspb.ComponentState_COMPONENT_STATE_ENABLED,
		Coordinates: &componentspb.ResolvedComponentCoordinates{
			ClusterName: "test-cluster",
			Namespace:   "default",
			ServiceName: "obs-service",
			Port:        8080,
			Scheme:      "grpc",
		},
		Ready: true,
	}

	updated, err := s.UpdateObservedGeneration(ctx, "obs-comp", 2, access, componentspb.ComponentState_COMPONENT_STATE_ENABLED, "reconciled successfully")
	if err != nil {
		t.Fatalf("UpdateObservedGeneration failed: %v", err)
	}

	if updated.ObservedGeneration == nil || *updated.ObservedGeneration != 2 {
		t.Errorf("expected ObservedGeneration 2, got %v", updated.ObservedGeneration)
	}
	if updated.State != componentspb.ComponentState_COMPONENT_STATE_ENABLED {
		t.Errorf("expected state ENABLED, got %v", updated.State)
	}
	if updated.Status.GetHealthMessage() != "reconciled successfully" {
		t.Errorf("expected health message 'reconciled successfully', got %q", updated.Status.GetHealthMessage())
	}
	if updated.Status.GetCoordinates().GetServiceName() != "obs-service" {
		t.Errorf("expected coordinate serviceName 'obs-service', got %q", updated.Status.GetCoordinates().GetServiceName())
	}

	// Negative observed generation rejection
	if _, err := s.UpdateObservedGeneration(ctx, "obs-comp", -1, nil, componentspb.ComponentState_COMPONENT_STATE_UNSPECIFIED, ""); status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument for negative generation, got %v", err)
	}

	// Non-existent component
	if _, err := s.UpdateObservedGeneration(ctx, "unknown", 1, nil, componentspb.ComponentState_COMPONENT_STATE_UNSPECIFIED, ""); status.Code(err) != codes.NotFound {
		t.Errorf("expected NotFound for unknown component, got %v", err)
	}
}

func TestSpannerStore_PrepareDeactivation_And_CompleteDeactivation(t *testing.T) {
	ctx := context.Background()
	s, _ := setupTestStore(ctx, t)

	spec := &componentspb.ComponentSpec{
		Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version: "v1.0.0",
		LifecycleControl: &componentspb.LifecycleControl{
			GracePeriod:     durationpb.New(45 * time.Second),
			DrainingTimeout: durationpb.New(60 * time.Second),
		},
	}

	// Create base-dep and client-app depending on base-dep
	_, err := s.SetComponentIntent(ctx, "base-dep", spec, nil, 1)
	if err != nil {
		t.Fatalf("failed to create base-dep: %v", err)
	}
	_, err = s.SetComponentIntent(ctx, "client-app", spec, map[string]string{"xmc.dev/depends-on": "base-dep"}, 1)
	if err != nil {
		t.Fatalf("failed to create client-app: %v", err)
	}

	// Mark both as ENABLED
	_, _ = s.UpdateObservedGeneration(ctx, "base-dep", 1, nil, componentspb.ComponentState_COMPONENT_STATE_ENABLED, "")
	_, _ = s.UpdateObservedGeneration(ctx, "client-app", 1, nil, componentspb.ComponentState_COMPONENT_STATE_ENABLED, "")

	// 1. PrepareDeactivation without cascade -> should fail with blocking dependents
	_, err = s.PrepareDeactivation(ctx, "base-dep", false)
	if err == nil {
		t.Fatalf("expected error deactivating base-dep without cascade, got nil")
	}
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("expected FailedPrecondition, got %v", status.Code(err))
	}

	// 2. PrepareDeactivation with cascade -> should succeed and transition both to DISABLING
	res, err := s.PrepareDeactivation(ctx, "base-dep", true)
	if err != nil {
		t.Fatalf("PrepareDeactivation with cascade failed: %v", err)
	}
	if !res.IsSafeToDeactivate {
		t.Errorf("expected IsSafeToDeactivate=true")
	}
	if res.RecommendedDrainSec != 60 {
		t.Errorf("expected RecommendedDrainSec=60, got %d", res.RecommendedDrainSec)
	}

	// Verify both are now in DISABLING state
	baseRec, _ := s.GetComponent(ctx, "base-dep")
	clientRec, _ := s.GetComponent(ctx, "client-app")
	if baseRec.State != componentspb.ComponentState_COMPONENT_STATE_DISABLING {
		t.Errorf("expected base-dep state DISABLING, got %v", baseRec.State)
	}
	if clientRec.State != componentspb.ComponentState_COMPONENT_STATE_DISABLING {
		t.Errorf("expected client-app state DISABLING, got %v", clientRec.State)
	}

	// 3. CompleteDeactivation on base-dep while client-app is still DISABLING (active dependent)
	err = s.CompleteDeactivation(ctx, "base-dep", false)
	if err == nil {
		t.Fatalf("expected CompleteDeactivation to fail when active dependent client-app is remaining")
	}

	// Complete client-app first
	if err := s.CompleteDeactivation(ctx, "client-app", false); err != nil {
		t.Fatalf("CompleteDeactivation on client-app failed: %v", err)
	}
	clientRec, _ = s.GetComponent(ctx, "client-app")
	if clientRec.State != componentspb.ComponentState_COMPONENT_STATE_DISABLED {
		t.Errorf("expected client-app state DISABLED, got %v", clientRec.State)
	}

	// Now complete base-dep
	if err := s.CompleteDeactivation(ctx, "base-dep", false); err != nil {
		t.Fatalf("CompleteDeactivation on base-dep failed: %v", err)
	}
	baseRec, _ = s.GetComponent(ctx, "base-dep")
	if baseRec.State != componentspb.ComponentState_COMPONENT_STATE_DISABLED {
		t.Errorf("expected base-dep state DISABLED, got %v", baseRec.State)
	}

	// Idempotency: CompleteDeactivation again on DISABLED component returns nil
	if err := s.CompleteDeactivation(ctx, "base-dep", false); err != nil {
		t.Errorf("expected idempotent CompleteDeactivation, got %v", err)
	}
}

func TestSpannerStore_ExecuteDeletion(t *testing.T) {
	ctx := context.Background()
	s, _ := setupTestStore(ctx, t)

	spec := &componentspb.ComponentSpec{
		Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version: "v1.0.0",
	}

	// 1. Deletion protection enforcement
	protectedLabels := map[string]string{"xmc.dev/deletion-protection": "true"}
	_, err := s.SetComponentIntent(ctx, "protected-comp", spec, protectedLabels, 1)
	if err != nil {
		t.Fatalf("failed to create protected-comp: %v", err)
	}

	err = s.ExecuteDeletion(ctx, "protected-comp", componentspb.DeletionPolicy_DELETION_POLICY_DELETE, false)
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("expected FailedPrecondition for protected component deletion, got %v", err)
	}

	// Force deletion overrides deletion protection
	err = s.ExecuteDeletion(ctx, "protected-comp", componentspb.DeletionPolicy_DELETION_POLICY_DELETE, true)
	if err != nil {
		t.Fatalf("forced ExecuteDeletion failed: %v", err)
	}
	if _, err := s.GetComponent(ctx, "protected-comp"); status.Code(err) != codes.NotFound {
		t.Errorf("expected NotFound after deletion, got %v", err)
	}

	// 2. Cascade deletion of parent and dependent
	_, err = s.SetComponentIntent(ctx, "parent-del", spec, nil, 1)
	if err != nil {
		t.Fatalf("failed to create parent-del: %v", err)
	}
	_, err = s.SetComponentIntent(ctx, "child-del", spec, map[string]string{"xmc.dev/depends-on": "parent-del"}, 1)
	if err != nil {
		t.Fatalf("failed to create child-del: %v", err)
	}

	// Delete parent with cascade (DELETION_POLICY_DELETE)
	err = s.ExecuteDeletion(ctx, "parent-del", componentspb.DeletionPolicy_DELETION_POLICY_DELETE, false)
	if err != nil {
		t.Fatalf("cascade ExecuteDeletion failed: %v", err)
	}

	if _, err := s.GetComponent(ctx, "parent-del"); status.Code(err) != codes.NotFound {
		t.Errorf("expected parent-del NotFound after cascade deletion")
	}
	if _, err := s.GetComponent(ctx, "child-del"); status.Code(err) != codes.NotFound {
		t.Errorf("expected child-del NotFound after cascade deletion")
	}
}

func TestSpannerStore_SoftDeletion_And_ActiveNameRelease(t *testing.T) {
	ctx := context.Background()
	s, essClient := setupTestStore(ctx, t)

	spec := &componentspb.ComponentSpec{
		Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version: "v1.0.0",
	}

	// Step 1: Create initial component
	origRec, err := s.SetComponentIntent(ctx, "recycled-comp", spec, map[string]string{"tag": "v1"}, 1)
	if err != nil {
		t.Fatalf("SetComponentIntent failed: %v", err)
	}
	origID := origRec.ComponentID

	// Step 2: Delete the component (soft deletion, State = 5)
	if err := s.ExecuteDeletion(ctx, "recycled-comp", componentspb.DeletionPolicy_DELETION_POLICY_DELETE, false); err != nil {
		t.Fatalf("ExecuteDeletion failed: %v", err)
	}

	// Verify active lookup fails
	if _, err := s.GetComponent(ctx, "recycled-comp"); status.Code(err) != codes.NotFound {
		t.Errorf("expected NotFound after deletion, got %v", err)
	}
	if _, err := s.GetComponentByID(ctx, origID); status.Code(err) != codes.NotFound {
		t.Errorf("expected NotFound by ID for soft-deleted record, got %v", err)
	}

	// Step 3: Re-create the same component name -> Spanner unique index Idx_Components_ActiveName allows it
	newRec, err := s.SetComponentIntent(ctx, "recycled-comp", spec, map[string]string{"tag": "v2"}, 1)
	if err != nil {
		t.Fatalf("re-creating component with released ActiveName failed: %v", err)
	}
	newID := newRec.ComponentID

	if newID == origID {
		t.Errorf("expected distinct new ComponentID, got %d for both", newID)
	}

	// Verify active lookup returns the new record
	activeRec, err := s.GetComponent(ctx, "recycled-comp")
	if err != nil {
		t.Fatalf("GetComponent on recycled-comp failed: %v", err)
	}
	if activeRec.ComponentID != newID {
		t.Errorf("expected active ComponentID %d, got %d", newID, activeRec.ComponentID)
	}
	if activeRec.Labels["tag"] != "v2" {
		t.Errorf("expected tag 'v2', got %q", activeRec.Labels["tag"])
	}

	// Step 4: Verify directly in Spanner that both records exist in Components table
	stmt := spanner.Statement{
		SQL: `SELECT ComponentId, State, ActiveName FROM Components WHERE Name = @name ORDER BY ComponentId ASC`,
		Params: map[string]any{
			"name": "recycled-comp",
		},
	}
	iter := essClient.Single().Query(ctx, stmt)
	defer iter.Stop()

	type rowResult struct {
		id         int64
		state      int64
		activeName spanner.NullString
	}
	var rows []rowResult
	err = iter.Do(func(row *spanner.Row) error {
		var r rowResult
		if err := row.ColumnByName("ComponentId", &r.id); err != nil {
			return err
		}
		if err := row.ColumnByName("State", &r.state); err != nil {
			return err
		}
		if err := row.ColumnByName("ActiveName", &r.activeName); err != nil {
			return err
		}
		rows = append(rows, r)
		return nil
	})
	if err != nil {
		t.Fatalf("failed to query raw Components table: %v", err)
	}

	if len(rows) != 2 {
		t.Fatalf("expected 2 historical/active rows in Components table, got %d", len(rows))
	}

	var deletedRow, activeRow *rowResult
	for i := range rows {
		if rows[i].id == origID {
			deletedRow = &rows[i]
		}
		if rows[i].id == newID {
			activeRow = &rows[i]
		}
	}

	if deletedRow == nil || deletedRow.state != 5 || deletedRow.activeName.Valid {
		t.Errorf("expected deleted row: id=%d, state=5, activeName=NULL; got: %v", origID, deletedRow)
	}
	if activeRow == nil || activeRow.state == 5 || !activeRow.activeName.Valid || activeRow.activeName.StringVal != "recycled-comp" {
		t.Errorf("expected active row: id=%d, activeName='recycled-comp'; got: %v", newID, activeRow)
	}
}

func TestSpannerStore_ResolveComponentSnapshot(t *testing.T) {
	ctx := context.Background()
	s, essClient := setupTestStore(ctx, t)

	spec := &componentspb.ComponentSpec{
		Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version: "v1.0.0",
	}
	rec, err := s.SetComponentIntent(ctx, "snap-comp", spec, nil, 1)
	if err != nil {
		t.Fatalf("SetComponentIntent failed: %v", err)
	}

	// Update intent to generation 2
	_, err = s.SetComponentIntent(ctx, "snap-comp", spec, nil, 2)
	if err != nil {
		t.Fatalf("SetComponentIntent gen 2 failed: %v", err)
	}

	access := &componentspb.ComponentAccessStatus{
		State: componentspb.ComponentState_COMPONENT_STATE_ENABLED,
		Coordinates: &componentspb.ResolvedComponentCoordinates{
			ClusterName: "gke-cluster",
			Namespace:   "default",
			ServiceName: "snap-service",
			Port:        9000,
		},
	}
	_, err = s.UpdateObservedGeneration(ctx, "snap-comp", 2, access, componentspb.ComponentState_COMPONENT_STATE_ENABLED, "")
	if err != nil {
		t.Fatalf("UpdateObservedGeneration failed: %v", err)
	}

	// 1. Resolve current generation (gen=0 or gen=2)
	snapRec0, coords0, err := s.ResolveComponentSnapshot(ctx, "snap-comp", 0)
	if err != nil {
		t.Fatalf("ResolveComponentSnapshot gen 0 failed: %v", err)
	}
	if snapRec0.Generation != 2 {
		t.Errorf("expected generation 2, got %d", snapRec0.Generation)
	}
	if coords0.GetServiceName() != "snap-service" {
		t.Errorf("expected serviceName snap-service, got %q", coords0.GetServiceName())
	}

	snapRec2, coords2, err := s.ResolveComponentSnapshot(ctx, "snap-comp", 2)
	if err != nil {
		t.Fatalf("ResolveComponentSnapshot gen 2 failed: %v", err)
	}
	if snapRec2.Generation != 2 || coords2.GetPort() != 9000 {
		t.Errorf("unexpected snapshot results: rec=%v, coords=%v", snapRec2, coords2)
	}

	// 2. Future generation returns NotFound
	if _, _, err := s.ResolveComponentSnapshot(ctx, "snap-comp", 5); status.Code(err) != codes.NotFound {
		t.Errorf("expected NotFound for future generation 5, got %v", err)
	}

	// 3. Historical generation (gen 1) missing from ExperimentComponentSnapshots returns NotFound
	if _, _, err := s.ResolveComponentSnapshot(ctx, "snap-comp", 1); status.Code(err) != codes.NotFound {
		t.Errorf("expected NotFound for unrecorded historical generation 1, got %v", err)
	}

	// 4. Insert an experiment snapshot row into ExperimentComponentSnapshots and resolve it
	// First insert dummy experiment row if needed or direct snapshot
	histSpec := &componentspb.ComponentSpec{
		Type:       componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version:    "v1.0.0-beta",
		Generation: 1,
	}
	histCompProto := &componentspb.Component{
		Name: "components/snap-comp",
		Spec: histSpec,
	}
	histConfigBytes, _ := proto.Marshal(histCompProto)
	histAccessProto := &componentspb.ComponentAccessStatus{
		State: componentspb.ComponentState_COMPONENT_STATE_ENABLED,
		Coordinates: &componentspb.ResolvedComponentCoordinates{
			ClusterName: "gke-cluster",
			Namespace:   "default",
			ServiceName: "snap-service-v1",
			Port:        8000,
		},
	}
	histAccessBytes, _ := proto.Marshal(histAccessProto)

	// Create dummy experiment in Experiments table
	testExp := &essmsgpb.Experiment{
		Name:      "experiments/12345",
		Title:     "Test Experiment",
		Creator:   "test-user",
		Priority:  100,
		Urls:      []*essmsgpb.Url{},
		Tags:      []string{},
		Notes:     "test-notes",
		Resources: []*essmsgpb.ResourceSet{},
		Status: &essmsgpb.ExperimentStatus{
			ActiveState: essmsgpb.ExperimentStatus_ACTIVE,
			LaunchState: essmsgpb.ExperimentStatus_LAUNCHING,
		},
		CreateTime: timestamppb.Now(),
		UpdateTime: timestamppb.Now(),
	}
	expMut, err := spannerdata.ExperimentProtoToInsertMutation(testExp)
	if err != nil {
		t.Fatalf("failed to create experiment mutation: %v", err)
	}

	bitReversedXid := data.ReverseBits(12345)

	_, err = essClient.Apply(ctx, []*spanner.Mutation{
		expMut,
		spanner.Insert("ExperimentComponentSnapshots", []string{"BitReversedXid", "ComponentId", "LockedGeneration", "ConfigProto", "AccessPayload"},
			[]any{bitReversedXid, rec.ComponentID, int64(1), histConfigBytes, histAccessBytes}),
	})
	if err != nil {
		t.Fatalf("failed to insert ExperimentComponentSnapshots: %v", err)
	}

	// Now resolve generation 1
	histRec, histCoords, err := s.ResolveComponentSnapshot(ctx, "snap-comp", 1)
	if err != nil {
		t.Fatalf("ResolveComponentSnapshot for historical generation 1 failed: %v", err)
	}
	if histRec.Generation != 1 {
		t.Errorf("expected Generation 1, got %d", histRec.Generation)
	}
	if histRec.Spec.GetVersion() != "v1.0.0-beta" {
		t.Errorf("expected version v1.0.0-beta, got %s", histRec.Spec.GetVersion())
	}
	if histCoords.GetServiceName() != "snap-service-v1" || histCoords.GetPort() != 8000 {
		t.Errorf("expected historical coords snap-service-v1:8000, got %v", histCoords)
	}
}

func TestSpannerStore_ValidateComponentRequirement(t *testing.T) {
	ctx := context.Background()
	s, _ := setupTestStore(ctx, t)

	spec := &componentspb.ComponentSpec{
		Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version: "v2.3.4",
	}
	_, err := s.SetComponentIntent(ctx, "req-comp", spec, nil, 1)
	if err != nil {
		t.Fatalf("SetComponentIntent failed: %v", err)
	}

	// 1. Component is in PENDING_RECONCILIATION -> not yet satisfied
	res, err := s.ValidateComponentRequirement(ctx, "req-comp", componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT, "v2.0.0")
	if err != nil {
		t.Fatalf("ValidateComponentRequirement failed: %v", err)
	}
	if res.IsSatisfied {
		t.Errorf("expected IsSatisfied=false when component is not ENABLED")
	}

	// Transition to ENABLED
	_, _ = s.UpdateObservedGeneration(ctx, "req-comp", 1, nil, componentspb.ComponentState_COMPONENT_STATE_ENABLED, "")

	// 2. Satisfied requirement
	res, err = s.ValidateComponentRequirement(ctx, "req-comp", componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT, "v2.0.0")
	if err != nil {
		t.Fatalf("ValidateComponentRequirement failed: %v", err)
	}
	if !res.IsSatisfied {
		t.Errorf("expected IsSatisfied=true, got reason: %s", res.Reason)
	}

	// 3. Version constraint failure
	res, err = s.ValidateComponentRequirement(ctx, "req-comp", componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT, "v3.0.0")
	if err != nil {
		t.Fatalf("ValidateComponentRequirement failed: %v", err)
	}
	if res.IsSatisfied {
		t.Errorf("expected IsSatisfied=false for version v3.0.0 > v2.3.4")
	}

	// 4. Type constraint failure
	res, err = s.ValidateComponentRequirement(ctx, "req-comp", componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER, "v2.0.0")
	if err != nil {
		t.Fatalf("ValidateComponentRequirement failed: %v", err)
	}
	if res.IsSatisfied {
		t.Errorf("expected IsSatisfied=false for type mismatch")
	}

	// 5. Non-existent component
	res, err = s.ValidateComponentRequirement(ctx, "non-existent", componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT, "")
	if err != nil {
		t.Fatalf("ValidateComponentRequirement failed: %v", err)
	}
	if res.IsSatisfied {
		t.Errorf("expected IsSatisfied=false for non-existent component")
	}

	// 6. Invalid semver string
	if _, err := s.ValidateComponentRequirement(ctx, "req-comp", componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT, "invalid-semver-string"); status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument for invalid semver, got %v", err)
	}
}

func TestSpannerStore_GetPlatformHealth(t *testing.T) {
	ctx := context.Background()
	s, _ := setupTestStore(ctx, t)

	_, err := s.GetPlatformHealth(ctx, "", 10, "")
	if status.Code(err) != codes.Unimplemented {
		t.Errorf("expected Unimplemented code for GetPlatformHealth, got %v", err)
	}
}

func TestSpannerStore_ConcurrentOperations(t *testing.T) {
	ctx := context.Background()
	s, _ := setupTestStore(ctx, t)

	spec := &componentspb.ComponentSpec{
		Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version: "v1.0.0",
	}

	const numGoroutines = 8
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		compName := fmt.Sprintf("concurrent-comp-%d", i)
		go func(name string) {
			defer wg.Done()
			_, err := s.SetComponentIntent(ctx, name, spec, map[string]string{"worker": name}, 1)
			if err != nil {
				t.Errorf("concurrent SetComponentIntent failed for %s: %v", name, err)
			}
			_, err = s.GetComponent(ctx, name)
			if err != nil {
				t.Errorf("concurrent GetComponent failed for %s: %v", name, err)
			}
		}(compName)
	}

	wg.Wait()

	list, err := s.ListComponents(ctx)
	if err != nil {
		t.Fatalf("ListComponents failed after concurrent operations: %v", err)
	}
	if len(list) != numGoroutines {
		t.Errorf("expected %d components, got %d", numGoroutines, len(list))
	}
}
