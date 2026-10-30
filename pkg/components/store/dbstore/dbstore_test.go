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

package dbstore_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/pkg/components/store"
	"xmanager_cloud/pkg/components/store/dbstore"
)

// setupTestStore returns a Store on a fresh Spanner emulator database and the hooks that reach its
// rows.
func setupTestStore(ctx context.Context, t *testing.T) (*dbstore.Store, backendRows) {
	t.Helper()
	return spannerBackend().newStore(ctx, t)
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
	s, db := setupTestStore(ctx, t)

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
	_, storedUpdateTime, _ := readComponentRowTimestamps(ctx, t, db, updated.ComponentID)
	if !updated.UpdateTime.Equal(storedUpdateTime) {
		t.Errorf("UpdateObservedGeneration UpdateTime = %v, want commit timestamp %v", updated.UpdateTime, storedUpdateTime)
	}
	if wantEtag := store.GenerateEtag(updated); updated.Etag != wantEtag {
		t.Errorf("UpdateObservedGeneration Etag = %q, want %q", updated.Etag, wantEtag)
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

func TestSpannerStore_CommitTimestamps(t *testing.T) {
	ctx := context.Background()
	s, db := setupTestStore(ctx, t)
	declareOwnerCluster(ctx, t, s)

	const name = "commit-ts-comp"

	// 1. Create: CreateTime, UpdateTime, SourceLastSeenTime, and Etag match the commit timestamp.
	created, err := s.SetSourcedComponentIntent(ctx, name, intentSpec("v1.0.0"), nil, ownerSource)
	if err != nil {
		t.Fatalf("SetSourcedComponentIntent(create) failed: %v", err)
	}
	rawCreate, rawUpdate, rawLastSeen := readComponentRowTimestamps(ctx, t, db, created.ComponentID)
	if !created.CreateTime.Equal(rawCreate) || !created.UpdateTime.Equal(rawUpdate) || !created.SourceLastSeenTime.Equal(rawLastSeen) {
		t.Errorf("create timestamps = (%v, %v, %v), want (%v, %v, %v)",
			created.CreateTime, created.UpdateTime, created.SourceLastSeenTime, rawCreate, rawUpdate, rawLastSeen)
	}
	if wantEtag := store.GenerateEtag(created); created.Etag != wantEtag {
		t.Errorf("create Etag = %q, want %q", created.Etag, wantEtag)
	}

	// 2. Spec change: UpdateTime, SourceLastSeenTime, and Etag advance to the new commit timestamp; CreateTime is kept.
	specChanged, err := s.SetSourcedComponentIntent(ctx, name, intentSpec("v1.1.0"), nil, ownerSource)
	if err != nil {
		t.Fatalf("SetSourcedComponentIntent(spec change) failed: %v", err)
	}
	rawCreate, rawUpdate, rawLastSeen = readComponentRowTimestamps(ctx, t, db, created.ComponentID)
	if !specChanged.CreateTime.Equal(created.CreateTime) || !specChanged.CreateTime.Equal(rawCreate) {
		t.Errorf("spec change CreateTime = %v, want preserved %v", specChanged.CreateTime, created.CreateTime)
	}
	if !specChanged.UpdateTime.Equal(rawUpdate) || !specChanged.UpdateTime.After(created.UpdateTime) {
		t.Errorf("spec change UpdateTime = %v, want %v (after %v)", specChanged.UpdateTime, rawUpdate, created.UpdateTime)
	}
	if !specChanged.SourceLastSeenTime.Equal(rawLastSeen) {
		t.Errorf("spec change SourceLastSeenTime = %v, want %v", specChanged.SourceLastSeenTime, rawLastSeen)
	}
	if wantEtag := store.GenerateEtag(specChanged); specChanged.Etag != wantEtag || specChanged.Etag == created.Etag {
		t.Errorf("spec change Etag = %q, want %q (distinct from %q)", specChanged.Etag, wantEtag, created.Etag)
	}

	// 3. Owner-only write: SourceLastSeenTime advances to the commit timestamp; UpdateTime and Etag stay unchanged.
	newerSrc := sourceWith(func(src *store.RequestSource) { src.Generation = ownerSource.Generation + 1 })
	ownerOnly, err := s.SetSourcedComponentIntent(ctx, name, intentSpec("v1.1.0"), nil, newerSrc)
	if err != nil {
		t.Fatalf("SetSourcedComponentIntent(owner-only) failed: %v", err)
	}
	_, rawUpdate, rawLastSeen = readComponentRowTimestamps(ctx, t, db, created.ComponentID)
	if !ownerOnly.UpdateTime.Equal(specChanged.UpdateTime) || !ownerOnly.UpdateTime.Equal(rawUpdate) {
		t.Errorf("owner-only UpdateTime = %v, want unchanged %v", ownerOnly.UpdateTime, specChanged.UpdateTime)
	}
	if !ownerOnly.SourceLastSeenTime.Equal(rawLastSeen) || !ownerOnly.SourceLastSeenTime.After(specChanged.SourceLastSeenTime) {
		t.Errorf("owner-only SourceLastSeenTime = %v, want %v (after %v)", ownerOnly.SourceLastSeenTime, rawLastSeen, specChanged.SourceLastSeenTime)
	}
	if ownerOnly.Etag != specChanged.Etag || ownerOnly.Etag != store.GenerateEtag(ownerOnly) {
		t.Errorf("owner-only Etag = %q, want unchanged %q", ownerOnly.Etag, specChanged.Etag)
	}

	// 4. Changed observation followed by a sourced unchanged heartbeat.
	observed, err := s.UpdateObservedGeneration(ctx, name, 2, nil, componentspb.ComponentState_COMPONENT_STATE_ENABLED, "Ready", newerSrc)
	if err != nil {
		t.Fatalf("UpdateObservedGeneration(changed) failed: %v", err)
	}
	_, rawUpdate, rawLastSeen = readComponentRowTimestamps(ctx, t, db, created.ComponentID)
	if !observed.UpdateTime.Equal(rawUpdate) || !observed.SourceLastSeenTime.Equal(rawLastSeen) {
		t.Errorf("observed timestamps = (%v, %v), want (%v, %v)", observed.UpdateTime, observed.SourceLastSeenTime, rawUpdate, rawLastSeen)
	}
	if wantEtag := store.GenerateEtag(observed); observed.Etag != wantEtag {
		t.Errorf("observed Etag = %q, want %q", observed.Etag, wantEtag)
	}

	heartbeat, err := s.UpdateObservedGeneration(ctx, name, 2, nil, componentspb.ComponentState_COMPONENT_STATE_ENABLED, "Ready", newerSrc)
	if err != nil {
		t.Fatalf("UpdateObservedGeneration(unchanged heartbeat) failed: %v", err)
	}
	_, rawUpdate, rawLastSeen = readComponentRowTimestamps(ctx, t, db, created.ComponentID)
	if !heartbeat.UpdateTime.Equal(observed.UpdateTime) || !heartbeat.UpdateTime.Equal(rawUpdate) {
		t.Errorf("heartbeat UpdateTime = %v, want unchanged %v", heartbeat.UpdateTime, observed.UpdateTime)
	}
	if !heartbeat.SourceLastSeenTime.Equal(rawLastSeen) || !heartbeat.SourceLastSeenTime.After(observed.SourceLastSeenTime) {
		t.Errorf("heartbeat SourceLastSeenTime = %v, want %v (after %v)", heartbeat.SourceLastSeenTime, rawLastSeen, observed.SourceLastSeenTime)
	}
	if heartbeat.Etag != observed.Etag || heartbeat.Etag != store.GenerateEtag(heartbeat) {
		t.Errorf("heartbeat Etag = %q, want unchanged %q", heartbeat.Etag, observed.Etag)
	}
}

func readComponentRowTimestamps(ctx context.Context, t *testing.T, db backendRows, componentID int64) (createTime, updateTime, lastSeenTime time.Time) {
	t.Helper()
	ts, err := db.ComponentRowTimestamps(ctx, componentID)
	if err != nil {
		t.Fatalf("ComponentRowTimestamps(%d) failed: %v", componentID, err)
	}
	return ts.Create, ts.Update, ts.LastSeen
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
	if diff := cmp.Diff([]string{"client-app"}, res.CascadedDeactivations); diff != "" {
		t.Errorf("CascadedDeactivations mismatch (-want +got):\n%s", diff)
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
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("expected FailedPrecondition, got %v", status.Code(err))
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

func TestSpannerStore_CompleteDeactivation_TransitiveInactiveChain(t *testing.T) {
	ctx := context.Background()
	s, _ := setupTestStore(ctx, t)

	spec := &componentspb.ComponentSpec{
		Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version: "v1.0.0",
	}

	// Chain: a-active -> b-mid -> c-base
	if _, err := s.SetComponentIntent(ctx, "c-base", spec, nil, 1); err != nil {
		t.Fatalf("failed to create c-base: %v", err)
	}
	if _, err := s.SetComponentIntent(ctx, "b-mid", spec, map[string]string{"xmc.dev/depends-on": "c-base"}, 1); err != nil {
		t.Fatalf("failed to create b-mid: %v", err)
	}
	if _, err := s.SetComponentIntent(ctx, "a-active", spec, map[string]string{"xmc.dev/depends-on": "b-mid"}, 1); err != nil {
		t.Fatalf("failed to create a-active: %v", err)
	}

	// Make sure all are ENABLED
	for _, name := range []string{"c-base", "b-mid", "a-active"} {
		if _, err := s.UpdateObservedGeneration(ctx, name, 1, &componentspb.ComponentAccessStatus{State: componentspb.ComponentState_COMPONENT_STATE_ENABLED}, componentspb.ComponentState_COMPONENT_STATE_ENABLED, "ready"); err != nil {
			t.Fatalf("failed to mark %s ENABLED: %v", name, err)
		}
	}

	// Disable b-mid directly (Prepare + Complete with force or single)
	if _, err := s.PrepareDeactivation(ctx, "b-mid", true); err != nil {
		t.Fatalf("failed to prepare b-mid: %v", err)
	}
	if err := s.CompleteDeactivation(ctx, "b-mid", true); err != nil {
		t.Fatalf("failed to complete b-mid: %v", err)
	}

	// Prepare c-base for deactivation with cascade=true (transitions c-base and a-active to DISABLING)
	if _, err := s.PrepareDeactivation(ctx, "c-base", true); err != nil {
		t.Fatalf("failed to prepare c-base: %v", err)
	}

	// Try to CompleteDeactivation on c-base while a-active is still in DISABLING (not yet DISABLED)
	err := s.CompleteDeactivation(ctx, "c-base", false)
	if err == nil {
		t.Fatalf("expected CompleteDeactivation on c-base to fail because transitive dependent a-active is still active (in DISABLING)")
	}

	// Complete deactivation of a-active first
	if err := s.CompleteDeactivation(ctx, "a-active", false); err != nil {
		t.Fatalf("failed to complete a-active: %v", err)
	}

	// Now CompleteDeactivation on c-base should succeed
	if err := s.CompleteDeactivation(ctx, "c-base", false); err != nil {
		t.Fatalf("expected CompleteDeactivation on c-base to succeed after all transitive dependents are disabled, got: %v", err)
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
	s, db := setupTestStore(ctx, t)

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

	// Step 4: Verify directly in the database that both records exist in Components table
	rows, err := db.ComponentRows(ctx, "recycled-comp")
	if err != nil {
		t.Fatalf("failed to query raw Components table: %v", err)
	}

	if len(rows) != 2 {
		t.Fatalf("expected 2 historical/active rows in Components table, got %d", len(rows))
	}

	var deletedRow, activeRow *componentRow
	for i := range rows {
		if rows[i].ID == origID {
			deletedRow = &rows[i]
		}
		if rows[i].ID == newID {
			activeRow = &rows[i]
		}
	}

	if deletedRow == nil || deletedRow.State != 5 || deletedRow.ActiveName != nil {
		t.Errorf("expected deleted row: id=%d, state=5, activeName=NULL; got: %v", origID, deletedRow)
	}
	if activeRow == nil || activeRow.State == 5 || activeRow.ActiveName == nil || *activeRow.ActiveName != "recycled-comp" {
		t.Errorf("expected active row: id=%d, activeName='recycled-comp'; got: %v", newID, activeRow)
	}
}

func TestSpannerStore_ResolveComponentSnapshot(t *testing.T) {
	ctx := context.Background()
	s, db := setupTestStore(ctx, t)

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
	seedExperimentWithSnapshot(ctx, t, db, 12345, rec.ComponentID, 1, histConfigBytes, histAccessBytes)

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

func TestSpannerStore_ResolveComponentSnapshot_SoftDeleted(t *testing.T) {
	ctx := context.Background()
	s, db := setupTestStore(ctx, t)

	spec := &componentspb.ComponentSpec{
		Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version: "v1.0.0",
	}
	rec, err := s.SetComponentIntent(ctx, "soft-del-comp", spec, nil, 1)
	if err != nil {
		t.Fatalf("SetComponentIntent failed: %v", err)
	}

	access := &componentspb.ComponentAccessStatus{
		State: componentspb.ComponentState_COMPONENT_STATE_ENABLED,
		Coordinates: &componentspb.ResolvedComponentCoordinates{
			ClusterName: "gke-cluster",
			Namespace:   "default",
			ServiceName: "soft-del-service",
			Port:        8080,
		},
	}
	_, err = s.UpdateObservedGeneration(ctx, "soft-del-comp", 1, access, componentspb.ComponentState_COMPONENT_STATE_ENABLED, "")
	if err != nil {
		t.Fatalf("UpdateObservedGeneration failed: %v", err)
	}

	// Insert historical snapshot in ExperimentComponentSnapshots for gen 1
	histCompProto := &componentspb.Component{
		Name: "components/soft-del-comp",
		Spec: spec,
	}
	histConfigBytes, _ := proto.Marshal(histCompProto)
	histAccessBytes, _ := proto.Marshal(access)
	seedExperimentWithSnapshot(ctx, t, db, 20001, rec.ComponentID, 1, histConfigBytes, histAccessBytes)

	// Soft-delete the component via ExecuteDeletion
	if err := s.ExecuteDeletion(ctx, "soft-del-comp", componentspb.DeletionPolicy_DELETION_POLICY_DELETE, false); err != nil {
		t.Fatalf("ExecuteDeletion failed: %v", err)
	}

	// Active record is now soft-deleted (GetComponent returns NotFound)
	if _, err := s.GetComponent(ctx, "soft-del-comp"); status.Code(err) != codes.NotFound {
		t.Fatalf("expected NotFound for soft-deleted component, got %v", err)
	}

	// 1. Resolve gen 0 fails with NotFound because active component is deleted
	if _, _, err := s.ResolveComponentSnapshot(ctx, "soft-del-comp", 0); status.Code(err) != codes.NotFound {
		t.Errorf("expected NotFound resolving gen 0 on soft-deleted component, got %v", err)
	}

	// 2. Resolve historical gen 1 succeeds via resolveHistoricalSnapshotByName
	histRec, histCoords, err := s.ResolveComponentSnapshot(ctx, "soft-del-comp", 1)
	if err != nil {
		t.Fatalf("ResolveComponentSnapshot for historical gen 1 on soft-deleted component failed: %v", err)
	}
	if histRec.Generation != 1 {
		t.Errorf("expected generation 1, got %d", histRec.Generation)
	}
	if histRec.ComponentID != rec.ComponentID {
		t.Errorf("expected ComponentID %d, got %d", rec.ComponentID, histRec.ComponentID)
	}
	if histCoords.GetServiceName() != "soft-del-service" || histCoords.GetPort() != 8080 {
		t.Errorf("expected coordinates soft-del-service:8080, got %v", histCoords)
	}

	// 3. Resolve historical generation that does not exist returns NotFound
	if _, _, err := s.ResolveComponentSnapshot(ctx, "soft-del-comp", 99); status.Code(err) != codes.NotFound {
		t.Errorf("expected NotFound for non-existent generation 99, got %v", err)
	}
}

func TestSpannerStore_ResolveComponentSnapshot_RecreatedLowerGeneration(t *testing.T) {
	ctx := context.Background()
	s, db := setupTestStore(ctx, t)

	specV1 := &componentspb.ComponentSpec{
		Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version: "v1.0.0",
	}
	rec1, err := s.SetComponentIntent(ctx, "reincarnated-comp", specV1, nil, 1)
	if err != nil {
		t.Fatalf("SetComponentIntent reincarnation 1 failed: %v", err)
	}
	_, err = s.SetComponentIntent(ctx, "reincarnated-comp", specV1, nil, 2)
	if err != nil {
		t.Fatalf("SetComponentIntent gen 2 failed: %v", err)
	}
	// Generation 3 carries a distinguishable version so the resolved snapshot payload can be
	// attributed to this incarnation rather than the one recreated later.
	specV3 := &componentspb.ComponentSpec{
		Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version: "v1.3.0-historical",
	}
	_, err = s.SetComponentIntent(ctx, "reincarnated-comp", specV3, nil, 3)
	if err != nil {
		t.Fatalf("SetComponentIntent gen 3 failed: %v", err)
	}

	accessV3 := &componentspb.ComponentAccessStatus{
		State: componentspb.ComponentState_COMPONENT_STATE_ENABLED,
		Coordinates: &componentspb.ResolvedComponentCoordinates{
			ClusterName: "gke-cluster",
			Namespace:   "default",
			ServiceName: "reincarnated-v3",
			Port:        3000,
		},
	}
	_, err = s.UpdateObservedGeneration(ctx, "reincarnated-comp", 3, accessV3, componentspb.ComponentState_COMPONENT_STATE_ENABLED, "")
	if err != nil {
		t.Fatalf("UpdateObservedGeneration gen 3 failed: %v", err)
	}

	// Snapshot for gen 3 under reincarnation 1
	compProto3 := &componentspb.Component{
		Name: "components/reincarnated-comp",
		Spec: specV3,
	}
	configBytes3, _ := proto.Marshal(compProto3)
	accessBytes3, _ := proto.Marshal(accessV3)
	seedExperimentWithSnapshot(ctx, t, db, 20002, rec1.ComponentID, 3, configBytes3, accessBytes3)

	// Soft-delete reincarnation 1 via ExecuteDeletion
	if err := s.ExecuteDeletion(ctx, "reincarnated-comp", componentspb.DeletionPolicy_DELETION_POLICY_DELETE, false); err != nil {
		t.Fatalf("ExecuteDeletion failed: %v", err)
	}

	// Re-create component under the same name with Generation 1
	specV2 := &componentspb.ComponentSpec{
		Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version: "v2.0.0",
	}
	rec2, err := s.SetComponentIntent(ctx, "reincarnated-comp", specV2, nil, 1)
	if err != nil {
		t.Fatalf("SetComponentIntent reincarnation 2 failed: %v", err)
	}
	if rec2.ComponentID == rec1.ComponentID {
		t.Fatalf("expected new ComponentID for reincarnated component, got same %d", rec2.ComponentID)
	}

	// Active component has rec2.Generation == 1.
	// Resolving targetGen 3 (which is > rec2.Generation) falls back to resolveHistoricalSnapshotByName and finds rec1's gen 3 snapshot.
	histRec, histCoords, err := s.ResolveComponentSnapshot(ctx, "reincarnated-comp", 3)
	if err != nil {
		t.Fatalf("ResolveComponentSnapshot for gen 3 (> active gen 1) failed: %v", err)
	}
	if histRec.ComponentID != rec1.ComponentID {
		t.Errorf("expected historical ComponentID %d, got %d", rec1.ComponentID, histRec.ComponentID)
	}
	if histRec.Generation != 3 {
		t.Errorf("expected generation 3, got %d", histRec.Generation)
	}
	if got := histRec.Spec.GetVersion(); got != "v1.3.0-historical" {
		t.Errorf("expected historical spec version v1.3.0-historical, got %q (live incarnation is %q)", got, specV2.GetVersion())
	}
	if histCoords.GetServiceName() != "reincarnated-v3" || histCoords.GetPort() != 3000 {
		t.Errorf("expected coordinates reincarnated-v3:3000, got %v", histCoords)
	}
}

func TestSpannerStore_ResolveComponentSnapshot_HistoricalDependenciesAndStateIsolation(t *testing.T) {
	ctx := context.Background()
	s, db := setupTestStore(ctx, t)

	// Active component has state ENABLED, observed generation 2, active labels
	activeSpec := &componentspb.ComponentSpec{
		Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version: "v2.0.0",
	}
	activeLabels := map[string]string{"env": "production", "active": "true"}
	rec, err := s.SetComponentIntent(ctx, "isolated-comp", activeSpec, activeLabels, 2)
	if err != nil {
		t.Fatalf("SetComponentIntent failed: %v", err)
	}

	activeAccess := &componentspb.ComponentAccessStatus{
		State: componentspb.ComponentState_COMPONENT_STATE_ENABLED,
		Coordinates: &componentspb.ResolvedComponentCoordinates{
			ClusterName: "gke-cluster",
			Namespace:   "default",
			ServiceName: "active-service",
			Port:        8000,
		},
	}
	_, err = s.UpdateObservedGeneration(ctx, "isolated-comp", 2, activeAccess, componentspb.ComponentState_COMPONENT_STATE_ENABLED, "")
	if err != nil {
		t.Fatalf("UpdateObservedGeneration failed: %v", err)
	}

	// Historical snapshot for gen 1 with different spec, dependencies (via directives/labels), and historical labels
	histSpec := &componentspb.ComponentSpec{
		Type:        componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version:     "v1.0.0",
		Description: "Historical worker component\n@depends-on: dep-service-a, dep-service-b",
	}
	histLabels := map[string]string{
		"env":                  "staging",
		"xmc.dev/dependencies": "dep-service-c",
	}
	histCompProto := &componentspb.Component{
		Name:   "components/isolated-comp",
		Spec:   histSpec,
		Labels: histLabels,
	}
	histConfigBytes, _ := proto.Marshal(histCompProto)
	histAccessProto := &componentspb.ComponentAccessStatus{
		State:              componentspb.ComponentState_COMPONENT_STATE_DISABLED,
		ObservedGeneration: 1,
		Coordinates: &componentspb.ResolvedComponentCoordinates{
			ClusterName: "gke-cluster",
			Namespace:   "staging",
			ServiceName: "hist-service",
			Port:        7000,
		},
	}
	histAccessBytes, _ := proto.Marshal(histAccessProto)

	seedExperimentWithSnapshot(ctx, t, db, 20003, rec.ComponentID, 1, histConfigBytes, histAccessBytes)

	// Resolve gen 1 (via resolveHistoricalSnapshot)
	histRec, histCoords, err := s.ResolveComponentSnapshot(ctx, "isolated-comp", 1)
	if err != nil {
		t.Fatalf("ResolveComponentSnapshot gen 1 failed: %v", err)
	}

	// 1. Verify active state did not leak
	if histRec.State != componentspb.ComponentState_COMPONENT_STATE_DISABLED {
		t.Errorf("expected State DISABLED in historical snapshot, got %v", histRec.State)
	}
	if histRec.ObservedGeneration == nil || *histRec.ObservedGeneration != 1 {
		t.Errorf("expected ObservedGeneration 1 in historical snapshot, got %v", histRec.ObservedGeneration)
	}

	// 2. Verify historical labels, not active labels
	if diff := cmp.Diff(histLabels, histRec.Labels); diff != "" {
		t.Errorf("historical labels mismatch (-want +got):\n%s", diff)
	}

	// 3. Verify historical dependencies extracted via ExtractDependencies
	wantDeps := []string{"dep-service-a", "dep-service-b", "dep-service-c"}
	if diff := cmp.Diff(wantDeps, histRec.Dependencies); diff != "" {
		t.Errorf("historical dependencies mismatch (-want +got):\n%s", diff)
	}

	// 4. Verify coordinates
	if histCoords.GetServiceName() != "hist-service" || histCoords.GetPort() != 7000 {
		t.Errorf("expected coordinates hist-service:7000, got %v", histCoords)
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

// seedExperimentWithSnapshot creates an ACTIVE experiment for xid and binds componentID to it via
// an ExperimentComponentSnapshots row locked at lockedGen.
func seedExperimentWithSnapshot(ctx context.Context, t *testing.T, db backendRows, xid, componentID, lockedGen int64, configProto, accessPayload []byte) {
	t.Helper()
	testExp := &essmsgpb.Experiment{
		Name:      fmt.Sprintf("experiments/%d", xid),
		Title:     fmt.Sprintf("Test Experiment %d", xid),
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
	snap := snapshotRow{Xid: xid, ComponentID: componentID, LockedGeneration: lockedGen, ConfigProto: configProto, AccessPayload: accessPayload}
	if err := db.InsertExperimentWithSnapshot(ctx, testExp, snap); err != nil {
		t.Fatalf("failed to seed experiment %d with snapshot: %v", xid, err)
	}
}

func TestSpannerStore_SoftDeletion_PreservesSnapshotForeignKey(t *testing.T) {
	ctx := context.Background()
	s, db := setupTestStore(ctx, t)

	spec := &componentspb.ComponentSpec{
		Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version: "v1.0.0",
	}
	if _, err := s.SetComponentIntent(ctx, "fk-comp", spec, nil, 1); err != nil {
		t.Fatalf("SetComponentIntent failed: %v", err)
	}
	access := &componentspb.ComponentAccessStatus{
		State:              componentspb.ComponentState_COMPONENT_STATE_ENABLED,
		ObservedGeneration: 1,
	}
	rec, err := s.UpdateObservedGeneration(ctx, "fk-comp", 1, access, componentspb.ComponentState_COMPONENT_STATE_ENABLED, "")
	if err != nil {
		t.Fatalf("UpdateObservedGeneration failed: %v", err)
	}

	compProto := &componentspb.Component{Name: "components/fk-comp", Spec: spec}
	configBytes, _ := proto.Marshal(compProto)
	accessBytes, _ := proto.Marshal(access)
	seedExperimentWithSnapshot(ctx, t, db, 20004, rec.ComponentID, 1, configBytes, accessBytes)

	// A hard row delete must be rejected: FK_Snapshots_Component still references this component.
	err = db.HardDeleteComponent(ctx, rec.ComponentID)
	if err == nil {
		t.Fatalf("hard DELETE on Components succeeded, want rejection by FK_Snapshots_Component")
	}
	errStr := strings.ToLower(err.Error())
	if !strings.Contains(errStr, "foreign key") && !strings.Contains(errStr, "fk_snapshots_component") && status.Code(err) != codes.FailedPrecondition {
		t.Errorf("expected a foreign key violation, got: %v", err)
	}

	// Soft deletion is the supported path and must leave the row (and therefore the FK) intact.
	if err := s.ExecuteDeletion(ctx, "fk-comp", componentspb.DeletionPolicy_DELETION_POLICY_DELETE, false); err != nil {
		t.Fatalf("ExecuteDeletion failed: %v", err)
	}

	rows, err := db.ComponentRows(ctx, "fk-comp")
	if err != nil || len(rows) != 1 || rows[0].ID != rec.ComponentID {
		t.Fatalf("soft-deleted Components row is unreadable: rows = %+v, error = %v", rows, err)
	}
	if want := int64(componentspb.ComponentState_COMPONENT_STATE_DELETED); rows[0].State != want {
		t.Errorf("State = %d, want %d (DELETED)", rows[0].State, want)
	}
	if rows[0].ActiveName != nil {
		t.Errorf("ActiveName = %q, want NULL so the name can be reused", *rows[0].ActiveName)
	}

	snapCount, err := db.CountSnapshots(ctx, rec.ComponentID)
	if err != nil {
		t.Fatalf("failed to count snapshots: %v", err)
	}
	if snapCount != 1 {
		t.Errorf("snapshot count = %d, want 1 preserved after soft deletion", snapCount)
	}
}

func TestSpannerStore_ConcurrentIntentUpdatesOnSameComponent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	s, _ := setupTestStore(ctx, t)

	// The seed version must differ from every writer's version below; an unchanged intent is a
	// no-op and would not bump the generation.
	spec := &componentspb.ComponentSpec{
		Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version: "v100.0.0",
	}
	if _, err := s.SetComponentIntent(ctx, "contended-comp", spec, nil, 1); err != nil {
		t.Fatalf("SetComponentIntent failed: %v", err)
	}
	access := &componentspb.ComponentAccessStatus{
		State:              componentspb.ComponentState_COMPONENT_STATE_ENABLED,
		ObservedGeneration: 1,
	}
	if _, err := s.UpdateObservedGeneration(ctx, "contended-comp", 1, access, componentspb.ComponentState_COMPONENT_STATE_ENABLED, ""); err != nil {
		t.Fatalf("UpdateObservedGeneration failed: %v", err)
	}

	// Unlike TestSpannerStore_ConcurrentOperations, every worker contends for the same row, so
	// Spanner's 2PL must serialize the read-modify-write generation bumps rather than interleave
	// them. Generations are collected to assert no two writers were handed the same one.
	const (
		numWriters       = 8
		writerIterations = 4
	)
	var (
		mu       sync.Mutex
		observed = make(map[int64]int)
		wg       sync.WaitGroup
	)
	wg.Add(numWriters)
	for w := 0; w < numWriters; w++ {
		go func(workerID int) {
			defer wg.Done()
			for it := 0; it < writerIterations; it++ {
				wSpec := &componentspb.ComponentSpec{
					Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
					Version: fmt.Sprintf("v%d.%d.0", workerID, it),
				}
				// Generation 0 asks the store to auto-increment, which is the contended path.
				rec, err := s.SetComponentIntent(ctx, "contended-comp", wSpec, nil, 0)
				if err != nil {
					t.Errorf("writer %d iteration %d: SetComponentIntent failed: %v", workerID, it, err)
					return
				}
				mu.Lock()
				observed[rec.Generation]++
				mu.Unlock()
			}
		}(w)
	}
	wg.Wait()

	for gen, count := range observed {
		if count > 1 {
			t.Errorf("generation %d was handed out %d times, want 1 (lost update under contention)", gen, count)
		}
	}

	finalRec, err := s.GetComponent(ctx, "contended-comp")
	if err != nil {
		t.Fatalf("GetComponent failed after concurrent writes: %v", err)
	}
	if want := int64(1 + numWriters*writerIterations); finalRec.Generation != want {
		t.Errorf("final Generation = %d, want %d (one bump per serialized write)", finalRec.Generation, want)
	}
	// Every writer bumped Generation without a matching UpdateObservedGeneration, so the last
	// write must leave the component awaiting reconciliation of its newest generation.
	if finalRec.State != componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION {
		t.Errorf("final State = %v, want PENDING_RECONCILIATION", finalRec.State)
	}
}

func TestSpannerStore_ConcurrentDeactivationAndIntentUpdate(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	s, _ := setupTestStore(ctx, t)

	spec := &componentspb.ComponentSpec{
		Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version: "v1.0.0",
	}
	if _, err := s.SetComponentIntent(ctx, "conflict-comp", spec, nil, 1); err != nil {
		t.Fatalf("SetComponentIntent failed: %v", err)
	}
	access := &componentspb.ComponentAccessStatus{
		State:              componentspb.ComponentState_COMPONENT_STATE_ENABLED,
		ObservedGeneration: 1,
	}
	if _, err := s.UpdateObservedGeneration(ctx, "conflict-comp", 1, access, componentspb.ComponentState_COMPONENT_STATE_ENABLED, ""); err != nil {
		t.Fatalf("UpdateObservedGeneration failed: %v", err)
	}

	// Deactivation and intent updates mutate the same row. Either may lose the race, but neither
	// may leave the record in a state outside the lifecycle contract.
	const concurrentPairs = 4
	var wg sync.WaitGroup
	wg.Add(concurrentPairs * 2)
	for i := 0; i < concurrentPairs; i++ {
		go func(workerID int) {
			defer wg.Done()
			if _, err := s.PrepareDeactivation(ctx, "conflict-comp", false); err != nil && status.Code(err) != codes.FailedPrecondition {
				t.Errorf("worker %d: PrepareDeactivation returned unexpected code %v: %v", workerID, status.Code(err), err)
			}
		}(i)

		go func(workerID int) {
			defer wg.Done()
			intentSpec := &componentspb.ComponentSpec{
				Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
				Version: fmt.Sprintf("v1.%d.0", workerID),
			}
			if _, err := s.SetComponentIntent(ctx, "conflict-comp", intentSpec, nil, 0); err != nil && status.Code(err) != codes.FailedPrecondition {
				t.Errorf("worker %d: SetComponentIntent returned unexpected code %v: %v", workerID, status.Code(err), err)
			}
		}(i)
	}
	wg.Wait()

	finalRec, err := s.GetComponent(ctx, "conflict-comp")
	if err != nil {
		t.Fatalf("GetComponent failed after concurrent deactivation and intent updates: %v", err)
	}
	legalStates := map[componentspb.ComponentState]bool{
		componentspb.ComponentState_COMPONENT_STATE_ENABLED:                true,
		componentspb.ComponentState_COMPONENT_STATE_DISABLING:              true,
		componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION: true,
	}
	if !legalStates[finalRec.State] {
		t.Errorf("final State = %v, want one of ENABLED, DISABLING, PENDING_RECONCILIATION", finalRec.State)
	}
	if finalRec.Generation < 1 {
		t.Errorf("final Generation = %d, want monotonically >= 1", finalRec.Generation)
	}
}
