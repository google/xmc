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
	"context"
	"errors"
	"slices"

	storagepkg "xmanager_cloud/internal/storage"
)

// dropReasonWorkUnitNotFound labels updates dropped because storage reported
// their work unit as missing.
const dropReasonWorkUnitNotFound = "work_unit_not_found"

// dropMissingWorkUnits handles a *storage.WorkUnitNotFoundError that names work
// units in batch: their updates, and any queued retries for them, are dropped
// and the remaining updates are resent once as a single batch. It returns
// false, without side effects, if err names no work unit in batch.
func (b *UpdateBatcher) dropMissingWorkUnits(ctx context.Context, xid int64, batch []batchEntry, err error) bool {
	var notFound *storagepkg.WorkUnitNotFoundError
	if !errors.As(err, &notFound) || notFound.XID != xid {
		return false
	}
	missingWIDs := make(map[int64]bool, len(notFound.WIDs))
	for _, wid := range notFound.WIDs {
		missingWIDs[wid] = true
	}
	missing, remaining := partitionByWorkUnit(batch, missingWIDs)
	if len(missing) == 0 {
		return false
	}
	b.recordDropped(ctx, xid, append(missing, b.purgeRetries(xid, missingWIDs)...), dropReasonWorkUnitNotFound, err)
	if len(remaining) == 0 {
		return true
	}
	if resendErr := b.callUpdateWorkUnitExecutions(ctx, xid, updatePointers(remaining)); resendErr != nil {
		b.handleResendFailure(ctx, xid, remaining, resendErr)
	}
	return true
}

// handleResendFailure requeues a transiently failed resend and falls back to
// per-work-unit isolation for a permanent one. It never resends as a batch
// again, so a WorkUnitNotFoundError cannot recurse.
func (b *UpdateBatcher) handleResendFailure(ctx context.Context, xid int64, batch []batchEntry, err error) {
	if !IsPermanentStorageError(err) {
		b.requeue(ctx, xid, batch, err)
		return
	}
	b.handlePermanentFailure(ctx, xid, batch, err)
}

// partitionByWorkUnit splits batch into the entries whose WID is in wids and
// the rest, preserving order.
func partitionByWorkUnit(batch []batchEntry, wids map[int64]bool) (matched, rest []batchEntry) {
	for _, entry := range batch {
		if wids[entry.update.WID] {
			matched = append(matched, entry)
			continue
		}
		rest = append(rest, entry)
	}
	return matched, rest
}

// purgeRetries removes and returns queued retries of xid for the given work
// units, which storage reported as missing.
func (b *UpdateBatcher) purgeRetries(xid int64, wids map[int64]bool) []batchEntry {
	b.UpdatesMu.Lock()
	defer b.UpdatesMu.Unlock()
	var purged []batchEntry
	for key, retry := range b.retries {
		if key.xid == xid && wids[key.wid] {
			purged = append(purged, retry.entry)
			delete(b.retries, key)
		}
	}
	retryQueueSize.WithLabelValues().Set(float64(len(b.retries)))
	return purged
}

// dropReasonFor returns dropReasonWorkUnitNotFound if err reports wid as a
// missing work unit, and dropReasonPermanent otherwise.
func dropReasonFor(err error, wid int64) string {
	var notFound *storagepkg.WorkUnitNotFoundError
	if errors.As(err, &notFound) && slices.Contains(notFound.WIDs, wid) {
		return dropReasonWorkUnitNotFound
	}
	return dropReasonPermanent
}
