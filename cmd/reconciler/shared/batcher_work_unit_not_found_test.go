// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package shared

// These tests reset package-level Prometheus metrics, so they must not use
// t.Parallel.

import (
	"fmt"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"google.golang.org/protobuf/testing/protocmp"
	"xmanager_cloud/internal/grpcerr"
	storagepkg "xmanager_cloud/internal/storage"
)

func TestDropMissingWorkUnits(t *testing.T) {
	notFound := func(wids ...int64) error {
		return fmt.Errorf("transaction failed: %w", &storagepkg.WorkUnitNotFoundError{XID: retryTestXID, WIDs: wids})
	}
	tests := []struct {
		name                string
		wids                []int64
		errs                []error
		wantCallWIDs        [][]int64
		wantNotFoundDropped float64
		wantPermDropped     float64
		wantQueue           int
	}{
		{
			name:                "one missing work unit dropped, rest persisted in one resend",
			wids:                []int64{1, 2, 3},
			errs:                []error{notFound(2)},
			wantCallWIDs:        [][]int64{{1, 2, 3}, {1, 3}},
			wantNotFoundDropped: 1,
		},
		{
			name:                "all work units missing drops all without resend",
			wids:                []int64{1, 2},
			errs:                []error{notFound(1, 2)},
			wantCallWIDs:        [][]int64{{1, 2}},
			wantNotFoundDropped: 2,
		},
		{
			name:                "transient resend failure is requeued",
			wids:                []int64{1, 2, 3},
			errs:                []error{notFound(2), grpcerr.ErrUnavailable},
			wantCallWIDs:        [][]int64{{1, 2, 3}, {1, 3}},
			wantNotFoundDropped: 1,
			wantQueue:           2,
		},
		{
			name:                "permanent resend failure falls back to bounded isolation",
			wids:                []int64{1, 2, 3},
			errs:                []error{notFound(2), grpcerr.ErrNotFound},
			wantCallWIDs:        [][]int64{{1, 2, 3}, {1, 3}, {1}, {3}},
			wantNotFoundDropped: 1,
		},
		{
			name:            "error naming no work unit in batch uses isolation",
			wids:            []int64{1, 2},
			errs:            []error{notFound(99), nil, grpcerr.ErrNotFound},
			wantCallWIDs:    [][]int64{{1, 2}, {1}, {2}},
			wantPermDropped: 1,
		},
		{
			name:                "isolation labels a work unit reported missing as work_unit_not_found",
			wids:                []int64{1, 2, 3},
			errs:                []error{notFound(2), notFound(3), nil, notFound(3)},
			wantCallWIDs:        [][]int64{{1, 2, 3}, {1, 3}, {1}, {3}},
			wantNotFoundDropped: 2,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			storage := &scriptedStorage{errs: tc.errs}
			b, _ := newRetryTestBatcher(t, storage)
			addRunning(t, b, retryTestXID, tc.wids...)

			b.processBatches(t.Context())

			if diff := cmp.Diff(tc.wantCallWIDs, callWIDs(storage)); diff != "" {
				t.Errorf("storage call WIDs mismatch (-want +got):\n%s", diff)
			}
			if got := droppedCount(dropReasonWorkUnitNotFound); got != tc.wantNotFoundDropped {
				t.Errorf("dropped{work_unit_not_found} = %v, want %v", got, tc.wantNotFoundDropped)
			}
			if got := droppedCount(dropReasonPermanent); got != tc.wantPermDropped {
				t.Errorf("dropped{permanent} = %v, want %v", got, tc.wantPermDropped)
			}
			if got := queuedRetries(b); got != tc.wantQueue {
				t.Errorf("queued retries = %d, want %d", got, tc.wantQueue)
			}
		})
	}
}

func callWIDs(s *scriptedStorage) [][]int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	calls := make([][]int64, len(s.calls))
	for i, call := range s.calls {
		for _, u := range call {
			calls[i] = append(calls[i], u.WID)
		}
	}
	return calls
}

// widEntry returns a minimal batch entry for wid from source.
func widEntry(wid int64, source UpdateSource) batchEntry {
	return batchEntry{update: storagepkg.WorkUnitExecutionUpdate{WID: wid, EID: 1}, source: source}
}

func TestPartitionByWorkUnit(t *testing.T) {
	batch := []batchEntry{widEntry(1, JobsetSource), widEntry(2, JobsetSource), widEntry(2, WorkloadSource), widEntry(3, JobsetSource)}

	matched, rest := partitionByWorkUnit(batch, map[int64]bool{2: true, 9: true})

	opts := cmp.AllowUnexported(batchEntry{}, storagepkg.WorkUnitExecutionUpdate{})
	if diff := cmp.Diff([]batchEntry{batch[1], batch[2]}, matched, opts, protocmp.Transform()); diff != "" {
		t.Errorf("matched mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]batchEntry{batch[0], batch[3]}, rest, opts, protocmp.Transform()); diff != "" {
		t.Errorf("rest mismatch (-want +got):\n%s", diff)
	}
}

func TestDropMissingWorkUnits_PurgesQueuedRetries(t *testing.T) {
	b, clock := newRetryTestBatcher(t, &scriptedStorage{})
	retry := func(wid int64) retryEntry {
		return retryEntry{entry: widEntry(wid, JobsetSource), notBefore: clock.t.Add(time.Hour)}
	}
	keep := []slotKey{{xid: retryTestXID, wid: 5, source: JobsetSource}, {xid: otherTestXID, wid: 2, source: JobsetSource}}
	b.retries = map[slotKey]retryEntry{
		{xid: retryTestXID, wid: 2, source: WorkloadSource}: retry(2),
		keep[0]: retry(5),
		keep[1]: retry(2),
	}
	batch := []batchEntry{widEntry(1, JobsetSource), widEntry(2, JobsetSource)}
	err := &storagepkg.WorkUnitNotFoundError{XID: retryTestXID, WIDs: []int64{2}}

	if !b.dropMissingWorkUnits(t.Context(), retryTestXID, batch, err) {
		t.Fatal("dropMissingWorkUnits() = false, want true")
	}

	var gotKeys []slotKey
	for key := range b.retries {
		gotKeys = append(gotKeys, key)
	}
	sortKeys := cmpopts.SortSlices(func(x, y slotKey) bool { return x.xid < y.xid })
	if diff := cmp.Diff(keep, gotKeys, cmp.AllowUnexported(slotKey{}), sortKeys); diff != "" {
		t.Errorf("remaining retries mismatch (-want +got):\n%s", diff)
	}
	if got := droppedCount(dropReasonWorkUnitNotFound); got != 2 {
		t.Errorf("dropped{work_unit_not_found} = %v, want 2 (batch entry + purged retry)", got)
	}
	if got := testutil.ToFloat64(retryQueueSize.WithLabelValues()); got != 2 {
		t.Errorf("retry queue size gauge = %v, want 2", got)
	}
}
