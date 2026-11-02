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

package component

import (
	"context"
	"errors"
	"maps"
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/spanner/emulator"
	"xmanager_cloud/pkg/components/naming"
	"xmanager_cloud/pkg/components/store"
)

var baseTime = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

func seedCatalog(t *testing.T, client *spanner.Client) (*store.ComponentRecord, *store.ComponentRecord) {
	t.Helper()
	app := buildRecord(1, "app", componentspb.ComponentState_COMPONENT_STATE_ENABLED, "auth", "db")
	db := buildRecord(2, "db", componentspb.ComponentState_COMPONENT_STATE_ENABLED)
	deleted := buildRecord(3, "cache", componentspb.ComponentState_COMPONENT_STATE_DELETED)
	auth := buildRecord(4, "auth", componentspb.ComponentState_COMPONENT_STATE_ENABLED)

	mutations := []*spanner.Mutation{
		insertComponentMutation(t, app),
		insertComponentMutation(t, db),
		insertComponentMutation(t, deleted),
		insertComponentMutation(t, auth),
		spanner.Insert("ComponentDependencies", []string{"ComponentId", "DependencyComponentId"}, []any{int64(1), int64(2)}),
		spanner.Insert("ComponentDependencies", []string{"ComponentId", "DependencyComponentId"}, []any{int64(1), int64(3)}),
		spanner.Insert("ComponentDependencies", []string{"ComponentId", "DependencyComponentId"}, []any{int64(1), int64(4)}),
	}
	if _, err := client.Apply(context.Background(), mutations); err != nil {
		t.Fatalf("Apply() seed failed: %v", err)
	}
	return app, db
}

func buildRecord(id int64, name string, state componentspb.ComponentState, deps ...string) *store.ComponentRecord {
	rec := &store.ComponentRecord{
		ComponentID:  id,
		Name:         name,
		Spec:         &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT, Version: "v1.2.0", Generation: 1},
		Status:       &componentspb.ComponentAccessStatus{State: state},
		State:        state,
		Generation:   1,
		Dependencies: deps,
		CreateTime:   baseTime,
		UpdateTime:   baseTime,
		Labels:       map[string]string{"env": "prod"},
	}
	rec.Etag = store.GenerateEtag(rec)
	return rec
}

func insertComponentMutation(t *testing.T, rec *store.ComponentRecord) *spanner.Mutation {
	t.Helper()
	cfgBytes, err := proto.Marshal(&componentspb.Component{
		Name:   naming.FullComponentName(rec.Name),
		Spec:   rec.Spec,
		Labels: rec.Labels,
	})
	if err != nil {
		t.Fatalf("proto.Marshal(Component) failed: %v", err)
	}
	statusBytes, err := proto.Marshal(rec.Status)
	if err != nil {
		t.Fatalf("proto.Marshal(ComponentAccessStatus) failed: %v", err)
	}
	return spanner.Insert(
		"Components",
		[]string{"ComponentId", "Name", "ConfigProto", "AccessPayload", "State", "Generation", "CreateTime", "UpdateTime"},
		[]any{rec.ComponentID, rec.Name, cfgBytes, statusBytes, int64(rec.State), rec.Generation, rec.CreateTime, rec.UpdateTime},
	)
}

func TestRepository_ReadsAndWithTx(t *testing.T) {
	ctx := context.Background()
	_, client := emulator.Setup(ctx, t)
	repo := NewRepository(client)
	wantApp, wantDB := seedCatalog(t, client)

	t.Run("GetActiveComponent", func(t *testing.T) {
		got, err := repo.GetActiveComponent(ctx, "app")
		if err != nil {
			t.Fatalf("GetActiveComponent(app) failed: %v", err)
		}
		if diff := cmp.Diff(wantApp, got, protocmp.Transform()); diff != "" {
			t.Errorf("GetActiveComponent(app) mismatch (-want +got):\n%s", diff)
		}
		for _, missing := range []string{"missing", "cache"} {
			if _, err := repo.GetActiveComponent(ctx, missing); !errors.Is(err, store.ErrComponentNotFound) {
				t.Errorf("GetActiveComponent(%q) error = %v, want %v", missing, err, store.ErrComponentNotFound)
			}
		}
	})

	t.Run("GetComponentByID", func(t *testing.T) {
		got, err := repo.GetComponentByID(ctx, 1)
		if err != nil {
			t.Fatalf("GetComponentByID(1) failed: %v", err)
		}
		if diff := cmp.Diff(wantApp, got, protocmp.Transform()); diff != "" {
			t.Errorf("GetComponentByID(1) mismatch (-want +got):\n%s", diff)
		}
		for _, id := range []int64{3, 99} {
			if _, err := repo.GetComponentByID(ctx, id); !errors.Is(err, store.ErrComponentNotFound) {
				t.Errorf("GetComponentByID(%d) error = %v, want %v", id, err, store.ErrComponentNotFound)
			}
		}
	})

	t.Run("ListActiveComponents and WithTx", func(t *testing.T) {
		wantCatalog := map[string]*store.ComponentRecord{"app": wantApp, "auth": buildRecord(4, "auth", componentspb.ComponentState_COMPONENT_STATE_ENABLED), "db": wantDB}
		gotCatalog, err := repo.ListActiveComponents(ctx)
		if err != nil {
			t.Fatalf("ListActiveComponents() failed: %v", err)
		}
		if diff := cmp.Diff(wantCatalog, gotCatalog, protocmp.Transform()); diff != "" {
			t.Errorf("ListActiveComponents() mismatch (-want +got):\n%s", diff)
		}

		_, err = client.ReadWriteTransaction(ctx, func(ctx context.Context, tx *spanner.ReadWriteTransaction) error {
			txRepo := repo.WithTx(tx)
			gotApp, err := txRepo.GetActiveComponent(ctx, "app")
			if err != nil {
				return err
			}
			if diff := cmp.Diff(wantApp, gotApp, protocmp.Transform()); diff != "" {
				t.Errorf("WithTx().GetActiveComponent(app) mismatch (-want +got):\n%s", diff)
			}
			txCatalog, err := txRepo.ListActiveComponents(ctx)
			if err != nil {
				return err
			}
			if diff := cmp.Diff(wantCatalog, txCatalog, protocmp.Transform()); diff != "" {
				t.Errorf("WithTx().ListActiveComponents() mismatch (-want +got):\n%s", diff)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("ReadWriteTransaction() failed: %v", err)
		}
	})

	t.Run("FenceAndSnapshots", func(t *testing.T) {
		if _, err := client.Apply(ctx, []*spanner.Mutation{spanner.UpdateMap("Components", map[string]any{
			"ComponentId": int64(2), "SourceNamespace": "xmc", "SourceClusterUid": "old-uid",
			"SourceEpoch": int64(1), "SourceLastSeenTime": baseTime,
		})}); err != nil {
			t.Fatalf("Apply(seed owner) failed: %v", err)
		}

		cluster := store.ClusterIdentity{UID: "c-uid", Name: "prod", Epoch: 2}
		if _, err := client.ReadWriteTransaction(ctx, func(ctx context.Context, tx *spanner.ReadWriteTransaction) error {
			txRepo := repo.WithTx(tx)
			if f, err := txRepo.ReadFence(ctx); err != nil || f != nil {
				t.Fatalf("ReadFence() = %v, %v, want nil, nil", f, err)
			}
			count, err := txRepo.ReassignOwnedComponents(ctx, cluster.UID, cluster.Epoch)
			if err != nil {
				return err
			}
			if count != 1 {
				t.Errorf("ReassignOwnedComponents() count = %d, want 1", count)
			}
			return txRepo.ReplaceFence(ctx, cluster)
		}); err != nil {
			t.Fatalf("ReplaceFence() failed: %v", err)
		}

		renamed := "prod-east"
		if _, err := client.ReadWriteTransaction(ctx, func(ctx context.Context, tx *spanner.ReadWriteTransaction) error {
			txRepo := repo.WithTx(tx)
			gotFence, err := txRepo.ReadFence(ctx)
			if err != nil {
				return err
			}
			if diff := cmp.Diff(&cluster, gotFence); diff != "" {
				t.Errorf("ReadFence() mismatch (-want +got):\n%s", diff)
			}
			return txRepo.RefreshFence(ctx, &renamed)
		}); err != nil {
			t.Fatalf("RefreshFence() failed: %v", err)
		}

		gotRec, gotFence, err := repo.GetComponentWithFence(ctx, "app")
		if err != nil {
			t.Fatalf("GetComponentWithFence(app) failed: %v", err)
		}
		if diff := cmp.Diff(wantApp, gotRec, protocmp.Transform()); diff != "" {
			t.Errorf("GetComponentWithFence(app) rec mismatch (-want +got):\n%s", diff)
		}
		wantFence := &store.ClusterIdentity{UID: "c-uid", Name: renamed, Epoch: 2}
		if diff := cmp.Diff(wantFence, gotFence); diff != "" {
			t.Errorf("GetComponentWithFence(app) fence mismatch (-want +got):\n%s", diff)
		}

		gauges, err := repo.ReadOwnershipGauges(ctx, baseTime)
		if err != nil || !gauges.FenceDeclared || gauges.FenceEpoch != 2 || gauges.StaleOwners != 0 {
			t.Errorf("ReadOwnershipGauges(baseTime) = %+v, %v, want FenceDeclared=true, FenceEpoch=2, StaleOwners=0", gauges, err)
		}
		staleGauges, err := repo.ReadOwnershipGauges(ctx, time.Now().Add(time.Minute))
		if err != nil || staleGauges.StaleOwners != 1 {
			t.Errorf("ReadOwnershipGauges(future) = %+v, %v, want StaleOwners=1", staleGauges, err)
		}
	})
}

// componentRowColumns lists the columns of ComponentColumnsSQL in order.
var componentRowColumns = []string{
	"ComponentId", "Name", "ComponentClass", "ConfigProto", "AccessPayload", "State", "Generation", "ObservedGeneration",
	"CreateTime", "UpdateTime", "SourceUid", "SourceGeneration", "SourceNamespace", "SourceClusterUid", "SourceEpoch", "SourceLastSeenTime",
}

var rowSpec = &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT, Version: "v1.2.0"}

func mustMarshal(t *testing.T, m proto.Message) []byte {
	t.Helper()
	b, err := proto.Marshal(m)
	if err != nil {
		t.Fatalf("proto.Marshal(%T) failed: %v", m, err)
	}
	return b
}

// newComponentRow returns the Components row of an ENABLED component "app" with no status, observed
// generation or owner, with the columns in set replaced.
func newComponentRow(t *testing.T, set map[string]any) *spanner.Row {
	t.Helper()
	values := map[string]any{
		"ComponentId": int64(7), "Name": "app", "ComponentClass": spanner.NullString{},
		"ConfigProto":   mustMarshal(t, &componentspb.Component{Spec: rowSpec, Labels: map[string]string{"env": "prod"}}),
		"AccessPayload": []byte(nil), "State": int64(componentspb.ComponentState_COMPONENT_STATE_ENABLED), "Generation": int64(1),
		"ObservedGeneration": spanner.NullInt64{}, "CreateTime": baseTime, "UpdateTime": baseTime,
		"SourceUid": spanner.NullString{}, "SourceGeneration": spanner.NullInt64{}, "SourceNamespace": spanner.NullString{},
		"SourceClusterUid": spanner.NullString{}, "SourceEpoch": spanner.NullInt64{}, "SourceLastSeenTime": spanner.NullTime{},
	}
	maps.Copy(values, set)
	vals := make([]any, len(componentRowColumns))
	for i, col := range componentRowColumns {
		vals[i] = values[col]
	}
	row, err := spanner.NewRow(componentRowColumns, vals)
	if err != nil {
		t.Fatalf("spanner.NewRow() failed: %v", err)
	}
	return row
}

func TestRowToComponentRecord(t *testing.T) {
	// A bare ComponentSpec whose fields do not decode as a Component spec or labels, as old rows store.
	legacySpec := &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT, DeletionPolicy: componentspb.DeletionPolicy_DELETION_POLICY_ORPHAN}
	status := &componentspb.ComponentAccessStatus{State: componentspb.ComponentState_COMPONENT_STATE_ENABLED, ObservedGeneration: 1}
	owner := &store.SourceOwner{UID: "uid-a", Generation: 3, Namespace: "xmc", ClusterUID: "cluster-a", Epoch: 2}
	observedGen := int64(1)
	deleteTime := baseTime
	tests := []struct {
		name    string
		set     map[string]any
		modify  func(*store.ComponentRecord)
		wantErr bool
	}{
		{name: "component without status, observed generation or owner"},
		{
			name:   "legacy ComponentSpec config",
			set:    map[string]any{"ConfigProto": mustMarshal(t, legacySpec)},
			modify: func(r *store.ComponentRecord) { r.Spec, r.Labels = legacySpec, nil },
		},
		{name: "corrupt config", set: map[string]any{"ConfigProto": []byte{0xff}}, wantErr: true},
		{
			name:   "access status and observed generation",
			set:    map[string]any{"AccessPayload": mustMarshal(t, status), "ObservedGeneration": spanner.NullInt64{Int64: 1, Valid: true}},
			modify: func(r *store.ComponentRecord) { r.Status, r.ObservedGeneration = status, &observedGen },
		},
		{
			name: "recorded owner",
			set: map[string]any{
				"SourceUid": spanner.NullString{StringVal: owner.UID, Valid: true}, "SourceGeneration": spanner.NullInt64{Int64: owner.Generation, Valid: true},
				"SourceNamespace": spanner.NullString{StringVal: owner.Namespace, Valid: true}, "SourceClusterUid": spanner.NullString{StringVal: owner.ClusterUID, Valid: true},
				"SourceEpoch": spanner.NullInt64{Int64: owner.Epoch, Valid: true}, "SourceLastSeenTime": spanner.NullTime{Time: baseTime, Valid: true},
			},
			modify: func(r *store.ComponentRecord) { r.Owner, r.SourceLastSeenTime = owner, baseTime },
		},
		{
			name: "deleted record is deleted at its update time",
			set:  map[string]any{"State": int64(componentspb.ComponentState_COMPONENT_STATE_DELETED)},
			modify: func(r *store.ComponentRecord) {
				r.State, r.DeleteTime = componentspb.ComponentState_COMPONENT_STATE_DELETED, &deleteTime
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := RowToComponentRecord(newComponentRow(t, tt.set))
			if gotErr := err != nil; gotErr != tt.wantErr {
				t.Fatalf("RowToComponentRecord() error = %v, wantErr %t", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			want := &store.ComponentRecord{
				ComponentID: 7, Name: "app", Spec: rowSpec, Labels: map[string]string{"env": "prod"},
				State: componentspb.ComponentState_COMPONENT_STATE_ENABLED, Generation: 1, CreateTime: baseTime, UpdateTime: baseTime,
			}
			if tt.modify != nil {
				tt.modify(want)
			}
			want.Etag = store.GenerateEtag(want)
			if diff := cmp.Diff(want, got, protocmp.Transform()); diff != "" {
				t.Errorf("RowToComponentRecord() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestRepository_WithReadOnlyTransaction reads through a repository bound to a read-only
// transaction, as ResolveComponentSnapshot does.
func TestRepository_WithReadOnlyTransaction(t *testing.T) {
	ctx := context.Background()
	_, client := emulator.Setup(ctx, t)
	wantApp, _ := seedCatalog(t, client)
	ro := client.ReadOnlyTransaction()
	defer ro.Close()

	txRepo := NewRepository(client).WithTx(ro)
	got, err := txRepo.GetComponentByID(ctx, wantApp.ComponentID)
	if err != nil {
		t.Fatalf("WithTx(ReadOnlyTransaction).GetComponentByID(%d) failed: %v", wantApp.ComponentID, err)
	}
	if diff := cmp.Diff(wantApp, got, protocmp.Transform()); diff != "" {
		t.Errorf("WithTx(ReadOnlyTransaction).GetComponentByID(%d) mismatch (-want +got):\n%s", wantApp.ComponentID, diff)
	}
	if _, _, err := txRepo.GetSnapshotByID(ctx, 1, 99, "app", baseTime, baseTime); status.Code(err) != codes.NotFound {
		t.Errorf("GetSnapshotByID(missing) error = %v, want code %v", err, codes.NotFound)
	}
	if _, _, err := txRepo.GetSnapshotByName(ctx, "app", 99); status.Code(err) != codes.NotFound {
		t.Errorf("GetSnapshotByName(missing) error = %v, want code %v", err, codes.NotFound)
	}
	if err := NewRepository(client).WithReadOnlySnapshot(ctx, func(ctx context.Context, snapRepo storage.ComponentTxRepository) error {
		snapGot, err := snapRepo.GetComponentByID(ctx, wantApp.ComponentID)
		if err != nil {
			return err
		}
		if diff := cmp.Diff(wantApp, snapGot, protocmp.Transform()); diff != "" {
			t.Errorf("WithReadOnlySnapshot().GetComponentByID(%d) mismatch (-want +got):\n%s", wantApp.ComponentID, diff)
		}
		return nil
	}); err != nil {
		t.Fatalf("WithReadOnlySnapshot() failed: %v", err)
	}
}

func TestRepository_MutationsEdgeCases(t *testing.T) {
	ctx := context.Background()
	_, client := emulator.Setup(ctx, t)
	repo := NewRepository(client)
	_, wantDB := seedCatalog(t, client)

	t.Run("missing dependency ID is rejected", func(t *testing.T) {
		rec := buildRecord(10, "worker", componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION, "db")
		_, err := client.ReadWriteTransaction(ctx, func(ctx context.Context, tx *spanner.ReadWriteTransaction) error {
			return repo.WithTx(tx).InsertComponent(ctx, rec, nil)
		})
		if !errors.Is(err, grpcerr.ErrFailedPrecondition) {
			t.Errorf("InsertComponent() with missing depID error = %v, want %v", err, grpcerr.ErrFailedPrecondition)
		}
		_, err = client.ReadWriteTransaction(ctx, func(ctx context.Context, tx *spanner.ReadWriteTransaction) error {
			return repo.WithTx(tx).UpdateComponentIntent(ctx, rec, map[string]int64{})
		})
		if !errors.Is(err, grpcerr.ErrFailedPrecondition) {
			t.Errorf("UpdateComponentIntent() with missing depID error = %v, want %v", err, grpcerr.ErrFailedPrecondition)
		}
	})

	t.Run("nil access status and nil observed generation", func(t *testing.T) {
		_, err := client.ReadWriteTransaction(ctx, func(ctx context.Context, tx *spanner.ReadWriteTransaction) error {
			txRepo := repo.WithTx(tx)
			if err := txRepo.UpdateComponentState(ctx, wantDB.ComponentID, componentspb.ComponentState_COMPONENT_STATE_DISABLING, nil); err != nil {
				return err
			}
			obsRec := &store.ComponentRecord{
				ComponentID: wantDB.ComponentID,
				State:       componentspb.ComponentState_COMPONENT_STATE_ENABLED,
			}
			return txRepo.UpdateComponentObservation(ctx, obsRec, true)
		})
		if err != nil {
			t.Fatalf("UpdateComponentState/Observation with nil fields failed: %v", err)
		}
		got, err := repo.GetComponentByID(ctx, wantDB.ComponentID)
		if err != nil {
			t.Fatalf("GetComponentByID(%d) failed: %v", wantDB.ComponentID, err)
		}
		if got.ObservedGeneration != nil || got.Status != nil || got.State != componentspb.ComponentState_COMPONENT_STATE_ENABLED {
			t.Errorf("got ObservedGeneration=%v, Status=%v, State=%v; want nil, nil, ENABLED", got.ObservedGeneration, got.Status, got.State)
		}
	})

	t.Run("writes persist commit timestamps and nil records are rejected", func(t *testing.T) {
		inserted := buildRecord(20, "hydrated", componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION, "auth")
		inserted.CreateTime, inserted.UpdateTime, inserted.Etag = time.Time{}, time.Time{}, ""
		owner := &store.SourceOwner{UID: "u-1", Generation: 1, Namespace: "ns", ClusterUID: "c-1", Epoch: 1}

		insertTs, err := client.ReadWriteTransaction(ctx, func(ctx context.Context, tx *spanner.ReadWriteTransaction) error {
			txRepo := repo.WithTx(tx)
			if err := txRepo.InsertComponent(ctx, nil, nil); !errors.Is(err, grpcerr.ErrInvalidArgument) {
				t.Errorf("InsertComponent(nil) error = %v, want %v", err, grpcerr.ErrInvalidArgument)
			}
			if err := txRepo.WriteComponentOwner(ctx, inserted.ComponentID, nil); err != nil {
				t.Errorf("WriteComponentOwner(id, nil) error = %v, want nil", err)
			}
			if err := txRepo.InsertComponent(ctx, inserted, map[string]int64{"auth": 4}); err != nil {
				return err
			}
			return txRepo.WriteComponentOwner(ctx, inserted.ComponentID, owner)
		})
		if err != nil {
			t.Fatalf("InsertComponent + WriteComponentOwner failed: %v", err)
		}

		gotInserted, err := repo.GetComponentByID(ctx, inserted.ComponentID)
		if err != nil {
			t.Fatalf("GetComponentByID(%d) failed: %v", inserted.ComponentID, err)
		}
		if !gotInserted.CreateTime.Equal(insertTs) || !gotInserted.UpdateTime.Equal(insertTs) || !gotInserted.SourceLastSeenTime.Equal(insertTs) {
			t.Errorf("stored timestamps = (%v, %v, %v), want all %v", gotInserted.CreateTime, gotInserted.UpdateTime, gotInserted.SourceLastSeenTime, insertTs)
		}
		wantInserted := store.CloneRecord(inserted)
		wantInserted.CreateTime, wantInserted.UpdateTime, wantInserted.SourceLastSeenTime = insertTs, insertTs, insertTs
		wantInserted.Owner = owner
		wantInserted.Etag = store.GenerateEtag(wantInserted)
		if diff := cmp.Diff(wantInserted, gotInserted, protocmp.Transform()); diff != "" {
			t.Errorf("inserted record mismatch (-want +got):\n%s", diff)
		}

		intentRec := store.CloneRecord(wantInserted)
		intentRec.Generation = 2
		intentRec.Spec.Generation = 2
		intentRec.Spec.Version = "v2.0.0"
		intentRec.Dependencies = []string{"db"}
		intentRec.Labels = map[string]string{"env": "staging"}
		intentTs, err := client.ReadWriteTransaction(ctx, func(ctx context.Context, tx *spanner.ReadWriteTransaction) error {
			txRepo := repo.WithTx(tx)
			if err := txRepo.UpdateComponentIntent(ctx, nil, nil); !errors.Is(err, grpcerr.ErrInvalidArgument) {
				t.Errorf("UpdateComponentIntent(nil) error = %v, want %v", err, grpcerr.ErrInvalidArgument)
			}
			return txRepo.UpdateComponentIntent(ctx, intentRec, map[string]int64{"db": wantDB.ComponentID})
		})
		if err != nil {
			t.Fatalf("UpdateComponentIntent failed: %v", err)
		}
		gotIntent, err := repo.GetComponentByID(ctx, inserted.ComponentID)
		if err != nil {
			t.Fatalf("GetComponentByID(%d) after UpdateComponentIntent failed: %v", inserted.ComponentID, err)
		}
		if !gotIntent.UpdateTime.Equal(intentTs) {
			t.Errorf("UpdateComponentIntent UpdateTime = %v, want %v", gotIntent.UpdateTime, intentTs)
		}
		intentRec.UpdateTime = intentTs
		intentRec.Etag = store.GenerateEtag(intentRec)
		if diff := cmp.Diff(intentRec, gotIntent, protocmp.Transform()); diff != "" {
			t.Errorf("record after UpdateComponentIntent mismatch (-want +got):\n%s", diff)
		}

		obsGen := int64(2)
		obsInput := store.CloneRecord(intentRec)
		obsInput.ObservedGeneration = &obsGen
		obsInput.Status = &componentspb.ComponentAccessStatus{
			State:              componentspb.ComponentState_COMPONENT_STATE_ENABLED,
			ObservedGeneration: 2,
			HealthMessage:      "ready",
		}
		obsInput.State = componentspb.ComponentState_COMPONENT_STATE_ENABLED
		obsTs, err := client.ReadWriteTransaction(ctx, func(ctx context.Context, tx *spanner.ReadWriteTransaction) error {
			txRepo := repo.WithTx(tx)
			if err := txRepo.UpdateComponentObservation(ctx, nil, false); !errors.Is(err, grpcerr.ErrInvalidArgument) {
				t.Errorf("UpdateComponentObservation(nil) error = %v, want %v", err, grpcerr.ErrInvalidArgument)
			}
			return txRepo.UpdateComponentObservation(ctx, obsInput, false)
		})
		if err != nil {
			t.Fatalf("UpdateComponentObservation failed: %v", err)
		}
		gotObs, err := repo.GetComponentByID(ctx, inserted.ComponentID)
		if err != nil {
			t.Fatalf("GetComponentByID(%d) after UpdateComponentObservation failed: %v", inserted.ComponentID, err)
		}
		if !gotObs.UpdateTime.Equal(obsTs) {
			t.Errorf("UpdateComponentObservation UpdateTime = %v, want %v", gotObs.UpdateTime, obsTs)
		}
		wantObs := store.CloneRecord(obsInput)
		wantObs.State = componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION
		wantObs.UpdateTime = obsTs
		wantObs.Etag = store.GenerateEtag(wantObs)
		if diff := cmp.Diff(wantObs, gotObs, protocmp.Transform()); diff != "" {
			t.Errorf("record after UpdateComponentObservation mismatch (-want +got):\n%s", diff)
		}

		deleteInput := store.CloneRecord(wantObs)
		deleteInput.Spec.DeletionPolicy = componentspb.DeletionPolicy_DELETION_POLICY_ORPHAN
		deleteInput.Status = &componentspb.ComponentAccessStatus{State: componentspb.ComponentState_COMPONENT_STATE_DELETED}
		nextOwner := &store.SourceOwner{UID: "u-2", Generation: 2, Namespace: "ns", ClusterUID: "c-1", Epoch: 1}
		deleteTs, err := client.ReadWriteTransaction(ctx, func(ctx context.Context, tx *spanner.ReadWriteTransaction) error {
			txRepo := repo.WithTx(tx)
			if err := txRepo.SoftDeleteComponent(ctx, nil, nil); !errors.Is(err, grpcerr.ErrInvalidArgument) {
				t.Errorf("SoftDeleteComponent(nil) error = %v, want %v", err, grpcerr.ErrInvalidArgument)
			}
			return txRepo.SoftDeleteComponent(ctx, deleteInput, nextOwner)
		})
		if err != nil {
			t.Fatalf("SoftDeleteComponent failed: %v", err)
		}

		stmt := spanner.Statement{SQL: `SELECT ` + ComponentColumnsSQL + ` FROM Components WHERE ComponentId = @id`, Params: map[string]any{"id": inserted.ComponentID}}
		var deletedRec *store.ComponentRecord
		if err := client.Single().Query(ctx, stmt).Do(func(row *spanner.Row) error {
			var err error
			deletedRec, err = RowToComponentRecord(row)
			return err
		}); err != nil || deletedRec == nil {
			t.Fatalf("query deleted component failed: rec=%v, err=%v", deletedRec, err)
		}
		if !deletedRec.UpdateTime.Equal(deleteTs) || !deletedRec.SourceLastSeenTime.Equal(deleteTs) {
			t.Errorf("deleted timestamps = (%v, %v), want (%v, %v)", deletedRec.UpdateTime, deletedRec.SourceLastSeenTime, deleteTs, deleteTs)
		}
		wantDeleted := store.CloneRecord(deleteInput)
		wantDeleted.Dependencies = nil
		wantDeleted.State = componentspb.ComponentState_COMPONENT_STATE_DELETED
		wantDeleted.Owner = nextOwner
		wantDeleted.UpdateTime, wantDeleted.SourceLastSeenTime = deleteTs, deleteTs
		wantDeleted.DeleteTime = &deleteTs
		wantDeleted.Etag = store.GenerateEtag(wantDeleted)
		if diff := cmp.Diff(wantDeleted, deletedRec, protocmp.Transform()); diff != "" {
			t.Errorf("deleted record mismatch (-want +got):\n%s", diff)
		}
	})
}

// TestRepository_NilClientOrTransaction checks that reads and writes without a client, or through
// WithTx without the required transaction, fail instead of dereferencing nil.
func TestRepository_NilClientOrTransaction(t *testing.T) {
	ctx := context.Background()
	nilRepo := NewRepository(nil)
	nilTxRepo := nilRepo.WithTx(nil)

	for name, r := range map[string]interface {
		GetActiveComponent(ctx context.Context, canonicalName string) (*store.ComponentRecord, error)
		GetComponentByID(ctx context.Context, componentID int64) (*store.ComponentRecord, error)
		ListActiveComponents(ctx context.Context) (map[string]*store.ComponentRecord, error)
	}{
		"NewRepository(nil)": nilRepo,
		"WithTx(nil)":        nilTxRepo,
	} {
		if _, err := r.GetActiveComponent(ctx, "app"); !errors.Is(err, grpcerr.ErrFailedPrecondition) {
			t.Errorf("%s.GetActiveComponent() error = %v, want %v", name, err, grpcerr.ErrFailedPrecondition)
		}
		if _, err := r.GetComponentByID(ctx, 1); !errors.Is(err, grpcerr.ErrFailedPrecondition) {
			t.Errorf("%s.GetComponentByID() error = %v, want %v", name, err, grpcerr.ErrFailedPrecondition)
		}
		if _, err := r.ListActiveComponents(ctx); !errors.Is(err, grpcerr.ErrFailedPrecondition) {
			t.Errorf("%s.ListActiveComponents() error = %v, want %v", name, err, grpcerr.ErrFailedPrecondition)
		}
	}

	if _, _, err := nilRepo.GetComponentWithFence(ctx, "app"); !errors.Is(err, grpcerr.ErrFailedPrecondition) {
		t.Errorf("NewRepository(nil).GetComponentWithFence() error = %v, want %v", err, grpcerr.ErrFailedPrecondition)
	}
	if _, err := nilRepo.ReadOwnershipGauges(ctx, baseTime); !errors.Is(err, grpcerr.ErrFailedPrecondition) {
		t.Errorf("NewRepository(nil).ReadOwnershipGauges() error = %v, want %v", err, grpcerr.ErrFailedPrecondition)
	}
	if err := nilRepo.WithReadOnlySnapshot(ctx, nil); !errors.Is(err, grpcerr.ErrInvalidArgument) {
		t.Errorf("NewRepository(nil).WithReadOnlySnapshot(nil) error = %v, want %v", err, grpcerr.ErrInvalidArgument)
	}
	if err := nilRepo.WithReadOnlySnapshot(ctx, func(context.Context, storage.ComponentTxRepository) error { return nil }); !errors.Is(err, grpcerr.ErrFailedPrecondition) {
		t.Errorf("NewRepository(nil).WithReadOnlySnapshot(fn) error = %v, want %v", err, grpcerr.ErrFailedPrecondition)
	}
	if _, _, err := nilTxRepo.GetSnapshotByID(ctx, 1, 1, "app", baseTime, baseTime); !errors.Is(err, grpcerr.ErrFailedPrecondition) {
		t.Errorf("WithTx(nil).GetSnapshotByID() error = %v, want %v", err, grpcerr.ErrFailedPrecondition)
	}
	if _, _, err := nilTxRepo.GetSnapshotByName(ctx, "app", 1); !errors.Is(err, grpcerr.ErrFailedPrecondition) {
		t.Errorf("WithTx(nil).GetSnapshotByName() error = %v, want %v", err, grpcerr.ErrFailedPrecondition)
	}
	if _, err := nilTxRepo.ReadFence(ctx); !errors.Is(err, grpcerr.ErrFailedPrecondition) {
		t.Errorf("WithTx(nil).ReadFence() error = %v, want %v", err, grpcerr.ErrFailedPrecondition)
	}
	if err := nilTxRepo.RefreshFence(ctx, nil); !errors.Is(err, grpcerr.ErrFailedPrecondition) {
		t.Errorf("WithTx(nil).RefreshFence() error = %v, want %v", err, grpcerr.ErrFailedPrecondition)
	}
	if err := nilTxRepo.ReplaceFence(ctx, store.ClusterIdentity{}); !errors.Is(err, grpcerr.ErrFailedPrecondition) {
		t.Errorf("WithTx(nil).ReplaceFence() error = %v, want %v", err, grpcerr.ErrFailedPrecondition)
	}
	if _, err := nilTxRepo.ReassignOwnedComponents(ctx, "uid", 1); !errors.Is(err, grpcerr.ErrFailedPrecondition) {
		t.Errorf("WithTx(nil).ReassignOwnedComponents() error = %v, want %v", err, grpcerr.ErrFailedPrecondition)
	}
	if err := nilTxRepo.InsertComponent(ctx, &store.ComponentRecord{}, nil); !errors.Is(err, grpcerr.ErrFailedPrecondition) {
		t.Errorf("WithTx(nil).InsertComponent() error = %v, want %v", err, grpcerr.ErrFailedPrecondition)
	}
	if err := nilTxRepo.UpdateComponentIntent(ctx, &store.ComponentRecord{}, nil); !errors.Is(err, grpcerr.ErrFailedPrecondition) {
		t.Errorf("WithTx(nil).UpdateComponentIntent() error = %v, want %v", err, grpcerr.ErrFailedPrecondition)
	}
	if err := nilTxRepo.UpdateComponentState(ctx, 1, 0, nil); !errors.Is(err, grpcerr.ErrFailedPrecondition) {
		t.Errorf("WithTx(nil).UpdateComponentState() error = %v, want %v", err, grpcerr.ErrFailedPrecondition)
	}
	if err := nilTxRepo.UpdateComponentObservation(ctx, &store.ComponentRecord{}, false); !errors.Is(err, grpcerr.ErrFailedPrecondition) {
		t.Errorf("WithTx(nil).UpdateComponentObservation() error = %v, want %v", err, grpcerr.ErrFailedPrecondition)
	}
	if err := nilTxRepo.SoftDeleteComponent(ctx, &store.ComponentRecord{}, nil); !errors.Is(err, grpcerr.ErrFailedPrecondition) {
		t.Errorf("WithTx(nil).SoftDeleteComponent() error = %v, want %v", err, grpcerr.ErrFailedPrecondition)
	}
	if err := nilTxRepo.WriteComponentOwner(ctx, 1, nil); !errors.Is(err, grpcerr.ErrFailedPrecondition) {
		t.Errorf("WithTx(nil).WriteComponentOwner() error = %v, want %v", err, grpcerr.ErrFailedPrecondition)
	}
}
