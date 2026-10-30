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
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/internal/storage"
	postgrestest "xmanager_cloud/internal/storage/postgres/testing"
	"xmanager_cloud/pkg/components/naming"
	"xmanager_cloud/pkg/components/store"
)

const (
	seedComponentSQL = `INSERT INTO Components (ComponentId, Name, ConfigProto, AccessPayload, State, Generation, ObservedGeneration, CreateTime, UpdateTime,
  SourceUid, SourceGeneration, SourceNamespace, SourceClusterUid, SourceEpoch, SourceLastSeenTime)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)`
	insertDependencySQL = `INSERT INTO ComponentDependencies (ComponentId, DependencyComponentId) VALUES ($1, $2)`
	insertFenceSQL      = `INSERT INTO ComponentOwnershipFence (FenceId, ActiveClusterUid, ActiveClusterName, Epoch, DeclaredTime, LastSeenTime) VALUES (1, $1, $2, $3, $4, $4)`
	insertSnapshotSQL   = `INSERT INTO ExperimentComponentSnapshots (Xid, ComponentId, LockedGeneration, ConfigProto, AccessPayload) VALUES ($1, $2, $3, $4, $5)`
)

// baseTime has microsecond precision, so it survives a TIMESTAMPTZ round trip unchanged.
var baseTime = time.Date(2026, 1, 2, 3, 4, 5, 123456000, time.UTC)

// buildRecord returns an unowned record at generation 1 created offset after baseTime and updated a
// second later.
func buildRecord(id int64, name string, state componentspb.ComponentState, offset time.Duration) *store.ComponentRecord {
	return &store.ComponentRecord{
		ComponentID: id,
		Name:        name,
		Spec:        &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT, Version: "v1.0.0", Generation: 1},
		Status:      &componentspb.ComponentAccessStatus{State: state},
		State:       state,
		Generation:  1,
		CreateTime:  baseTime.Add(offset),
		UpdateTime:  baseTime.Add(offset + time.Second),
		Labels:      map[string]string{"env": "prod"},
	}
}

// wantRecord returns rec as the repository reads it back with the active dependencies deps.
func wantRecord(rec *store.ComponentRecord, deps ...string) *store.ComponentRecord {
	want := store.CloneRecord(rec)
	want.Dependencies = deps
	if want.State == componentspb.ComponentState_COMPONENT_STATE_DELETED {
		deleteTime := want.UpdateTime
		want.DeleteTime = &deleteTime
	}
	want.Etag = store.GenerateEtag(want)
	return want
}

// marshal returns the wire encoding of m, failing t on error.
func marshal(t *testing.T, m proto.Message) []byte {
	t.Helper()
	b, err := proto.Marshal(m)
	if err != nil {
		t.Fatalf("proto.Marshal(%T) unexpected error: %v", m, err)
	}
	return b
}

// exec runs a seeding statement, failing t on error.
func exec(ctx context.Context, t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(ctx, sql, args...); err != nil {
		t.Fatalf("Exec(%q) unexpected error: %v", sql, err)
	}
}

// seedComponent inserts rec with raw SQL, with an edge to each of depIDs. An empty owner UID, as a
// takeover leaves it, is stored as NULL.
func seedComponent(ctx context.Context, t *testing.T, pool *pgxpool.Pool, rec *store.ComponentRecord, depIDs ...int64) {
	t.Helper()
	config := marshal(t, &componentspb.Component{Name: naming.FullComponentName(rec.Name), Spec: rec.Spec, Labels: rec.Labels})
	owner := make([]any, 6)
	if o := rec.Owner; o != nil {
		owner = []any{o.UID, o.Generation, o.Namespace, o.ClusterUID, o.Epoch, rec.SourceLastSeenTime}
		if o.UID == "" {
			owner[0] = nil
		}
	}
	args := append([]any{rec.ComponentID, rec.Name, config, marshal(t, rec.Status), int64(rec.State), rec.Generation, rec.ObservedGeneration, rec.CreateTime, rec.UpdateTime}, owner...)
	exec(ctx, t, pool, seedComponentSQL, args...)
	for _, depID := range depIDs {
		exec(ctx, t, pool, insertDependencySQL, rec.ComponentID, depID)
	}
}

// seedSnapshot binds generation gen of componentID, with spec and status, to a new experiment xid.
func seedSnapshot(ctx context.Context, t *testing.T, pool *pgxpool.Pool, xid, componentID, gen int64, name string, spec *componentspb.ComponentSpec, status *componentspb.ComponentAccessStatus) {
	t.Helper()
	postgrestest.SeedExperiment(ctx, t, pool, xid)
	config := marshal(t, &componentspb.Component{Name: naming.FullComponentName(name), Spec: spec})
	exec(ctx, t, pool, insertSnapshotSQL, xid, componentID, gen, config, marshal(t, status))
}

func TestRepository_Reads(t *testing.T) {
	ctx := t.Context()
	_, pool := postgrestest.Setup(ctx, t)
	repo := NewRepository(pool)
	enabled, deleted := componentspb.ComponentState_COMPONENT_STATE_ENABLED, componentspb.ComponentState_COMPONENT_STATE_DELETED

	db := buildRecord(2, "db", enabled, time.Minute)
	observedGen := int64(1)
	db.ObservedGeneration = &observedGen
	db.Owner = &store.SourceOwner{UID: "uid-a", Generation: 3, Namespace: "xmc", ClusterUID: "cluster-a", Epoch: 2}
	db.SourceLastSeenTime = baseTime
	zeta := buildRecord(4, "zeta", enabled, 2*time.Minute)
	// A takeover leaves zeta owned by a cluster with a NULL SourceUid, which reads as an empty UID.
	zeta.Owner = &store.SourceOwner{Generation: 3, Namespace: "xmc", ClusterUID: "cluster-b", Epoch: 3}
	zeta.SourceLastSeenTime = baseTime.Add(time.Hour)
	cache := buildRecord(3, "cache", deleted, 3*time.Minute)
	app := buildRecord(1, "app", enabled, 4*time.Minute)
	for _, rec := range []*store.ComponentRecord{db, zeta, cache} {
		seedComponent(ctx, t, pool, rec)
	}
	seedComponent(ctx, t, pool, app, zeta.ComponentID, db.ComponentID, cache.ComponentID)
	// Dependencies are active only and sorted by name; the deleted cache is dropped.
	wantApp := wantRecord(app, "db", "zeta")

	t.Run("GetActiveComponent and GetComponentByID", func(t *testing.T) {
		byName, err := repo.GetActiveComponent(ctx, "app")
		if err != nil {
			t.Fatalf("GetActiveComponent(app) unexpected error: %v", err)
		}
		byID, err := repo.GetComponentByID(ctx, app.ComponentID)
		if err != nil {
			t.Fatalf("GetComponentByID(%d) unexpected error: %v", app.ComponentID, err)
		}
		for _, got := range []*store.ComponentRecord{byName, byID} {
			if diff := cmp.Diff(wantApp, got, protocmp.Transform()); diff != "" {
				t.Errorf("record mismatch (-want +got):\n%s", diff)
			}
		}
	})

	t.Run("ListActiveComponents", func(t *testing.T) {
		got, err := repo.ListActiveComponents(ctx)
		if err != nil {
			t.Fatalf("ListActiveComponents() unexpected error: %v", err)
		}
		want := map[string]*store.ComponentRecord{"app": wantApp, "db": wantRecord(db), "zeta": wantRecord(zeta)}
		if diff := cmp.Diff(want, got, protocmp.Transform()); diff != "" {
			t.Errorf("ListActiveComponents() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("missing and deleted components are ErrComponentNotFound", func(t *testing.T) {
		lookups := map[string]func() error{
			"GetActiveComponent(missing)": func() error { _, err := repo.GetActiveComponent(ctx, "missing"); return err },
			"GetActiveComponent(deleted)": func() error { _, err := repo.GetActiveComponent(ctx, "cache"); return err },
			"GetComponentByID(missing)":   func() error { _, err := repo.GetComponentByID(ctx, 99); return err },
			"GetComponentByID(deleted)":   func() error { _, err := repo.GetComponentByID(ctx, cache.ComponentID); return err },
			"GetComponentWithFence":       func() error { _, _, err := repo.GetComponentWithFence(ctx, "cache"); return err },
		}
		for name, lookup := range lookups {
			if err := lookup(); !errors.Is(err, store.ErrComponentNotFound) {
				t.Errorf("%s error = %v, want %v", name, err, store.ErrComponentNotFound)
			}
		}
	})

	// The fence subtests run last and in order: the first sees no fence, the second declares one.
	fence := &store.ClusterIdentity{UID: "cluster-b", Name: "dr", Epoch: 3}
	fenceTime := baseTime.Add(time.Hour)
	for _, tt := range []struct {
		name      string
		declare   bool
		wantFence *store.ClusterIdentity
		wantGauge store.OwnershipGauges
	}{
		{name: "before the first declaration", wantGauge: store.OwnershipGauges{StaleOwners: 1}},
		{name: "after a declaration", declare: true, wantFence: fence, wantGauge: store.OwnershipGauges{FenceDeclared: true, FenceEpoch: 3, FenceLastSeenTime: fenceTime, StaleOwners: 1}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if tt.declare {
				exec(ctx, t, pool, insertFenceSQL, fence.UID, fence.Name, fence.Epoch, fenceTime)
			}
			assertFence(ctx, t, repo, pool, wantApp, tt.wantFence)
			// db was last seen at baseTime, so it is stale at that cutoff only; zeta, last seen an
			// hour later, is never stale here.
			for cutoff, wantStale := range map[time.Time]int64{baseTime: 1, baseTime.Add(-time.Microsecond): 0} {
				want := tt.wantGauge
				want.StaleOwners = wantStale
				got, err := repo.ReadOwnershipGauges(ctx, cutoff)
				if diff := cmp.Diff(want, got); err != nil || diff != "" {
					t.Errorf("ReadOwnershipGauges(%v) error = %v, mismatch (-want +got):\n%s", cutoff, err, diff)
				}
			}
		})
	}
}

// assertFence checks that GetComponentWithFence returns wantApp with wantFence, and that ReadFence
// in a transaction returns wantFence.
func assertFence(ctx context.Context, t *testing.T, repo *Repository, pool *pgxpool.Pool, wantApp *store.ComponentRecord, wantFence *store.ClusterIdentity) {
	t.Helper()
	rec, fence, err := repo.GetComponentWithFence(ctx, "app")
	if err != nil {
		t.Fatalf("GetComponentWithFence(app) unexpected error: %v", err)
	}
	if diff := cmp.Diff(wantApp, rec, protocmp.Transform()); diff != "" {
		t.Errorf("GetComponentWithFence(app) record mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(wantFence, fence); diff != "" {
		t.Errorf("GetComponentWithFence(app) fence mismatch (-want +got):\n%s", diff)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin() unexpected error: %v", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	got, err := repo.WithTx(tx).ReadFence(ctx)
	if diff := cmp.Diff(wantFence, got); err != nil || diff != "" {
		t.Errorf("ReadFence() error = %v, mismatch (-want +got):\n%s", err, diff)
	}
}

func TestRepository_Snapshots(t *testing.T) {
	ctx := t.Context()
	_, pool := postgrestest.Setup(ctx, t)
	repo := NewRepository(pool)
	enabled := componentspb.ComponentState_COMPONENT_STATE_ENABLED

	// An older, deleted incarnation of cache locked generations 2 and 3; the live one locked 2.
	oldCache := buildRecord(3, "cache", componentspb.ComponentState_COMPONENT_STATE_DELETED, 0)
	liveCache := buildRecord(5, "cache", enabled, time.Hour)
	seedComponent(ctx, t, pool, oldCache)
	seedComponent(ctx, t, pool, liveCache)
	snapSpec := &componentspb.ComponentSpec{Version: "v0.9.0"}
	snapStatus := &componentspb.ComponentAccessStatus{State: componentspb.ComponentState_COMPONENT_STATE_DISABLED, ObservedGeneration: 2, Coordinates: &componentspb.ResolvedComponentCoordinates{ServiceName: "svc", Port: 80}}
	seedSnapshot(ctx, t, pool, 100, oldCache.ComponentID, 2, "cache", snapSpec, snapStatus)
	seedSnapshot(ctx, t, pool, 101, oldCache.ComponentID, 3, "cache", snapSpec, snapStatus)
	seedSnapshot(ctx, t, pool, 102, liveCache.ComponentID, 2, "cache", snapSpec, snapStatus)
	wantSnapshot := func(owner *store.ComponentRecord, gen int64) *store.ComponentRecord {
		rec := &store.ComponentRecord{ComponentID: owner.ComponentID, Name: "cache", Spec: snapSpec, Status: snapStatus, State: snapStatus.State,
			Generation: gen, ObservedGeneration: &snapStatus.ObservedGeneration, CreateTime: owner.CreateTime, UpdateTime: owner.UpdateTime}
		rec.Etag = store.GenerateEtag(rec)
		return rec
	}
	stampTime := baseTime.Add(48 * time.Hour)
	nonUTCStampTime := stampTime.In(time.FixedZone("CET", 2*3600))

	tests := []struct {
		name string
		call func(snap storage.ComponentTxRepository) (*store.ComponentRecord, *componentspb.ResolvedComponentCoordinates, error)
		want *store.ComponentRecord
	}{
		{name: "by ID stamps the given times in UTC", call: func(snap storage.ComponentTxRepository) (*store.ComponentRecord, *componentspb.ResolvedComponentCoordinates, error) {
			return snap.GetSnapshotByID(ctx, oldCache.ComponentID, 3, "cache", nonUTCStampTime, nonUTCStampTime)
		}, want: wantSnapshot(&store.ComponentRecord{ComponentID: oldCache.ComponentID, CreateTime: stampTime, UpdateTime: stampTime}, 3)},
		{name: "by name prefers the newest incarnation", call: func(snap storage.ComponentTxRepository) (*store.ComponentRecord, *componentspb.ResolvedComponentCoordinates, error) {
			return snap.GetSnapshotByName(ctx, "cache", 2)
		}, want: wantSnapshot(liveCache, 2)},
		{name: "by name finds a deleted incarnation", call: func(snap storage.ComponentTxRepository) (*store.ComponentRecord, *componentspb.ResolvedComponentCoordinates, error) {
			return snap.GetSnapshotByName(ctx, "cache", 3)
		}, want: wantSnapshot(oldCache, 3)},
		{name: "missing by ID is NotFound", call: func(snap storage.ComponentTxRepository) (*store.ComponentRecord, *componentspb.ResolvedComponentCoordinates, error) {
			return snap.GetSnapshotByID(ctx, liveCache.ComponentID, 3, "cache", stampTime, stampTime)
		}},
		{name: "missing by name is NotFound", call: func(snap storage.ComponentTxRepository) (*store.ComponentRecord, *componentspb.ResolvedComponentCoordinates, error) {
			return snap.GetSnapshotByName(ctx, "cache", 9)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var rec *store.ComponentRecord
			var coords *componentspb.ResolvedComponentCoordinates
			err := repo.WithReadOnlySnapshot(ctx, func(_ context.Context, snap storage.ComponentTxRepository) error {
				var snapErr error
				rec, coords, snapErr = tt.call(snap)
				return snapErr
			})
			if tt.want == nil {
				if status.Code(err) != codes.NotFound || errors.Is(err, store.ErrComponentNotFound) {
					t.Errorf("error = %v, want a snapshot NotFound error", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if rec.CreateTime.Location() != time.UTC || rec.UpdateTime.Location() != time.UTC {
				t.Errorf("timestamp locations = (%v, %v), want both UTC", rec.CreateTime.Location(), rec.UpdateTime.Location())
			}
			if diff := cmp.Diff(tt.want, rec, protocmp.Transform()); diff != "" {
				t.Errorf("record mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(snapStatus.Coordinates, coords, protocmp.Transform()); diff != "" {
				t.Errorf("coordinates mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestRepository_WithReadOnlySnapshotReadsOneSnapshot(t *testing.T) {
	ctx := t.Context()
	_, pool := postgrestest.Setup(ctx, t)
	repo := NewRepository(pool)
	seedComponent(ctx, t, pool, buildRecord(1, "app", componentspb.ComponentState_COMPONENT_STATE_ENABLED, 0))

	err := repo.WithReadOnlySnapshot(ctx, func(ctx context.Context, snap storage.ComponentTxRepository) error {
		if _, err := snap.GetActiveComponent(ctx, "app"); err != nil {
			return err
		}
		// A write committed after the snapshot's first read stays invisible to it.
		exec(ctx, t, pool, `UPDATE Components SET Generation = 2 WHERE ComponentId = 1`)
		catalog, err := snap.ListActiveComponents(ctx)
		if err != nil {
			return err
		}
		if got := catalog["app"].Generation; got != 1 {
			t.Errorf("generation read in the snapshot = %d, want 1", got)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WithReadOnlySnapshot() unexpected error: %v", err)
	}
	if got, err := repo.GetActiveComponent(ctx, "app"); err != nil || got.Generation != 2 {
		t.Errorf("GetActiveComponent(app) after the snapshot = %+v, %v; want generation 2", got, err)
	}
}
