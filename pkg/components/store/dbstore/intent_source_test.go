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

// Cross-backend parity tests for RequestSource ownership (R1-R6) and cluster fence enforcement.
// Production targets: source_ownership.go, ownership/ownership_fence.go, dbstore/ownership_fence.go,
// memory_store.go, and dbstore.go.
// Test type: hermetic cross-backend unit/emulator suite (runs every ownership and fence case
// against both MemoryStore and dbstore.Store backed by the in-process Spanner emulator). Kept
// separate from source_ownership_test.go (which unit-tests the pure CheckSourceOwnership rules in
// package store) so end-to-end Store RPC ownership parity is verified in package dbstore_test.
package dbstore_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/google/go-cmp/cmp"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/testing/protocmp"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/pkg/components/store"
	"xmanager_cloud/pkg/components/store/dbstore"
	"xmanager_cloud/pkg/components/store/memory"
)

// ownerSource is the source that seeds owned rows; cases modify it with sourceWith. It comes from
// ownerCluster, which seedOwnedComponent declares as the fence holder.
var ownerSource = store.RequestSource{UID: "uid-a", Generation: 3, Namespace: "xmc", ClusterUID: prodClusterUID, Epoch: 1}

// ownerCluster is the cluster of ownerSource at its epoch.
var ownerCluster = store.ClusterIdentity{UID: ownerSource.ClusterUID, Name: "prod", Epoch: ownerSource.Epoch}

// declareOwnerCluster declares ownerCluster on s, so that intents from ownerSource pass the fence.
func declareOwnerCluster(ctx context.Context, t *testing.T, s store.Store) {
	t.Helper()
	if _, err := s.DeclareClusterEpoch(ctx, ownerCluster); err != nil {
		t.Fatalf("DeclareClusterEpoch(%+v) error = %v", ownerCluster, err)
	}
}

// sourceWith returns ownerSource changed by mutate.
func sourceWith(mutate func(*store.RequestSource)) store.RequestSource {
	src := ownerSource
	mutate(&src)
	return src
}

// ownerFor returns the owner that a write from src records.
func ownerFor(src store.RequestSource) *store.SourceOwner {
	return &store.SourceOwner{UID: src.UID, Generation: src.Generation, Namespace: src.Namespace, ClusterUID: src.ClusterUID, Epoch: src.Epoch}
}

// TestSetSourcedComponentIntent_OwnerParity runs the same rows against MemoryStore and
// SpannerStore. Each row seeds an ENABLED component, from ownerSource or, when legacy is set,
// through SetComponentIntent at generation 5000, and then applies spec from src. Every source comes
// from the fence holder at the fence epoch: owners from other clusters or epochs are reachable only
// through the fence (TestSetSourcedComponentIntent_FenceParity).
func TestSetSourcedComponentIntent_OwnerParity(t *testing.T) {
	unchanged, changed := intentSpec("v1.0.0"), intentSpec("v1.1.0")
	newerGen := sourceWith(func(s *store.RequestSource) { s.Generation = 4 })
	olderGen := sourceWith(func(s *store.RequestSource) { s.Generation = 2 })
	otherObject := sourceWith(func(s *store.RequestSource) { s.UID, s.Generation = "uid-b", 1 })
	casAdopter := sourceWith(func(s *store.RequestSource) { s.UID, s.Generation, s.ExpectedOwnerUID = "uid-b", 1, "uid-a" })
	otherNamespace := sourceWith(func(s *store.RequestSource) { s.Namespace, s.UID, s.ExpectedOwnerUID = "other", "uid-b", "uid-a" })
	withUnregisteredDep := intentSpec("v1.1.0", "intent-source-unregistered")
	tests := []struct {
		name       string
		legacy     bool
		spec       *componentspb.ComponentSpec
		src        store.RequestSource
		wantReason string
		wantGen    int64
		wantOwner  *store.SourceOwner
		// wantReset expects the state reset by a spec write; otherwise the seeded state,
		// UpdateTime and Etag must be kept.
		wantReset bool
	}{
		{name: "legacy record adopts an unchanged intent without a bump (R2)", legacy: true, spec: unchanged, src: otherNamespace, wantGen: 5000, wantOwner: ownerFor(otherNamespace)},
		{name: "legacy record adopts a changed intent with a bump by one (R2)", legacy: true, spec: changed, src: ownerSource, wantGen: 5001, wantOwner: ownerFor(ownerSource), wantReset: true},
		{name: "owner replay is a no-op (R3)", spec: unchanged, src: ownerSource, wantGen: 1, wantOwner: ownerFor(ownerSource)},
		{name: "owner at a newer generation records only the owner (R3)", spec: unchanged, src: newerGen, wantGen: 1, wantOwner: ownerFor(newerGen)},
		{name: "owner with a changed spec bumps by one (R3)", spec: changed, src: ownerSource, wantGen: 2, wantOwner: ownerFor(ownerSource), wantReset: true},
		{name: "owner at an older generation with an unchanged spec is a no-op (R3)", spec: unchanged, src: olderGen, wantGen: 1, wantOwner: ownerFor(ownerSource)},
		{name: "owner at an older generation with a changed spec is stale (R3)", spec: changed, src: olderGen, wantReason: store.ReasonStaleGeneration},
		{name: "stale intent naming an unregistered dependency is rejected as stale (R3)", spec: withUnregisteredDep, src: olderGen, wantReason: store.ReasonStaleGeneration},
		{name: "another namespace conflicts even with a CAS guard (R4)", spec: unchanged, src: otherNamespace, wantReason: store.ReasonSourceNamespaceConflict},
		{name: "conflicting namespace naming an unregistered dependency is rejected as a conflict (R4)", spec: withUnregisteredDep, src: otherNamespace, wantReason: store.ReasonSourceNamespaceConflict},
		{name: "another object naming the owner adopts an unchanged intent (R5)", spec: unchanged, src: casAdopter, wantGen: 1, wantOwner: ownerFor(casAdopter)},
		{name: "another object naming the owner adopts a changed intent (R5)", spec: changed, src: casAdopter, wantGen: 2, wantOwner: ownerFor(casAdopter), wantReset: true},
		{name: "another object without a CAS guard mismatches (R6)", spec: unchanged, src: otherObject, wantReason: store.ReasonSourceIncarnationMismatch},
		{name: "mismatching object naming an unregistered dependency is rejected as a mismatch (R6)", spec: withUnregisteredDep, src: otherObject, wantReason: store.ReasonSourceIncarnationMismatch},
	}
	for _, backend := range intentStoreBackends() {
		t.Run(backend.name, func(t *testing.T) {
			ctx := context.Background()
			s := backend.newStore(t)
			// Subtests are intentionally sequential (no t.Parallel) because they share one store.
			for i, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					name := fmt.Sprintf("intent-source-%d", i)
					before := seedOwnedComponent(ctx, t, s, name, tt.legacy)

					got, err := s.SetSourcedComponentIntent(ctx, name, tt.spec, nil, tt.src)
					if tt.wantReason != "" {
						assertOwnershipRejected(ctx, t, s, name, err, tt.wantReason, before)
						return
					}
					if err != nil {
						t.Fatalf("SetSourcedComponentIntent() error = %v", err)
					}
					stored, err := s.GetComponent(ctx, name)
					if err != nil {
						t.Fatalf("GetComponent() error = %v", err)
					}
					for _, rec := range []*store.ComponentRecord{got, stored} {
						assertOwnerOutcome(t, before, rec, tt.wantGen, tt.wantOwner, tt.wantReset)
					}
				})
			}
		})
	}
}

// TestSetSourcedComponentIntent_RecordLifecycle checks on both backends that an unsourced intent
// keeps the recorded owner, and that a soft-deleted record is replaced by a new one at generation
// 1 owned by the new source.
func TestSetSourcedComponentIntent_RecordLifecycle(t *testing.T) {
	otherNamespace := sourceWith(func(s *store.RequestSource) { s.Namespace, s.UID = "other", "uid-b" })
	for _, backend := range intentStoreBackends() {
		t.Run(backend.name, func(t *testing.T) {
			ctx := context.Background()
			s := backend.newStore(t)
			created := seedOwnedComponent(ctx, t, s, "intent-source-lifecycle", false)

			got, err := s.SetComponentIntent(ctx, "intent-source-lifecycle", intentSpec("v1.1.0"), nil, 0)
			if err != nil {
				t.Fatalf("SetComponentIntent() error = %v", err)
			}
			assertOwnerOutcome(t, created, got, 2, ownerFor(ownerSource), true)

			if err := s.ExecuteDeletion(ctx, "intent-source-lifecycle", componentspb.DeletionPolicy_DELETION_POLICY_ORPHAN, true); err != nil {
				t.Fatalf("ExecuteDeletion() error = %v", err)
			}
			recreated, err := s.SetSourcedComponentIntent(ctx, "intent-source-lifecycle", intentSpec("v1.0.0"), nil, otherNamespace)
			if err != nil {
				t.Fatalf("SetSourcedComponentIntent() after deletion error = %v", err)
			}
			if recreated.ComponentID == created.ComponentID || recreated.Generation != 1 {
				t.Errorf("ComponentID = %d, generation = %d, want a new ComponentId (not %d) at generation 1", recreated.ComponentID, recreated.Generation, created.ComponentID)
			}
			if diff := cmp.Diff(ownerFor(otherNamespace), recreated.Owner); diff != "" {
				t.Errorf("recreated Owner mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestSetSourcedComponentIntent_CASGuardWithoutRecord checks on both backends that a CAS guard
// (expected_owner_uid) on a component without an active record, never created or soft-deleted, is
// rejected with SOURCE_INCARNATION_MISMATCH and empty owner metadata instead of creating one (R1).
func TestSetSourcedComponentIntent_CASGuardWithoutRecord(t *testing.T) {
	casAdopter := sourceWith(func(s *store.RequestSource) { s.UID, s.Generation, s.ExpectedOwnerUID = "uid-b", 1, "uid-a" })
	wantDetails := []any{&errdetails.ErrorInfo{
		Reason: store.ReasonSourceIncarnationMismatch,
		Domain: store.ErrorInfoDomain,
		Metadata: map[string]string{
			"operation": "SetComponentIntent", "owner_namespace": "", "owner_uid": "", "owner_cluster_uid": "",
		},
	}}
	tests := []struct {
		name        string
		softDeleted bool
	}{
		{name: "never created"},
		{name: "soft-deleted", softDeleted: true},
	}
	for _, backend := range intentStoreBackends() {
		t.Run(backend.name, func(t *testing.T) {
			ctx := context.Background()
			s := backend.newStore(t)
			declareOwnerCluster(ctx, t, s)
			for i, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					name := fmt.Sprintf("intent-source-cas-missing-%d", i)
					if tt.softDeleted {
						seedOwnedComponent(ctx, t, s, name, false)
						if err := s.ExecuteDeletion(ctx, name, componentspb.DeletionPolicy_DELETION_POLICY_ORPHAN, true); err != nil {
							t.Fatalf("ExecuteDeletion() error = %v", err)
						}
					}

					_, err := s.SetSourcedComponentIntent(ctx, name, intentSpec("v1.0.0"), nil, casAdopter)
					if status.Code(err) != codes.FailedPrecondition {
						t.Fatalf("SetSourcedComponentIntent() error = %v, want code %v", err, codes.FailedPrecondition)
					}
					if diff := cmp.Diff(wantDetails, status.Convert(err).Details(), protocmp.Transform()); diff != "" {
						t.Errorf("error details mismatch (-want +got):\n%s", diff)
					}
					if _, err := s.GetComponent(ctx, name); status.Code(err) != codes.NotFound {
						t.Errorf("GetComponent() error = %v, want code %v", err, codes.NotFound)
					}
				})
			}
		})
	}
}

// TestSpannerStore_SetSourcedComponentIntent_ReadOnlyFastPath checks that only sourced intents that
// write nothing skip the read-write transaction, which decides every owner write and rejection.
func TestSpannerStore_SetSourcedComponentIntent_ReadOnlyFastPath(t *testing.T) {
	tests := []struct {
		name          string
		src           store.RequestSource
		wantFallbacks int
		wantCode      codes.Code
	}{
		{name: "owner replay skips the read-write transaction", src: ownerSource},
		{name: "older owner generation skips the read-write transaction", src: sourceWith(func(s *store.RequestSource) { s.Generation = 2 })},
		{name: "newer owner generation falls back to record the owner", src: sourceWith(func(s *store.RequestSource) { s.Generation = 4 }), wantFallbacks: 1},
		{name: "incarnation mismatch falls back and is rejected", src: sourceWith(func(s *store.RequestSource) { s.UID = "uid-b" }), wantFallbacks: 1, wantCode: codes.FailedPrecondition},
	}
	ctx := context.Background()
	s, _ := setupTestStore(ctx, t)
	// Subtests are intentionally sequential (no t.Parallel) because they share one store and hook.
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name := fmt.Sprintf("intent-source-fast-path-%d", i)
			seedOwnedComponent(ctx, t, s, name, false)
			fallbacks := 0
			s.SetBeforeIntentWriteForTest(func() { fallbacks++ })
			defer s.SetBeforeIntentWriteForTest(nil)

			if _, err := s.SetSourcedComponentIntent(ctx, name, intentSpec("v1.0.0"), nil, tt.src); status.Code(err) != tt.wantCode {
				t.Fatalf("SetSourcedComponentIntent() error = %v, want code %v", err, tt.wantCode)
			}
			if fallbacks != tt.wantFallbacks {
				t.Errorf("read-write fallbacks = %d, want %d", fallbacks, tt.wantFallbacks)
			}
		})
	}
}

// TestSetSourcedComponentIntent_UndeclaredFence checks on both backends that, before any cluster
// declares the fence, a sourced intent is rejected with CLUSTER_NOT_DECLARED and no active_*
// metadata, whether it would create a component (read-write path) or replay over an unsourced one
// (Spanner's read-only fast path), and that nothing is written.
func TestSetSourcedComponentIntent_UndeclaredFence(t *testing.T) {
	wantDetails := []any{&errdetails.ErrorInfo{
		Reason:   store.ReasonClusterNotDeclared,
		Domain:   store.ErrorInfoDomain,
		Metadata: map[string]string{"operation": "SetComponentIntent"},
	}}
	for _, backend := range intentStoreBackends() {
		t.Run(backend.name, func(t *testing.T) {
			ctx := context.Background()
			s := backend.newStore(t)
			legacy := seedIntentComponent(ctx, t, s, "fence-undeclared-legacy", intentSpec("v1.0.0"), 5000, nil, componentspb.ComponentState_COMPONENT_STATE_ENABLED)
			tests := []struct {
				name      string
				component string
			}{
				{name: "new component", component: "fence-undeclared-new"},
				{name: "legacy component", component: legacy.Name},
			}
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					_, err := s.SetSourcedComponentIntent(ctx, tt.component, intentSpec("v1.0.0"), nil, ownerSource)
					if diff := cmp.Diff(wantDetails, status.Convert(err).Details(), protocmp.Transform()); status.Code(err) != codes.FailedPrecondition || diff != "" {
						t.Errorf("SetSourcedComponentIntent(%s) error = %v, details mismatch (-want +got):\n%s", tt.component, err, diff)
					}
				})
			}
			if _, err := s.GetComponent(ctx, "fence-undeclared-new"); status.Code(err) != codes.NotFound {
				t.Errorf("GetComponent(fence-undeclared-new) error = %v, want code %v", err, codes.NotFound)
			}
			assertStoredOwner(ctx, t, s, legacy.Name, nil)
		})
	}
}

// TestSetSourcedComponentIntent_InvalidSource checks on both backends that an invalid source is
// rejected before anything is written.
func TestSetSourcedComponentIntent_InvalidSource(t *testing.T) {
	for _, backend := range intentStoreBackends() {
		t.Run(backend.name, func(t *testing.T) {
			ctx := context.Background()
			s := backend.newStore(t)
			_, err := s.SetSourcedComponentIntent(ctx, "intent-source-invalid", intentSpec("v1.0.0"), nil, sourceWith(func(s *store.RequestSource) { s.ClusterUID = "" }))
			if status.Code(err) != codes.InvalidArgument {
				t.Fatalf("SetSourcedComponentIntent() error = %v, want code %v", err, codes.InvalidArgument)
			}
			if _, err := s.GetComponent(ctx, "intent-source-invalid"); status.Code(err) != codes.NotFound {
				t.Errorf("GetComponent() error = %v, want code %v", err, codes.NotFound)
			}
		})
	}
}

// seedOwnedComponent declares ownerCluster (refreshing it if already declared), creates name with
// spec v1.0.0 from ownerSource (checking that it starts at generation 1 owned by ownerSource) or,
// when legacy is set, unsourced at generation 5000, records it as observed and ENABLED, and returns
// the stored record.
func seedOwnedComponent(ctx context.Context, t *testing.T, s store.Store, name string, legacy bool) *store.ComponentRecord {
	t.Helper()
	declareOwnerCluster(ctx, t, s)
	if legacy {
		return seedIntentComponent(ctx, t, s, name, intentSpec("v1.0.0"), 5000, nil, componentspb.ComponentState_COMPONENT_STATE_ENABLED)
	}
	rec, err := s.SetSourcedComponentIntent(ctx, name, intentSpec("v1.0.0"), nil, ownerSource)
	if err != nil {
		t.Fatalf("seed SetSourcedComponentIntent(%s) error = %v", name, err)
	}
	if rec.Generation != 1 || !cmp.Equal(rec.Owner, ownerFor(ownerSource)) {
		t.Fatalf("seed %s: generation = %d, owner = %+v, want generation 1 owned by %+v", name, rec.Generation, rec.Owner, ownerSource)
	}
	if _, err := s.UpdateObservedGeneration(ctx, name, 1, nil, componentspb.ComponentState_COMPONENT_STATE_ENABLED, "Ready"); err != nil {
		t.Fatalf("seed UpdateObservedGeneration(%s) error = %v", name, err)
	}
	stored, err := s.GetComponent(ctx, name)
	if err != nil {
		t.Fatalf("seed GetComponent(%s) error = %v", name, err)
	}
	return stored
}

// assertOwnerOutcome checks rec against the seeded record before: the same ComponentId, wantGen,
// wantOwner, and either a reset state or the seeded state, UpdateTime and Etag.
func assertOwnerOutcome(t *testing.T, before, rec *store.ComponentRecord, wantGen int64, wantOwner *store.SourceOwner, wantReset bool) {
	t.Helper()
	if rec.ComponentID != before.ComponentID {
		t.Errorf("ComponentID = %d, want the seeded %d", rec.ComponentID, before.ComponentID)
	}
	if rec.Generation != wantGen || rec.Spec.GetGeneration() != wantGen {
		t.Errorf("generation = %d, spec.generation = %d, want %d", rec.Generation, rec.Spec.GetGeneration(), wantGen)
	}
	if diff := cmp.Diff(wantOwner, rec.Owner); diff != "" {
		t.Errorf("Owner mismatch (-want +got):\n%s", diff)
	}
	if wantReset {
		if rec.State != componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION {
			t.Errorf("state = %v, want COMPONENT_STATE_PENDING_RECONCILIATION", rec.State)
		}
		return
	}
	if rec.State != before.State || !rec.UpdateTime.Equal(before.UpdateTime) || rec.Etag != before.Etag {
		t.Errorf("state %v, UpdateTime %v, Etag %q changed from %v, %v, %q", rec.State, rec.UpdateTime, rec.Etag, before.State, before.UpdateTime, before.Etag)
	}
}

// assertOwnershipRejected checks that err is a FailedPrecondition with an ErrorInfo wantReason
// naming the seeded owner, and that the stored record of name still equals before.
func assertOwnershipRejected(ctx context.Context, t *testing.T, s store.Store, name string, err error, wantReason string, before *store.ComponentRecord) {
	t.Helper()
	assertOperationOwnershipRejected(ctx, t, s, "SetComponentIntent", name, err, wantReason, before)
}

func assertOperationOwnershipRejected(ctx context.Context, t *testing.T, s store.Store, operation, name string, err error, wantReason string, before *store.ComponentRecord) {
	t.Helper()
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("%s error = %v, want code %v", operation, err, codes.FailedPrecondition)
	}
	metadata := map[string]string{"operation": operation, "owner_namespace": "xmc", "owner_uid": "uid-a", "owner_cluster_uid": ownerSource.ClusterUID}
	if wantReason == store.ReasonStaleGeneration {
		metadata["owner_generation"] = "3"
	}
	wantDetails := []any{&errdetails.ErrorInfo{Reason: wantReason, Domain: store.ErrorInfoDomain, Metadata: metadata}}
	if diff := cmp.Diff(wantDetails, status.Convert(err).Details(), protocmp.Transform()); diff != "" {
		t.Errorf("%s error details mismatch (-want +got):\n%s", operation, diff)
	}
	stored, err := s.GetComponent(ctx, name)
	if err != nil {
		t.Fatalf("GetComponent(%s) error = %v", name, err)
	}
	if diff := cmp.Diff(before, stored, protocmp.Transform()); diff != "" {
		t.Errorf("rejected %s changed the stored record (-before +stored):\n%s", operation, diff)
	}
}

// sourceRejection is a RequestSource that a sourced store RPC must reject with wantReason.
type sourceRejection struct {
	name       string
	src        store.RequestSource
	wantReason string
}

// assertRejectedBy runs call with each rejection's source, in order, as the subtest op/name and
// checks that the returned error carries the rejection's ErrorInfo reason.
func assertRejectedBy(t *testing.T, op string, rejections []sourceRejection, call func(src store.RequestSource) error) {
	t.Helper()
	t.Run(op, func(t *testing.T) {
		for _, tc := range rejections {
			t.Run(tc.name, func(t *testing.T) {
				assertReason(t, call(tc.src), tc.wantReason)
			})
		}
	})
}

// TestSourcedObservationAndDeactivation_Parity checks on both MemoryStore and SpannerStore that
// UpdateObservedGeneration, PrepareDeactivation, and CompleteDeactivation enforce the cluster
// fence and source ownership when a RequestSource is supplied, while unsourced calls still work and
// the owner object is never rejected as STALE_GENERATION, even at an older CR generation.
func TestSourcedObservationAndDeactivation_Parity(t *testing.T) {
	activeCluster := store.ClusterIdentity{UID: prodClusterUID, Name: "prod", Epoch: 2}
	ownerOnProd := store.RequestSource{UID: "uid-a", Generation: 3, Namespace: "xmc", ClusterUID: prodClusterUID, Epoch: 2}
	newerGen := ownerOnProd
	newerGen.Generation = 4
	olderGen := ownerOnProd
	olderGen.Generation = 2
	otherNamespace := ownerOnProd
	otherNamespace.Namespace, otherNamespace.UID = "other", "uid-b"
	otherUID := ownerOnProd
	otherUID.UID = "uid-b"
	casUID := otherUID
	casUID.ExpectedOwnerUID = "uid-a"
	fencedCluster := ownerOnProd
	fencedCluster.ClusterUID = drClusterUID
	// rejections are the sources that every sourced non-intent RPC must reject.
	rejections := []sourceRejection{
		{name: "fenced cluster", src: fencedCluster, wantReason: store.ReasonClusterFenced},
		{name: "other namespace", src: otherNamespace, wantReason: store.ReasonSourceNamespaceConflict},
		{name: "other UID", src: otherUID, wantReason: store.ReasonSourceIncarnationMismatch},
	}

	for _, backend := range intentStoreBackends() {
		t.Run(backend.name, func(t *testing.T) {
			ctx := context.Background()
			s := backend.newStore(t)
			if _, err := s.DeclareClusterEpoch(ctx, activeCluster); err != nil {
				t.Fatalf("DeclareClusterEpoch() error = %v", err)
			}
			const name = "obs-deact-owned"
			if _, err := s.SetSourcedComponentIntent(ctx, name, intentSpec("v1.0.0"), nil, ownerOnProd); err != nil {
				t.Fatalf("SetSourcedComponentIntent() error = %v", err)
			}

			// Rejections on UpdateObservedGeneration leave the component in PENDING_RECONCILIATION.
			assertRejectedBy(t, "UpdateObservedGeneration", rejections, func(src store.RequestSource) error {
				_, err := s.UpdateObservedGeneration(ctx, name, 1, nil, componentspb.ComponentState_COMPONENT_STATE_ENABLED, "Ready=True", src)
				return err
			})

			// The owner object at an older CR generation (informer lag) is never STALE_GENERATION on
			// a non-intent RPC, and keeps the recorded intent owner generation (3).
			obsRec, err := s.UpdateObservedGeneration(ctx, name, 1, nil, componentspb.ComponentState_COMPONENT_STATE_ENABLED, "Ready=True", olderGen)
			if err != nil {
				t.Fatalf("UpdateObservedGeneration(older owner generation) error = %v", err)
			}
			if !cmp.Equal(obsRec.Owner, ownerFor(ownerOnProd)) {
				t.Fatalf("UpdateObservedGeneration(older owner generation) owner = %+v, want %+v", obsRec.Owner, ownerOnProd)
			}

			// Owner updates observed generation at newer CR generation (e.g., while a spec upgrade is held on dependencies)
			// without advancing the recorded intent owner generation past ownerOnProd.Generation (3).
			obsRec, err = s.UpdateObservedGeneration(ctx, name, 1, nil, componentspb.ComponentState_COMPONENT_STATE_ENABLED, "Ready=True", newerGen)
			if err != nil {
				t.Fatalf("UpdateObservedGeneration(owner) error = %v", err)
			}
			if obsRec.State != componentspb.ComponentState_COMPONENT_STATE_ENABLED || !cmp.Equal(obsRec.Owner, ownerFor(ownerOnProd)) {
				t.Fatalf("UpdateObservedGeneration(owner) = (state=%v, owner=%+v), want ENABLED owned by %+v", obsRec.State, obsRec.Owner, ownerOnProd)
			}

			// Rejections on PrepareDeactivation leave the component ENABLED.
			assertRejectedBy(t, "PrepareDeactivation", rejections, func(src store.RequestSource) error {
				_, err := s.PrepareDeactivation(ctx, name, false, src)
				return err
			})
			if got, _ := s.GetComponent(ctx, name); got.State != componentspb.ComponentState_COMPONENT_STATE_ENABLED {
				t.Fatalf("state after rejected PrepareDeactivation = %v, want ENABLED", got.State)
			}

			// PrepareDeactivation from the owner at an older CR generation (generation 2 < recorded
			// generation 3) is allowed and transitions to DISABLING while keeping intent generation 3.
			if _, err := s.PrepareDeactivation(ctx, name, false, olderGen); err != nil {
				t.Fatalf("PrepareDeactivation(older owner generation) error = %v", err)
			}

			// Rejections on CompleteDeactivation leave the component DISABLING.
			assertRejectedBy(t, "CompleteDeactivation", rejections, func(src store.RequestSource) error {
				return s.CompleteDeactivation(ctx, name, false, src)
			})
			if got, _ := s.GetComponent(ctx, name); got.State != componentspb.ComponentState_COMPONENT_STATE_DISABLING {
				t.Fatalf("state after rejected CompleteDeactivation = %v, want DISABLING", got.State)
			}

			// CompleteDeactivation with CAS takeover succeeds and adopts ownership with
			// SourceGeneration 0, as every non-intent adoption does.
			casUID.ExpectedOwnerUID = newerGen.UID
			if err := s.CompleteDeactivation(ctx, name, false, casUID); err != nil {
				t.Fatalf("CompleteDeactivation(CAS) error = %v", err)
			}
			finalRec, err := s.GetComponent(ctx, name)
			if err != nil {
				t.Fatalf("GetComponent() error = %v", err)
			}
			wantOwner := ownerFor(casUID)
			wantOwner.Generation = 0
			if finalRec.State != componentspb.ComponentState_COMPONENT_STATE_DISABLED || !cmp.Equal(finalRec.Owner, wantOwner) {
				t.Errorf("final record = (state=%v, owner=%+v), want DISABLED owned by %+v", finalRec.State, finalRec.Owner, wantOwner)
			}

			// Rejections on ExecuteDeletion leave the component intact in the store. After the CAS
			// takeover, the former owner ownerOnProd is another incarnation.
			deletionRejections := []sourceRejection{
				{name: "fenced cluster", src: fencedCluster, wantReason: store.ReasonClusterFenced},
				{name: "other namespace", src: otherNamespace, wantReason: store.ReasonSourceNamespaceConflict},
				{name: "former owner UID", src: ownerOnProd, wantReason: store.ReasonSourceIncarnationMismatch},
			}
			assertRejectedBy(t, "ExecuteDeletion", deletionRejections, func(src store.RequestSource) error {
				return s.ExecuteDeletion(ctx, name, componentspb.DeletionPolicy_DELETION_POLICY_DELETE, false, src)
			})
			if _, err := s.GetComponent(ctx, name); err != nil {
				t.Fatalf("GetComponent() after rejected ExecuteDeletion = %v, want component preserved", err)
			}

			// ExecuteDeletion by the owner object is never STALE_GENERATION, even from an informer
			// snapshot older than the recorded generation, so the CMS row is never leaked.
			staleDelete := casUID
			staleDelete.ExpectedOwnerUID = ""
			staleDelete.Generation = 2 // older than casUID.Generation (3)
			if err := s.ExecuteDeletion(ctx, name, componentspb.DeletionPolicy_DELETION_POLICY_DELETE, false, staleDelete); err != nil {
				t.Fatalf("ExecuteDeletion(older owner generation) error = %v", err)
			}
			if _, err := s.GetComponent(ctx, name); status.Code(err) != codes.NotFound {
				t.Errorf("GetComponent() after ExecuteDeletion = %v, want NotFound", err)
			}
		})
	}
}

// TestPrepareDeactivation_CascadeKeepsDependentOwner checks the cascade exception on both backends:
// a forced PrepareDeactivation from the target's owner moves a dependent owned by another CR to
// DISABLING without checking the dependent's ownership, and keeps the dependent's recorded owner
// (and, on Spanner, which alone records it, its SourceLastSeenTime).
func TestPrepareDeactivation_CascadeKeepsDependentOwner(t *testing.T) {
	const target, dependent = "cascade-target", "cascade-dependent"
	cluster := store.ClusterIdentity{UID: prodClusterUID, Name: "prod", Epoch: 1}
	targetOwner := store.RequestSource{UID: "uid-a", Generation: 1, Namespace: "xmc", ClusterUID: prodClusterUID, Epoch: 1}
	dependentOwner := targetOwner
	dependentOwner.UID = "uid-b"
	backends := []struct {
		name     string
		newStore func(context.Context, *testing.T) (store.Store, *spanner.Client)
	}{
		{name: "memory", newStore: func(context.Context, *testing.T) (store.Store, *spanner.Client) { return memory.NewMemoryStore(), nil }},
		{name: "spanner", newStore: func(ctx context.Context, t *testing.T) (store.Store, *spanner.Client) { return setupTestStore(ctx, t) }},
	}
	for _, backend := range backends {
		t.Run(backend.name, func(t *testing.T) {
			ctx := context.Background()
			s, client := backend.newStore(ctx, t)
			if _, err := s.DeclareClusterEpoch(ctx, cluster); err != nil {
				t.Fatalf("DeclareClusterEpoch() error = %v", err)
			}
			if _, err := s.SetSourcedComponentIntent(ctx, target, intentSpec("v1.0.0"), nil, targetOwner); err != nil {
				t.Fatalf("SetSourcedComponentIntent(%s) error = %v", target, err)
			}
			if _, err := s.SetSourcedComponentIntent(ctx, dependent, intentSpec("v1.0.0", target), nil, dependentOwner); err != nil {
				t.Fatalf("SetSourcedComponentIntent(%s) error = %v", dependent, err)
			}
			var lastSeenBefore time.Time
			if client != nil {
				lastSeenBefore, _ = readSourceTimes(ctx, t, client, dependent)
			}

			res, err := s.PrepareDeactivation(ctx, target, true, targetOwner)
			if err != nil {
				t.Fatalf("PrepareDeactivation(%s, force, target owner) error = %v", target, err)
			}
			if diff := cmp.Diff([]string{dependent}, res.CascadedDeactivations); diff != "" {
				t.Errorf("CascadedDeactivations mismatch (-want +got):\n%s", diff)
			}
			got, err := s.GetComponent(ctx, dependent)
			if err != nil {
				t.Fatalf("GetComponent(%s) error = %v", dependent, err)
			}
			if got.State != componentspb.ComponentState_COMPONENT_STATE_DISABLING || !cmp.Equal(got.Owner, ownerFor(dependentOwner)) {
				t.Errorf("dependent = (state=%v, owner=%+v), want DISABLING owned by %+v", got.State, got.Owner, ownerFor(dependentOwner))
			}
			if client != nil {
				if lastSeenAfter, _ := readSourceTimes(ctx, t, client, dependent); !lastSeenAfter.Equal(lastSeenBefore) {
					t.Errorf("dependent SourceLastSeenTime = %v, want unchanged %v", lastSeenAfter, lastSeenBefore)
				}
			}
		})
	}
}

// TestExecuteDeletion_OwnerNeverLowersSourceGeneration checks on both backends that a deletion
// from the owner object at an older CR generation keeps the recorded SourceGeneration, and that a
// newer one records its own generation.
func TestExecuteDeletion_OwnerNeverLowersSourceGeneration(t *testing.T) {
	tests := []struct {
		name    string
		delGen  int64
		wantGen int64
	}{
		{name: "older informer snapshot keeps the recorded generation", delGen: 2, wantGen: 3},
		{name: "newer generation is recorded", delGen: 5, wantGen: 5},
	}
	for _, backend := range intentStoreBackends() {
		t.Run(backend.name, func(t *testing.T) {
			ctx := context.Background()
			s := backend.newStore(t)
			for i, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					name := fmt.Sprintf("deletion-owner-%d", i)
					seeded := seedOwnedComponent(ctx, t, s, name, false)
					del := sourceWith(func(s *store.RequestSource) { s.Generation = tt.delGen })
					if err := s.ExecuteDeletion(ctx, name, componentspb.DeletionPolicy_DELETION_POLICY_ORPHAN, true, del); err != nil {
						t.Fatalf("ExecuteDeletion(generation %d) error = %v", tt.delGen, err)
					}
					owner, err := dbstore.OwnerByIDForTest(ctx, s, seeded.ComponentID)
					if err != nil {
						t.Fatalf("OwnerByIDForTest() error = %v", err)
					}
					want := ownerFor(del)
					want.Generation = tt.wantGen
					if !cmp.Equal(owner, want) {
						t.Errorf("owner after ExecuteDeletion = %+v, want %+v", owner, want)
					}
				})
			}
		})
	}
}
