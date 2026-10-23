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

// Unit tests for snapshot resolution in snapshot.go, run against memoryRepository.
package dbstore

import (
	"context"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/testing/protocmp"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/pkg/components/store"
)

// snapshotFixture is a catalog with snapshot history:
//   - "app" (ID 1) is active at generation 3 and has a snapshot at generation 2.
//   - An older, deleted incarnation of "app" (ID 9) has a snapshot at generation 5.
//   - "gone" (ID 10) is deleted and has a snapshot at generation 1.
type snapshotFixture struct {
	repo                  *memoryRepository
	app, oldApp, gone     *store.ComponentRecord
	appGen2, oldAppGen5   *store.ComponentRecord
	goneGen1              *store.ComponentRecord
	appCoords, gen2Coords *componentspb.ResolvedComponentCoordinates
}

func newSnapshotFixture() *snapshotFixture {
	f := &snapshotFixture{
		repo:       newMemoryRepository(),
		appCoords:  &componentspb.ResolvedComponentCoordinates{ServiceName: "app-v3", Port: 8080},
		gen2Coords: &componentspb.ResolvedComponentCoordinates{ServiceName: "app-v2", Port: 8080},
	}
	f.app = enabledRecord(1, "app")
	f.app.Generation = 3
	f.app.UpdateTime = baseTime.Add(time.Hour)
	f.app.Status = &componentspb.ComponentAccessStatus{State: componentspb.ComponentState_COMPONENT_STATE_ENABLED, ObservedGeneration: 3, Coordinates: f.appCoords}

	f.oldApp = withState(enabledRecord(9, "app"), componentspb.ComponentState_COMPONENT_STATE_DELETED)
	f.oldApp.Generation = 5
	f.oldApp.CreateTime = baseTime.Add(-48 * time.Hour)
	f.oldApp.UpdateTime = baseTime.Add(-24 * time.Hour)

	f.gone = withState(enabledRecord(10, "gone"), componentspb.ComponentState_COMPONENT_STATE_DELETED)
	f.gone.UpdateTime = baseTime.Add(2 * time.Hour)

	f.appGen2 = &store.ComponentRecord{
		ComponentID: 1,
		Generation:  2,
		Spec:        &componentspb.ComponentSpec{Version: "v1.1.0", Generation: 2, Dependencies: []string{"db"}},
		Labels:      map[string]string{"env": "prod"},
		Status:      &componentspb.ComponentAccessStatus{State: componentspb.ComponentState_COMPONENT_STATE_DISABLED, ObservedGeneration: 2, Coordinates: f.gen2Coords},
	}
	f.oldAppGen5 = &store.ComponentRecord{ComponentID: 9, Generation: 5, Spec: &componentspb.ComponentSpec{Version: "v0.5.0", Dependencies: []string{"db"}}}
	f.goneGen1 = &store.ComponentRecord{ComponentID: 10, Generation: 1, Spec: &componentspb.ComponentSpec{Version: "v0.1.0", Dependencies: []string{"db"}}}

	for _, rec := range []*store.ComponentRecord{f.app, f.oldApp, f.gone} {
		f.repo.putComponent(rec)
	}
	for _, snap := range []*store.ComponentRecord{f.appGen2, f.oldAppGen5, f.goneGen1} {
		f.repo.putSnapshot(snap)
	}
	return f
}

// historical returns snap as ResolveComponentSnapshot returns it for the incarnation owner.
func historical(snap, owner *store.ComponentRecord, state componentspb.ComponentState, observedGen *int64) *store.ComponentRecord {
	rec := store.CloneRecord(snap)
	rec.Name = owner.Name
	rec.CreateTime, rec.UpdateTime = owner.CreateTime, owner.UpdateTime
	rec.State, rec.ObservedGeneration = state, observedGen
	rec.Dependencies = []string{"db"}
	rec.Etag = store.GenerateEtag(rec)
	return rec
}

func TestStore_ResolveComponentSnapshot(t *testing.T) {
	f := newSnapshotFixture()
	observedGen2 := int64(2)
	tests := []struct {
		name       string
		lookup     string
		generation int64
		readErr    error
		want       *store.ComponentRecord
		wantCoords *componentspb.ResolvedComponentCoordinates
		wantCode   codes.Code
		wantMsg    string
		wantErr    error
	}{
		{name: "unspecified generation resolves the active record", lookup: "app", want: readRecord(f.app), wantCoords: f.appCoords},
		{name: "current generation resolves the active record", lookup: "app", generation: 3, want: readRecord(f.app), wantCoords: f.appCoords},
		{
			name:       "older generation resolves the stored snapshot with the active timestamps",
			lookup:     "app",
			generation: 2,
			want:       historical(f.appGen2, f.app, componentspb.ComponentState_COMPONENT_STATE_DISABLED, &observedGen2),
			wantCoords: f.gen2Coords,
		},
		{
			name:       "older generation without a snapshot",
			lookup:     "app",
			generation: 1,
			wantCode:   codes.NotFound,
			wantMsg:    "historical snapshot for generation 1 of component app not found",
		},
		{
			name:       "newer generation resolves a deleted incarnation by name",
			lookup:     "app",
			generation: 5,
			want:       historical(f.oldAppGen5, f.oldApp, componentspb.ComponentState_COMPONENT_STATE_UNSPECIFIED, nil),
		},
		{
			name:       "newer generation without a snapshot",
			lookup:     "app",
			generation: 7,
			wantCode:   codes.NotFound,
			wantMsg:    "snapshot for generation 7 of component app not found (current generation is 3)",
		},
		{
			name:       "deleted component resolves its snapshot by name",
			lookup:     "components/gone",
			generation: 1,
			want:       historical(f.goneGen1, f.gone, componentspb.ComponentState_COMPONENT_STATE_UNSPECIFIED, nil),
		},
		{name: "deleted component at the unspecified generation", lookup: "gone", wantCode: codes.NotFound, wantMsg: "component not found"},
		{
			name:       "deleted component without a snapshot",
			lookup:     "gone",
			generation: 2,
			wantCode:   codes.NotFound,
			wantMsg:    "historical snapshot for generation 2 of component gone not found",
		},
		{name: "negative generation", lookup: "app", generation: -1, wantCode: codes.InvalidArgument},
		{name: "invalid name", lookup: "", wantCode: codes.InvalidArgument},
		{name: "read failure", lookup: "app", readErr: errInjectedRead, wantErr: errInjectedRead},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f.repo.readErr = tt.readErr
			got, gotCoords, err := New(f.repo).ResolveComponentSnapshot(context.Background(), tt.lookup, tt.generation)
			assertError(t, err, tt.wantCode, tt.wantErr)
			if tt.wantMsg != "" {
				if msg := status.Convert(err).Message(); msg != tt.wantMsg {
					t.Errorf("error message = %q, want %q", msg, tt.wantMsg)
				}
			}
			if diff := cmp.Diff(tt.want, got, protocmp.Transform()); diff != "" {
				t.Errorf("ResolveComponentSnapshot(%q, %d) record mismatch (-want +got):\n%s", tt.lookup, tt.generation, diff)
			}
			if diff := cmp.Diff(tt.wantCoords, gotCoords, protocmp.Transform()); diff != "" {
				t.Errorf("ResolveComponentSnapshot(%q, %d) coordinates mismatch (-want +got):\n%s", tt.lookup, tt.generation, diff)
			}
		})
	}
}
