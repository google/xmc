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

package spanner_test

import (
	"context"
	"fmt"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/google/go-cmp/cmp"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/testing/protocmp"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/pkg/components/store"
)

// UIDs of the kube-system Namespaces of the prod and DR test clusters.
const (
	prodClusterUID = "7d3e2a10-8c4b-4f6e-b1a2-3c4d5e6f7a01"
	drClusterUID   = "7d3e2a10-8c4b-4f6e-b1a2-3c4d5e6f7a02"
)

// TestDeclareClusterEpoch_Parity runs one declaration sequence against MemoryStore and SpannerStore:
// the first owner keeps the fence, the active cluster refreshes (possibly renamed) or raises its
// epoch, and only a strictly higher epoch from another cluster takes the fence over.
func TestDeclareClusterEpoch_Parity(t *testing.T) {
	prod := func(name string, epoch int64) store.ClusterIdentity {
		return store.ClusterIdentity{UID: prodClusterUID, Name: name, Epoch: epoch}
	}
	dr := func(epoch int64) store.ClusterIdentity {
		return store.ClusterIdentity{UID: drClusterUID, Name: "dr", Epoch: epoch}
	}
	steps := []struct {
		name        string
		cluster     store.ClusterIdentity
		wantOutcome store.DeclarationOutcome
		wantReason  string
		wantActive  store.ClusterIdentity // Fence holder named by the rejection metadata.
	}{
		{name: "first declaration creates the fence", cluster: prod("prod", 1), wantOutcome: store.DeclarationCreated},
		{name: "active cluster refreshes its epoch", cluster: prod("prod", 1), wantOutcome: store.DeclarationRefreshed},
		{name: "active cluster refreshes under a new name", cluster: prod("prod-east", 1), wantOutcome: store.DeclarationRefreshed},
		{name: "active cluster raises its epoch", cluster: prod("prod-east", 3), wantOutcome: store.DeclarationRaised},
		{name: "active cluster lowering its epoch regressed", cluster: prod("prod-east", 2), wantReason: store.ReasonClusterEpochRegressed, wantActive: prod("prod-east", 3)},
		{name: "another cluster at the active epoch is fenced", cluster: dr(3), wantReason: store.ReasonClusterFenced, wantActive: prod("prod-east", 3)},
		{name: "another cluster at a lower epoch is fenced", cluster: dr(1), wantReason: store.ReasonClusterFenced, wantActive: prod("prod-east", 3)},
	}
	for _, backend := range intentStoreBackends() {
		t.Run(backend.name, func(t *testing.T) {
			ctx := context.Background()
			s := backend.newStore(t)
			// Steps are intentionally sequential because each one depends on the fence left by the last.
			for _, step := range steps {
				res, err := s.DeclareClusterEpoch(ctx, step.cluster)
				if step.wantReason != "" {
					assertFenceRejected(t, err, step.wantReason, step.wantActive)
					continue
				}
				want := &store.DeclarationResult{Outcome: step.wantOutcome, Active: step.cluster}
				if diff := cmp.Diff(want, res); err != nil || diff != "" {
					t.Fatalf("%s: DeclareClusterEpoch() error = %v, result mismatch (-want +got):\n%s", step.name, err, diff)
				}
			}
		})
	}
}

// TestDeclareClusterEpoch_TakeoverParity checks on both backends that a takeover moves every active
// owned component to the new cluster without its owner UID, leaves unowned and deleted components
// alone, and that the new cluster's next intent in the same namespace adopts the component under
// the same ComponentId (R2), while the old primary is fenced.
func TestDeclareClusterEpoch_TakeoverParity(t *testing.T) {
	prod := store.ClusterIdentity{UID: prodClusterUID, Name: "prod", Epoch: 4}
	dr := store.ClusterIdentity{UID: drClusterUID, Name: "dr", Epoch: 5}
	for _, backend := range intentStoreBackends() {
		t.Run(backend.name, func(t *testing.T) {
			ctx := context.Background()
			s := backend.newStore(t)
			owned := seedOwnedComponent(ctx, t, s, "fence-owned", false)
			seedOwnedComponent(ctx, t, s, "fence-legacy", true)
			seedOwnedComponent(ctx, t, s, "fence-deleted", false)
			if err := s.ExecuteDeletion(ctx, "fence-deleted", componentspb.DeletionPolicy_DELETION_POLICY_ORPHAN, true); err != nil {
				t.Fatalf("ExecuteDeletion() error = %v", err)
			}
			if _, err := s.DeclareClusterEpoch(ctx, prod); err != nil {
				t.Fatalf("DeclareClusterEpoch(prod) error = %v", err)
			}
			before, err := s.GetComponent(ctx, "fence-owned")
			if err != nil {
				t.Fatalf("GetComponent(fence-owned) error = %v", err)
			}

			res, err := s.DeclareClusterEpoch(ctx, dr)
			if diff := cmp.Diff(&store.DeclarationResult{Outcome: store.DeclarationTakeover, Active: dr, RecordsReassigned: 1}, res); err != nil || diff != "" {
				t.Fatalf("DeclareClusterEpoch(dr) error = %v, result mismatch (-want +got):\n%s", err, diff)
			}
			assertStoredOwner(ctx, t, s, "fence-owned", &store.SourceOwner{Generation: 3, Namespace: "xmc", ClusterUID: drClusterUID, Epoch: 5})
			assertOwnerOnlyWrite(ctx, t, s, before)
			assertStoredOwner(ctx, t, s, "fence-legacy", nil)
			_, err = s.DeclareClusterEpoch(ctx, store.ClusterIdentity{UID: prodClusterUID, Name: "prod", Epoch: 5})
			assertFenceRejected(t, err, store.ReasonClusterFenced, dr)

			// The fence is enforced before the ownership rules, so the fenced-out cluster is rejected
			// with CLUSTER_FENCED before its source is compared with the reassigned owner.
			prodSource := store.RequestSource{UID: "uid-prod-2", Generation: 1, Namespace: "xmc", ClusterUID: prodClusterUID, Epoch: 4}
			_, err = s.SetSourcedComponentIntent(ctx, "fence-owned", intentSpec("v1.0.0"), nil, prodSource)
			if reason := store.ErrorInfoReason(err); status.Code(err) != codes.FailedPrecondition || reason != store.ReasonClusterFenced {
				t.Errorf("SetSourcedComponentIntent() from the fenced-out cluster error = %v (reason %q), want reason %q", err, reason, store.ReasonClusterFenced)
			}
			assertStoredOwner(ctx, t, s, "fence-owned", &store.SourceOwner{Generation: 3, Namespace: "xmc", ClusterUID: drClusterUID, Epoch: 5})

			drSource := store.RequestSource{UID: "uid-dr", Generation: 1, Namespace: "xmc", ClusterUID: drClusterUID, Epoch: 5}
			adopted, err := s.SetSourcedComponentIntent(ctx, "fence-owned", intentSpec("v1.0.0"), nil, drSource)
			if err != nil {
				t.Fatalf("SetSourcedComponentIntent() after takeover error = %v", err)
			}
			if adopted.ComponentID != owned.ComponentID || adopted.Generation != owned.Generation {
				t.Errorf("adopted ComponentID %d, generation %d; want the seeded %d, %d", adopted.ComponentID, adopted.Generation, owned.ComponentID, owned.Generation)
			}
			assertStoredOwner(ctx, t, s, "fence-owned", ownerFor(drSource))
		})
	}
}

// TestSpannerStore_DeclareClusterEpoch_TakeoverStartsAdoptionGrace checks that a takeover sets
// SourceLastSeenTime of the reassigned record to the takeover's commit time (the fence's
// DeclaredTime), which starts the owner_stale adoption grace period. MemoryStore records no
// SourceLastSeenTime, so this is Spanner-only.
func TestSpannerStore_DeclareClusterEpoch_TakeoverStartsAdoptionGrace(t *testing.T) {
	ctx := context.Background()
	s, client := setupTestStore(ctx, t)
	seedOwnedComponent(ctx, t, s, "fence-grace", false)
	if _, err := s.DeclareClusterEpoch(ctx, store.ClusterIdentity{UID: prodClusterUID, Name: "prod", Epoch: 1}); err != nil {
		t.Fatalf("DeclareClusterEpoch(prod) error = %v", err)
	}
	if _, err := s.DeclareClusterEpoch(ctx, store.ClusterIdentity{UID: drClusterUID, Name: "dr", Epoch: 2}); err != nil {
		t.Fatalf("DeclareClusterEpoch(dr) error = %v", err)
	}

	stmt := spanner.Statement{
		SQL:    `SELECT c.SourceLastSeenTime, f.DeclaredTime FROM Components c, ComponentOwnershipFence f WHERE c.ActiveName = @name AND f.FenceId = 1`,
		Params: map[string]any{"name": "fence-grace"},
	}
	var lastSeen, declared spanner.NullTime
	err := client.Single().Query(ctx, stmt).Do(func(row *spanner.Row) error {
		return row.Columns(&lastSeen, &declared)
	})
	if err != nil {
		t.Fatalf("query SourceLastSeenTime error = %v", err)
	}
	if !lastSeen.Valid || !lastSeen.Time.Equal(declared.Time) {
		t.Errorf("SourceLastSeenTime = %v, want the takeover commit time %v", lastSeen, declared)
	}
}

// TestSpannerStore_OwnershipFenceSchema checks the ComponentOwnershipFence constraints: Epoch must
// be at least 1, LastSeenTime is required, and FenceId has no singleton CHECK.
func TestSpannerStore_OwnershipFenceSchema(t *testing.T) {
	cols := []string{"FenceId", "ActiveClusterUid", "ActiveClusterName", "Epoch", "DeclaredTime", "LastSeenTime"}
	tests := []struct {
		name    string
		vals    []any
		wantErr bool
	}{
		{name: "epoch 0 violates the CHECK", vals: []any{int64(1), prodClusterUID, "prod", int64(0), spanner.CommitTimestamp, spanner.CommitTimestamp}, wantErr: true},
		{name: "NULL LastSeenTime is rejected", vals: []any{int64(1), prodClusterUID, "prod", int64(1), spanner.CommitTimestamp, nil}, wantErr: true},
		{name: "FenceId other than 1 is allowed", vals: []any{int64(2), prodClusterUID, "prod", int64(1), spanner.CommitTimestamp, spanner.CommitTimestamp}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			_, client := setupTestStore(ctx, t)
			_, err := client.Apply(ctx, []*spanner.Mutation{spanner.Insert("ComponentOwnershipFence", cols, tt.vals)})
			if gotErr := err != nil; gotErr != tt.wantErr {
				t.Errorf("Insert(%v) error = %v, wantErr %t", tt.vals, err, tt.wantErr)
			}
		})
	}
}

// assertFenceRejected checks that err is a FailedPrecondition with an ErrorInfo wantReason naming
// active as the fence holder.
func assertFenceRejected(t *testing.T, err error, wantReason string, active store.ClusterIdentity) {
	t.Helper()
	st := status.Convert(err)
	want := []any{&errdetails.ErrorInfo{
		Reason: wantReason,
		Domain: store.ErrorInfoDomain,
		Metadata: map[string]string{
			"operation":           "DeclareClusterEpoch",
			"active_cluster_uid":  active.UID,
			"active_cluster_name": active.Name,
			"active_epoch":        fmt.Sprint(active.Epoch),
		},
	}}
	if st.Code() != codes.FailedPrecondition {
		t.Errorf("DeclareClusterEpoch() code = %v, want %v (error = %v)", st.Code(), codes.FailedPrecondition, err)
	}
	if diff := cmp.Diff(want, st.Details(), protocmp.Transform()); diff != "" {
		t.Errorf("DeclareClusterEpoch() details mismatch (-want +got):\n%s", diff)
	}
}

// assertOwnerOnlyWrite checks that the stored record of before changed only its owner: the
// generation, UpdateTime and etag stay, since the owner is not part of the Component resource.
func assertOwnerOnlyWrite(ctx context.Context, t *testing.T, s store.Store, before *store.ComponentRecord) {
	t.Helper()
	after, err := s.GetComponent(ctx, before.Name)
	if err != nil {
		t.Fatalf("GetComponent(%s) error = %v", before.Name, err)
	}
	type versioned struct {
		Generation int64
		UpdateTime int64
		Etag       string
	}
	want := versioned{before.Generation, before.UpdateTime.UnixNano(), before.Etag}
	if diff := cmp.Diff(want, versioned{after.Generation, after.UpdateTime.UnixNano(), after.Etag}); diff != "" {
		t.Errorf("takeover changed the version of %s (-before +after):\n%s", before.Name, diff)
	}
}

// assertStoredOwner checks the recorded owner of name.
func assertStoredOwner(ctx context.Context, t *testing.T, s store.Store, name string, want *store.SourceOwner) {
	t.Helper()
	rec, err := s.GetComponent(ctx, name)
	if err != nil {
		t.Fatalf("GetComponent(%s) error = %v", name, err)
	}
	if diff := cmp.Diff(want, rec.Owner); diff != "" {
		t.Errorf("Owner of %s mismatch (-want +got):\n%s", name, diff)
	}
}

// TestSetSourcedComponentIntent_FenceParity checks on both backends that the fence admits only
// sourced intents from the active cluster at the fence epoch, while unsourced intents bypass it.
func TestSetSourcedComponentIntent_FenceParity(t *testing.T) {
	prod := store.ClusterIdentity{UID: prodClusterUID, Name: "prod", Epoch: 2}
	fromCluster := func(clusterUID string, epoch int64) store.RequestSource {
		return sourceWith(func(s *store.RequestSource) { s.ClusterUID, s.Epoch = clusterUID, epoch })
	}
	tests := []struct {
		name       string
		component  string
		spec       *componentspb.ComponentSpec // Defaults to v1.1.0, a spec change.
		src        store.RequestSource
		wantReason string
		// wantOwnerOnly expects the admitted intent to record only its owner.
		wantOwnerOnly bool
	}{
		{name: "owner at the raised fence epoch records only the owner of an unchanged intent (R3)", component: "fence-owned", spec: intentSpec("v1.0.0"), src: fromCluster(prodClusterUID, 2), wantOwnerOnly: true},
		{name: "active cluster at the fence epoch writes", component: "fence-owned", src: fromCluster(prodClusterUID, 2)},
		{name: "active cluster at an undeclared epoch must declare it", component: "fence-owned", src: fromCluster(prodClusterUID, 3), wantReason: store.ReasonClusterNotDeclared},
		{name: "active cluster at an older epoch regressed", component: "fence-owned", src: fromCluster(prodClusterUID, 1), wantReason: store.ReasonClusterEpochRegressed},
		{name: "another cluster is fenced", component: "fence-owned", src: fromCluster(drClusterUID, 2), wantReason: store.ReasonClusterFenced},
		{name: "another cluster cannot create a component", component: "fence-new", src: fromCluster(drClusterUID, 2), wantReason: store.ReasonClusterFenced},
	}
	for _, backend := range intentStoreBackends() {
		t.Run(backend.name, func(t *testing.T) {
			ctx := context.Background()
			s := backend.newStore(t)
			seeded := seedOwnedComponent(ctx, t, s, "fence-owned", false)
			if _, err := s.DeclareClusterEpoch(ctx, prod); err != nil {
				t.Fatalf("DeclareClusterEpoch() error = %v", err)
			}
			// Subtests are intentionally sequential (no t.Parallel) because they share one store.
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					spec := tt.spec
					if spec == nil {
						spec = intentSpec("v1.1.0")
					}
					_, err := s.SetSourcedComponentIntent(ctx, tt.component, spec, nil, tt.src)
					if tt.wantReason == "" {
						if err != nil {
							t.Fatalf("SetSourcedComponentIntent() error = %v, want nil", err)
						}
						if tt.wantOwnerOnly {
							assertOwnerOnlyWrite(ctx, t, s, seeded)
							assertStoredOwner(ctx, t, s, tt.component, ownerFor(tt.src))
						}
						return
					}
					if status.Code(err) != codes.FailedPrecondition {
						t.Fatalf("SetSourcedComponentIntent() error = %v, want FailedPrecondition", err)
					}
					assertReason(t, err, tt.wantReason)
				})
			}
			if _, err := s.GetComponent(ctx, "fence-new"); status.Code(err) != codes.NotFound {
				t.Errorf("GetComponent(fence-new) error = %v, want NotFound", err)
			}
			if _, err := s.SetComponentIntent(ctx, "fence-owned", intentSpec("v1.2.0"), nil, 0); err != nil {
				t.Errorf("unsourced SetComponentIntent() error = %v, want nil", err)
			}
		})
	}
}

// TestSpannerStore_SetSourcedComponentIntent_FencedReplay checks that the read-only fast path
// rejects an unchanged intent from a fenced cluster itself, without a read-write transaction.
func TestSpannerStore_SetSourcedComponentIntent_FencedReplay(t *testing.T) {
	ctx := context.Background()
	s, _ := setupTestStore(ctx, t)
	seedOwnedComponent(ctx, t, s, "fence-replay", false)
	if _, err := s.DeclareClusterEpoch(ctx, store.ClusterIdentity{UID: drClusterUID, Name: "dr", Epoch: 2}); err != nil {
		t.Fatalf("DeclareClusterEpoch() error = %v", err)
	}
	fallbacks := 0
	s.SetBeforeIntentWriteForTest(func() { fallbacks++ })
	defer s.SetBeforeIntentWriteForTest(nil)

	_, err := s.SetSourcedComponentIntent(ctx, "fence-replay", intentSpec("v1.0.0"), nil, ownerSource)
	assertReason(t, err, store.ReasonClusterFenced)
	if fallbacks != 0 {
		t.Errorf("read-write fallbacks = %d, want 0", fallbacks)
	}
}

// TestSpannerStore_SetSourcedComponentIntent_AdmittedReplay checks that the read-only fast path
// decodes a declared fence and answers an unchanged intent from the fence holder at the fence
// epoch without a read-write transaction.
func TestSpannerStore_SetSourcedComponentIntent_AdmittedReplay(t *testing.T) {
	ctx := context.Background()
	s, _ := setupTestStore(ctx, t)
	prodSource := sourceWith(func(s *store.RequestSource) { s.ClusterUID, s.Epoch = prodClusterUID, 2 })
	if _, err := s.DeclareClusterEpoch(ctx, store.ClusterIdentity{UID: prodClusterUID, Name: "prod", Epoch: 2}); err != nil {
		t.Fatalf("DeclareClusterEpoch(prod) error = %v", err)
	}
	seeded, err := s.SetSourcedComponentIntent(ctx, "fence-admitted", intentSpec("v1.0.0"), nil, prodSource)
	if err != nil {
		t.Fatalf("seed SetSourcedComponentIntent() error = %v", err)
	}
	fallbacks := 0
	s.SetBeforeIntentWriteForTest(func() { fallbacks++ })
	defer s.SetBeforeIntentWriteForTest(nil)

	got, err := s.SetSourcedComponentIntent(ctx, "fence-admitted", intentSpec("v1.0.0"), nil, prodSource)
	if err != nil {
		t.Fatalf("replay SetSourcedComponentIntent() error = %v, want nil", err)
	}
	if got.Generation != seeded.Generation {
		t.Errorf("replay generation = %d, want the seeded %d", got.Generation, seeded.Generation)
	}
	if fallbacks != 0 {
		t.Errorf("read-write fallbacks = %d, want 0", fallbacks)
	}
}

// TestSpannerStore_SetSourcedComponentIntent_TakeoverBeforeWrite checks that a takeover committed
// after the fast-path read but before the read-write transaction fences the in-flight intent: the
// transaction rereads the fence, rejects the intent and leaves the taken-over record unchanged.
func TestSpannerStore_SetSourcedComponentIntent_TakeoverBeforeWrite(t *testing.T) {
	ctx := context.Background()
	s, _ := setupTestStore(ctx, t)
	prodSource := sourceWith(func(s *store.RequestSource) { s.ClusterUID, s.Epoch = prodClusterUID, 2 })
	if _, err := s.DeclareClusterEpoch(ctx, store.ClusterIdentity{UID: prodClusterUID, Name: "prod", Epoch: 2}); err != nil {
		t.Fatalf("DeclareClusterEpoch(prod) error = %v", err)
	}
	seeded, err := s.SetSourcedComponentIntent(ctx, "fence-race", intentSpec("v1.0.0"), nil, prodSource)
	if err != nil {
		t.Fatalf("seed SetSourcedComponentIntent() error = %v", err)
	}
	dr := store.ClusterIdentity{UID: drClusterUID, Name: "dr", Epoch: 3}
	s.SetBeforeIntentWriteForTest(func() {
		if _, err := s.DeclareClusterEpoch(ctx, dr); err != nil {
			t.Errorf("concurrent DeclareClusterEpoch(dr) error = %v", err)
		}
	})
	defer s.SetBeforeIntentWriteForTest(nil)

	_, err = s.SetSourcedComponentIntent(ctx, "fence-race", intentSpec("v1.1.0"), nil, prodSource)
	assertReason(t, err, store.ReasonClusterFenced)
	got, err := s.GetComponent(ctx, "fence-race")
	if err != nil {
		t.Fatalf("GetComponent() error = %v", err)
	}
	if got.Generation != seeded.Generation || got.Spec.GetVersion() != "v1.0.0" {
		t.Errorf("record generation %d, version %q; want the seeded %d, v1.0.0", got.Generation, got.Spec.GetVersion(), seeded.Generation)
	}
	assertStoredOwner(ctx, t, s, "fence-race", &store.SourceOwner{Generation: prodSource.Generation, Namespace: "xmc", ClusterUID: drClusterUID, Epoch: 3})
}

// assertReason checks that err carries an ErrorInfo with wantReason in store.ErrorInfoDomain.
func assertReason(t *testing.T, err error, wantReason string) {
	t.Helper()
	for _, d := range status.Convert(err).Details() {
		if info, ok := d.(*errdetails.ErrorInfo); ok && info.GetDomain() == store.ErrorInfoDomain && info.GetReason() == wantReason {
			return
		}
	}
	t.Errorf("error = %v, want ErrorInfo reason %q", err, wantReason)
}
