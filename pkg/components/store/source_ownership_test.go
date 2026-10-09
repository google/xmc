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
	"fmt"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/pkg/components/store"
)

// TestExecuteDeletion_EpochParity runs the same rows against MemoryStore and SpannerStore so both
// decide sourced deletions identically: only the owner (recorded source) deletes a sourced record.
// Each row seeds an ENABLED component, from seedSource or unsourced when it is nil, and deletes it
// from src.
func TestExecuteDeletion_EpochParity(t *testing.T) {
	seeded := epochSource("uid-b", 0, 3)
	const ownedBy = `is owned by source "uid-b"`
	tests := []struct {
		name       string
		seedSource *store.IntentSource
		src        *store.IntentSource
		wantCode   codes.Code
		wantPrefix string
		// wantDetails are substrings the rejection message must contain.
		wantDetails []string
	}{
		{name: "older object is stale", seedSource: seeded, src: epochSource("uid-a", -time.Hour, 5), wantCode: codes.FailedPrecondition, wantPrefix: store.StaleDeletionMessagePrefix, wantDetails: []string{ownedBy, "from an older object"}},
		{name: "same creation time with a smaller uid is stale", seedSource: seeded, src: epochSource("uid-a", 0, 1), wantCode: codes.FailedPrecondition, wantPrefix: store.StaleDeletionMessagePrefix, wantDetails: []string{ownedBy, "from an older object"}},
		{name: "newer object that has not adopted the record is stale", seedSource: seeded, src: epochSource("uid-c", time.Hour, 1), wantCode: codes.FailedPrecondition, wantPrefix: store.StaleDeletionMessagePrefix, wantDetails: []string{ownedBy, "from a newer object that has not adopted it"}},
		{name: "same creation time with a greater uid is stale", seedSource: seeded, src: epochSource("uid-c", 0, 1), wantCode: codes.FailedPrecondition, wantPrefix: store.StaleDeletionMessagePrefix, wantDetails: []string{ownedBy, "from a newer object that has not adopted it"}},
		{name: "recorded object deletes", seedSource: seeded, src: epochSource("uid-b", 0, 3)},
		{name: "older generation of the recorded object deletes", seedSource: seeded, src: epochSource("uid-b", 0, 1)},
		{name: "unsourced deletion of a sourced record deletes", seedSource: seeded},
		{name: "sourced deletion of an unsourced record deletes", src: epochSource("uid-a", -time.Hour, 1)},
		{name: "invalid source is rejected", seedSource: seeded, src: &store.IntentSource{CreateTime: epochTime, Generation: 1}, wantCode: codes.InvalidArgument},
	}
	for _, backend := range intentStoreBackends() {
		t.Run(backend.name, func(t *testing.T) {
			ctx := context.Background()
			s := backend.newStore(t)
			// Subtests are intentionally sequential (no t.Parallel) because they share one store.
			for i, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					name := fmt.Sprintf("deletion-epoch-%d", i)
					before := seedEpochComponent(ctx, t, s, name, tt.seedSource, 1)

					err := s.ExecuteDeletion(ctx, name, componentspb.DeletionPolicy_DELETION_POLICY_DELETE, false, tt.src)
					if tt.wantCode != codes.OK {
						assertOwnershipRejected(ctx, t, s, "ExecuteDeletion", name, err, tt.wantCode, tt.wantPrefix, tt.wantDetails, before)
						return
					}
					if err != nil {
						t.Fatalf("ExecuteDeletion() error = %v", err)
					}
					if _, err := s.GetComponent(ctx, name); status.Code(err) != codes.NotFound {
						t.Errorf("GetComponent(%s) after deletion error = %v, want NotFound", name, err)
					}
				})
			}
		})
	}
}

// observationSeed is how TestUpdateObservedGeneration_OwnershipParity prepares a row's record.
type observationSeed int

const (
	// seedEnabled stores an ENABLED component.
	seedEnabled observationSeed = iota
	// seedAbsent stores nothing.
	seedAbsent
	// seedSoftDeleted stores an ENABLED component, then soft-deletes it.
	seedSoftDeleted
)

// TestUpdateObservedGeneration_OwnershipParity runs the same rows against MemoryStore and
// SpannerStore so both decide sourced observed-state updates identically: only the owner (recorded
// source) updates a sourced record, and a missing or soft-deleted record is NotFound before
// ownership is checked. Each row prepares its record as seed says, from seedSource or unsourced
// when it is nil, and reports it PENDING_RECONCILIATION from src.
func TestUpdateObservedGeneration_OwnershipParity(t *testing.T) {
	seeded := epochSource("uid-b", 0, 3)
	const ownedBy = `is owned by source "uid-b"`
	const fromOlder = "the observation comes from an older object"
	tests := []struct {
		name        string
		seed        observationSeed
		seedSource  *store.IntentSource
		src         *store.IntentSource
		wantCode    codes.Code
		wantDetails []string
	}{
		{name: "older object is stale", seedSource: seeded, src: epochSource("uid-a", -time.Hour, 5), wantCode: codes.FailedPrecondition, wantDetails: []string{ownedBy, fromOlder}},
		{name: "same creation time with a smaller uid is stale", seedSource: seeded, src: epochSource("uid-a", 0, 1), wantCode: codes.FailedPrecondition, wantDetails: []string{ownedBy, fromOlder}},
		{name: "newer object that has not adopted the record is stale", seedSource: seeded, src: epochSource("uid-c", time.Hour, 1), wantCode: codes.FailedPrecondition, wantDetails: []string{ownedBy, "the observation comes from a newer object that has not adopted it"}},
		{name: "recorded object updates", seedSource: seeded, src: epochSource("uid-b", 0, 3)},
		{name: "older generation of the recorded object updates", seedSource: seeded, src: epochSource("uid-b", 0, 1)},
		{name: "unsourced update of a sourced record updates", seedSource: seeded},
		{name: "sourced update of an unsourced record updates", src: epochSource("uid-a", -time.Hour, 1)},
		{name: "invalid source is rejected", seedSource: seeded, src: &store.IntentSource{CreateTime: epochTime, Generation: 1}, wantCode: codes.InvalidArgument},
		{name: "non-owner on a missing record is not found", seed: seedAbsent, src: epochSource("uid-a", -time.Hour, 5), wantCode: codes.NotFound},
		{name: "non-owner on a soft-deleted record is not found", seed: seedSoftDeleted, seedSource: seeded, src: epochSource("uid-a", -time.Hour, 5), wantCode: codes.NotFound},
	}
	const pending = componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION
	for _, backend := range intentStoreBackends() {
		t.Run(backend.name, func(t *testing.T) {
			ctx := context.Background()
			s := backend.newStore(t)
			// Subtests are intentionally sequential (no t.Parallel) because they share one store.
			for i, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					name := fmt.Sprintf("observation-owner-%d", i)
					before := seedObservationRecord(ctx, t, s, name, tt.seed, tt.seedSource)

					rec, err := s.UpdateObservedGeneration(ctx, name, 1, nil, pending, "Ready=False", tt.src)
					if tt.wantCode == codes.NotFound {
						if status.Code(err) != codes.NotFound {
							t.Fatalf("UpdateObservedGeneration() error = %v, want NotFound", err)
						}
						return
					}
					if tt.wantCode != codes.OK {
						assertOwnershipRejected(ctx, t, s, "UpdateObservedGeneration", name, err, tt.wantCode, store.StaleObservationMessagePrefix, tt.wantDetails, before)
						return
					}
					if err != nil {
						t.Fatalf("UpdateObservedGeneration() error = %v", err)
					}
					if rec.State != pending {
						t.Errorf("UpdateObservedGeneration() state = %v, want %v", rec.State, pending)
					}
				})
			}
		})
	}
}

// seedObservationRecord prepares the record name as seed says, from src or unsourced when it is
// nil, and returns it as stored, or nil when seed leaves no live record.
func seedObservationRecord(ctx context.Context, t *testing.T, s store.Store, name string, seed observationSeed, src *store.IntentSource) *store.ComponentRecord {
	t.Helper()
	if seed == seedAbsent {
		return nil
	}
	rec := seedEpochComponent(ctx, t, s, name, src, 1)
	if seed != seedSoftDeleted {
		return rec
	}
	if err := s.ExecuteDeletion(ctx, name, componentspb.DeletionPolicy_DELETION_POLICY_ORPHAN, true, nil); err != nil {
		t.Fatalf("ExecuteDeletion(%s) error = %v", name, err)
	}
	return nil
}
