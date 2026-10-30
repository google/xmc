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

// Package retentionsweep periodically deletes expired rows from the ESS database through
// storage.ExpiredRowStore. Every ESS replica runs a sweeper; the storage backend serializes their
// sweeps, so no leader election is needed.
package retentionsweep

import (
	"context"
	"errors"
	"fmt"
	"time"

	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/storage"
)

const (
	// DefaultInterval is how often each ESS replica starts a sweep.
	DefaultInterval = 12 * time.Hour
	// DefaultBatchSize is the maximum number of rows that one DeleteExpiredRows call deletes from
	// each table.
	DefaultBatchSize = 1000
	// maxBatchesPerSweep bounds one sweep, so a large backlog drains over several intervals instead
	// of in one unbounded loop.
	maxBatchesPerSweep = 100
	// batchTimeout bounds one DeleteExpiredRows call.
	batchTimeout = time.Minute
)

// Sweeper deletes expired rows in the background until Close is called.
type Sweeper struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// Start starts a Sweeper that sweeps store immediately and then every interval, deleting up to
// batchSize rows per table in each DeleteExpiredRows call.
func Start(ctx context.Context, store storage.ExpiredRowStore, interval time.Duration, batchSize int) (*Sweeper, error) {
	if store == nil {
		return nil, errors.New("expired row store cannot be nil")
	}
	if interval <= 0 {
		return nil, fmt.Errorf("sweep interval must be positive, got %v", interval)
	}
	if batchSize <= 0 {
		return nil, fmt.Errorf("sweep batch size must be positive, got %d", batchSize)
	}
	runCtx, cancel := context.WithCancel(ctx)
	s := &Sweeper{cancel: cancel, done: make(chan struct{})}
	go func() {
		defer cancel()
		defer close(s.done)
		run(runCtx, store, interval, batchSize)
	}()
	return s, nil
}

// Close stops the Sweeper, canceling any in-flight batch, and waits for it to exit. It is safe to
// call more than once.
func (s *Sweeper) Close() error {
	if s == nil {
		return nil
	}
	s.cancel()
	<-s.done
	return nil
}

// run sweeps store immediately and then every interval until ctx is done.
func run(ctx context.Context, store storage.ExpiredRowStore, interval time.Duration, batchSize int) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		deleted, err := sweep(ctx, store, batchSize)
		if err != nil && ctx.Err() == nil {
			logging.Warn(ctx, "Failed to delete expired rows", "deleted", deleted, "error", err)
		}
		if deleted > 0 {
			logging.Info(ctx, "Deleted expired rows", "deleted", deleted)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// sweep deletes expired rows batch by batch until a batch shows that every table is drained,
// ctx is done, or maxBatchesPerSweep batches have run, and returns the number of rows deleted.
func sweep(ctx context.Context, store storage.ExpiredRowStore, batchSize int) (int64, error) {
	var total int64
	for range maxBatchesPerSweep {
		n, err := deleteBatch(ctx, store, batchSize)
		total += n
		if err != nil {
			return total, fmt.Errorf("failed to delete expired rows: %w", err)
		}
		// A batch deletes at most batchSize rows from each table, so a smaller total means that every
		// table is drained, or that another replica holds the sweep lock and is sweeping instead.
		if n < int64(batchSize) {
			return total, nil
		}
	}
	return total, nil
}

// deleteBatch runs one DeleteExpiredRows call bounded by batchTimeout.
func deleteBatch(ctx context.Context, store storage.ExpiredRowStore, batchSize int) (int64, error) {
	batchCtx, cancel := context.WithTimeout(ctx, batchTimeout)
	defer cancel()
	return store.DeleteExpiredRows(batchCtx, batchSize)
}
