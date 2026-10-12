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

// Retry tests reset package-level Prometheus metrics, so they must not use
// t.Parallel.

import (
	"context"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/prometheus/client_golang/prometheus/testutil"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/grpcerr"
	storagepkg "xmanager_cloud/internal/storage"
)

const (
	retryTestXID  = int64(123)
	otherTestXID  = int64(456)
	retryTestTick = 250 * time.Millisecond
)

// sentUpdate is the projection of a WorkUnitExecutionUpdate asserted by retry tests.
type sentUpdate struct {
	WID, EID int64
	State    essmsgpb.WorkUnitStatus_WorkUnitState
}

// scriptedStorage is a fault-injecting StorageUpdater. A call that includes a
// WID in errForWID fails with that error; otherwise each call consumes the next
// error in errs (nil once exhausted), or alwaysErr if set. Every call is recorded.
type scriptedStorage struct {
	mu        sync.Mutex
	errs      []error
	alwaysErr error
	errForWID map[int64]error
	calls     [][]sentUpdate
	callXIDs  []int64
}

func (s *scriptedStorage) UpdateWorkUnitExecutions(_ context.Context, xid int64, updates []*storagepkg.WorkUnitExecutionUpdate) (map[int64]*essmsgpb.WorkUnitExecution, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	call := make([]sentUpdate, len(updates))
	for i, u := range updates {
		call[i] = sentUpdate{WID: u.WID, EID: u.EID, State: u.WorkUnitExecution.GetStatus().GetState()}
	}
	s.calls = append(s.calls, call)
	s.callXIDs = append(s.callXIDs, xid)
	for _, u := range updates {
		if err, ok := s.errForWID[u.WID]; ok {
			return nil, err
		}
	}
	if len(s.errs) > 0 {
		err := s.errs[0]
		s.errs = s.errs[1:]
		return nil, err
	}
	return nil, s.alwaysErr
}

func (s *scriptedStorage) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

// fakeClock is a manually advanced clock. Tests only advance it between
// processBatches calls, which complete before returning.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newRetryTestBatcher(t *testing.T, storage StorageUpdater) (*UpdateBatcher, *fakeClock) {
	t.Helper()
	updatesDroppedTotal.Reset()
	updatesRetriedTotal.Reset()
	retryQueueSize.Reset()
	clock := &fakeClock{t: time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)}
	b := NewUpdateBatcher(t.Context(), &BatcherConfig{TickDuration: retryTestTick}, storage)
	b.now = clock.now
	return b, clock
}

func testUpdate(wid, eid int64, state essmsgpb.WorkUnitStatus_WorkUnitState) storagepkg.WorkUnitExecutionUpdate {
	return storagepkg.WorkUnitExecutionUpdate{
		WID:               wid,
		EID:               eid,
		WorkUnitExecution: &essmsgpb.WorkUnitExecution{Status: &essmsgpb.WorkUnitStatus{State: state}},
	}
}

func addRunning(t *testing.T, b *UpdateBatcher, xid int64, wids ...int64) {
	t.Helper()
	for _, wid := range wids {
		b.Add(t.Context(), xid, testUpdate(wid, 1, essmsgpb.WorkUnitStatus_RUNNING), JobsetSource)
	}
}

func queuedRetries(b *UpdateBatcher) int {
	b.UpdatesMu.Lock()
	defer b.UpdatesMu.Unlock()
	return len(b.retries)
}

func droppedCount(reason string) float64 {
	return testutil.ToFloat64(updatesDroppedTotal.WithLabelValues(strconv.FormatInt(retryTestXID, 10), reason))
}

func retriedCount(xid int64) float64 {
	return testutil.ToFloat64(updatesRetriedTotal.WithLabelValues(strconv.FormatInt(xid, 10)))
}

func TestRetry_TransientThenSuccessPersistsOnRetry(t *testing.T) {
	storage := &scriptedStorage{errs: []error{grpcerr.ErrUnavailable}}
	b, clock := newRetryTestBatcher(t, storage)
	addRunning(t, b, retryTestXID, 1)

	b.processBatches(t.Context())
	if got := testutil.ToFloat64(retryQueueSize.WithLabelValues()); got != 1 {
		t.Errorf("retry queue size gauge after failure = %v, want 1", got)
	}
	clock.advance(retryTestTick)
	if !b.processBatches(t.Context()) {
		t.Error("processBatches() = false with a due retry and no pending updates, want true")
	}

	running := sentUpdate{WID: 1, EID: 1, State: essmsgpb.WorkUnitStatus_RUNNING}
	if diff := cmp.Diff([][]sentUpdate{{running}, {running}}, storage.calls); diff != "" {
		t.Errorf("storage calls mismatch (-want +got):\n%s", diff)
	}
	if got := queuedRetries(b); got != 0 {
		t.Errorf("queued retries = %d, want 0 after success", got)
	}
	if got := testutil.ToFloat64(retryQueueSize.WithLabelValues()); got != 0 {
		t.Errorf("retry queue size gauge after success = %v, want 0", got)
	}
	if got := retriedCount(retryTestXID); got != 1 {
		t.Errorf("batcher_updates_retried_total = %v, want 1", got)
	}
}

func TestRetry_NotSentBeforeNotBefore(t *testing.T) {
	storage := &scriptedStorage{errs: []error{grpcerr.ErrUnavailable}}
	b, clock := newRetryTestBatcher(t, storage)
	addRunning(t, b, retryTestXID, 1)
	b.processBatches(t.Context())

	clock.advance(retryTestTick - time.Millisecond)
	if b.processBatches(t.Context()) {
		t.Error("processBatches() = true before the retry is due, want false")
	}
	if got := storage.callCount(); got != 1 {
		t.Fatalf("storage calls before notBefore = %d, want 1", got)
	}
	clock.advance(time.Millisecond)
	b.processBatches(t.Context())
	if got := storage.callCount(); got != 2 {
		t.Errorf("storage calls at notBefore = %d, want 2", got)
	}
}

func TestRetryBackoff(t *testing.T) {
	tests := []struct {
		name    string
		tick    time.Duration
		attempt int
		want    time.Duration
	}{
		{name: "first retry uses tick", tick: retryTestTick, attempt: 1, want: retryTestTick},
		{name: "doubles", tick: retryTestTick, attempt: 3, want: time.Second},
		{name: "caps at 30s", tick: retryTestTick, attempt: 8, want: 30 * time.Second},
		{name: "stays capped", tick: retryTestTick, attempt: 50, want: 30 * time.Second},
		{name: "small tick floors at 250ms", tick: time.Millisecond, attempt: 1, want: 250 * time.Millisecond},
		{name: "large tick is the base", tick: 2 * time.Second, attempt: 2, want: 4 * time.Second},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b := &UpdateBatcher{tickDuration: tc.tick}
			if got := b.retryBackoff(tc.attempt); got != tc.want {
				t.Errorf("retryBackoff(%d) = %v, want %v", tc.attempt, got, tc.want)
			}
		})
	}
}

func TestRetry_BackoffDoublesAndCapsAcrossFailures(t *testing.T) {
	storage := &scriptedStorage{alwaysErr: grpcerr.ErrUnavailable}
	b, clock := newRetryTestBatcher(t, storage)
	addRunning(t, b, retryTestXID, 1)
	key := slotKey{xid: retryTestXID, wid: 1, source: JobsetSource}

	var got []time.Duration
	for range 9 {
		b.processBatches(t.Context())
		b.UpdatesMu.Lock()
		delay := b.retries[key].notBefore.Sub(clock.t)
		b.UpdatesMu.Unlock()
		got = append(got, delay)
		clock.advance(delay)
	}

	want := []time.Duration{
		250 * time.Millisecond, 500 * time.Millisecond, time.Second, 2 * time.Second,
		4 * time.Second, 8 * time.Second, 16 * time.Second, 30 * time.Second, 30 * time.Second,
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("retry delays mismatch (-want +got):\n%s", diff)
	}
}

func TestRetry_PendingUpdateMerge(t *testing.T) {
	running := essmsgpb.WorkUnitStatus_RUNNING
	completed := essmsgpb.WorkUnitStatus_COMPLETED
	tests := []struct {
		name      string
		failed    storagepkg.WorkUnitExecutionUpdate
		pending   storagepkg.WorkUnitExecutionUpdate
		advance   time.Duration
		wantSent  []sentUpdate // nil means no second storage call
		wantQueue int
	}{
		{
			name:     "newer pending with same EID supersedes due retry",
			failed:   testUpdate(1, 1, running),
			pending:  testUpdate(1, 1, completed),
			advance:  retryTestTick,
			wantSent: []sentUpdate{{WID: 1, EID: 1, State: completed}},
		},
		{
			name:     "newer pending supersedes retry that is not yet due",
			failed:   testUpdate(1, 1, running),
			pending:  testUpdate(1, 1, completed),
			wantSent: []sentUpdate{{WID: 1, EID: 1, State: completed}},
		},
		{
			name:     "due higher-EID retry replaces lower-EID pending",
			failed:   testUpdate(1, 2, running),
			pending:  testUpdate(1, 1, completed),
			advance:  retryTestTick,
			wantSent: []sentUpdate{{WID: 1, EID: 2, State: running}},
		},
		{
			name:      "lower-EID pending is discarded while higher-EID retry waits",
			failed:    testUpdate(1, 2, running),
			pending:   testUpdate(1, 1, completed),
			wantQueue: 1,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			storage := &scriptedStorage{errs: []error{grpcerr.ErrUnavailable}}
			b, clock := newRetryTestBatcher(t, storage)
			b.Add(t.Context(), retryTestXID, tc.failed, JobsetSource)
			b.processBatches(t.Context())

			b.Add(t.Context(), retryTestXID, tc.pending, JobsetSource)
			clock.advance(tc.advance)
			b.processBatches(t.Context())

			var gotSent []sentUpdate
			if len(storage.calls) > 1 {
				gotSent = storage.calls[1]
			}
			if diff := cmp.Diff(tc.wantSent, gotSent); diff != "" {
				t.Errorf("second flush mismatch (-want +got):\n%s", diff)
			}
			if got := queuedRetries(b); got != tc.wantQueue {
				t.Errorf("queued retries = %d, want %d", got, tc.wantQueue)
			}
		})
	}
}

func TestRetry_NotDueRetryPiggybacksOnExperimentBeingFlushed(t *testing.T) {
	storage := &scriptedStorage{alwaysErr: grpcerr.ErrUnavailable}
	b, _ := newRetryTestBatcher(t, storage)
	addRunning(t, b, retryTestXID, 1)
	addRunning(t, b, otherTestXID, 1)
	b.processBatches(t.Context())
	storage.alwaysErr = nil

	// Neither retry is due yet, but retryTestXID is flushed for a new update.
	addRunning(t, b, retryTestXID, 2)
	b.processBatches(t.Context())

	running := essmsgpb.WorkUnitStatus_RUNNING
	wantLast := []sentUpdate{{WID: 1, EID: 1, State: running}, {WID: 2, EID: 1, State: running}}
	if got := len(storage.calls); got != 3 {
		t.Fatalf("storage calls = %d, want 3 (two failures, one piggybacked flush)", got)
	}
	if diff := cmp.Diff(wantLast, storage.calls[2]); diff != "" {
		t.Errorf("piggybacked flush mismatch (-want +got):\n%s", diff)
	}
	if got := storage.callXIDs[2]; got != retryTestXID {
		t.Errorf("piggybacked flush xid = %d, want %d", got, retryTestXID)
	}
	if got := queuedRetries(b); got != 1 {
		t.Errorf("queued retries = %d, want 1 (other experiment still waiting)", got)
	}
	if got, gotOther := retriedCount(retryTestXID), retriedCount(otherTestXID); got != 1 || gotOther != 0 {
		t.Errorf("batcher_updates_retried_total = {%d: %v, %d: %v}, want {1, 0}", retryTestXID, got, otherTestXID, gotOther)
	}
}

func TestRetry_BudgetExhaustion(t *testing.T) {
	tests := []struct {
		name        string
		elapsed     time.Duration
		wantDropped float64
		wantQueue   int
	}{
		{name: "exactly at budget keeps retrying", elapsed: maxRetryAge, wantQueue: 1},
		{name: "one nanosecond past budget drops", elapsed: maxRetryAge + time.Nanosecond, wantDropped: 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			storage := &scriptedStorage{alwaysErr: grpcerr.ErrUnavailable}
			b, clock := newRetryTestBatcher(t, storage)
			addRunning(t, b, retryTestXID, 1)
			b.processBatches(t.Context())

			clock.advance(tc.elapsed)
			b.processBatches(t.Context())

			if got := droppedCount(dropReasonRetryBudgetExhausted); got != tc.wantDropped {
				t.Errorf("dropped{retry_budget_exhausted} = %v, want %v", got, tc.wantDropped)
			}
			if got := queuedRetries(b); got != tc.wantQueue {
				t.Errorf("queued retries = %d, want %d", got, tc.wantQueue)
			}
		})
	}
}

func TestScheduleRetries_QueuedHigherEIDSurvivesLowerEIDFailure(t *testing.T) {
	b, clock := newRetryTestBatcher(t, &scriptedStorage{})
	key := slotKey{xid: retryTestXID, wid: 1, source: JobsetSource}
	queued := retryEntry{entry: batchEntry{update: testUpdate(1, 2, essmsgpb.WorkUnitStatus_RUNNING), attempt: 1}, notBefore: clock.t}
	b.retries = map[slotKey]retryEntry{key: queued}

	lower := batchEntry{update: testUpdate(1, 1, essmsgpb.WorkUnitStatus_COMPLETED), source: JobsetSource}
	if expired := b.scheduleRetries(retryTestXID, []batchEntry{lower}, clock.t); len(expired) != 0 {
		t.Errorf("scheduleRetries() expired = %v, want none", expired)
	}

	if got := b.retries[key].entry.update.EID; got != 2 {
		t.Errorf("queued retry EID = %d, want 2 (higher EID kept)", got)
	}
}

func TestRetry_PermanentSingleUpdateDroppedWithoutRetry(t *testing.T) {
	storage := &scriptedStorage{alwaysErr: grpcerr.ErrNotFound}
	b, clock := newRetryTestBatcher(t, storage)
	addRunning(t, b, retryTestXID, 1)

	b.processBatches(t.Context())
	clock.advance(time.Minute)
	b.processBatches(t.Context())

	if got := storage.callCount(); got != 1 {
		t.Errorf("storage calls = %d, want 1 (no retry)", got)
	}
	if got := droppedCount(dropReasonPermanent); got != 1 {
		t.Errorf("dropped{permanent} = %v, want 1", got)
	}
	if got := queuedRetries(b); got != 0 {
		t.Errorf("queued retries = %d, want 0", got)
	}
}

func TestRetry_PermanentMultiWorkUnitBatchDropped(t *testing.T) {
	storage := &scriptedStorage{errForWID: map[int64]error{2: grpcerr.ErrNotFound}}
	b, _ := newRetryTestBatcher(t, storage)
	addRunning(t, b, retryTestXID, 1, 2, 3)

	b.processBatches(t.Context())

	if got := storage.callCount(); got != 1 {
		t.Errorf("storage calls = %d, want 1 (no retry)", got)
	}
	if got := droppedCount(dropReasonPermanent); got != 3 {
		t.Errorf("dropped{permanent} = %v, want 3 (whole batch)", got)
	}
	if got := queuedRetries(b); got != 0 {
		t.Errorf("queued retries = %d, want 0", got)
	}
}

func TestRetry_NilClockFallsBackToTimeNow(t *testing.T) {
	storage := &scriptedStorage{errs: []error{grpcerr.ErrUnavailable}}
	b := NewUpdateBatcher(t.Context(), &BatcherConfig{TickDuration: retryTestTick}, storage)
	before := time.Now()
	addRunning(t, b, retryTestXID, 1)

	b.processBatches(t.Context())

	b.UpdatesMu.Lock()
	defer b.UpdatesMu.Unlock()
	retry, ok := b.retries[slotKey{xid: retryTestXID, wid: 1, source: JobsetSource}]
	if !ok {
		t.Fatal("retry not queued with nil clock")
	}
	if retry.entry.firstFailedAt.Before(before) {
		t.Errorf("firstFailedAt = %v, want >= %v (time.Now fallback)", retry.entry.firstFailedAt, before)
	}
}

// TestRetry_ConcurrentAddAndRequeue interleaves Add, processBatches, and
// requeue from multiple goroutines; run it with the race detector. The clock
// advances 300ms per read, more than the 250ms first backoff, so a first retry
// is always due on the next tick, while the whole run stays far below the 5m
// retry budget.
func TestRetry_ConcurrentAddAndRequeue(t *testing.T) {
	storage := &scriptedStorage{alwaysErr: grpcerr.ErrUnavailable}
	b, _ := newRetryTestBatcher(t, storage)
	b.maxParallelExperiments = 4
	var reads atomic.Int64
	base := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	b.now = func() time.Time { return base.Add(time.Duration(reads.Add(1)) * 300 * time.Millisecond) }

	var wg sync.WaitGroup
	for i := range 4 {
		wg.Go(func() {
			for j := range 50 {
				addRunning(t, b, int64(i), int64(j%5))
			}
		})
	}
	wg.Go(func() {
		for range 50 {
			b.processBatches(t.Context())
		}
	})
	wg.Wait()
	// Two more ticks guarantee at least one failed flush followed by one retry,
	// regardless of how the goroutines were scheduled.
	b.processBatches(t.Context())
	b.processBatches(t.Context())

	if got := storage.callCount(); got == 0 {
		t.Error("storage calls = 0, want > 0")
	}
	var retried, dropped float64
	for xid := range int64(4) {
		retried += retriedCount(xid)
		dropped += testutil.ToFloat64(updatesDroppedTotal.WithLabelValues(strconv.FormatInt(xid, 10), dropReasonRetryBudgetExhausted))
	}
	if retried == 0 {
		t.Error("batcher_updates_retried_total = 0, want > 0")
	}
	if dropped != 0 {
		t.Errorf("batcher_updates_dropped_total{retry_budget_exhausted} = %v, want 0", dropped)
	}
}
