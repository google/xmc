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

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/pkg/components/store"
)

const pinnedComponent = "pinned-comp"

// snapshotSeeder stores the snapshot of incarnation componentID of name at gen with spec. A
// SpannerStore reads snapshots from experiment rows, while a MemoryStore records them on writes.
type snapshotSeeder func(t *testing.T, name string, componentID, gen int64, spec *componentspb.ComponentSpec)

// snapshotLookupBackend builds a store and the seeder of its snapshots.
type snapshotLookupBackend struct {
	name     string
	newStore func(t *testing.T) (store.Store, snapshotSeeder)
}

func snapshotLookupBackends() []snapshotLookupBackend {
	return []snapshotLookupBackend{
		{name: "memory", newStore: func(*testing.T) (store.Store, snapshotSeeder) {
			return store.NewMemoryStore(), func(*testing.T, string, int64, int64, *componentspb.ComponentSpec) {}
		}},
		{name: "spanner", newStore: func(t *testing.T) (store.Store, snapshotSeeder) {
			ctx := context.Background()
			s, essClient := setupTestStore(ctx, t)
			xid := int64(31000)
			return s, func(t *testing.T, name string, componentID, gen int64, spec *componentspb.ComponentSpec) {
				t.Helper()
				xid++
				config := mustMarshal(t, &componentspb.Component{Name: "components/" + name, Spec: spec})
				access := mustMarshal(t, &componentspb.ComponentAccessStatus{State: componentspb.ComponentState_COMPONENT_STATE_ENABLED})
				seedExperimentWithSnapshot(ctx, t, essClient, xid, componentID, gen, config, access)
			}
		}},
	}
}

func mustMarshal(t *testing.T, m proto.Message) []byte {
	t.Helper()
	b, err := proto.Marshal(m)
	if err != nil {
		t.Fatalf("proto.Marshal() error = %v", err)
	}
	return b
}

// writeIncarnation writes one intent per version, at generations 1 to len(versions), seeds the
// snapshots of the generations in seedGens, and returns the ComponentId of the incarnation.
func writeIncarnation(ctx context.Context, t *testing.T, s store.Store, seed snapshotSeeder, name string, versions []string, seedGens ...int64) int64 {
	t.Helper()
	var rec *store.ComponentRecord
	for i, version := range versions {
		var err error
		if rec, err = s.SetComponentIntent(ctx, name, intentSpec(version), nil, int64(i+1)); err != nil {
			t.Fatalf("SetComponentIntent(%q, %s) error = %v", name, version, err)
		}
	}
	for _, gen := range seedGens {
		seed(t, name, rec.ComponentID, gen, intentSpec(versions[gen-1]))
	}
	return rec.ComponentID
}

// snapshotOutcome is the part of a resolved snapshot that identifies its incarnation and payload.
type snapshotOutcome struct {
	ComponentID int64
	Generation  int64
	Version     string
}

// TestResolveComponentSnapshot_PinnedIncarnationParity runs the same rows against MemoryStore and
// SpannerStore after the name pinned-comp is reused: the soft-deleted incarnation "old" reached
// generation 3 and the active incarnation "live" is at generation 2, so both hold a generation 1
// and 2. An unpinned request resolves by name and generation; a pinned one resolves only the
// incarnation it names. No experiment pins old generation 1, so only MemoryStore, which records
// every written generation, resolves it (rows with a backend run only on that backend).
func TestResolveComponentSnapshot_PinnedIncarnationParity(t *testing.T) {
	const unknownID = int64(987654321)
	for _, backend := range snapshotLookupBackends() {
		t.Run(backend.name, func(t *testing.T) {
			ctx := context.Background()
			s, seed := backend.newStore(t)
			oldID := writeIncarnation(ctx, t, s, seed, pinnedComponent, []string{"v1.1.0", "v1.2.0", "v1.3.0"}, 2, 3)
			if err := s.ExecuteDeletion(ctx, pinnedComponent, componentspb.DeletionPolicy_DELETION_POLICY_DELETE, false, nil); err != nil {
				t.Fatalf("ExecuteDeletion() error = %v", err)
			}
			liveID := writeIncarnation(ctx, t, s, seed, pinnedComponent, []string{"v2.1.0", "v2.2.0"}, 1)
			otherID := writeIncarnation(ctx, t, s, seed, "other-comp", []string{"v3.1.0", "v3.2.0"}, 1)
			if oldID == liveID {
				t.Fatalf("recreated incarnation reused ComponentId %d", oldID)
			}

			tests := []struct {
				name     string
				backend  string
				pin      *int64
				gen      int64
				want     *snapshotOutcome
				wantCode codes.Code
			}{
				{name: "unpinned generation of the live incarnation", gen: 2, want: &snapshotOutcome{liveID, 2, "v2.2.0"}},
				{name: "unpinned shared generation resolves the live incarnation", gen: 1, want: &snapshotOutcome{liveID, 1, "v2.1.0"}},
				{name: "unpinned later generation falls back to the old incarnation", gen: 3, want: &snapshotOutcome{oldID, 3, "v1.3.0"}},
				{name: "unpinned unknown generation", gen: 4, wantCode: codes.NotFound},
				{name: "pinned live current generation", pin: proto.Int64(liveID), gen: 0, want: &snapshotOutcome{liveID, 2, "v2.2.0"}},
				{name: "pinned live earlier generation", pin: proto.Int64(liveID), gen: 1, want: &snapshotOutcome{liveID, 1, "v2.1.0"}},
				{name: "pinned live later generation", pin: proto.Int64(liveID), gen: 3, wantCode: codes.NotFound},
				{name: "pinned old shared generation resolves the old incarnation", pin: proto.Int64(oldID), gen: 2, want: &snapshotOutcome{oldID, 2, "v1.2.0"}},
				{name: "pinned old last generation", pin: proto.Int64(oldID), gen: 3, want: &snapshotOutcome{oldID, 3, "v1.3.0"}},
				{name: "pinned old generation without an experiment snapshot", backend: "spanner", pin: proto.Int64(oldID), gen: 1, wantCode: codes.NotFound},
				{name: "pinned old generation recorded on write", backend: "memory", pin: proto.Int64(oldID), gen: 1, want: &snapshotOutcome{oldID, 1, "v1.1.0"}},
				{name: "pinned old current generation has no snapshot", pin: proto.Int64(oldID), gen: 0, wantCode: codes.NotFound},
				{name: "pinned old unknown generation", pin: proto.Int64(oldID), gen: 4, wantCode: codes.NotFound},
				{name: "pinned unknown component ID", pin: proto.Int64(unknownID), gen: 1, wantCode: codes.NotFound},
				{name: "pinned component ID of another name", pin: proto.Int64(otherID), gen: 1, wantCode: codes.NotFound},
				{name: "pinned zero component ID", pin: proto.Int64(0), gen: 1, wantCode: codes.InvalidArgument},
				{name: "pinned negative generation", pin: proto.Int64(liveID), gen: -1, wantCode: codes.InvalidArgument},
			}
			for _, tt := range tests {
				if tt.backend != "" && tt.backend != backend.name {
					continue
				}
				t.Run(tt.name, func(t *testing.T) {
					rec, _, err := resolveSnapshot(ctx, s, tt.pin, tt.gen)
					if got := status.Code(err); got != tt.wantCode {
						t.Fatalf("resolve error = %v, want code %v", err, tt.wantCode)
					}
					if tt.want == nil {
						return
					}
					got := &snapshotOutcome{rec.ComponentID, rec.Generation, rec.Spec.GetVersion()}
					if diff := cmp.Diff(tt.want, got); diff != "" {
						t.Errorf("resolved snapshot mismatch (-want +got):\n%s", diff)
					}
				})
			}
		})
	}
}

// resolveSnapshot resolves pinned-comp at gen, pinned to the incarnation pin when it is set.
func resolveSnapshot(ctx context.Context, s store.Store, pin *int64, gen int64) (*store.ComponentRecord, *componentspb.ResolvedComponentCoordinates, error) {
	if pin == nil {
		return s.ResolveComponentSnapshot(ctx, pinnedComponent, gen)
	}
	return s.ResolveComponentSnapshotByID(ctx, pinnedComponent, *pin, gen)
}
