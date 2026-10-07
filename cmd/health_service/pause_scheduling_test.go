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
	"encoding/json"
	"errors"
	"testing"

	"xmanager_cloud/internal/broadcast"
	"xmanager_cloud/internal/storage"
	storagetest "xmanager_cloud/internal/storage/testing"
	"xmanager_cloud/task_queue_redis/redisclient"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

type fakeLazyStorage struct {
	storage PauseSchedulingStorage
	err     error
}

func (f *fakeLazyStorage) Storage(ctx context.Context) (PauseSchedulingStorage, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.storage, nil
}

func newFakeLazyStorage(repo storage.PauseStateRepository) LazyStorageProvider {
	return &fakeLazyStorage{
		storage: &storage.MockStorage{
			PauseStateRepository: repo,
		},
	}
}

// newFakePauseScheduler builds a PauseScheduler wired to in-memory publishers for tests
// that only exercise state transitions.
func newFakePauseScheduler(pauseStateRepository storage.PauseStateRepository) PauseScheduler {
	return NewPauseScheduler(PauseSchedulerParams{
		LazyStorage:        newFakeLazyStorage(pauseStateRepository),
		BroadcastPublisher: broadcast.NewFakeClient(""),
		TaskQueuePublisher: redisclient.NewFakeClient(),
	})
}

func assertBroadcastPaused(t *testing.T, msgs []string) {
	t.Helper()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 broadcast message, got %d", len(msgs))
	}
	var event broadcast.SchedulingPaused
	if err := json.Unmarshal([]byte(msgs[0]), &event); err != nil {
		t.Fatalf("failed to unmarshal broadcast event: %v", err)
	}
	if event.Event != broadcast.EventSchedulingPaused {
		t.Errorf("expected event %q, got %q", broadcast.EventSchedulingPaused, event.Event)
	}
	if event.Timestamp.IsZero() {
		t.Errorf("expected non-zero event timestamp")
	}
}

func assertBroadcastResumed(t *testing.T, msgs []string, expectedRunID string) {
	t.Helper()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 broadcast message, got %d", len(msgs))
	}
	var event broadcast.SchedulingResumed
	if err := json.Unmarshal([]byte(msgs[0]), &event); err != nil {
		t.Fatalf("failed to unmarshal broadcast event: %v", err)
	}
	if event.Event != broadcast.EventSchedulingResumed {
		t.Errorf("expected event %q, got %q", broadcast.EventSchedulingResumed, event.Event)
	}
	if event.ResumptionRunID != expectedRunID {
		t.Errorf("expected resumption_run_id %q, got %q", expectedRunID, event.ResumptionRunID)
	}
	if event.Timestamp.IsZero() {
		t.Errorf("expected non-zero event timestamp")
	}
}

func assertResumeTrigger(t *testing.T, msgs []string, expectedRunID string, expectedBatchSize, expectedMaxBacklog int) {
	t.Helper()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 published task, got %d", len(msgs))
	}
	var payload struct {
		ResumptionRunID     string `json:"resumption_run_id"`
		BatchSize           int    `json:"batch_size"`
		MaxBacklogThreshold int    `json:"max_backlog_threshold"`
	}
	if err := json.Unmarshal([]byte(msgs[0]), &payload); err != nil {
		t.Fatalf("failed to unmarshal trigger payload: %v", err)
	}
	if payload.ResumptionRunID != expectedRunID {
		t.Errorf("expected resumption_run_id %q, got %q", expectedRunID, payload.ResumptionRunID)
	}
	if payload.BatchSize != expectedBatchSize {
		t.Errorf("expected batch_size %d, got %d", expectedBatchSize, payload.BatchSize)
	}
	if payload.MaxBacklogThreshold != expectedMaxBacklog {
		t.Errorf("expected max_backlog_threshold %d, got %d", expectedMaxBacklog, payload.MaxBacklogThreshold)
	}
}

func TestPauseScheduler_Pause(t *testing.T) {
	ctx := context.Background()

	t.Run("successfully pauses from initial unpaused state", func(t *testing.T) {
		fakeRepo := storagetest.NewFakePauseStateRepository()
		scheduler := newFakePauseScheduler(fakeRepo)

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
		fakeRepo := storagetest.NewFakePauseStateRepositoryWithState(&storage.PauseSchedulingState{
			SchedulingPaused:   false,
			ResumptionRunID:    "previous-run-id",
			ResumptionWorkerID: "previous-worker-id",
		})
		scheduler := newFakePauseScheduler(fakeRepo)

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
		failingRepo := &storagetest.FakePauseStateRepository{SetErr: repoErr}
		scheduler := newFakePauseScheduler(failingRepo)

		err := scheduler.Pause(ctx)
		if err == nil {
			t.Fatalf("expected error from Pause(), got nil")
		}
		if !errors.Is(err, repoErr) {
			t.Errorf("expected wrapped repoErr, got %v", err)
		}
	})

	t.Run("returns error when lazy storage initialization fails", func(t *testing.T) {
		storageErr := errors.New("spanner client init failed")
		scheduler := NewPauseScheduler(PauseSchedulerParams{
			LazyStorage:        &fakeLazyStorage{err: storageErr},
			BroadcastPublisher: broadcast.NewFakeClient(""),
		})

		err := scheduler.Pause(ctx)
		if !errors.Is(err, storageErr) {
			t.Fatalf("expected wrapped storageErr, got %v", err)
		}
	})

	t.Run("fails when lazy storage is not configured", func(t *testing.T) {
		scheduler := NewPauseScheduler(PauseSchedulerParams{
			BroadcastPublisher: broadcast.NewFakeClient(""),
		})

		err := scheduler.Pause(ctx)
		if !errors.Is(err, errStorageNotConfigured) {
			t.Fatalf("expected errStorageNotConfigured, got %v", err)
		}
	})

	t.Run("successfully broadcasts pause event when publisher provided", func(t *testing.T) {
		fakeRepo := storagetest.NewFakePauseStateRepository()
		fakePublisher := broadcast.NewFakeClient("")
		scheduler := NewPauseScheduler(PauseSchedulerParams{
			LazyStorage:        newFakeLazyStorage(fakeRepo),
			BroadcastPublisher: fakePublisher,
		})

		if err := scheduler.Pause(ctx); err != nil {
			t.Fatalf("Pause() unexpected error: %v", err)
		}

		assertBroadcastPaused(t, fakePublisher.Messages())
	})

	t.Run("returns error when broadcast publisher fails", func(t *testing.T) {
		fakeRepo := storagetest.NewFakePauseStateRepository()
		fakePublisher := broadcast.NewFakeClient("")
		fakePublisher.SetPublishError(errors.New("broadcast stream failure"))
		scheduler := NewPauseScheduler(PauseSchedulerParams{
			LazyStorage:        newFakeLazyStorage(fakeRepo),
			BroadcastPublisher: fakePublisher,
		})

		err := scheduler.Pause(ctx)
		if err == nil {
			t.Fatalf("expected error from Pause() when broadcast fails, got nil")
		}
	})

	t.Run("fails when broadcast publisher is not configured", func(t *testing.T) {
		fakeRepo := storagetest.NewFakePauseStateRepository()
		scheduler := NewPauseScheduler(PauseSchedulerParams{
			LazyStorage: newFakeLazyStorage(fakeRepo),
		})

		err := scheduler.Pause(ctx)
		if !errors.Is(err, errBroadcastPublisherNotConfigured) {
			t.Fatalf("expected errBroadcastPublisherNotConfigured, got %v", err)
		}

		state, err := fakeRepo.Get(ctx)
		if err != nil {
			t.Fatalf("Get() unexpected error: %v", err)
		}
		if state != nil && state.SchedulingPaused {
			t.Errorf("expected scheduling state to remain unchanged, got SchedulingPaused=true")
		}
	})
}

func TestPauseScheduler_Resume(t *testing.T) {
	ctx := context.Background()

	t.Run("successfully resumes and generates valid UUID run id", func(t *testing.T) {
		fakeRepo := storagetest.NewFakePauseStateRepositoryWithState(&storage.PauseSchedulingState{
			SchedulingPaused:   true,
			ResumptionRunID:    "",
			ResumptionWorkerID: "old-worker",
		})
		scheduler := newFakePauseScheduler(fakeRepo)

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

	t.Run("consecutive resumes when already unpaused are idempotent and preserve worker lease", func(t *testing.T) {
		fakeRepo := storagetest.NewFakePauseStateRepositoryWithState(&storage.PauseSchedulingState{
			SchedulingPaused:   false,
			ResumptionRunID:    "active-run-id",
			ResumptionWorkerID: "active-worker-id",
		})
		scheduler := newFakePauseScheduler(fakeRepo)

		runID, err := scheduler.Resume(ctx)
		if err != nil {
			t.Fatalf("Resume() unexpected error: %v", err)
		}
		if runID != "active-run-id" {
			t.Errorf("expected existing ResumptionRunID %q, got %q", "active-run-id", runID)
		}

		state, err := fakeRepo.Get(ctx)
		if err != nil {
			t.Fatalf("Get() unexpected error: %v", err)
		}
		if state.ResumptionWorkerID != "active-worker-id" {
			t.Errorf("expected ResumptionWorkerID to be preserved as %q, got %q", "active-worker-id", state.ResumptionWorkerID)
		}
		if state.ResumptionRunID != "active-run-id" {
			t.Errorf("expected ResumptionRunID to be preserved as %q, got %q", "active-run-id", state.ResumptionRunID)
		}
	})

	t.Run("successive pause and resume cycles generate unique run ids", func(t *testing.T) {
		fakeRepo := storagetest.NewFakePauseStateRepository()
		scheduler := newFakePauseScheduler(fakeRepo)

		if err := scheduler.Pause(ctx); err != nil {
			t.Fatalf("first Pause() failed: %v", err)
		}
		runID1, err := scheduler.Resume(ctx)
		if err != nil {
			t.Fatalf("first Resume() failed: %v", err)
		}

		if err := scheduler.Pause(ctx); err != nil {
			t.Fatalf("second Pause() failed: %v", err)
		}
		runID2, err := scheduler.Resume(ctx)
		if err != nil {
			t.Fatalf("second Resume() failed: %v", err)
		}

		if runID1 == runID2 {
			t.Errorf("expected distinct resumption run IDs across cycles, got %q for both", runID1)
		}
	})

	t.Run("returns error when repo Get fails", func(t *testing.T) {
		repoErr := errors.New("spanner read failed")
		failingRepo := &storagetest.FakePauseStateRepository{GetErr: repoErr}
		scheduler := newFakePauseScheduler(failingRepo)

		_, err := scheduler.Resume(ctx)
		if err == nil {
			t.Fatalf("expected error from Resume(), got nil")
		}
		if !errors.Is(err, repoErr) {
			t.Errorf("expected wrapped repoErr, got %v", err)
		}
	})

	t.Run("returns error when repo Set fails", func(t *testing.T) {
		repoErr := errors.New("spanner commit failed")
		failingRepo := storagetest.NewFakePauseStateRepositoryWithState(&storage.PauseSchedulingState{SchedulingPaused: true})
		failingRepo.SetErr = repoErr
		scheduler := newFakePauseScheduler(failingRepo)

		_, err := scheduler.Resume(ctx)
		if err == nil {
			t.Fatalf("expected error from Resume(), got nil")
		}
		if !errors.Is(err, repoErr) {
			t.Errorf("expected wrapped repoErr, got %v", err)
		}
	})

	t.Run("returns error when lazy storage initialization fails", func(t *testing.T) {
		storageErr := errors.New("spanner client init failed")
		scheduler := NewPauseScheduler(PauseSchedulerParams{
			LazyStorage:        &fakeLazyStorage{err: storageErr},
			BroadcastPublisher: broadcast.NewFakeClient(""),
			TaskQueuePublisher: redisclient.NewFakeClient(),
		})

		_, err := scheduler.Resume(ctx)
		if !errors.Is(err, storageErr) {
			t.Fatalf("expected wrapped storageErr, got %v", err)
		}
	})

	t.Run("fails when lazy storage is not configured", func(t *testing.T) {
		scheduler := NewPauseScheduler(PauseSchedulerParams{
			BroadcastPublisher: broadcast.NewFakeClient(""),
			TaskQueuePublisher: redisclient.NewFakeClient(),
		})

		_, err := scheduler.Resume(ctx)
		if !errors.Is(err, errStorageNotConfigured) {
			t.Fatalf("expected errStorageNotConfigured, got %v", err)
		}
	})

	t.Run("successfully broadcasts resume event and publishes task queue trigger", func(t *testing.T) {
		fakeRepo := storagetest.NewFakePauseStateRepositoryWithState(&storage.PauseSchedulingState{
			SchedulingPaused: true,
		})
		fakePublisher := broadcast.NewFakeClient("")
		fakeRedis := redisclient.NewFakeClient()
		scheduler := NewPauseScheduler(PauseSchedulerParams{
			LazyStorage:         newFakeLazyStorage(fakeRepo),
			BroadcastPublisher:  fakePublisher,
			TaskQueuePublisher:  fakeRedis,
			BatchSize:           42,
			MaxBacklogThreshold: 150,
		})

		runID, err := scheduler.Resume(ctx)
		if err != nil {
			t.Fatalf("Resume() unexpected error: %v", err)
		}

		assertBroadcastResumed(t, fakePublisher.Messages(), runID)
		assertResumeTrigger(t, fakeRedis.Messages, runID, 42, 150)
	})

	t.Run("consecutive resumes when already unpaused do not broadcast or publish task trigger", func(t *testing.T) {
		fakeRepo := storagetest.NewFakePauseStateRepositoryWithState(&storage.PauseSchedulingState{
			SchedulingPaused:   false,
			ResumptionRunID:    "active-run-id",
			ResumptionWorkerID: "active-worker-id",
		})
		fakePublisher := broadcast.NewFakeClient("")
		fakeRedis := redisclient.NewFakeClient()
		scheduler := NewPauseScheduler(PauseSchedulerParams{
			LazyStorage:        newFakeLazyStorage(fakeRepo),
			BroadcastPublisher: fakePublisher,
			TaskQueuePublisher: fakeRedis,
		})

		runID, err := scheduler.Resume(ctx)
		if err != nil {
			t.Fatalf("Resume() unexpected error: %v", err)
		}
		if runID != "active-run-id" {
			t.Errorf("expected run ID %q, got %q", "active-run-id", runID)
		}
		if len(fakePublisher.Messages()) > 0 {
			t.Errorf("expected no broadcast messages on idempotent resume, got %d", len(fakePublisher.Messages()))
		}
		if len(fakeRedis.Messages) > 0 {
			t.Errorf("expected no task queue messages on idempotent resume, got %d", len(fakeRedis.Messages))
		}
	})

	t.Run("returns wrapped error and skips task queue trigger when broadcast publisher fails", func(t *testing.T) {
		fakeRepo := storagetest.NewFakePauseStateRepositoryWithState(&storage.PauseSchedulingState{
			SchedulingPaused: true,
		})
		fakePublisher := broadcast.NewFakeClient("")
		fakePublisher.SetPublishError(errors.New("broadcast stream failure"))
		fakeRedis := redisclient.NewFakeClient()
		scheduler := NewPauseScheduler(PauseSchedulerParams{
			LazyStorage:        newFakeLazyStorage(fakeRepo),
			BroadcastPublisher: fakePublisher,
			TaskQueuePublisher: fakeRedis,
		})

		_, err := scheduler.Resume(ctx)
		if err == nil {
			t.Fatalf("expected error from Resume() when broadcast fails, got nil")
		}
		if len(fakeRedis.Messages) > 0 {
			t.Errorf("expected no task queue messages when broadcast fails, got %d", len(fakeRedis.Messages))
		}
	})

	t.Run("fails when broadcast publisher is not configured", func(t *testing.T) {
		fakeRepo := storagetest.NewFakePauseStateRepositoryWithState(&storage.PauseSchedulingState{SchedulingPaused: true})
		scheduler := NewPauseScheduler(PauseSchedulerParams{
			LazyStorage:        newFakeLazyStorage(fakeRepo),
			TaskQueuePublisher: redisclient.NewFakeClient(),
		})

		_, err := scheduler.Resume(ctx)
		if !errors.Is(err, errBroadcastPublisherNotConfigured) {
			t.Fatalf("expected errBroadcastPublisherNotConfigured, got %v", err)
		}
		assertStillPaused(ctx, t, fakeRepo)
	})

	t.Run("fails when task queue publisher is not configured", func(t *testing.T) {
		fakeRepo := storagetest.NewFakePauseStateRepositoryWithState(&storage.PauseSchedulingState{SchedulingPaused: true})
		fakePublisher := broadcast.NewFakeClient("")
		scheduler := NewPauseScheduler(PauseSchedulerParams{
			LazyStorage:        newFakeLazyStorage(fakeRepo),
			BroadcastPublisher: fakePublisher,
		})

		_, err := scheduler.Resume(ctx)
		if !errors.Is(err, errTaskQueuePublisherNotConfigured) {
			t.Fatalf("expected errTaskQueuePublisherNotConfigured, got %v", err)
		}
		if len(fakePublisher.Messages()) > 0 {
			t.Errorf("expected no broadcast messages, got %d", len(fakePublisher.Messages()))
		}
		assertStillPaused(ctx, t, fakeRepo)
	})
}

func assertStillPaused(ctx context.Context, t *testing.T, repo storage.PauseStateRepository) {
	t.Helper()
	state, err := repo.Get(ctx)
	if err != nil {
		t.Fatalf("Get() unexpected error: %v", err)
	}
	if !state.SchedulingPaused {
		t.Errorf("expected scheduling to remain paused, got SchedulingPaused=false")
	}
	if state.ResumptionRunID != "" {
		t.Errorf("expected ResumptionRunID to remain empty, got %q", state.ResumptionRunID)
	}
}

func TestPauseScheduler_IsPaused(t *testing.T) {
	ctx := context.Background()

	t.Run("defaults to false on uninitialized repo", func(t *testing.T) {
		fakeRepo := storagetest.NewFakePauseStateRepository()
		scheduler := newFakePauseScheduler(fakeRepo)

		paused, err := scheduler.IsPaused(ctx)
		if err != nil {
			t.Fatalf("IsPaused() unexpected error: %v", err)
		}
		if paused {
			t.Errorf("expected IsPaused() to be false initially, got true")
		}
	})

	t.Run("reflects pause and resume transitions", func(t *testing.T) {
		fakeRepo := storagetest.NewFakePauseStateRepository()
		scheduler := newFakePauseScheduler(fakeRepo)

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
		failingRepo := &storagetest.FakePauseStateRepository{GetErr: repoErr}
		scheduler := newFakePauseScheduler(failingRepo)

		_, err := scheduler.IsPaused(ctx)
		if err == nil {
			t.Fatalf("expected error from IsPaused(), got nil")
		}
		if !errors.Is(err, repoErr) {
			t.Errorf("expected wrapped repoErr, got %v", err)
		}
	})

	t.Run("returns error when lazy storage initialization fails", func(t *testing.T) {
		storageErr := errors.New("spanner client init failed")
		scheduler := NewPauseScheduler(PauseSchedulerParams{
			LazyStorage: &fakeLazyStorage{err: storageErr},
		})

		_, err := scheduler.IsPaused(ctx)
		if !errors.Is(err, storageErr) {
			t.Fatalf("expected wrapped storageErr, got %v", err)
		}
	})

	t.Run("fails when lazy storage is not configured", func(t *testing.T) {
		scheduler := NewPauseScheduler(PauseSchedulerParams{})

		_, err := scheduler.IsPaused(ctx)
		if !errors.Is(err, errStorageNotConfigured) {
			t.Fatalf("expected errStorageNotConfigured, got %v", err)
		}
	})
}

func TestPauseSchedulerClose(t *testing.T) {
	t.Run("closes publisher if closer", func(t *testing.T) {
		pub := broadcast.NewPublisher(nil, "stream")
		scheduler := NewPauseScheduler(PauseSchedulerParams{
			BroadcastPublisher: pub,
		})
		if err := scheduler.Close(); err != nil {
			t.Errorf("Close() error = %v, want nil", err)
		}
	})

	t.Run("noop if publisher is not closer or nil", func(t *testing.T) {
		scheduler := NewPauseScheduler(PauseSchedulerParams{})
		if err := scheduler.Close(); err != nil {
			t.Errorf("Close() error = %v, want nil", err)
		}
	})
}

func TestPauseScheduler_Metrics(t *testing.T) {
	ctx := context.Background()

	t.Run("increments pause success and error counters", func(t *testing.T) {
		schedulingPauseEventsTotal.Reset()
		fakeRepo := storagetest.NewFakePauseStateRepository()
		scheduler := newFakePauseScheduler(fakeRepo)

		if err := scheduler.Pause(ctx); err != nil {
			t.Fatalf("Pause() unexpected error: %v", err)
		}
		if got := testutil.ToFloat64(schedulingPauseEventsTotal.WithLabelValues("pause", "success")); got != 1 {
			t.Errorf("xmc_scheduling_pause_events_total{action=pause,status=success} = %v, want 1", got)
		}
		if got := testutil.ToFloat64(schedulingPauseEventsTotal.WithLabelValues("pause", "error")); got != 0 {
			t.Errorf("xmc_scheduling_pause_events_total{action=pause,status=error} = %v, want 0", got)
		}

		failingScheduler := newFakePauseScheduler(&storagetest.FakePauseStateRepository{SetErr: errors.New("db error")})
		if err := failingScheduler.Pause(ctx); err == nil {
			t.Fatalf("expected error from Pause(), got nil")
		}
		if got := testutil.ToFloat64(schedulingPauseEventsTotal.WithLabelValues("pause", "error")); got != 1 {
			t.Errorf("xmc_scheduling_pause_events_total{action=pause,status=error} = %v, want 1", got)
		}
	})

	t.Run("increments resume success and error counters", func(t *testing.T) {
		schedulingPauseEventsTotal.Reset()
		fakeRepo := storagetest.NewFakePauseStateRepositoryWithState(&storage.PauseSchedulingState{SchedulingPaused: true})
		scheduler := newFakePauseScheduler(fakeRepo)

		if _, err := scheduler.Resume(ctx); err != nil {
			t.Fatalf("Resume() unexpected error: %v", err)
		}
		if got := testutil.ToFloat64(schedulingPauseEventsTotal.WithLabelValues("resume", "success")); got != 1 {
			t.Errorf("xmc_scheduling_pause_events_total{action=resume,status=success} = %v, want 1", got)
		}
		if got := testutil.ToFloat64(schedulingPauseEventsTotal.WithLabelValues("resume", "error")); got != 0 {
			t.Errorf("xmc_scheduling_pause_events_total{action=resume,status=error} = %v, want 0", got)
		}

		failingScheduler := newFakePauseScheduler(&storagetest.FakePauseStateRepository{GetErr: errors.New("db error")})
		if _, err := failingScheduler.Resume(ctx); err == nil {
			t.Fatalf("expected error from Resume(), got nil")
		}
		if got := testutil.ToFloat64(schedulingPauseEventsTotal.WithLabelValues("resume", "error")); got != 1 {
			t.Errorf("xmc_scheduling_pause_events_total{action=resume,status=error} = %v, want 1", got)
		}
	})
}
