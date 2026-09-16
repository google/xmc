// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"context"
	"errors"
	"testing"

	"xmanager_cloud/internal/storage/spanner/pausestate"

	"github.com/google/uuid"
)

type failingPauseStateRepo struct {
	err error
}

func (r *failingPauseStateRepo) Get(ctx context.Context) (*pausestate.PauseSchedulingState, error) {
	return nil, r.err
}

func (r *failingPauseStateRepo) Set(ctx context.Context, state *pausestate.PauseSchedulingState) error {
	return r.err
}

func TestPauseScheduler_Pause(t *testing.T) {
	ctx := context.Background()

	t.Run("successfully pauses from initial unpaused state", func(t *testing.T) {
		fakeRepo := pausestate.NewFake()
		scheduler := NewPauseScheduler(fakeRepo)

		if err := scheduler.Pause(ctx); err != nil {
			t.Fatalf("Pause() unexpected error: %v", err)
		}

		state, err := fakeRepo.Get(ctx)
		if err != nil {
			t.Fatalf("Get() unexpected error: %v", err)
		}

		if !state.SchedulingPaused {
			t.Errorf("expected SchedulingPaused to be true, got false")
		}
		if state.ResumptionRunID != "" {
			t.Errorf("expected ResumptionRunID to be empty, got %q", state.ResumptionRunID)
		}
		if state.ResumptionWorkerID != "" {
			t.Errorf("expected ResumptionWorkerID to be empty, got %q", state.ResumptionWorkerID)
		}
	})

	t.Run("successfully pauses when previous resumption metadata exists", func(t *testing.T) {
		fakeRepo := pausestate.NewFake()
		_ = fakeRepo.Set(ctx, &pausestate.PauseSchedulingState{
			SchedulingPaused:   false,
			ResumptionRunID:    "previous-run-id",
			ResumptionWorkerID: "previous-worker-id",
		})
		scheduler := NewPauseScheduler(fakeRepo)

		if err := scheduler.Pause(ctx); err != nil {
			t.Fatalf("Pause() unexpected error: %v", err)
		}

		state, err := fakeRepo.Get(ctx)
		if err != nil {
			t.Fatalf("Get() unexpected error: %v", err)
		}

		if !state.SchedulingPaused {
			t.Errorf("expected SchedulingPaused to be true, got false")
		}
		if state.ResumptionRunID != "" {
			t.Errorf("expected ResumptionRunID to be cleared, got %q", state.ResumptionRunID)
		}
		if state.ResumptionWorkerID != "" {
			t.Errorf("expected ResumptionWorkerID to be cleared, got %q", state.ResumptionWorkerID)
		}
	})

	t.Run("returns error when repo fails", func(t *testing.T) {
		repoErr := errors.New("spanner connection failure")
		failingRepo := &failingPauseStateRepo{err: repoErr}
		scheduler := NewPauseScheduler(failingRepo)

		err := scheduler.Pause(ctx)
		if err == nil {
			t.Fatalf("expected error from Pause(), got nil")
		}
		if !errors.Is(err, repoErr) {
			t.Errorf("expected wrapped repoErr, got %v", err)
		}
	})
}

func TestPauseScheduler_Resume(t *testing.T) {
	ctx := context.Background()

	t.Run("successfully resumes and generates valid UUID run id", func(t *testing.T) {
		fakeRepo := pausestate.NewFake()
		_ = fakeRepo.Set(ctx, &pausestate.PauseSchedulingState{
			SchedulingPaused:   true,
			ResumptionRunID:    "",
			ResumptionWorkerID: "old-worker",
		})
		scheduler := NewPauseScheduler(fakeRepo)

		runID, err := scheduler.Resume(ctx)
		if err != nil {
			t.Fatalf("Resume() unexpected error: %v", err)
		}

		if runID == "" {
			t.Fatal("expected non-empty resumption run ID")
		}
		if _, err := uuid.Parse(runID); err != nil {
			t.Errorf("expected valid UUID for resumption run ID, got %q (err: %v)", runID, err)
		}

		state, err := fakeRepo.Get(ctx)
		if err != nil {
			t.Fatalf("Get() unexpected error: %v", err)
		}

		if state.SchedulingPaused {
			t.Errorf("expected SchedulingPaused to be false, got true")
		}
		if state.ResumptionRunID != runID {
			t.Errorf("expected ResumptionRunID %q, got %q", runID, state.ResumptionRunID)
		}
		if state.ResumptionWorkerID != "" {
			t.Errorf("expected ResumptionWorkerID to be empty, got %q", state.ResumptionWorkerID)
		}
	})

	t.Run("successive resumes generate unique run ids", func(t *testing.T) {
		fakeRepo := pausestate.NewFake()
		scheduler := NewPauseScheduler(fakeRepo)

		runID1, err := scheduler.Resume(ctx)
		if err != nil {
			t.Fatalf("first Resume() failed: %v", err)
		}
		runID2, err := scheduler.Resume(ctx)
		if err != nil {
			t.Fatalf("second Resume() failed: %v", err)
		}

		if runID1 == runID2 {
			t.Errorf("expected distinct resumption run IDs, got %q for both", runID1)
		}
	})

	t.Run("returns error when repo fails", func(t *testing.T) {
		repoErr := errors.New("spanner commit failed")
		failingRepo := &failingPauseStateRepo{err: repoErr}
		scheduler := NewPauseScheduler(failingRepo)

		_, err := scheduler.Resume(ctx)
		if err == nil {
			t.Fatalf("expected error from Resume(), got nil")
		}
		if !errors.Is(err, repoErr) {
			t.Errorf("expected wrapped repoErr, got %v", err)
		}
	})
}

func TestPauseScheduler_IsPaused(t *testing.T) {
	ctx := context.Background()

	t.Run("defaults to false on uninitialized repo", func(t *testing.T) {
		fakeRepo := pausestate.NewFake()
		scheduler := NewPauseScheduler(fakeRepo)

		paused, err := scheduler.IsPaused(ctx)
		if err != nil {
			t.Fatalf("IsPaused() unexpected error: %v", err)
		}
		if paused {
			t.Errorf("expected IsPaused() to be false initially, got true")
		}
	})

	t.Run("reflects pause and resume transitions", func(t *testing.T) {
		fakeRepo := pausestate.NewFake()
		scheduler := NewPauseScheduler(fakeRepo)

		if err := scheduler.Pause(ctx); err != nil {
			t.Fatalf("Pause() failed: %v", err)
		}
		paused, err := scheduler.IsPaused(ctx)
		if err != nil {
			t.Fatalf("IsPaused() failed: %v", err)
		}
		if !paused {
			t.Errorf("expected IsPaused() to be true after Pause(), got false")
		}

		if _, err := scheduler.Resume(ctx); err != nil {
			t.Fatalf("Resume() failed: %v", err)
		}
		paused, err = scheduler.IsPaused(ctx)
		if err != nil {
			t.Fatalf("IsPaused() failed: %v", err)
		}
		if paused {
			t.Errorf("expected IsPaused() to be false after Resume(), got true")
		}
	})

	t.Run("returns error when repo fails", func(t *testing.T) {
		repoErr := errors.New("spanner read failed")
		failingRepo := &failingPauseStateRepo{err: repoErr}
		scheduler := NewPauseScheduler(failingRepo)

		_, err := scheduler.IsPaused(ctx)
		if err == nil {
			t.Fatalf("expected error from IsPaused(), got nil")
		}
		if !errors.Is(err, repoErr) {
			t.Errorf("expected wrapped repoErr, got %v", err)
		}
	})
}
