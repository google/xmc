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

package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	postgrestest "xmanager_cloud/internal/storage/postgres/testing"
	"xmanager_cloud/pkg/components/naming"
	"xmanager_cloud/pkg/components/store"
)

func TestUpdateObservedGeneration(t *testing.T) {
	ctx := context.Background()
	_, pool := postgrestest.Setup(ctx, t)
	s, err := NewPostgresStore(pool)
	if err != nil {
		t.Fatalf("NewPostgresStore() failed: %v", err)
	}

	if _, err := s.DeclareClusterEpoch(ctx, store.ClusterIdentity{UID: testClusterAUID, Name: "cluster-a", Epoch: 1}); err != nil {
		t.Fatalf("DeclareClusterEpoch() failed: %v", err)
	}
	src := store.RequestSource{
		UID: "cr-obs-1", Generation: 3, Namespace: "xmc-system", ClusterUID: testClusterAUID, Epoch: 1,
	}
	spec := &componentspb.ComponentSpec{
		Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version: "1.0.0",
	}
	created, err := s.SetSourcedComponentIntent(ctx, "obs-comp", spec, nil, src)
	if err != nil {
		t.Fatalf("SetSourcedComponentIntent() failed: %v", err)
	}

	access := &componentspb.ComponentAccessStatus{
		Coordinates: &componentspb.ResolvedComponentCoordinates{
			ServiceName: "obs-comp-svc",
			Port:        8080,
		},
	}
	obs1, err := s.UpdateObservedGeneration(ctx, "obs-comp", 1, access, componentspb.ComponentState_COMPONENT_STATE_ENABLED, "ready", src)
	if err != nil {
		t.Fatalf("UpdateObservedGeneration(1) failed: %v", err)
	}
	if obs1.ObservedGeneration == nil || *obs1.ObservedGeneration != 1 || obs1.State != componentspb.ComponentState_COMPONENT_STATE_ENABLED {
		t.Fatalf("obs1 = (obsGen=%v, state=%v), want (1, ENABLED)", obs1.ObservedGeneration, obs1.State)
	}
	if obs1.Owner == nil || obs1.Owner.Generation != 3 {
		t.Errorf("obs1.Owner = %+v, want Generation=3 preserved", obs1.Owner)
	}
	if !obs1.CreateTime.Equal(created.CreateTime) {
		t.Errorf("CreateTime changed: got %v, want %v", obs1.CreateTime, created.CreateTime)
	}

	// Unchanged observation heartbeat preserves UpdateTime and Etag while refreshing SourceLastSeenTime.
	time.Sleep(2 * time.Millisecond)
	obs2, err := s.UpdateObservedGeneration(ctx, "obs-comp", 1, access, componentspb.ComponentState_COMPONENT_STATE_ENABLED, "ready", src)
	if err != nil {
		t.Fatalf("UpdateObservedGeneration(heartbeat) failed: %v", err)
	}
	if !obs2.UpdateTime.Equal(obs1.UpdateTime) || obs2.Etag != obs1.Etag {
		t.Errorf("heartbeat changed UpdateTime/Etag: (%v, %q) vs (%v, %q)", obs2.UpdateTime, obs2.Etag, obs1.UpdateTime, obs1.Etag)
	}
	if !obs2.SourceLastSeenTime.After(obs1.SourceLastSeenTime) {
		t.Errorf("heartbeat SourceLastSeenTime = %v, want after %v", obs2.SourceLastSeenTime, obs1.SourceLastSeenTime)
	}
}

func TestDeactivationAndDeletionLifecycle(t *testing.T) {
	ctx := context.Background()
	_, pool := postgrestest.Setup(ctx, t)
	s, err := NewPostgresStore(pool)
	if err != nil {
		t.Fatalf("NewPostgresStore() failed: %v", err)
	}

	baseSpec := &componentspb.ComponentSpec{
		Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version: "1.0.0",
	}
	depSpec := &componentspb.ComponentSpec{
		Type:         componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version:      "1.0.0",
		Dependencies: []string{"core-comp"},
	}
	if _, err := s.SetComponentIntent(ctx, "core-comp", baseSpec, nil, 1); err != nil {
		t.Fatalf("SetComponentIntent(core-comp) failed: %v", err)
	}
	if _, err := s.SetComponentIntent(ctx, "child-comp", depSpec, nil, 1); err != nil {
		t.Fatalf("SetComponentIntent(child-comp) failed: %v", err)
	}
	if _, err := s.UpdateObservedGeneration(ctx, "core-comp", 1, nil, componentspb.ComponentState_COMPONENT_STATE_ENABLED, "ok"); err != nil {
		t.Fatalf("UpdateObservedGeneration(core-comp) failed: %v", err)
	}
	if _, err := s.UpdateObservedGeneration(ctx, "child-comp", 1, nil, componentspb.ComponentState_COMPONENT_STATE_ENABLED, "ok"); err != nil {
		t.Fatalf("UpdateObservedGeneration(child-comp) failed: %v", err)
	}

	// Non-cascading PrepareDeactivation is rejected while child-comp is active.
	if _, err := s.PrepareDeactivation(ctx, "core-comp", false); status.Code(err) != codes.FailedPrecondition || store.ErrorInfoReason(err) != store.ReasonActiveDependentsExist {
		t.Fatalf("PrepareDeactivation(no cascade) = (%v, %q), want (FailedPrecondition, %q)", status.Code(err), store.ErrorInfoReason(err), store.ReasonActiveDependentsExist)
	}

	// Cascading PrepareDeactivation transitions both core-comp and child-comp to DISABLING.
	prepRes, err := s.PrepareDeactivation(ctx, "core-comp", true)
	if err != nil {
		t.Fatalf("PrepareDeactivation(cascade) failed: %v", err)
	}
	if !prepRes.IsSafeToDeactivate {
		t.Fatalf("PrepareDeactivation(cascade) = %+v, want safe", prepRes)
	}
	if diff := cmp.Diff([]string{"child-comp"}, prepRes.CascadedDeactivations); diff != "" {
		t.Fatalf("CascadedDeactivations diff (-want +got):\n%s", diff)
	}

	// CompleteDeactivation on core-comp without force fails while child-comp is still DISABLING (active).
	if err := s.CompleteDeactivation(ctx, "core-comp", false); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("CompleteDeactivation(core-comp, force=false) code = %v, want FailedPrecondition", status.Code(err))
	}

	// CompleteDeactivation on child-comp then core-comp succeeds.
	if err := s.CompleteDeactivation(ctx, "child-comp", false); err != nil {
		t.Fatalf("CompleteDeactivation(child-comp) failed: %v", err)
	}
	if err := s.CompleteDeactivation(ctx, "core-comp", false); err != nil {
		t.Fatalf("CompleteDeactivation(core-comp) failed: %v", err)
	}
	coreRec, err := s.GetComponent(ctx, "core-comp")
	if err != nil || coreRec.State != componentspb.ComponentState_COMPONENT_STATE_DISABLED {
		t.Fatalf("GetComponent(core-comp) = (%+v, %v), want DISABLED", coreRec, err)
	}

	// Cascading ExecuteDeletion soft-deletes both core-comp and child-comp.
	if err := s.ExecuteDeletion(ctx, "core-comp", componentspb.DeletionPolicy_DELETION_POLICY_DELETE, false); err != nil {
		t.Fatalf("ExecuteDeletion(core-comp) failed: %v", err)
	}
	if _, err := s.GetComponent(ctx, "core-comp"); status.Code(err) != codes.NotFound {
		t.Errorf("GetComponent(core-comp) after deletion = %v, want NotFound", err)
	}
	if _, err := s.GetComponent(ctx, "child-comp"); status.Code(err) != codes.NotFound {
		t.Errorf("GetComponent(child-comp) after cascading deletion = %v, want NotFound", err)
	}
}

func TestResolveComponentSnapshot(t *testing.T) {
	ctx := context.Background()
	_, pool := postgrestest.Setup(ctx, t)
	s, err := NewPostgresStore(pool)
	if err != nil {
		t.Fatalf("NewPostgresStore() failed: %v", err)
	}

	specV1 := &componentspb.ComponentSpec{
		Type:         componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version:      "1.0.0",
		Dependencies: []string{"snap-dep"},
	}
	depSpec := &componentspb.ComponentSpec{
		Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version: "1.0.0",
	}
	if _, err := s.SetComponentIntent(ctx, "snap-dep", depSpec, nil, 1); err != nil {
		t.Fatalf("SetComponentIntent(snap-dep) failed: %v", err)
	}
	recV1, err := s.SetComponentIntent(ctx, "snap-comp", specV1, map[string]string{"env": "v1"}, 1)
	if err != nil {
		t.Fatalf("SetComponentIntent(snap-comp v1) failed: %v", err)
	}
	coordsV1 := &componentspb.ResolvedComponentCoordinates{ServiceName: "snap-v1-svc", Port: 7000}
	if _, err := s.UpdateObservedGeneration(ctx, "snap-comp", 1, &componentspb.ComponentAccessStatus{Coordinates: coordsV1}, componentspb.ComponentState_COMPONENT_STATE_ENABLED, "v1 ready"); err != nil {
		t.Fatalf("UpdateObservedGeneration(v1) failed: %v", err)
	}

	// Seed historical snapshot for generation 1 in ExperimentComponentSnapshots.
	seedExperimentSnapshot(ctx, t, pool, 1001, recV1.ComponentID, "snap-comp", 1, specV1, map[string]string{"env": "v1"}, coordsV1)

	// Advance snap-comp to generation 2.
	specV2 := &componentspb.ComponentSpec{
		Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version: "2.0.0",
	}
	coordsV2 := &componentspb.ResolvedComponentCoordinates{ServiceName: "snap-v2-svc", Port: 8000}
	if _, err := s.SetComponentIntent(ctx, "snap-comp", specV2, nil, 2); err != nil {
		t.Fatalf("SetComponentIntent(snap-comp v2) failed: %v", err)
	}
	if _, err := s.UpdateObservedGeneration(ctx, "snap-comp", 2, &componentspb.ComponentAccessStatus{Coordinates: coordsV2}, componentspb.ComponentState_COMPONENT_STATE_ENABLED, "v2 ready"); err != nil {
		t.Fatalf("UpdateObservedGeneration(v2) failed: %v", err)
	}

	// Resolve latest (generation=0) returns active v2 record and coordinates.
	gotLatest, gotCoordsLatest, err := s.ResolveComponentSnapshot(ctx, "snap-comp", 0)
	if err != nil {
		t.Fatalf("ResolveComponentSnapshot(0) failed: %v", err)
	}
	if gotLatest.Generation != 2 {
		t.Errorf("ResolveComponentSnapshot(0) gen = %d, want 2", gotLatest.Generation)
	}
	if diff := cmp.Diff(coordsV2, gotCoordsLatest, protocmp.Transform()); diff != "" {
		t.Errorf("ResolveComponentSnapshot(0) coords diff (-want +got):\n%s", diff)
	}

	// Resolve historical generation 1 returns hydrated v1 snapshot.
	gotV1, gotCoordsV1, err := s.ResolveComponentSnapshot(ctx, "snap-comp", 1)
	if err != nil {
		t.Fatalf("ResolveComponentSnapshot(1) failed: %v", err)
	}
	if gotV1.Generation != 1 || gotV1.Spec.GetVersion() != "1.0.0" {
		t.Errorf("ResolveComponentSnapshot(1) = (gen=%d, version=%q), want (1, 1.0.0)", gotV1.Generation, gotV1.Spec.GetVersion())
	}
	if diff := cmp.Diff(coordsV1, gotCoordsV1, protocmp.Transform()); diff != "" {
		t.Errorf("ResolveComponentSnapshot(1) coords diff (-want +got):\n%s", diff)
	}

	// Soft-delete snap-comp and verify historical snapshot 1 still resolves by name.
	if err := s.ExecuteDeletion(ctx, "snap-comp", componentspb.DeletionPolicy_DELETION_POLICY_DELETE, true); err != nil {
		t.Fatalf("ExecuteDeletion(snap-comp) failed: %v", err)
	}
	gotDeletedV1, _, err := s.ResolveComponentSnapshot(ctx, "snap-comp", 1)
	if err != nil || gotDeletedV1.Generation != 1 {
		t.Errorf("ResolveComponentSnapshot(1 after delete) = (%+v, %v), want gen=1", gotDeletedV1, err)
	}
}

func seedExperimentSnapshot(ctx context.Context, t *testing.T, pool *pgxpool.Pool, expID, compID int64, name string, gen int64, spec *componentspb.ComponentSpec, labels map[string]string, coords *componentspb.ResolvedComponentCoordinates) {
	t.Helper()
	const expSQL = `INSERT INTO Experiments (Xid, Title, Creator, Priority, Urls, Tags, Notes, Resources, Status, ActiveState, LaunchState, CreateTime, UpdateTime)
VALUES ($1, 'exp', 'user', 0, '{}', '{}', '', '{}', ''::bytea, 1, 1, NOW(), NOW())`
	if _, err := pool.Exec(ctx, expSQL, expID); err != nil {
		t.Fatalf("insert experiment %d failed: %v", expID, err)
	}
	specClone := proto.Clone(spec).(*componentspb.ComponentSpec)
	specClone.Generation = gen
	configBytes, err := proto.Marshal(&componentspb.Component{
		Name:   naming.FullComponentName(name),
		Spec:   specClone,
		Labels: labels,
	})
	if err != nil {
		t.Fatalf("proto.Marshal(Component) failed: %v", err)
	}
	accessBytes, err := proto.Marshal(&componentspb.ComponentAccessStatus{
		State:              componentspb.ComponentState_COMPONENT_STATE_ENABLED,
		ObservedGeneration: gen,
		Coordinates:        coords,
	})
	if err != nil {
		t.Fatalf("proto.Marshal(ComponentAccessStatus) failed: %v", err)
	}
	const snapSQL = `INSERT INTO ExperimentComponentSnapshots (Xid, ComponentId, LockedGeneration, ConfigProto, AccessPayload) VALUES ($1, $2, $3, $4, $5)`
	if _, err := pool.Exec(ctx, snapSQL, expID, compID, gen, configBytes, accessBytes); err != nil {
		t.Fatalf("insert snapshot (%d, %d) failed: %v", expID, compID, err)
	}
}
