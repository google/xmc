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

package retentionsweep

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"xmanager_cloud/internal/storage"
)

const waitTimeout = 5 * time.Second

// batchResult is the outcome of one fake DeleteExpiredRows call.
type batchResult struct {
	deleted int64
	err     error
}

// fakeStore records DeleteExpiredRows calls and answers each with deleteFn(ctx, call), where call
// counts from 1.
type fakeStore struct {
	mu         sync.Mutex
	batchSizes []int
	deleteFn   func(ctx context.Context, call int) (int64, error)
}

var _ storage.ExpiredRowStore = (*fakeStore)(nil)

func (f *fakeStore) DeleteExpiredRows(ctx context.Context, batchSize int) (int64, error) {
	f.mu.Lock()
	f.batchSizes = append(f.batchSizes, batchSize)
	call := len(f.batchSizes)
	f.mu.Unlock()
	return f.deleteFn(ctx, call)
}

func (f *fakeStore) recordedBatchSizes() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int(nil), f.batchSizes...)
}

// scripted answers call i with results[i-1], and with zero rows once results run out.
func scripted(results ...batchResult) func(context.Context, int) (int64, error) {
	return func(_ context.Context, call int) (int64, error) {
		if call > len(results) {
			return 0, nil
		}
		return results[call-1].deleted, results[call-1].err
	}
}

func TestSweep(t *testing.T) {
	const batchSize = 10
	errDB := errors.New("database unavailable")
	tests := []struct {
		name          string
		deleteFn      func(context.Context, int) (int64, error)
		wantDeleted   int64
		wantErr       error
		wantBatchSize []int
	}{
		{
			name:          "nothing expired or sweep lock held elsewhere",
			deleteFn:      scripted(batchResult{deleted: 0}),
			wantDeleted:   0,
			wantBatchSize: []int{batchSize},
		},
		{
			name:          "drained by a partial batch",
			deleteFn:      scripted(batchResult{deleted: 7}),
			wantDeleted:   7,
			wantBatchSize: []int{batchSize},
		},
		{
			name:          "full batches repeat until a partial batch",
			deleteFn:      scripted(batchResult{deleted: 25}, batchResult{deleted: batchSize}, batchResult{deleted: 3}),
			wantDeleted:   38,
			wantBatchSize: []int{batchSize, batchSize, batchSize},
		},
		{
			name:          "exactly one full batch needs an empty confirming batch",
			deleteFn:      scripted(batchResult{deleted: batchSize}, batchResult{deleted: 0}),
			wantDeleted:   batchSize,
			wantBatchSize: []int{batchSize, batchSize},
		},
		{
			name:          "error stops the sweep and keeps the count so far",
			deleteFn:      scripted(batchResult{deleted: batchSize}, batchResult{err: errDB}),
			wantDeleted:   batchSize,
			wantErr:       errDB,
			wantBatchSize: []int{batchSize, batchSize},
		},
		{
			name:          "backlog stops after maxBatchesPerSweep batches",
			deleteFn:      func(context.Context, int) (int64, error) { return batchSize, nil },
			wantDeleted:   maxBatchesPerSweep * batchSize,
			wantBatchSize: slices.Repeat([]int{batchSize}, maxBatchesPerSweep),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeStore{deleteFn: tt.deleteFn}
			got, err := sweep(context.Background(), store, batchSize)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("sweep() error = %v, want %v", err, tt.wantErr)
			}
			if got != tt.wantDeleted {
				t.Errorf("sweep() = %d, want %d", got, tt.wantDeleted)
			}
			if diff := cmp.Diff(tt.wantBatchSize, store.recordedBatchSizes()); diff != "" {
				t.Errorf("DeleteExpiredRows() batch sizes mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestSweep_BoundsEachBatchWithTimeout(t *testing.T) {
	var deadline time.Time
	var hasDeadline bool
	store := &fakeStore{deleteFn: func(ctx context.Context, _ int) (int64, error) {
		deadline, hasDeadline = ctx.Deadline()
		return 0, nil
	}}
	before := time.Now()
	if _, err := sweep(context.Background(), store, 10); err != nil {
		t.Fatalf("sweep() unexpected error: %v", err)
	}
	if !hasDeadline {
		t.Fatal("DeleteExpiredRows() context has no deadline, want one")
	}
	if limit := before.Add(batchTimeout); deadline.After(limit.Add(time.Second)) {
		t.Errorf("DeleteExpiredRows() deadline = %v, want at most %v", deadline, limit)
	}
}

func TestStart_InvalidArguments(t *testing.T) {
	tests := []struct {
		name      string
		store     storage.ExpiredRowStore
		interval  time.Duration
		batchSize int
	}{
		{name: "nil store", store: nil, interval: time.Minute, batchSize: 10},
		{name: "zero interval", store: &fakeStore{}, interval: 0, batchSize: 10},
		{name: "negative interval", store: &fakeStore{}, interval: -time.Minute, batchSize: 10},
		{name: "zero batch size", store: &fakeStore{}, interval: time.Minute, batchSize: 0},
		{name: "negative batch size", store: &fakeStore{}, interval: time.Minute, batchSize: -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, err := Start(context.Background(), tt.store, tt.interval, tt.batchSize)
			if err == nil {
				s.Close()
				t.Fatal("Start() succeeded, want error")
			}
		})
	}
}

func TestStart_SweepsImmediatelyAndEveryInterval(t *testing.T) {
	calls := make(chan struct{}, 100)
	store := &fakeStore{deleteFn: func(context.Context, int) (int64, error) {
		select {
		case calls <- struct{}{}:
		default:
		}
		return 0, nil
	}}
	s, err := Start(context.Background(), store, time.Millisecond, 10)
	if err != nil {
		t.Fatalf("Start() unexpected error: %v", err)
	}
	for i := range 3 {
		select {
		case <-calls:
		case <-time.After(waitTimeout):
			t.Fatalf("sweep %d did not run within %v", i+1, waitTimeout)
		}
	}
	if err := s.Close(); err != nil {
		t.Errorf("Close() unexpected error: %v", err)
	}
	select {
	case <-s.done:
	default:
		t.Error("Close() returned before the sweep loop exited")
	}
	if err := s.Close(); err != nil {
		t.Errorf("second Close() unexpected error: %v", err)
	}
}

func TestClose_CancelsInFlightBatch(t *testing.T) {
	started := make(chan struct{})
	var once sync.Once
	store := &fakeStore{deleteFn: func(ctx context.Context, _ int) (int64, error) {
		once.Do(func() { close(started) })
		<-ctx.Done()
		return 0, ctx.Err()
	}}
	s, err := Start(context.Background(), store, time.Hour, 10)
	if err != nil {
		t.Fatalf("Start() unexpected error: %v", err)
	}
	select {
	case <-started:
	case <-time.After(waitTimeout):
		t.Fatalf("first sweep did not start within %v", waitTimeout)
	}

	closed := make(chan struct{})
	go func() {
		s.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(waitTimeout):
		t.Fatalf("Close() did not return within %v while a batch was in flight", waitTimeout)
	}
}

func TestClose_NilSweeper(t *testing.T) {
	var s *Sweeper
	if err := s.Close(); err != nil {
		t.Errorf("(*Sweeper)(nil).Close() = %v, want nil", err)
	}
}
