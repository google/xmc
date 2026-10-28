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
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
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
		cluster := store.ClusterIdentity{UID: "c-uid", Name: "prod", Epoch: 2}
		if _, err := client.ReadWriteTransaction(ctx, func(ctx context.Context, tx *spanner.ReadWriteTransaction) error {
			txRepo := repo.WithTx(tx)
			if f, err := txRepo.ReadFence(ctx); err != nil || f != nil {
				t.Fatalf("ReadFence() = %v, %v, want nil, nil", f, err)
			}
			if _, err := txRepo.ReassignOwnedComponents(ctx, cluster.UID, cluster.Epoch); err != nil {
				return err
			}
			return txRepo.WriteFence(ctx, nil, cluster, false)
		}); err != nil {
			t.Fatalf("WriteFence() failed: %v", err)
		}

		gotRec, gotFence, err := repo.GetComponentWithFence(ctx, "app")
		if err != nil {
			t.Fatalf("GetComponentWithFence(app) failed: %v", err)
		}
		if diff := cmp.Diff(wantApp, gotRec, protocmp.Transform()); diff != "" {
			t.Errorf("GetComponentWithFence(app) rec mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(&cluster, gotFence); diff != "" {
			t.Errorf("GetComponentWithFence(app) fence mismatch (-want +got):\n%s", diff)
		}

		gauges, err := repo.ReadOwnershipGauges(ctx, baseTime)
		if err != nil || !gauges.FenceDeclared || gauges.FenceEpoch != 2 {
			t.Errorf("ReadOwnershipGauges() = %+v, %v, want FenceDeclared=true, FenceEpoch=2", gauges, err)
		}

		if _, _, err := repo.GetSnapshotByID(ctx, 1, 99, "app", baseTime, baseTime); err == nil {
			t.Errorf("GetSnapshotByID(missing) = nil, want NotFound error")
		}
		if _, _, err := repo.GetSnapshotByName(ctx, "app", 99); err == nil {
			t.Errorf("GetSnapshotByName(missing) = nil, want NotFound error")
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
}

// TestRepository_NilClientOrTransaction checks that reads and writes without a client, or through
// WithTx without the required transaction, fail instead of dereferencing nil.
func TestRepository_NilClientOrTransaction(t *testing.T) {
	ctx := context.Background()
	nilRepo := NewRepository(nil)
	nilTxRepo := nilRepo.WithTx(nil)

	for name, r := range map[string]interface {
		GetActiveComponent(ctx context.Context, canonicalName string) (*store.ComponentRecord, error)
		ListActiveComponents(ctx context.Context) (map[string]*store.ComponentRecord, error)
		GetSnapshotByID(ctx context.Context, componentID, generation int64, canonicalName string, createTime, updateTime time.Time) (*store.ComponentRecord, *componentspb.ResolvedComponentCoordinates, error)
		GetSnapshotByName(ctx context.Context, canonicalName string, generation int64) (*store.ComponentRecord, *componentspb.ResolvedComponentCoordinates, error)
	}{
		"NewRepository(nil)": nilRepo,
		"WithTx(nil)":        nilTxRepo,
	} {
		if _, err := r.GetActiveComponent(ctx, "app"); err == nil {
			t.Errorf("%s.GetActiveComponent() error = nil, want an error", name)
		}
		if _, err := r.ListActiveComponents(ctx); err == nil {
			t.Errorf("%s.ListActiveComponents() error = nil, want an error", name)
		}
		if _, _, err := r.GetSnapshotByID(ctx, 1, 1, "app", baseTime, baseTime); err == nil {
			t.Errorf("%s.GetSnapshotByID() error = nil, want an error", name)
		}
		if _, _, err := r.GetSnapshotByName(ctx, "app", 1); err == nil {
			t.Errorf("%s.GetSnapshotByName() error = nil, want an error", name)
		}
	}

	if _, _, err := nilRepo.GetComponentWithFence(ctx, "app"); err == nil {
		t.Errorf("NewRepository(nil).GetComponentWithFence() error = nil, want an error")
	}
	if _, err := nilRepo.ReadOwnershipGauges(ctx, baseTime); err == nil {
		t.Errorf("NewRepository(nil).ReadOwnershipGauges() error = nil, want an error")
	}
	if _, err := nilTxRepo.ReadFence(ctx); err == nil {
		t.Errorf("WithTx(nil).ReadFence() error = nil, want an error")
	}
	if err := nilTxRepo.WriteFence(ctx, nil, store.ClusterIdentity{}, false); err == nil {
		t.Errorf("WithTx(nil).WriteFence() error = nil, want an error")
	}
	if _, err := nilTxRepo.ReassignOwnedComponents(ctx, "uid", 1); err == nil {
		t.Errorf("WithTx(nil).ReassignOwnedComponents() error = nil, want an error")
	}
}
