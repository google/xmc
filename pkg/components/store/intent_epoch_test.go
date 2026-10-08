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
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/pkg/components/store"
)

// epochTime is the creation time of the object that seeds sourced rows.
var epochTime = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

// epochSource returns an IntentSource for object uid created offset after epochTime.
func epochSource(uid string, offset time.Duration, gen int64) *store.IntentSource {
	return &store.IntentSource{UID: uid, CreateTime: epochTime.Add(offset), Generation: gen}
}

// TestSetSourcedComponentIntent_EpochParity runs the same rows against MemoryStore and SpannerStore
// so both order sourced intents identically. Each row seeds an ENABLED component, through
// SetSourcedComponentIntent from seedSource or, when seedSource is nil, through SetComponentIntent
// at seedGen, and then applies spec from src.
func TestSetSourcedComponentIntent_EpochParity(t *testing.T) {
	seeded := epochSource("uid-b", 0, 3)
	unchanged, changed := intentSpec("v1.0.0"), intentSpec("v1.1.0")
	tests := []struct {
		name       string
		seedSource *store.IntentSource
		seedGen    int64
		spec       *componentspb.ComponentSpec
		src        *store.IntentSource
		wantStale  bool
		wantGen    int64
		wantSource *store.IntentSource
		// wantReset expects the state reset by a spec write; otherwise the seeded state, UpdateTime
		// and Etag must be kept.
		wantReset bool
	}{
		{name: "legacy inflated record adopts an unchanged intent without a bump", seedGen: 5000, spec: unchanged, src: seeded, wantGen: 5000, wantSource: seeded},
		{name: "legacy inflated record adopts a changed intent with a bump by one", seedGen: 5000, spec: changed, src: seeded, wantGen: 5001, wantSource: seeded, wantReset: true},
		{name: "same source replay is a no-op", seedSource: seeded, spec: unchanged, src: seeded, wantGen: 1, wantSource: seeded},
		{name: "same object at a newer generation records only the source", seedSource: seeded, spec: unchanged, src: epochSource("uid-b", 0, 4), wantGen: 1, wantSource: epochSource("uid-b", 0, 4)},
		{name: "same object with a changed spec bumps by one", seedSource: seeded, spec: changed, src: epochSource("uid-b", 0, 4), wantGen: 2, wantSource: epochSource("uid-b", 0, 4), wantReset: true},
		{name: "same object and generation with a changed spec bumps by one", seedSource: seeded, spec: changed, src: seeded, wantGen: 2, wantSource: seeded, wantReset: true},
		{name: "same object at an older generation with an unchanged spec is a no-op", seedSource: seeded, spec: unchanged, src: epochSource("uid-b", 0, 2), wantGen: 1, wantSource: seeded},
		{name: "same object at an older generation with a changed spec is stale", seedSource: seeded, spec: changed, src: epochSource("uid-b", 0, 2), wantStale: true},
		{name: "recreated object with an unchanged spec records only the source", seedSource: seeded, spec: unchanged, src: epochSource("uid-a", time.Hour, 1), wantGen: 1, wantSource: epochSource("uid-a", time.Hour, 1)},
		{name: "recreated object with a changed spec bumps by one", seedSource: seeded, spec: changed, src: epochSource("uid-a", time.Hour, 1), wantGen: 2, wantSource: epochSource("uid-a", time.Hour, 1), wantReset: true},
		{name: "different object created earlier is adopted", seedSource: seeded, spec: unchanged, src: epochSource("uid-z", -time.Hour, 9), wantGen: 1, wantSource: epochSource("uid-z", -time.Hour, 9)},
	}
	for _, backend := range intentStoreBackends() {
		t.Run(backend.name, func(t *testing.T) {
			ctx := context.Background()
			s := backend.newStore(t)
			// Subtests are intentionally sequential (no t.Parallel) because they share one store.
			for i, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					name := fmt.Sprintf("intent-epoch-%d", i)
					before := seedEpochComponent(ctx, t, s, name, tt.seedSource, tt.seedGen)

					got, err := s.SetSourcedComponentIntent(ctx, name, tt.spec, nil, *tt.src)
					if tt.wantStale {
						assertStaleIntentRejected(ctx, t, s, name, err, before)
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
						assertEpochOutcome(t, before, rec, tt.wantGen, tt.wantSource, tt.wantReset)
					}
				})
			}
		})
	}
}

// TestSetComponentIntent_KeepsRecordedSource checks on both backends that an unsourced intent
// leaves the recorded source in place.
func TestSetComponentIntent_KeepsRecordedSource(t *testing.T) {
	src := epochSource("uid-b", 0, 3)
	for _, backend := range intentStoreBackends() {
		t.Run(backend.name, func(t *testing.T) {
			ctx := context.Background()
			s := backend.newStore(t)
			before := seedEpochComponent(ctx, t, s, "intent-epoch-unsourced", src, 0)

			got, err := s.SetComponentIntent(ctx, "intent-epoch-unsourced", intentSpec("v1.1.0"), nil, 0)
			if err != nil {
				t.Fatalf("SetComponentIntent() error = %v", err)
			}
			assertEpochOutcome(t, before, got, 2, src, true)
			assertStoredSource(ctx, t, s, "intent-epoch-unsourced", src)
		})
	}
}

// TestSetSourcedComponentIntent_SoftDeletedRecord checks on both backends that a sourced intent for
// a soft-deleted component creates a new component at generation 1 with the source recorded.
func TestSetSourcedComponentIntent_SoftDeletedRecord(t *testing.T) {
	for _, backend := range intentStoreBackends() {
		t.Run(backend.name, func(t *testing.T) {
			ctx := context.Background()
			s := backend.newStore(t)
			deleted := seedEpochComponent(ctx, t, s, "intent-epoch-deleted", epochSource("uid-b", 0, 3), 0)
			if err := s.ExecuteDeletion(ctx, "intent-epoch-deleted", componentspb.DeletionPolicy_DELETION_POLICY_ORPHAN, true); err != nil {
				t.Fatalf("ExecuteDeletion() error = %v", err)
			}

			src := epochSource("uid-b", 0, 4)
			got, err := s.SetSourcedComponentIntent(ctx, "intent-epoch-deleted", intentSpec("v1.1.0"), nil, *src)
			if err != nil {
				t.Fatalf("SetSourcedComponentIntent() error = %v", err)
			}
			if got.ComponentID == deleted.ComponentID || got.Generation != 1 {
				t.Errorf("ComponentID = %d, generation = %d, want a new ComponentId (not %d) at generation 1", got.ComponentID, got.Generation, deleted.ComponentID)
			}
			assertStoredSource(ctx, t, s, "intent-epoch-deleted", src)
		})
	}
}

// TestMemoryStore_SourceOnlyWriteKeepsSnapshot checks that recording only a source leaves the
// snapshot of the current generation as it was written.
func TestMemoryStore_SourceOnlyWriteKeepsSnapshot(t *testing.T) {
	ctx := context.Background()
	ms := store.NewMemoryStore()
	seeded := epochSource("uid-a", 0, 1)
	seedEpochComponent(ctx, t, ms, "redpanda", seeded, 0)
	for _, src := range []*store.IntentSource{epochSource("uid-a", 0, 2), epochSource("uid-b", time.Hour, 1)} {
		if _, err := ms.SetSourcedComponentIntent(ctx, "redpanda", intentSpec("v1.0.0"), nil, *src); err != nil {
			t.Fatalf("SetSourcedComponentIntent(%s) error = %v", src.UID, err)
		}
	}
	assertStoredSource(ctx, t, ms, "redpanda", epochSource("uid-b", time.Hour, 1))
	snap, _, err := ms.ResolveComponentSnapshot(ctx, "redpanda", 1)
	if err != nil {
		t.Fatalf("ResolveComponentSnapshot() error = %v", err)
	}
	if diff := cmp.Diff(seeded, snap.Source); diff != "" {
		t.Errorf("snapshot Source mismatch (-want +got):\n%s", diff)
	}
}

// TestSetSourcedComponentIntent_InvalidSource checks on both backends that sources which cannot
// order intents are rejected before anything is written.
func TestSetSourcedComponentIntent_InvalidSource(t *testing.T) {
	tests := []struct {
		name string
		src  store.IntentSource
	}{
		{name: "empty uid", src: store.IntentSource{CreateTime: epochTime, Generation: 1}},
		{name: "uid wider than the column", src: store.IntentSource{UID: strings.Repeat("u", 65), CreateTime: epochTime, Generation: 1}},
		{name: "zero create time", src: store.IntentSource{UID: "uid-a", Generation: 1}},
		{name: "zero generation", src: store.IntentSource{UID: "uid-a", CreateTime: epochTime}},
	}
	for _, backend := range intentStoreBackends() {
		t.Run(backend.name, func(t *testing.T) {
			ctx := context.Background()
			s := backend.newStore(t)
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					_, err := s.SetSourcedComponentIntent(ctx, "intent-epoch-invalid", intentSpec("v1.0.0"), nil, tt.src)
					if status.Code(err) != codes.InvalidArgument {
						t.Fatalf("SetSourcedComponentIntent() error = %v, want code %v", err, codes.InvalidArgument)
					}
					if _, err := s.GetComponent(ctx, "intent-epoch-invalid"); status.Code(err) != codes.NotFound {
						t.Errorf("GetComponent() error = %v, want code %v", err, codes.NotFound)
					}
				})
			}
		})
	}
}

// TestSpannerStore_SetSourcedComponentIntent_ReadOnlyFastPath checks that only sourced intents that
// write nothing skip the read-write transaction.
func TestSpannerStore_SetSourcedComponentIntent_ReadOnlyFastPath(t *testing.T) {
	seeded := epochSource("uid-b", 0, 3)
	tests := []struct {
		name          string
		src           *store.IntentSource
		wantFallbacks int
		wantCode      codes.Code
		wantSource    *store.IntentSource
	}{
		{name: "same source replay skips the read-write transaction", src: seeded, wantSource: seeded},
		{name: "replay with a sub-microsecond creation time skips the read-write transaction", src: epochSource("uid-b", 300*time.Nanosecond, 3), wantSource: seeded},
		{name: "newer source generation falls back to record the source", src: epochSource("uid-b", 0, 4), wantFallbacks: 1, wantSource: epochSource("uid-b", 0, 4)},
		{name: "different object falls back to record the source", src: epochSource("uid-a", time.Hour, 1), wantFallbacks: 1, wantSource: epochSource("uid-a", time.Hour, 1)},
	}
	ctx := context.Background()
	s, _ := setupTestStore(ctx, t)
	// Subtests are intentionally sequential (no t.Parallel) because they share one store and hook.
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name := fmt.Sprintf("epoch-fast-path-%d", i)
			seedEpochComponent(ctx, t, s, name, seeded, 0)
			fallbacks := 0
			s.SetBeforeIntentWriteForTest(func() { fallbacks++ })
			defer s.SetBeforeIntentWriteForTest(nil)

			_, err := s.SetSourcedComponentIntent(ctx, name, intentSpec("v1.0.0"), nil, *tt.src)
			if status.Code(err) != tt.wantCode {
				t.Fatalf("SetSourcedComponentIntent() error = %v, want code %v", err, tt.wantCode)
			}
			if fallbacks != tt.wantFallbacks {
				t.Errorf("read-write fallbacks = %d, want %d", fallbacks, tt.wantFallbacks)
			}
			assertStoredSource(ctx, t, s, name, tt.wantSource)
		})
	}
}

// seedEpochComponent creates name with spec v1.0.0, from src when it is non-nil (checking that the
// new component starts at generation 1 whatever the source generation) and otherwise unsourced at
// gen, records it as observed and ENABLED, and returns the stored record.
func seedEpochComponent(ctx context.Context, t *testing.T, s store.Store, name string, src *store.IntentSource, gen int64) *store.ComponentRecord {
	t.Helper()
	if src == nil {
		return seedIntentComponent(ctx, t, s, name, intentSpec("v1.0.0"), gen, nil, componentspb.ComponentState_COMPONENT_STATE_ENABLED)
	}
	rec, err := s.SetSourcedComponentIntent(ctx, name, intentSpec("v1.0.0"), nil, *src)
	if err != nil {
		t.Fatalf("seed SetSourcedComponentIntent(%s) error = %v", name, err)
	}
	if rec.Generation != 1 {
		t.Fatalf("seed generation of %s = %d, want 1", name, rec.Generation)
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

// assertEpochOutcome checks rec against the seeded record before: the same ComponentId, wantGen,
// wantSource, and either a reset state or the seeded state, UpdateTime and Etag.
func assertEpochOutcome(t *testing.T, before, rec *store.ComponentRecord, wantGen int64, wantSource *store.IntentSource, wantReset bool) {
	t.Helper()
	if rec.ComponentID != before.ComponentID {
		t.Errorf("ComponentID = %d, want the seeded %d", rec.ComponentID, before.ComponentID)
	}
	if rec.Generation != wantGen || rec.Spec.GetGeneration() != wantGen {
		t.Errorf("generation = %d, spec.generation = %d, want %d", rec.Generation, rec.Spec.GetGeneration(), wantGen)
	}
	if diff := cmp.Diff(wantSource, rec.Source); diff != "" {
		t.Errorf("Source mismatch (-want +got):\n%s", diff)
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

// assertStoredSource checks the recorded source of name.
func assertStoredSource(ctx context.Context, t *testing.T, s store.Store, name string, want *store.IntentSource) {
	t.Helper()
	stored, err := s.GetComponent(ctx, name)
	if err != nil {
		t.Fatalf("GetComponent(%s) error = %v", name, err)
	}
	if diff := cmp.Diff(want, stored.Source); diff != "" {
		t.Errorf("stored Source of %s mismatch (-want +got):\n%s", name, diff)
	}
}
