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

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"xmanager_cloud/internal/logging"
	storagepkg "xmanager_cloud/internal/storage"
)

const (
	minRetryBackoff = 250 * time.Millisecond // smallest delay before resending
	maxRetryBackoff = 30 * time.Second       // cap on exponential retry backoff
	maxRetryAge     = 5 * time.Minute        // retry budget from first failure
	// maxIsolatedWorkUnits caps work units resent individually per failed batch.
	maxIsolatedWorkUnits = 32
	// maxConsecutivePermanentFailures (work units) stops isolation as experiment-wide.
	maxConsecutivePermanentFailures = 3

	dropReasonPermanent            = "permanent"
	dropReasonRetryBudgetExhausted = "retry_budget_exhausted"
)

var (
	updatesDroppedTotal = MetricsFactory.NewCounterVec(prometheus.CounterOpts{
		Name: "batcher_updates_dropped_total",
		Help: "Total number of work unit updates the batcher dropped without persisting, by reason",
	}, []string{"xid", "reason"})
	updatesRetriedTotal = MetricsFactory.NewCounterVec(prometheus.CounterOpts{
		Name: "batcher_updates_retried_total",
		Help: "Total number of queued retries merged back into a flush batch",
	}, []string{"xid"})
	retryQueueSize = MetricsFactory.NewGaugeVec(prometheus.GaugeOpts{
		Name: "batcher_retry_queue_size",
		Help: "Number of failed work unit updates waiting in the batcher retry queue",
	}, []string{})
)

// slotKey identifies a batcher slot: one update per (xid, wid, source).
type slotKey struct {
	xid, wid int64
	source   UpdateSource
}

// batchEntry is an update in a flush batch, tagged with its source and retry history.
type batchEntry struct {
	update        storagepkg.WorkUnitExecutionUpdate
	source        UpdateSource
	attempt       int       // number of failed flush attempts so far
	firstFailedAt time.Time // zero if the update has never failed
}

// retryEntry is a failed update waiting until notBefore to be resent.
type retryEntry struct {
	entry     batchEntry
	notBefore time.Time
}

func (b *UpdateBatcher) currentTime() time.Time {
	if b.now == nil {
		return time.Now()
	}
	return b.now()
}

// takeSnapshots swaps out pending updates in O(1) when no retries are queued,
// and otherwise drains due or piggybacking retries in O(|retries|) under UpdatesMu.
func (b *UpdateBatcher) takeSnapshots() (map[int64]map[int64]map[UpdateSource]storagepkg.WorkUnitExecutionUpdate, map[int64][]batchEntry) {
	b.UpdatesMu.Lock()
	defer b.UpdatesMu.Unlock()
	clear(b.removed)
	if len(b.Updates) == 0 && len(b.retries) == 0 {
		return nil, nil
	}
	var pending map[int64]map[int64]map[UpdateSource]storagepkg.WorkUnitExecutionUpdate
	if len(b.Updates) > 0 {
		pending = b.Updates
		b.Updates = make(map[int64]map[int64]map[UpdateSource]storagepkg.WorkUnitExecutionUpdate)
	}
	var retries map[int64][]batchEntry
	if len(b.retries) > 0 {
		retries = b.drainRetriesLocked(&pending, b.currentTime())
		retryQueueSize.WithLabelValues().Set(float64(len(b.retries)))
	}
	b.flushing = len(pending) > 0
	return pending, retries
}

func (b *UpdateBatcher) finishFlush() {
	b.UpdatesMu.Lock()
	defer b.UpdatesMu.Unlock()
	b.flushing = false
	clear(b.removed)
}

func (b *UpdateBatcher) removeRetryLocked(key slotKey, eid int64) {
	if b.flushing {
		if b.removed == nil {
			b.removed = make(map[slotKey]int64)
		}
		b.removed[key] = max(b.removed[key], eid)
	}
	if r, ok := b.retries[key]; ok && r.entry.update.EID <= eid {
		delete(b.retries, key)
		retryQueueSize.WithLabelValues().Set(float64(len(b.retries)))
	}
}

// drainRetriesLocked resolves EID collisions with pending updates, then drains
// due retries and retries piggybacking on an experiment flushing this tick.
func (b *UpdateBatcher) drainRetriesLocked(pending *map[int64]map[int64]map[UpdateSource]storagepkg.WorkUnitExecutionUpdate, now time.Time) map[int64][]batchEntry {
	for key, retry := range b.retries {
		if evictPendingUpTo(*pending, key, retry.entry.update.EID-1) {
			delete(b.retries, key)
			continue
		}
		if now.Before(retry.notBefore) {
			continue
		}
		if *pending == nil {
			*pending = make(map[int64]map[int64]map[UpdateSource]storagepkg.WorkUnitExecutionUpdate)
		}
		if _, ok := (*pending)[key.xid]; !ok {
			(*pending)[key.xid] = nil
		}
	}
	if len(*pending) == 0 {
		return nil
	}
	var drained map[int64][]batchEntry
	for key, retry := range b.retries {
		if _, flushing := (*pending)[key.xid]; !flushing {
			continue
		}
		if drained == nil {
			drained = make(map[int64][]batchEntry)
		}
		delete(b.retries, key)
		drained[key.xid] = append(drained[key.xid], retry.entry)
	}
	return drained
}

// evictPendingUpTo deletes m[key] when its EID is <= maxEID (pruning empty
// maps) and reports whether an entry with EID > maxEID remains in m.
func evictPendingUpTo(m map[int64]map[int64]map[UpdateSource]storagepkg.WorkUnitExecutionUpdate, key slotKey, maxEID int64) bool {
	u, ok := m[key.xid][key.wid][key.source]
	if !ok {
		return false
	}
	if u.EID > maxEID {
		return true
	}
	delete(m[key.xid][key.wid], key.source)
	if len(m[key.xid][key.wid]) == 0 {
		delete(m[key.xid], key.wid)
	}
	if len(m[key.xid]) == 0 {
		delete(m, key.xid)
	}
	return false
}

// buildExperimentBatch combines an experiment's pending updates and drained
// retries into a single slice ordered by (wid, source).
func buildExperimentBatch(widMap map[int64]map[UpdateSource]storagepkg.WorkUnitExecutionUpdate, retries []batchEntry) []batchEntry {
	count := len(retries)
	for _, sourceMap := range widMap {
		count += len(sourceMap)
	}
	batch := make([]batchEntry, 0, count)
	batch = append(batch, retries...)
	for _, sourceMap := range widMap {
		for source, update := range sourceMap {
			batch = append(batch, batchEntry{update: update, source: source})
		}
	}
	slices.SortFunc(batch, func(x, y batchEntry) int {
		return cmp.Or(cmp.Compare(x.update.WID, y.update.WID), cmp.Compare(x.source, y.source))
	})
	return batch
}

func updatePointers(batch []batchEntry) []*storagepkg.WorkUnitExecutionUpdate {
	updates := make([]*storagepkg.WorkUnitExecutionUpdate, len(batch))
	for i := range batch {
		updates[i] = &batch[i].update
	}
	return updates
}

// handleFlushFailure requeues a transiently failed batch. A permanent failure
// drops a single-work-unit batch; otherwise up to maxIsolatedWorkUnits work
// units are resent one work unit at a time so a single bad work unit does not
// drop the others. Remaining work units are requeued, or dropped if isolation
// found the failure to be experiment-wide.
func (b *UpdateBatcher) handleFlushFailure(ctx context.Context, xid int64, batch []batchEntry, err error) {
	if !IsPermanentStorageError(err) {
		b.requeue(ctx, xid, batch, err)
		return
	}
	groups := groupByWorkUnit(batch)
	if len(groups) == 1 {
		b.recordDropped(ctx, xid, batch, dropReasonPermanent, err)
		return
	}
	isolated := groups[:min(len(groups), maxIsolatedWorkUnits)]
	experimentWide := b.isolatePermanentFailure(ctx, xid, isolated)
	overflow := slices.Concat(groups[len(isolated):]...)
	if len(overflow) == 0 {
		return
	}
	if experimentWide {
		b.recordDropped(ctx, xid, overflow, dropReasonPermanent, err)
		return
	}
	b.requeue(ctx, xid, overflow, err)
}

func countWorkUnits(batch []batchEntry) int {
	count := 0
	for i, entry := range batch {
		if i == 0 || entry.update.WID != batch[i-1].update.WID {
			count++
		}
	}
	return count
}

// groupByWorkUnit splits a batch sorted by WID into contiguous per-WID groups.
func groupByWorkUnit(batch []batchEntry) [][]batchEntry {
	var groups [][]batchEntry
	for i, entry := range batch {
		if i == 0 || entry.update.WID != batch[i-1].update.WID {
			groups = append(groups, nil)
		}
		groups[len(groups)-1] = append(groups[len(groups)-1], entry)
	}
	return groups
}

// isolatePermanentFailure resends each work unit's updates in one call, at most
// once. It stops at the first transient failure or context cancellation,
// requeueing that work unit and all unsent ones. After
// maxConsecutivePermanentFailures work units in a row fail permanently, it drops
// the unsent ones and reports the failure as experiment-wide.
func (b *UpdateBatcher) isolatePermanentFailure(ctx context.Context, xid int64, groups [][]batchEntry) (experimentWide bool) {
	consecutivePermanent := 0
	// Requeueing must still run (and log) after ctx is cancelled.
	cleanupCtx := context.WithoutCancel(ctx)
	for i, group := range groups {
		if ctx.Err() != nil {
			b.requeue(cleanupCtx, xid, slices.Concat(groups[i:]...), ctx.Err())
			return false
		}
		err := b.callUpdateWorkUnitExecutions(ctx, xid, updatePointers(group))
		if err == nil {
			consecutivePermanent = 0
			continue
		}
		if !IsPermanentStorageError(err) {
			b.requeue(cleanupCtx, xid, slices.Concat(groups[i:]...), err)
			return false
		}
		b.recordDropped(ctx, xid, group, dropReasonPermanent, err)
		consecutivePermanent++
		if consecutivePermanent >= maxConsecutivePermanentFailures {
			b.recordDropped(ctx, xid, slices.Concat(groups[i+1:]...), dropReasonPermanent, err)
			return true
		}
	}
	return false
}

func (b *UpdateBatcher) requeue(ctx context.Context, xid int64, entries []batchEntry, err error) {
	if expired := b.scheduleRetries(xid, entries, b.currentTime()); len(expired) > 0 {
		b.recordDropped(ctx, xid, expired, dropReasonRetryBudgetExhausted, err)
	}
}

// scheduleRetries queues failed entries with exponential backoff, skipping
// entries superseded while in flight by Remove, a newer Add, or a higher-EID
// retry. Returns entries whose retry budget is exhausted.
func (b *UpdateBatcher) scheduleRetries(xid int64, entries []batchEntry, now time.Time) []batchEntry {
	b.UpdatesMu.Lock()
	defer b.UpdatesMu.Unlock()
	if b.retries == nil {
		b.retries = make(map[slotKey]retryEntry)
	}
	defer func() { retryQueueSize.WithLabelValues().Set(float64(len(b.retries))) }()
	var expired []batchEntry
	for _, entry := range entries {
		key := slotKey{xid: xid, wid: entry.update.WID, source: entry.source}
		if b.isSupersededLocked(key, entry.update.EID) {
			continue
		}
		entry.attempt++
		if entry.firstFailedAt.IsZero() {
			entry.firstFailedAt = now
		}
		if now.Sub(entry.firstFailedAt) > maxRetryAge {
			expired = append(expired, entry)
			continue
		}
		b.retries[key] = retryEntry{entry: entry, notBefore: now.Add(b.retryBackoff(entry.attempt))}
	}
	return expired
}

func (b *UpdateBatcher) isSupersededLocked(key slotKey, eid int64) bool {
	if evictPendingUpTo(b.Updates, key, eid-1) {
		return true
	}
	if removedEID, ok := b.removed[key]; ok && eid <= removedEID {
		return true
	}
	queued, ok := b.retries[key]
	return ok && queued.entry.update.EID > eid
}

func (b *UpdateBatcher) retryBackoff(attempt int) time.Duration {
	backoff := max(b.tickDuration, minRetryBackoff)
	for i := 1; i < attempt && backoff < maxRetryBackoff; i++ {
		backoff *= 2
	}
	return min(backoff, maxRetryBackoff)
}

func (b *UpdateBatcher) recordDropped(ctx context.Context, xid int64, entries []batchEntry, reason string, err error) {
	if len(entries) == 0 {
		return
	}
	updatesDroppedTotal.WithLabelValues(fmt.Sprintf("%d", xid), reason).Add(float64(len(entries)))
	for _, entry := range entries {
		logging.Error(ctx, "Dropping work unit execution update", "xid", xid, "wid", entry.update.WID, "eid", entry.update.EID, "source", entry.source, "reason", reason, "attempts", entry.attempt, "error", err)
	}
}
