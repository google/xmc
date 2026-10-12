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
	"maps"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"xmanager_cloud/pkg/components/store"
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

func verifyFenceAndConflictMetrics(ctx context.Context, t *testing.T, s store.Store, prod, dr store.ClusterIdentity) {
	t.Helper()
	if _, err := s.DeclareClusterEpoch(ctx, prod); err != nil {
		t.Fatalf("DeclareClusterEpoch(prod) error = %v", err)
	}
	if got := testutil.ToFloat64(store.ClusterFenceEpochGauge()); got != float64(prod.Epoch) {
		t.Errorf("xmc_cms_cluster_fence_epoch = %v, want %v", got, prod.Epoch)
	}

	beforeConflicts := conflictCounts()
	_, _ = s.DeclareClusterEpoch(ctx, store.ClusterIdentity{UID: prodClusterUID, Name: "prod", Epoch: 1})
	assertConflictIncremented(t, beforeConflicts, store.ReasonClusterEpochRegressed)

	beforeConflicts = conflictCounts()
	_, _ = s.DeclareClusterEpoch(ctx, store.ClusterIdentity{UID: drClusterUID, Name: "dr", Epoch: prod.Epoch})
	assertConflictIncremented(t, beforeConflicts, store.ReasonClusterFenced)
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
	beforeConflicts := conflictCounts()
	_, _ = s.SetSourcedComponentIntent(ctx, "metrics-comp", intentSpec("v2.0.0"), nil, store.RequestSource{UID: "uid-1", Generation: 1, Namespace: "xmc", ClusterUID: prodClusterUID, Epoch: 2})
	assertConflictIncremented(t, beforeConflicts, store.ReasonStaleGeneration)

	beforeConflicts = conflictCounts()
	_, _ = s.SetSourcedComponentIntent(ctx, "metrics-comp", intentSpec("v1.0.0"), nil, store.RequestSource{UID: "uid-other", Generation: 1, Namespace: "other", ClusterUID: prodClusterUID, Epoch: 2})
	assertConflictIncremented(t, beforeConflicts, store.ReasonSourceNamespaceConflict)

	beforeConflicts = conflictCounts()
	_, _ = s.SetSourcedComponentIntent(ctx, "metrics-comp", intentSpec("v1.0.0"), nil, store.RequestSource{UID: "uid-2", Generation: 1, Namespace: "xmc", ClusterUID: prodClusterUID, Epoch: 2})
	assertConflictIncremented(t, beforeConflicts, store.ReasonSourceIncarnationMismatch)

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

	// Fence takeover marks both reassigned components as owner-stale until re-adopted.
	if _, err := s.DeclareClusterEpoch(ctx, dr); err != nil {
		t.Fatalf("DeclareClusterEpoch(dr takeover) error = %v", err)
	}
	if got := testutil.ToFloat64(store.ClusterFenceEpochGauge()); got != float64(dr.Epoch) {
		t.Errorf("xmc_cms_cluster_fence_epoch after takeover = %v, want %v", got, dr.Epoch)
	}
	if got := testutil.ToFloat64(store.ComponentOwnerStaleGauge()); got != 2 {
		t.Errorf("xmc_cms_component_owner_stale after takeover = %v, want 2", got)
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

	// Refreshing the fence after both components are adopted clears the stale owner gauge,
	// and backdating one owner's SourceLastSeenTime past OwnerStaleThreshold reports 1 stale owner.
	if _, err := s.DeclareClusterEpoch(ctx, dr); err != nil {
		t.Fatalf("DeclareClusterEpoch(dr refresh) error = %v", err)
	}
	if got := testutil.ToFloat64(store.ComponentOwnerStaleGauge()); got != 0 {
		t.Errorf("xmc_cms_component_owner_stale after adoption = %v, want 0", got)
	}
	if err := store.SetSourceLastSeenTimeForTest(ctx, s, "metrics-comp", time.Now().UTC().Add(-store.OwnerStaleThreshold-time.Minute)); err != nil {
		t.Fatalf("SetSourceLastSeenTimeForTest() error = %v", err)
	}
	if _, err := s.DeclareClusterEpoch(ctx, dr); err != nil {
		t.Fatalf("DeclareClusterEpoch(dr refresh with aged owner) error = %v", err)
	}
	if got := testutil.ToFloat64(store.ComponentOwnerStaleGauge()); got != 1 {
		t.Errorf("xmc_cms_component_owner_stale with aged owner = %v, want 1", got)
	}
}
