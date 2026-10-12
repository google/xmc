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
// Production targets: source_ownership.go, ownership_fence.go, spanner_ownership_fence.go,
// memory_store.go, and spanner_store.go.
// Test type: hermetic cross-backend unit/emulator suite (runs every ownership and fence case
// against both MemoryStore and SpannerStore backed by the in-process Spanner emulator). Kept
// separate from source_ownership_test.go (which unit-tests the pure CheckSourceOwnership rules in
// package store) so end-to-end Store RPC ownership parity is verified in package store_test.
package store_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/testing/protocmp"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/pkg/components/store"
)

// ownerSource is the source that seeds owned rows; cases modify it with sourceWith.
var ownerSource = store.RequestSource{UID: "uid-a", Generation: 3, Namespace: "xmc", ClusterUID: "cluster-1", Epoch: 1}

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
// through SetComponentIntent at generation 5000, and then applies spec from src.
func TestSetSourcedComponentIntent_OwnerParity(t *testing.T) {
	unchanged, changed := intentSpec("v1.0.0"), intentSpec("v1.1.0")
	newerGen := sourceWith(func(s *store.RequestSource) { s.Generation = 4 })
	newEpoch := sourceWith(func(s *store.RequestSource) { s.Epoch = 2 })
	olderGen := sourceWith(func(s *store.RequestSource) { s.Generation = 2 })
	otherObject := sourceWith(func(s *store.RequestSource) { s.UID, s.Generation = "uid-b", 1 })
	casAdopter := sourceWith(func(s *store.RequestSource) { s.UID, s.Generation, s.ExpectedOwnerUID = "uid-b", 1, "uid-a" })
	otherNamespace := sourceWith(func(s *store.RequestSource) { s.Namespace, s.UID, s.ExpectedOwnerUID = "other", "uid-b", "uid-a" })
	casFromOtherCluster := sourceWith(func(s *store.RequestSource) { s.UID, s.ExpectedOwnerUID, s.ClusterUID = "uid-b", "uid-a", "cluster-2" })
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
		{name: "owner at a new cluster epoch records only the owner (R3)", spec: unchanged, src: newEpoch, wantGen: 1, wantOwner: ownerFor(newEpoch)},
		{name: "owner with a changed spec bumps by one (R3)", spec: changed, src: ownerSource, wantGen: 2, wantOwner: ownerFor(ownerSource), wantReset: true},
		{name: "owner at an older generation with an unchanged spec is a no-op (R3)", spec: unchanged, src: olderGen, wantGen: 1, wantOwner: ownerFor(ownerSource)},
		{name: "owner at an older generation with a changed spec is stale (R3)", spec: changed, src: olderGen, wantReason: store.ReasonStaleGeneration},
		{name: "stale intent naming an unregistered dependency is rejected as stale (R3)", spec: withUnregisteredDep, src: olderGen, wantReason: store.ReasonStaleGeneration},
		{name: "another namespace conflicts even with a CAS guard (R4)", spec: unchanged, src: otherNamespace, wantReason: store.ReasonSourceNamespaceConflict},
		{name: "conflicting namespace naming an unregistered dependency is rejected as a conflict (R4)", spec: withUnregisteredDep, src: otherNamespace, wantReason: store.ReasonSourceNamespaceConflict},
		{name: "another object naming the owner adopts an unchanged intent (R5)", spec: unchanged, src: casAdopter, wantGen: 1, wantOwner: ownerFor(casAdopter)},
		{name: "another object naming the owner adopts a changed intent (R5)", spec: changed, src: casAdopter, wantGen: 2, wantOwner: ownerFor(casAdopter), wantReset: true},
		{name: "another object without a CAS guard mismatches (R6)", spec: unchanged, src: otherObject, wantReason: store.ReasonSourceIncarnationMismatch},
		{name: "CAS guard from another cluster mismatches (R6)", spec: unchanged, src: casFromOtherCluster, wantReason: store.ReasonSourceIncarnationMismatch},
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

// seedOwnedComponent creates name with spec v1.0.0 from ownerSource (checking that it starts at
// generation 1 owned by ownerSource) or, when legacy is set, unsourced at generation 5000, records
// it as observed and ENABLED, and returns the stored record.
func seedOwnedComponent(ctx context.Context, t *testing.T, s store.Store, name string, legacy bool) *store.ComponentRecord {
	t.Helper()
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
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("SetSourcedComponentIntent() error = %v, want code %v", err, codes.FailedPrecondition)
	}
	metadata := map[string]string{"operation": "SetComponentIntent", "owner_namespace": "xmc", "owner_uid": "uid-a", "owner_cluster_uid": "cluster-1"}
	if wantReason == store.ReasonStaleGeneration {
		metadata["owner_generation"] = "3"
	}
	wantDetails := []any{&errdetails.ErrorInfo{Reason: wantReason, Domain: store.ErrorInfoDomain, Metadata: metadata}}
	if diff := cmp.Diff(wantDetails, status.Convert(err).Details(), protocmp.Transform()); diff != "" {
		t.Errorf("error details mismatch (-want +got):\n%s", diff)
	}
	stored, err := s.GetComponent(ctx, name)
	if err != nil {
		t.Fatalf("GetComponent(%s) error = %v", name, err)
	}
	if diff := cmp.Diff(before, stored, protocmp.Transform()); diff != "" {
		t.Errorf("rejected intent changed the stored record (-before +stored):\n%s", diff)
	}
}
