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
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/google/go-cmp/cmp"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/pkg/components/store"
)

// seededHeartbeat repeats the observation of seeded, as recorded by seedOwnedComponent, so it
// changes nothing.
func seededHeartbeat(ctx context.Context, s store.Store, name string, seeded *store.ComponentRecord, src ...store.RequestSource) (*store.ComponentRecord, error) {
	return s.UpdateObservedGeneration(ctx, name, *seeded.ObservedGeneration, nil, componentspb.ComponentState_COMPONENT_STATE_ENABLED, "Ready", src...)
}

// TestNonIntentAdoption_ResetsSourceGeneration_Parity checks on both stores that an adoption
// through a non-intent RPC (R2 of an unsourced record, R5 CAS of another incarnation) records
// SourceGeneration 0, so the adopter's next changed intent at a generation older than the adopting
// call's is applied instead of being rejected as STALE_GENERATION.
func TestNonIntentAdoption_ResetsSourceGeneration_Parity(t *testing.T) {
	tests := []struct {
		name    string
		legacy  bool
		src     store.RequestSource
		wantGen int64
	}{
		{name: "R2 adopts an unsourced record", legacy: true, src: ownerSource, wantGen: 5000},
		{name: "R5 CAS adopts another incarnation", src: sourceWith(func(s *store.RequestSource) {
			s.UID, s.Generation, s.ExpectedOwnerUID = "uid-b", 5, ownerSource.UID
		}), wantGen: 1},
	}
	for _, backend := range intentStoreBackends() {
		for _, tc := range tests {
			t.Run(backend.name+"/"+tc.name, func(t *testing.T) {
				ctx := context.Background()
				s := backend.newStore(t)
				const name = "non-intent-adoption"
				before := seedOwnedComponent(ctx, t, s, name, tc.legacy)

				rec, err := seededHeartbeat(ctx, s, name, before, tc.src)
				if err != nil {
					t.Fatalf("UpdateObservedGeneration(adopt) error = %v", err)
				}
				wantOwner := ownerFor(tc.src)
				wantOwner.Generation = 0
				assertOwnerOutcome(t, before, rec, tc.wantGen, wantOwner, false)

				// The adopter's next intent carries an older generation than its adopting call (the
				// non-intent RPC saw a newer CR), which a recorded src.Generation would reject.
				next := tc.src
				next.ExpectedOwnerUID = ""
				next.Generation = tc.src.Generation - 1
				rec, err = s.SetSourcedComponentIntent(ctx, name, intentSpec("v2.0.0"), nil, next)
				if err != nil {
					t.Fatalf("SetSourcedComponentIntent(adopter) error = %v", err)
				}
				if !cmp.Equal(rec.Owner, ownerFor(next)) {
					t.Errorf("owner after the adopter's intent = %+v, want %+v", rec.Owner, ownerFor(next))
				}
			})
		}
	}
}

// TestUpdateObservedGeneration_UnchangedHeartbeat_Parity checks on both stores that a heartbeat
// that changes nothing keeps UpdateTime and Etag, sourced or not, while a changed observation
// advances them.
func TestUpdateObservedGeneration_UnchangedHeartbeat_Parity(t *testing.T) {
	for _, backend := range intentStoreBackends() {
		t.Run(backend.name, func(t *testing.T) {
			ctx := context.Background()
			s := backend.newStore(t)
			const name = "unchanged-heartbeat"
			before := seedOwnedComponent(ctx, t, s, name, false)

			for _, srcs := range [][]store.RequestSource{nil, {ownerSource}} {
				rec, err := seededHeartbeat(ctx, s, name, before, srcs...)
				if err != nil {
					t.Fatalf("UpdateObservedGeneration(unchanged, sources %v) error = %v", srcs, err)
				}
				assertOwnerOutcome(t, before, rec, 1, ownerFor(ownerSource), false)
				stored, err := s.GetComponent(ctx, name)
				if err != nil {
					t.Fatalf("GetComponent() error = %v", err)
				}
				assertOwnerOutcome(t, before, stored, 1, ownerFor(ownerSource), false)
			}

			rec, err := s.UpdateObservedGeneration(ctx, name, 1, nil, componentspb.ComponentState_COMPONENT_STATE_ENABLED, "Degraded", ownerSource)
			if err != nil {
				t.Fatalf("UpdateObservedGeneration(changed) error = %v", err)
			}
			if rec.UpdateTime.Equal(before.UpdateTime) || rec.Etag == before.Etag {
				t.Errorf("changed observation kept UpdateTime %v and Etag %q", rec.UpdateTime, rec.Etag)
			}
		})
	}
}

// TestSpannerStore_UpdateObservedGeneration_UnchangedHeartbeatRefreshesLastSeen checks that an
// unchanged sourced heartbeat still refreshes SourceLastSeenTime while keeping UpdateTime.
// MemoryStore records no SourceLastSeenTime, so this is Spanner-only.
func TestSpannerStore_UpdateObservedGeneration_UnchangedHeartbeatRefreshesLastSeen(t *testing.T) {
	ctx := context.Background()
	s, client := setupTestStore(ctx, t)
	const name = "heartbeat-last-seen"
	seeded := seedOwnedComponent(ctx, t, s, name, false)
	lastSeenBefore, updateBefore := readSourceTimes(ctx, t, client, name)

	if _, err := seededHeartbeat(ctx, s, name, seeded, ownerSource); err != nil {
		t.Fatalf("UpdateObservedGeneration(unchanged) error = %v", err)
	}

	lastSeenAfter, updateAfter := readSourceTimes(ctx, t, client, name)
	if !lastSeenAfter.After(lastSeenBefore) {
		t.Errorf("SourceLastSeenTime = %v, want after %v", lastSeenAfter, lastSeenBefore)
	}
	if !updateAfter.Equal(updateBefore) {
		t.Errorf("UpdateTime = %v, want unchanged %v", updateAfter, updateBefore)
	}
}

// readSourceTimes returns SourceLastSeenTime and UpdateTime of the active record name.
func readSourceTimes(ctx context.Context, t *testing.T, client *spanner.Client, name string) (lastSeen, update time.Time) {
	t.Helper()
	stmt := spanner.Statement{
		SQL:    `SELECT SourceLastSeenTime, UpdateTime FROM Components WHERE ActiveName = @name`,
		Params: map[string]any{"name": name},
	}
	err := client.Single().Query(ctx, stmt).Do(func(row *spanner.Row) error {
		return row.Columns(&lastSeen, &update)
	})
	if err != nil {
		t.Fatalf("query source times of %s error = %v", name, err)
	}
	return lastSeen, update
}
