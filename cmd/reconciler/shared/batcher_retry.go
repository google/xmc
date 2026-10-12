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
	// minRetryBackoff is the smallest delay before resending a failed update.
	minRetryBackoff = 250 * time.Millisecond
	// maxRetryBackoff caps the exponential backoff between resend attempts.
	maxRetryBackoff = 30 * time.Second
	// maxRetryAge is how long after its first failure an update is still retried.
	maxRetryAge = 5 * time.Minute

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

// batchEntry is an update in a flush batch, tagged with its source and retry
// history so a failed flush can requeue it.
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

// currentTime returns the injected clock's time, falling back to time.Now when
// the batcher was constructed without one.
func (b *UpdateBatcher) currentTime() time.Time {
	if b.now == nil {
		return time.Now()
	}
	return b.now()
}

// takeSnapshots atomically swaps out pending updates in O(1) when no retries
// are queued, and otherwise resolves retry collisions and drains due or
// piggybacking retries in O(|retries|) under UpdatesMu. Slice construction and
// sorting happen outside UpdatesMu inside each experiment's flush goroutine.
func (b *UpdateBatcher) takeSnapshots() (map[int64]map[int64]map[UpdateSource]storagepkg.WorkUnitExecutionUpdate, map[int64][]batchEntry) {
	b.UpdatesMu.Lock()
	defer b.UpdatesMu.Unlock()
	if len(b.Updates) == 0 && len(b.retries) == 0 {
		return nil, nil
	}
	var pending map[int64]map[int64]map[UpdateSource]storagepkg.WorkUnitExecutionUpdate
	if len(b.Updates) > 0 {
		pending = b.Updates
		b.Updates = make(map[int64]map[int64]map[UpdateSource]storagepkg.WorkUnitExecutionUpdate)
	}
	if len(b.retries) == 0 {
		return pending, nil
	}
	retries := b.drainRetriesLocked(&pending, b.currentTime())
	retryQueueSize.WithLabelValues().Set(float64(len(b.retries)))
	return pending, retries
}

// drainRetriesLocked resolves EID collisions between queued retries and pending
// updates, then drains due retries and any retries piggybacking on an
// experiment already flushing this tick. Callers must hold UpdatesMu.
func (b *UpdateBatcher) drainRetriesLocked(pending *map[int64]map[int64]map[UpdateSource]storagepkg.WorkUnitExecutionUpdate, now time.Time) map[int64][]batchEntry {
	b.discardStaleLocked(*pending)
	for key, retry := range b.retries {
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

// discardStaleLocked applies the EID rule between a retry and a pending update
// for the same slot, mirroring Add: a pending update with an equal or higher
// EID discards the retry; a lower-EID pending update is discarded in favor of
// the retry, whether or not the retry is due. Callers must hold UpdatesMu.
func (b *UpdateBatcher) discardStaleLocked(pending map[int64]map[int64]map[UpdateSource]storagepkg.WorkUnitExecutionUpdate) {
	for key, retry := range b.retries {
		widMap, ok := pending[key.xid]
		if !ok {
			continue
		}
		sourceMap, ok := widMap[key.wid]
		if !ok {
			continue
		}
		u, ok := sourceMap[key.source]
		if !ok {
			continue
		}
		if u.EID >= retry.entry.update.EID {
			delete(b.retries, key)
			continue
		}
		delete(sourceMap, key.source)
		if len(sourceMap) == 0 {
			delete(widMap, key.wid)
		}
		if len(widMap) == 0 {
			delete(pending, key.xid)
		}
	}
}

// buildExperimentBatch combines an experiment's pending updates and drained
// retries into a single slice ordered by (wid, source). Executed inside the
// per-experiment goroutine outside UpdatesMu.
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

// handleFlushFailure requeues a transiently failed batch and drops a
// permanently failed one.
func (b *UpdateBatcher) handleFlushFailure(ctx context.Context, xid int64, batch []batchEntry, err error) {
	if !IsPermanentStorageError(err) {
		b.requeue(ctx, xid, batch, err)
		return
	}
	b.recordDropped(ctx, xid, batch, dropReasonPermanent, err)
}

// countWorkUnits counts distinct WIDs in a batch sorted by WID, without
// allocating.
func countWorkUnits(batch []batchEntry) int {
	count := 0
	for i, entry := range batch {
		if i == 0 || entry.update.WID != batch[i-1].update.WID {
			count++
		}
	}
	return count
}

// requeue schedules failed entries for retry, dropping those past maxRetryAge.
func (b *UpdateBatcher) requeue(ctx context.Context, xid int64, entries []batchEntry, err error) {
	expired := b.scheduleRetries(xid, entries, b.currentTime())
	if len(expired) > 0 {
		b.recordDropped(ctx, xid, expired, dropReasonRetryBudgetExhausted, err)
	}
}

// scheduleRetries records a failed attempt for each entry and queues it with
// exponential backoff. A queued retry with a higher EID for the same slot is
// kept. Returns the entries whose retry budget is exhausted.
func (b *UpdateBatcher) scheduleRetries(xid int64, entries []batchEntry, now time.Time) []batchEntry {
	b.UpdatesMu.Lock()
	defer b.UpdatesMu.Unlock()
	if b.retries == nil {
		b.retries = make(map[slotKey]retryEntry)
	}
	defer func() { retryQueueSize.WithLabelValues().Set(float64(len(b.retries))) }()
	var expired []batchEntry
	for _, entry := range entries {
		entry.attempt++
		if entry.firstFailedAt.IsZero() {
			entry.firstFailedAt = now
		}
		if now.Sub(entry.firstFailedAt) > maxRetryAge {
			expired = append(expired, entry)
			continue
		}
		key := slotKey{xid: xid, wid: entry.update.WID, source: entry.source}
		if queued, ok := b.retries[key]; ok && queued.entry.update.EID > entry.update.EID {
			continue
		}
		b.retries[key] = retryEntry{entry: entry, notBefore: now.Add(b.retryBackoff(entry.attempt))}
	}
	return expired
}

// retryBackoff returns the delay before resend attempt number attempt+1: the
// tick duration (at least minRetryBackoff), doubled per prior attempt, capped
// at maxRetryBackoff.
func (b *UpdateBatcher) retryBackoff(attempt int) time.Duration {
	backoff := max(b.tickDuration, minRetryBackoff)
	for i := 1; i < attempt && backoff < maxRetryBackoff; i++ {
		backoff *= 2
	}
	return min(backoff, maxRetryBackoff)
}

// recordDropped counts and logs entries that will not be persisted.
func (b *UpdateBatcher) recordDropped(ctx context.Context, xid int64, entries []batchEntry, reason string, err error) {
	if len(entries) == 0 {
		return
	}
	updatesDroppedTotal.WithLabelValues(fmt.Sprintf("%d", xid), reason).Add(float64(len(entries)))
	for _, entry := range entries {
		logging.Error(ctx, "Dropping work unit execution update", "xid", xid, "wid", entry.update.WID, "eid", entry.update.EID, "source", entry.source, "reason", reason, "attempts", entry.attempt, "error", err)
	}
}
