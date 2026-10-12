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

package storagetest

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/xmc/internal/grpcerr"
	"github.com/google/xmc/internal/storage"
)

func TestFakePauseState_UninitializedDefault(t *testing.T) {
	ctx := context.Background()
	fake := NewFakePauseStateRepository()

	state, err := fake.Get(ctx)
	if err != nil {
		t.Fatalf("Get() unexpected error: %v", err)
	}
	if state == nil {
		t.Fatal("Get() returned nil state")
	}
	want := storage.DefaultPauseSchedulingState()
	if diff := cmp.Diff(want, state); diff != "" {
		t.Errorf("Get() uninitialized mismatch (-want +got):\n%s", diff)
	}
}

func TestFakePauseState_SetAndGet(t *testing.T) {
	ctx := context.Background()
	fake := NewFakePauseStateRepository()

	input := &storage.PauseSchedulingState{
		SchedulingPaused:   true,
		ResumptionRunID:    "run-123",
		ResumptionWorkerID: "worker-456",
	}

	before := time.Now().Add(-time.Second)
	if err := fake.Set(ctx, input); err != nil {
		t.Fatalf("Set() unexpected error: %v", err)
	}
	after := time.Now().Add(time.Second)

	got, err := fake.Get(ctx)
	if err != nil {
		t.Fatalf("Get() unexpected error: %v", err)
	}
	if got.SchedulingPaused != input.SchedulingPaused ||
		got.ResumptionRunID != input.ResumptionRunID ||
		got.ResumptionWorkerID != input.ResumptionWorkerID {
		t.Errorf("Get() mismatch: got %+v, want %+v", got, input)
	}
	if got.UpdatedAt.Before(before) || got.UpdatedAt.After(after) {
		t.Errorf("UpdatedAt = %v, want between %v and %v", got.UpdatedAt, before, after)
	}
}

func TestFakePauseState_SetUpdatesTimestamp(t *testing.T) {
	ctx := context.Background()
	fake := NewFakePauseStateRepository()

	if err := fake.Set(ctx, &storage.PauseSchedulingState{SchedulingPaused: true}); err != nil {
		t.Fatalf("Set() error: %v", err)
	}
	firstState, err := fake.Get(ctx)
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}

	time.Sleep(10 * time.Millisecond)

	// Subsequent update with older/stale state in argument must refresh UpdatedAt.
	firstState.SchedulingPaused = false
	if err := fake.Set(ctx, firstState); err != nil {
		t.Fatalf("Set() error: %v", err)
	}

	secondState, err := fake.Get(ctx)
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if !secondState.UpdatedAt.After(firstState.UpdatedAt) {
		t.Errorf("UpdatedAt after second Set = %v, want after first UpdatedAt %v", secondState.UpdatedAt, firstState.UpdatedAt)
	}
	if secondState.SchedulingPaused {
		t.Errorf("SchedulingPaused = true, want false")
	}
}

func TestFakePauseState_DefaultTimestamp(t *testing.T) {
	ctx := context.Background()
	fake := NewFakePauseStateRepository()

	before := time.Now().Add(-time.Second)
	input := &storage.PauseSchedulingState{
		SchedulingPaused: true,
	}

	if err := fake.Set(ctx, input); err != nil {
		t.Fatalf("Set() unexpected error: %v", err)
	}

	got, err := fake.Get(ctx)
	if err != nil {
		t.Fatalf("Get() unexpected error: %v", err)
	}
	after := time.Now().Add(time.Second)

	if got.UpdatedAt.Before(before) || got.UpdatedAt.After(after) {
		t.Errorf("UpdatedAt = %v, want between %v and %v", got.UpdatedAt, before, after)
	}
}

func TestFakePauseState_SetNil(t *testing.T) {
	ctx := context.Background()
	fake := NewFakePauseStateRepository()

	err := fake.Set(ctx, nil)
	if !errors.Is(err, grpcerr.ErrInvalidArgument) {
		t.Errorf("Set(nil) error = %v, want %v", err, grpcerr.ErrInvalidArgument)
	}
}

func TestFakePauseState_MutationIsolation(t *testing.T) {
	ctx := context.Background()
	fake := NewFakePauseStateRepository()

	input := &storage.PauseSchedulingState{
		SchedulingPaused: true,
		ResumptionRunID:  "initial-run",
	}
	if err := fake.Set(ctx, input); err != nil {
		t.Fatalf("Set() error: %v", err)
	}

	// Mutating input must not mutate stored state.
	input.SchedulingPaused = false
	input.ResumptionRunID = "mutated-input"

	got1, err := fake.Get(ctx)
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if !got1.SchedulingPaused || got1.ResumptionRunID != "initial-run" {
		t.Errorf("Stored state was mutated through input reference: %+v", got1)
	}

	// Mutating retrieved state must not mutate stored state.
	got1.SchedulingPaused = false
	got1.ResumptionRunID = "mutated-output"

	got2, err := fake.Get(ctx)
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if !got2.SchedulingPaused || got2.ResumptionRunID != "initial-run" {
		t.Errorf("Stored state was mutated through output reference: %+v", got2)
	}
}

func TestFakePauseState_Reset(t *testing.T) {
	ctx := context.Background()
	fake := NewFakePauseStateRepository()

	_ = fake.Set(ctx, &storage.PauseSchedulingState{SchedulingPaused: true, ResumptionRunID: "run-1"})
	fake.Reset()

	got, err := fake.Get(ctx)
	if err != nil {
		t.Fatalf("Get() unexpected error: %v", err)
	}
	if got.SchedulingPaused {
		t.Errorf("Get() after Reset() = %+v, want SchedulingPaused = false", got)
	}
}

func TestFakePauseState_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	fake := NewFakePauseStateRepository()

	if _, err := fake.Get(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("Get() with canceled context error = %v, want %v", err, context.Canceled)
	}

	state := &storage.PauseSchedulingState{SchedulingPaused: true}
	if err := fake.Set(ctx, state); !errors.Is(err, context.Canceled) {
		t.Errorf("Set() with canceled context error = %v, want %v", err, context.Canceled)
	}
}

func TestFakePauseState_Concurrency(t *testing.T) {
	ctx := context.Background()
	fake := NewFakePauseStateRepository()

	var wg sync.WaitGroup
	for g := 0; g < 10; g++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				_ = fake.Set(ctx, &storage.PauseSchedulingState{
					SchedulingPaused: i%2 == 0,
					ResumptionRunID:  fmt.Sprintf("run-%d-%d", gid, i),
				})
				_, _ = fake.Get(ctx)
				if i%5 == 0 {
					fake.Reset()
				}
			}
		}(g)
	}
	wg.Wait()
}

func TestFakePauseState_WithStateAndInjectedErrors(t *testing.T) {
	ctx := context.Background()
	seed := &storage.PauseSchedulingState{
		SchedulingPaused: true,
		ResumptionRunID:  "seeded-run",
	}
	fake := NewFakePauseStateRepositoryWithState(seed)
	seed.ResumptionRunID = "mutated"

	got, err := fake.Get(ctx)
	if err != nil {
		t.Fatalf("Get() unexpected error: %v", err)
	}
	if !got.SchedulingPaused || got.ResumptionRunID != "seeded-run" || got.UpdatedAt.IsZero() {
		t.Errorf("Get() = %+v, want seeded state with non-zero UpdatedAt", got)
	}

	wantGetErr := errors.New("get error")
	fake.GetErr = wantGetErr
	if _, err := fake.Get(ctx); !errors.Is(err, wantGetErr) {
		t.Errorf("Get() error = %v, want %v", err, wantGetErr)
	}

	wantSetErr := errors.New("set error")
	fake.SetErr = wantSetErr
	if err := fake.Set(ctx, &storage.PauseSchedulingState{}); !errors.Is(err, wantSetErr) {
		t.Errorf("Set() error = %v, want %v", err, wantSetErr)
	}
}
