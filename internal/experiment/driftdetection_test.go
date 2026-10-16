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

package experiment

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/testing/protocmp"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/storage"
)

const testXID = int64(1002)

func TestDriftCache_Get(t *testing.T) {
	baseTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	const xid = int64(42)

	tests := []struct {
		name      string
		lookupXID int64
		lookupAt  time.Time
		wantOK    bool
		wantDiffs []string
	}{
		{
			name:      "hit within TTL",
			lookupXID: xid,
			lookupAt:  baseTime.Add(driftCacheTTL - time.Second),
			wantOK:    true,
			wantDiffs: []string{"comp-a"},
		},
		{
			name:      "hit exactly at expiry",
			lookupXID: xid,
			lookupAt:  baseTime.Add(driftCacheTTL),
			wantOK:    true,
			wantDiffs: []string{"comp-a"},
		},
		{
			name:      "miss after expiry",
			lookupXID: xid,
			lookupAt:  baseTime.Add(driftCacheTTL + time.Nanosecond),
		},
		{
			name:      "miss for unknown xid",
			lookupXID: xid + 1,
			lookupAt:  baseTime,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cache := newDriftCache()
			cache.set(xid, false, []string{"comp-a"}, baseTime)

			got, ok := cache.get(tt.lookupXID, tt.lookupAt)
			if ok != tt.wantOK {
				t.Fatalf("get(%d) ok = %v, want %v", tt.lookupXID, ok, tt.wantOK)
			}
			if diff := cmp.Diff(tt.wantDiffs, got.diffs); diff != "" {
				t.Errorf("get(%d) diffs mismatch (-want +got):\n%s", tt.lookupXID, diff)
			}
		})
	}
}

func TestDriftCache_NilReceiver(t *testing.T) {
	var cache *driftCache
	now := time.Now()

	cache.set(1, true, nil, now)
	if _, ok := cache.get(1, now); ok {
		t.Errorf("get on nil cache ok = true, want false")
	}
}

func TestDriftCache_SetEvictsExpiredEntriesAtCapacity(t *testing.T) {
	baseTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cache := newDriftCache()
	for xid := int64(0); xid < maxDriftCacheItems; xid++ {
		cache.set(xid, true, nil, baseTime)
	}
	// Refresh one entry so it survives eviction.
	const freshXID = int64(7)
	later := baseTime.Add(driftCacheTTL + time.Second)
	cache.set(freshXID, true, nil, later.Add(-time.Second))

	cache.set(maxDriftCacheItems, false, []string{"new"}, later)

	if got, want := len(cache.entries), 2; got != want {
		t.Fatalf("len(entries) = %d, want %d after evicting expired entries", got, want)
	}
	if _, ok := cache.get(freshXID, later); !ok {
		t.Errorf("get(%d) ok = false, want unexpired entry to survive eviction", freshXID)
	}
	if _, ok := cache.get(maxDriftCacheItems, later); !ok {
		t.Errorf("get(%d) ok = false, want newly set entry", int64(maxDriftCacheItems))
	}
}

func TestDriftCache_SetBelowCapacityKeepsExpiredEntries(t *testing.T) {
	baseTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cache := newDriftCache()
	cache.set(1, true, nil, baseTime)

	cache.set(2, true, nil, baseTime.Add(driftCacheTTL+time.Second))

	if got, want := len(cache.entries), 2; got != want {
		t.Errorf("len(entries) = %d, want %d (no eviction below capacity)", got, want)
	}
}

// fakeDriftStore records lookups and returns configured drift results.
type fakeDriftStore struct {
	diffs          []storage.ComponentDiff
	fetchErr       error
	fetchCalls     int
	lastFetchXID   int64
	lastFetchLimit int
}

func (f *fakeDriftStore) FetchDriftedComponents(_ context.Context, xid int64, limit int) ([]storage.ComponentDiff, error) {
	f.fetchCalls++
	f.lastFetchXID = xid
	f.lastFetchLimit = limit
	return f.diffs, f.fetchErr
}

func activeStatus() *essmsgpb.ExperimentStatus {
	return &essmsgpb.ExperimentStatus{ActiveState: essmsgpb.ExperimentStatus_ACTIVE}
}

func TestDriftEvaluator_Evaluate_SkipsWithoutLookups(t *testing.T) {
	compName := "comp-a"
	tests := []struct {
		name             string
		enableComponents bool
		expStatus        *essmsgpb.ExperimentStatus
	}{
		{name: "components disabled", enableComponents: false, expStatus: activeStatus()},
		{name: "nil status", enableComponents: true, expStatus: nil},
		{name: "inactive experiment", enableComponents: true, expStatus: &essmsgpb.ExperimentStatus{ActiveState: essmsgpb.ExperimentStatus_INACTIVE}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := &fakeDriftStore{diffs: []storage.ComponentDiff{{ComponentID: 1, Name: &compName, LockedGeneration: 1}}}
			e := NewDriftEvaluator(src, tt.enableComponents)

			if err := e.Evaluate(context.Background(), testXID, tt.expStatus); err != nil {
				t.Fatalf("Evaluate() error = %v, want nil", err)
			}
			if src.fetchCalls != 0 {
				t.Errorf("FetchDriftedComponents calls = %d, want 0", src.fetchCalls)
			}
			if msg := tt.expStatus.GetStructuredStatusMessage(); msg != nil {
				t.Errorf("StructuredStatusMessage = %v, want nil", msg)
			}
		})
	}
}

func TestDriftEvaluator_Evaluate_Consistent(t *testing.T) {
	src := &fakeDriftStore{}
	e := NewDriftEvaluator(src, true)

	for i := range 2 {
		expStatus := activeStatus()
		if err := e.Evaluate(context.Background(), testXID, expStatus); err != nil {
			t.Fatalf("Evaluate() call %d error = %v, want nil", i, err)
		}
		if msg := expStatus.GetStructuredStatusMessage(); msg != nil {
			t.Errorf("Evaluate() call %d StructuredStatusMessage = %v, want nil", i, msg)
		}
	}
	if src.fetchCalls != 1 {
		t.Errorf("FetchDriftedComponents calls = %d, want 1 with second call served from cache", src.fetchCalls)
	}
	if src.lastFetchXID != testXID {
		t.Errorf("FetchDriftedComponents xid = %d, want %d", src.lastFetchXID, testXID)
	}
}

func TestDriftEvaluator_Evaluate_DriftedAttachesWarningAndCaches(t *testing.T) {
	compName := "comp-a"
	observed := int64(2)
	src := &fakeDriftStore{
		diffs: []storage.ComponentDiff{{ComponentID: 1, Name: &compName, LockedGeneration: 1, ObservedGeneration: &observed}},
	}
	e := NewDriftEvaluator(src, true)
	want := &essmsgpb.StructuredMessage{
		Severity:       essmsgpb.StructuredMessage_WARNING,
		Source:         essmsgpb.StructuredMessage_XMANAGER_BACKEND,
		UserFacingText: "Component drift detected: live component generations diverge from snapshots: comp-a (snapshot generation 1 != live observed generation 2)",
	}

	for i := range 2 {
		expStatus := activeStatus()
		if err := e.Evaluate(context.Background(), testXID, expStatus); err != nil {
			t.Fatalf("Evaluate() call %d error = %v, want nil", i, err)
		}
		got := expStatus.GetStructuredStatusMessage()
		if diff := cmp.Diff(want, got, protocmp.Transform(), protocmp.IgnoreFields(&essmsgpb.StructuredMessage{}, "create_time")); diff != "" {
			t.Errorf("Evaluate() call %d message mismatch (-want +got):\n%s", i, diff)
		}
	}
	if src.fetchCalls != 1 {
		t.Errorf("FetchDriftedComponents calls = %d, want 1 with second call served from cache", src.fetchCalls)
	}
	if src.lastFetchXID != testXID {
		t.Errorf("FetchDriftedComponents xid = %d, want %d", src.lastFetchXID, testXID)
	}
	if src.lastFetchLimit != maxDriftDetails+1 {
		t.Errorf("FetchDriftedComponents limit = %d, want %d", src.lastFetchLimit, maxDriftDetails+1)
	}
}

func TestDriftEvaluator_Evaluate_LookupError(t *testing.T) {
	errLookup := errors.New("lookup failed")
	e := NewDriftEvaluator(&fakeDriftStore{fetchErr: errLookup}, true)
	expStatus := activeStatus()

	err := e.Evaluate(context.Background(), testXID, expStatus)
	if !errors.Is(err, errLookup) {
		t.Fatalf("Evaluate() error = %v, want wrapping %v", err, errLookup)
	}
	if msg := expStatus.GetStructuredStatusMessage(); msg != nil {
		t.Errorf("StructuredStatusMessage = %v, want nil on error", msg)
	}
	if _, ok := e.driftCache.get(testXID, time.Now()); ok {
		t.Errorf("cache populated after lookup error, want no entry")
	}
}

func TestDriftEvaluator_FetchDriftDetails_TruncatesAfterMaxDriftDetails(t *testing.T) {
	diffs := make([]storage.ComponentDiff, maxDriftDetails+1)
	for i := range diffs {
		diffs[i] = storage.ComponentDiff{ComponentID: int64(i + 1), LockedGeneration: 1}
	}
	e := NewDriftEvaluator(&fakeDriftStore{diffs: diffs}, true)

	got, err := e.fetchDriftDetails(context.Background(), testXID)
	if err != nil {
		t.Fatalf("fetchDriftDetails() error = %v, want nil", err)
	}
	if len(got) != maxDriftDetails+1 {
		t.Fatalf("len(fetchDriftDetails()) = %d, want %d", len(got), maxDriftDetails+1)
	}
	if got[maxDriftDetails] != "..." {
		t.Errorf("last detail = %q, want %q", got[maxDriftDetails], "...")
	}
}

func TestFormatComponentDiff(t *testing.T) {
	name := "redpanda"
	observed := int64(2)
	activeState := int64(1)
	deletedState := int64(storage.ComponentStateDeleted)

	tests := []struct {
		name string
		diff storage.ComponentDiff
		want string
	}{
		{
			name: "missing from catalog",
			diff: storage.ComponentDiff{ComponentID: 202, LockedGeneration: 1},
			want: "Component ID 202 (missing from catalog, snapshot generation 1)",
		},
		{
			name: "deleted from catalog",
			diff: storage.ComponentDiff{ComponentID: 202, Name: &name, LockedGeneration: 1, ObservedGeneration: &observed, State: &deletedState},
			want: "redpanda (deleted from catalog, snapshot generation 1)",
		},
		{
			name: "generation diverged",
			diff: storage.ComponentDiff{ComponentID: 202, Name: &name, LockedGeneration: 1, ObservedGeneration: &observed, State: &activeState},
			want: "redpanda (snapshot generation 1 != live observed generation 2)",
		},
		{
			name: "generation diverged with unknown state and observed generation",
			diff: storage.ComponentDiff{ComponentID: 202, Name: &name, LockedGeneration: 1},
			want: "redpanda (snapshot generation 1 != live observed generation 0)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatComponentDiff(tt.diff); got != tt.want {
				t.Errorf("formatComponentDiff() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFormatDriftWarningMessage(t *testing.T) {
	tests := []struct {
		name  string
		diffs []string
		want  *essmsgpb.StructuredMessage
	}{
		{
			name: "no diffs",
			want: &essmsgpb.StructuredMessage{
				Severity:       essmsgpb.StructuredMessage_WARNING,
				Source:         essmsgpb.StructuredMessage_XMANAGER_BACKEND,
				UserFacingText: "Component drift detected: live component generations diverge from snapshots",
			},
		},
		{
			name:  "with diffs",
			diffs: []string{"comp-a (deleted from catalog, snapshot generation 1)", "..."},
			want: &essmsgpb.StructuredMessage{
				Severity:       essmsgpb.StructuredMessage_WARNING,
				Source:         essmsgpb.StructuredMessage_XMANAGER_BACKEND,
				UserFacingText: "Component drift detected: live component generations diverge from snapshots: comp-a (deleted from catalog, snapshot generation 1), ...",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatDriftWarningMessage(tt.diffs)
			if got.GetCreateTime() == nil {
				t.Errorf("formatDriftWarningMessage() CreateTime = nil, want non-nil")
			}
			if diff := cmp.Diff(tt.want, got, protocmp.Transform(), protocmp.IgnoreFields(&essmsgpb.StructuredMessage{}, "create_time")); diff != "" {
				t.Errorf("formatDriftWarningMessage() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestAttachDriftWarningMessage(t *testing.T) {
	const driftText = "Component drift detected: comp-a"
	driftMsg := &essmsgpb.StructuredMessage{
		Severity:       essmsgpb.StructuredMessage_WARNING,
		Source:         essmsgpb.StructuredMessage_XMANAGER_BACKEND,
		UserFacingText: driftText,
	}

	tests := []struct {
		name    string
		current *essmsgpb.StructuredMessage
		want    *essmsgpb.StructuredMessage
	}{
		{
			name: "sets message when none exists",
			want: driftMsg,
		},
		{
			name:    "preserves higher severity message",
			current: &essmsgpb.StructuredMessage{Severity: essmsgpb.StructuredMessage_ERROR, UserFacingText: "Fatal crash"},
			want:    &essmsgpb.StructuredMessage{Severity: essmsgpb.StructuredMessage_ERROR, UserFacingText: "Fatal crash"},
		},
		{
			name:    "skips duplicate drift notice",
			current: &essmsgpb.StructuredMessage{Severity: essmsgpb.StructuredMessage_WARNING, UserFacingText: "Component drift detected: old"},
			want:    &essmsgpb.StructuredMessage{Severity: essmsgpb.StructuredMessage_WARNING, UserFacingText: "Component drift detected: old"},
		},
		{
			name:    "upgrades lower severity and appends text",
			current: &essmsgpb.StructuredMessage{Severity: essmsgpb.StructuredMessage_INFO, UserFacingText: "All workers scheduled"},
			want:    &essmsgpb.StructuredMessage{Severity: essmsgpb.StructuredMessage_WARNING, UserFacingText: "All workers scheduled; " + driftText},
		},
		{
			name:    "fills empty text on existing warning",
			current: &essmsgpb.StructuredMessage{Severity: essmsgpb.StructuredMessage_WARNING},
			want:    &essmsgpb.StructuredMessage{Severity: essmsgpb.StructuredMessage_WARNING, UserFacingText: driftText},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expStatus := &essmsgpb.ExperimentStatus{StructuredStatusMessage: tt.current}

			attachDriftWarningMessage(expStatus, driftMsg)

			if diff := cmp.Diff(tt.want, expStatus.GetStructuredStatusMessage(), protocmp.Transform()); diff != "" {
				t.Errorf("attachDriftWarningMessage() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
