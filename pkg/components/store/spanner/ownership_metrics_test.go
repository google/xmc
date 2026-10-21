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
	"maps"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"xmanager_cloud/pkg/components/store"
	spannerstore "xmanager_cloud/pkg/components/store/spanner"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
)

var (
	allAdoptionReasons = []string{
		store.AdoptionReasonInitial,
		store.AdoptionReasonSameClusterRecreate,
		store.AdoptionReasonEpochTakeover,
		store.AdoptionReasonUnsourcedClaim,
	}
	allConflictReasons = []string{
		store.ReasonStaleGeneration,
		store.ReasonSourceIncarnationMismatch,
		store.ReasonSourceNamespaceConflict,
		store.ReasonClusterFenced,
		store.ReasonClusterEpochRegressed,
		store.ReasonClusterNotDeclared,
	}
)

func adoptionCounts() map[string]float64 {
	counts := make(map[string]float64, len(allAdoptionReasons))
	for _, r := range allAdoptionReasons {
		counts[r] = testutil.ToFloat64(store.OwnerAdoptionsCounter(r))
	}
	return counts
}

func conflictCounts() map[string]float64 {
	counts := make(map[string]float64, len(allConflictReasons))
	for _, r := range allConflictReasons {
		counts[r] = testutil.ToFloat64(store.SourceConflictsCounter(r))
	}
	return counts
}

func assertAdoptionIncremented(t *testing.T, before map[string]float64, wantReason string) {
	t.Helper()
	want := maps.Clone(before)
	if wantReason != "" {
		want[wantReason]++
	}
	if diff := cmp.Diff(want, adoptionCounts()); diff != "" {
		t.Errorf("xmc_cms_component_owner_adoptions_total mismatch (-want +got):\n%s", diff)
	}
}

func assertConflictIncremented(t *testing.T, before map[string]float64, wantReason string) {
	t.Helper()
	want := maps.Clone(before)
	if wantReason != "" {
		want[wantReason]++
	}
	if diff := cmp.Diff(want, conflictCounts()); diff != "" {
		t.Errorf("xmc_cms_component_source_conflicts_total mismatch (-want +got):\n%s", diff)
	}
}

func TestOwnershipMetrics_Parity(t *testing.T) {
	prod := store.ClusterIdentity{UID: prodClusterUID, Name: "prod", Epoch: 2}
	dr := store.ClusterIdentity{UID: drClusterUID, Name: "dr", Epoch: 3}
	for _, backend := range intentStoreBackends() {
		t.Run(backend.name, func(t *testing.T) {
			ctx := context.Background()
			s := backend.newStore(t)

			verifyFenceAndConflictMetrics(ctx, t, s, prod, dr)
			verifyAdoptionAndStaleOwnerMetrics(ctx, t, s, dr)
		})
	}
}

func TestSourceLastSeenTime_Parity(t *testing.T) {
	src := store.RequestSource{UID: "uid-seen", Generation: 1, Namespace: "xmc", ClusterUID: prodClusterUID, Epoch: 2}
	for _, backend := range intentStoreBackends() {
		t.Run(backend.name, func(t *testing.T) {
			ctx := context.Background()
			s := backend.newStore(t)
			if _, err := s.DeclareClusterEpoch(ctx, store.ClusterIdentity{UID: prodClusterUID, Name: "prod", Epoch: 2}); err != nil {
				t.Fatalf("DeclareClusterEpoch() error = %v", err)
			}
			created, err := s.SetSourcedComponentIntent(ctx, "seen-comp", intentSpec("v1.0.0"), nil, src)
			if err != nil {
				t.Fatalf("SetSourcedComponentIntent(create) error = %v", err)
			}
			stored := getSourceLastSeenTime(ctx, t, s, "seen-comp")
			if stored.IsZero() || !created.SourceLastSeenTime.Equal(stored) {
				t.Errorf("created SourceLastSeenTime = %v, stored = %v, want equal and non-zero", created.SourceLastSeenTime, stored)
			}

			// An unchanged replay writes nothing, so it neither refreshes nor reports a new
			// timestamp: an aged owner stays aged.
			aged := time.Now().UTC().Add(-store.OwnerStaleThreshold - time.Minute).Truncate(time.Microsecond)
			if err := spannerstore.SetSourceLastSeenTimeForTest(ctx, s, "seen-comp", aged); err != nil {
				t.Fatalf("SetSourceLastSeenTimeForTest() error = %v", err)
			}
			replay, err := s.SetSourcedComponentIntent(ctx, "seen-comp", intentSpec("v1.0.0"), nil, src)
			if err != nil {
				t.Fatalf("SetSourcedComponentIntent(replay) error = %v", err)
			}
			if after := getSourceLastSeenTime(ctx, t, s, "seen-comp"); !after.Equal(aged) || !replay.SourceLastSeenTime.Equal(aged) {
				t.Errorf("replay SourceLastSeenTime returned = %v, stored = %v, want both %v", replay.SourceLastSeenTime, after, aged)
			}
			stored = aged

			// A sourced UpdateObservedGeneration records the owner and reports the new timestamp.
			updated, err := s.UpdateObservedGeneration(ctx, "seen-comp", created.Generation, nil, componentspb.ComponentState_COMPONENT_STATE_ENABLED, "Ready=True", src)
			if err != nil {
				t.Fatalf("UpdateObservedGeneration() error = %v", err)
			}
			if after := getSourceLastSeenTime(ctx, t, s, "seen-comp"); !updated.SourceLastSeenTime.Equal(after) || after.Before(stored) {
				t.Errorf("UpdateObservedGeneration SourceLastSeenTime = %v, stored = %v, want equal and not before %v", updated.SourceLastSeenTime, after, stored)
			}

			// An unsourced spec update keeps the recorded owner and reports its stored timestamp.
			unsourced, err := s.SetComponentIntent(ctx, "seen-comp", intentSpec("v1.1.0"), nil, 0)
			if err != nil {
				t.Fatalf("SetComponentIntent(unsourced update) error = %v", err)
			}
			if after := getSourceLastSeenTime(ctx, t, s, "seen-comp"); !after.Equal(updated.SourceLastSeenTime) || !unsourced.SourceLastSeenTime.Equal(after) {
				t.Errorf("unsourced update SourceLastSeenTime returned = %v, stored = %v, want both %v", unsourced.SourceLastSeenTime, after, updated.SourceLastSeenTime)
			}

			// A DeclareClusterEpoch takeover reassigns the component and records a new timestamp.
			if _, err := s.DeclareClusterEpoch(ctx, store.ClusterIdentity{UID: drClusterUID, Name: "dr", Epoch: 3}); err != nil {
				t.Fatalf("DeclareClusterEpoch(takeover) error = %v", err)
			}
			if takeover := getSourceLastSeenTime(ctx, t, s, "seen-comp"); !takeover.After(updated.SourceLastSeenTime) {
				t.Errorf("DeclareClusterEpoch(takeover) SourceLastSeenTime = %v, want after %v", takeover, updated.SourceLastSeenTime)
			}
		})
	}
}

func getSourceLastSeenTime(ctx context.Context, t *testing.T, s store.Store, name string) time.Time {
	t.Helper()
	rec, err := s.GetComponent(ctx, name)
	if err != nil {
		t.Fatalf("GetComponent(%q) error = %v", name, err)
	}
	return rec.SourceLastSeenTime
}

func verifyFenceAndConflictMetrics(ctx context.Context, t *testing.T, s store.Store, prod, dr store.ClusterIdentity) {
	t.Helper()
	if _, err := s.DeclareClusterEpoch(ctx, prod); err != nil {
		t.Fatalf("DeclareClusterEpoch(prod) error = %v", err)
	}
	now := time.Now().UTC()
	g := readOwnershipGauges(ctx, t, s, now)
	if !g.FenceDeclared || g.FenceEpoch != prod.Epoch {
		t.Errorf("ReadOwnershipGauges() fence = (declared %v, epoch %v), want (true, %v)", g.FenceDeclared, g.FenceEpoch, prod.Epoch)
	}
	if age := now.Sub(g.FenceLastSeenTime); g.FenceLastSeenTime.IsZero() || age < -time.Minute || age > time.Minute {
		t.Errorf("ReadOwnershipGauges() FenceLastSeenTime = %v, want within a minute of %v", g.FenceLastSeenTime, now)
	}

	conflicts := []struct {
		name       string
		identity   store.ClusterIdentity
		wantReason string
	}{
		{name: "epoch regressed", identity: store.ClusterIdentity{UID: prodClusterUID, Name: "prod", Epoch: 1}, wantReason: store.ReasonClusterEpochRegressed},
		{name: "dr cluster fenced", identity: store.ClusterIdentity{UID: drClusterUID, Name: "dr", Epoch: prod.Epoch}, wantReason: store.ReasonClusterFenced},
	}
	for _, tc := range conflicts {
		t.Run(tc.name, func(t *testing.T) {
			beforeConflicts := conflictCounts()
			_, _ = s.DeclareClusterEpoch(ctx, tc.identity)
			assertConflictIncremented(t, beforeConflicts, tc.wantReason)
		})
	}
}

func verifyAdoptionAndStaleOwnerMetrics(ctx context.Context, t *testing.T, s store.Store, dr store.ClusterIdentity) {
	t.Helper()
	srcV1 := store.RequestSource{UID: "uid-1", Generation: 2, Namespace: "xmc", ClusterUID: prodClusterUID, Epoch: 2}
	beforeAdoptions := adoptionCounts()
	if _, err := s.SetSourcedComponentIntent(ctx, "metrics-comp", intentSpec("v1.0.0"), nil, srcV1); err != nil {
		t.Fatalf("SetSourcedComponentIntent(initial) error = %v", err)
	}
	assertAdoptionIncremented(t, beforeAdoptions, store.AdoptionReasonInitial)

	// Unchanged replay does not increment adoptions.
	beforeAdoptions = adoptionCounts()
	if _, err := s.SetSourcedComponentIntent(ctx, "metrics-comp", intentSpec("v1.0.0"), nil, srcV1); err != nil {
		t.Fatalf("SetSourcedComponentIntent(replay) error = %v", err)
	}
	assertAdoptionIncremented(t, beforeAdoptions, "")

	// Stale generation, namespace conflict, and incarnation mismatch increment conflict counters.
	conflicts := []struct {
		name       string
		version    string
		src        store.RequestSource
		wantReason string
	}{
		{
			name:       "stale generation",
			version:    "v2.0.0",
			src:        store.RequestSource{UID: "uid-1", Generation: 1, Namespace: "xmc", ClusterUID: prodClusterUID, Epoch: 2},
			wantReason: store.ReasonStaleGeneration,
		},
		{
			name:       "namespace conflict",
			version:    "v1.0.0",
			src:        store.RequestSource{UID: "uid-other", Generation: 1, Namespace: "other", ClusterUID: prodClusterUID, Epoch: 2},
			wantReason: store.ReasonSourceNamespaceConflict,
		},
		{
			name:       "incarnation mismatch",
			version:    "v1.0.0",
			src:        store.RequestSource{UID: "uid-2", Generation: 1, Namespace: "xmc", ClusterUID: prodClusterUID, Epoch: 2},
			wantReason: store.ReasonSourceIncarnationMismatch,
		},
		{
			name:       "cluster not declared",
			version:    "v1.0.0",
			src:        store.RequestSource{UID: "uid-1", Generation: 2, Namespace: "xmc", ClusterUID: prodClusterUID, Epoch: 3},
			wantReason: store.ReasonClusterNotDeclared,
		},
	}
	for _, tc := range conflicts {
		t.Run(tc.name, func(t *testing.T) {
			beforeConflicts := conflictCounts()
			_, _ = s.SetSourcedComponentIntent(ctx, "metrics-comp", intentSpec(tc.version), nil, tc.src)
			assertConflictIncremented(t, beforeConflicts, tc.wantReason)
		})
	}

	// Same-cluster CAS recreate increments same_cluster_recreate.
	beforeAdoptions = adoptionCounts()
	srcCAS := store.RequestSource{UID: "uid-2", Generation: 1, Namespace: "xmc", ClusterUID: prodClusterUID, Epoch: 2, ExpectedOwnerUID: "uid-1"}
	if _, err := s.SetSourcedComponentIntent(ctx, "metrics-comp", intentSpec("v1.0.0"), nil, srcCAS); err != nil {
		t.Fatalf("SetSourcedComponentIntent(CAS) error = %v", err)
	}
	assertAdoptionIncremented(t, beforeAdoptions, store.AdoptionReasonSameClusterRecreate)

	// Unsourced record claimed by a sourced request increments unsourced_claim.
	if _, err := s.SetComponentIntent(ctx, "metrics-unsourced", intentSpec("v1.0.0"), nil, 1); err != nil {
		t.Fatalf("SetComponentIntent(unsourced) error = %v", err)
	}
	beforeAdoptions = adoptionCounts()
	if _, err := s.SetSourcedComponentIntent(ctx, "metrics-unsourced", intentSpec("v1.0.0"), nil, srcCAS); err != nil {
		t.Fatalf("SetSourcedComponentIntent(unsourced_claim) error = %v", err)
	}
	assertAdoptionIncremented(t, beforeAdoptions, store.AdoptionReasonUnsourcedClaim)

	// A fence takeover reassigns both components and starts a 15-minute adoption grace period:
	// they are not stale now, but are once the grace period has ended without an adoption.
	if _, err := s.DeclareClusterEpoch(ctx, dr); err != nil {
		t.Fatalf("DeclareClusterEpoch(dr takeover) error = %v", err)
	}
	now := time.Now().UTC()
	if g := readOwnershipGauges(ctx, t, s, now); g.FenceEpoch != dr.Epoch || g.StaleOwners != 0 {
		t.Errorf("ReadOwnershipGauges() after takeover = (epoch %v, stale %v), want (%v, 0)", g.FenceEpoch, g.StaleOwners, dr.Epoch)
	}
	if g := readOwnershipGauges(ctx, t, s, now.Add(store.OwnerStaleThreshold+time.Minute)); g.StaleOwners != 2 {
		t.Errorf("ReadOwnershipGauges() after the grace period StaleOwners = %v, want 2", g.StaleOwners)
	}

	drSrc := store.RequestSource{UID: "uid-dr", Generation: 1, Namespace: "xmc", ClusterUID: drClusterUID, Epoch: 3}
	beforeAdoptions = adoptionCounts()
	if _, err := s.SetSourcedComponentIntent(ctx, "metrics-comp", intentSpec("v1.0.0"), nil, drSrc); err != nil {
		t.Fatalf("SetSourcedComponentIntent(epoch_takeover) error = %v", err)
	}
	assertAdoptionIncremented(t, beforeAdoptions, store.AdoptionReasonEpochTakeover)
	if _, err := s.SetSourcedComponentIntent(ctx, "metrics-unsourced", intentSpec("v1.0.0"), nil, drSrc); err != nil {
		t.Fatalf("SetSourcedComponentIntent(epoch_takeover 2) error = %v", err)
	}

	// After both adoptions no owner is stale; backdating one owner's SourceLastSeenTime past
	// OwnerStaleThreshold reports 1 stale owner, with no declaration in between.
	if g := readOwnershipGauges(ctx, t, s, time.Now().UTC()); g.StaleOwners != 0 {
		t.Errorf("ReadOwnershipGauges() after adoption StaleOwners = %v, want 0", g.StaleOwners)
	}
	if err := spannerstore.SetSourceLastSeenTimeForTest(ctx, s, "metrics-comp", time.Now().UTC().Add(-store.OwnerStaleThreshold-time.Minute)); err != nil {
		t.Fatalf("SetSourceLastSeenTimeForTest() error = %v", err)
	}
	if g := readOwnershipGauges(ctx, t, s, time.Now().UTC()); g.StaleOwners != 1 {
		t.Errorf("ReadOwnershipGauges() with aged owner StaleOwners = %v, want 1", g.StaleOwners)
	}
}

func TestReadOwnershipGauges_BeforeDeclaration_Parity(t *testing.T) {
	for _, backend := range intentStoreBackends() {
		t.Run(backend.name, func(t *testing.T) {
			g := readOwnershipGauges(context.Background(), t, backend.newStore(t), time.Now().UTC())
			if diff := cmp.Diff(store.OwnershipGauges{}, g); diff != "" {
				t.Errorf("ReadOwnershipGauges() before any declaration mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// readOwnershipGauges returns the ownership gauges of s at now.
func readOwnershipGauges(ctx context.Context, t *testing.T, s store.Store, now time.Time) store.OwnershipGauges {
	t.Helper()
	reader, ok := s.(store.OwnershipGaugeReader)
	if !ok {
		t.Fatalf("%T does not implement store.OwnershipGaugeReader", s)
	}
	g, err := reader.ReadOwnershipGauges(ctx, now)
	if err != nil {
		t.Fatalf("ReadOwnershipGauges() error = %v", err)
	}
	return g
}
