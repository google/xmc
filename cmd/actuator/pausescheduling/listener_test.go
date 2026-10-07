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

package pausescheduling

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"xmanager_cloud/internal/broadcast"
	"xmanager_cloud/internal/storage"
	storagetest "xmanager_cloud/internal/storage/testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

var _ StreamSubscriber = (*mockStreamSubscriber)(nil)

type mockStreamSubscriber struct {
	mu            sync.Mutex
	latestOffset  string
	offsetErr     error
	subscribeErr  error
	subscribeDone chan struct{}
	handler       func(msg string)
	subscribed    chan struct{}
}

func newMockSubscriber() *mockStreamSubscriber {
	return &mockStreamSubscriber{
		latestOffset:  "0",
		subscribeDone: make(chan struct{}),
		subscribed:    make(chan struct{}, 10),
	}
}

func (m *mockStreamSubscriber) LatestOffset(ctx context.Context) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.offsetErr != nil {
		return "", m.offsetErr
	}
	return m.latestOffset, nil
}

func (m *mockStreamSubscriber) Subscribe(ctx context.Context, startID string, handler func(msg string)) error {
	m.mu.Lock()
	m.handler = handler
	err := m.subscribeErr
	m.mu.Unlock()

	select {
	case m.subscribed <- struct{}{}:
	default:
	}

	if err != nil {
		return err
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-m.subscribeDone:
		return nil
	}
}

func (m *mockStreamSubscriber) emit(msg string) {
	m.mu.Lock()
	h := m.handler
	m.mu.Unlock()
	if h != nil {
		h(msg)
	}
}

func newTestListener(t *testing.T, sub StreamSubscriber, repo storage.PauseStateRepository) (*Listener, Cache) {
	t.Helper()
	c := NewCache()
	l, err := New(Params{
		Cache:                c,
		Subscriber:           sub,
		PauseStateRepository: repo,
		ReconnectBackoff:     15 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New() unexpected error: %v", err)
	}
	return l, c
}

func TestNewListener_Validation(t *testing.T) {
	cache := NewCache()
	sub := newMockSubscriber()
	repo := storagetest.NewFakePauseStateRepository()

	tests := []struct {
		name    string
		params  Params
		wantErr string
	}{
		{name: "missing_cache", params: Params{Subscriber: sub, PauseStateRepository: repo}, wantErr: "cache cannot be nil"},
		{name: "missing_subscriber", params: Params{Cache: cache, PauseStateRepository: repo}, wantErr: "subscriber cannot be nil"},
		{name: "missing_repo", params: Params{Cache: cache, Subscriber: sub}, wantErr: "pause state repository cannot be nil"},
		{name: "all_valid", params: Params{Cache: cache, Subscriber: sub, PauseStateRepository: repo}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			listener, err := New(tt.params)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("New() error = %v, want substring %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("New() unexpected error: %v", err)
			}
			if listener == nil {
				t.Fatalf("New() returned nil listener")
			}
		})
	}
}

func TestListener_Init_ColdStart(t *testing.T) {
	ctx := context.Background()

	t.Run("cold_start_seeds_cache_and_offset", func(t *testing.T) {
		sub := newMockSubscriber()
		sub.latestOffset = "100-0"
		repo := storagetest.NewFakePauseStateRepositoryWithState(&storage.PauseSchedulingState{SchedulingPaused: true})

		listener, cache := newTestListener(t, sub, repo)
		if err := listener.Init(ctx); err != nil {
			t.Fatalf("Init() unexpected error: %v", err)
		}

		if !cache.State().IsPaused {
			t.Errorf("IsPaused = false, want true after cold-start init")
		}
		if got := testutil.ToFloat64(schedulingPausedGauge); got != 1 {
			t.Errorf("xmc_actuator_scheduling_paused = %v, want 1", got)
		}
		if listener.getStartID() != "100-0" {
			t.Errorf("getStartID() = %q, want %q", listener.getStartID(), "100-0")
		}
	})

	t.Run("cold_start_uninitialized_table_defaults_to_active", func(t *testing.T) {
		sub := newMockSubscriber()
		repo := storagetest.NewFakePauseStateRepository()
		listener, cache := newTestListener(t, sub, repo)

		if err := listener.Init(ctx); err != nil {
			t.Fatalf("Init() unexpected error: %v", err)
		}
		if cache.State().IsPaused {
			t.Errorf("IsPaused = true, want false for uninitialized table")
		}
		if got := testutil.ToFloat64(schedulingPausedGauge); got != 0 {
			t.Errorf("xmc_actuator_scheduling_paused = %v, want 0", got)
		}
		if listener.getStartID() != "0" {
			t.Errorf("getStartID() = %q, want %q", listener.getStartID(), "0")
		}
	})

	t.Run("cold_start_empty_stream_offset_defaults_to_zero_id", func(t *testing.T) {
		sub := newMockSubscriber()
		sub.latestOffset = ""
		repo := storagetest.NewFakePauseStateRepository()
		listener, _ := newTestListener(t, sub, repo)

		if err := listener.Init(ctx); err != nil {
			t.Fatalf("Init() unexpected error: %v", err)
		}
		if listener.getStartID() != "0" {
			t.Errorf("getStartID() = %q, want %q for empty stream offset", listener.getStartID(), "0")
		}
	})

	t.Run("stream_offset_error_fails_init", func(t *testing.T) {
		sub := newMockSubscriber()
		sub.offsetErr = errors.New("redis connection refused")
		repo := storagetest.NewFakePauseStateRepository()
		listener, _ := newTestListener(t, sub, repo)

		err := listener.Init(ctx)
		if err == nil || !strings.Contains(err.Error(), "failed to query stream offset") {
			t.Errorf("Init() error = %v, want stream offset failure", err)
		}
	})

	t.Run("repo_error_fails_init", func(t *testing.T) {
		canceledCtx, cancel := context.WithCancel(ctx)
		cancel()
		listener, _ := newTestListener(t, newMockSubscriber(), storagetest.NewFakePauseStateRepository())

		err := listener.Init(canceledCtx)
		if err == nil || !strings.Contains(err.Error(), "failed cold-start read") {
			t.Errorf("Init() error = %v, want cold-start read failure", err)
		}
	})
}

func TestListener_BroadcastUpdate(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sub := newMockSubscriber()
	repo := storagetest.NewFakePauseStateRepository()
	listener, cache := newTestListener(t, sub, repo)

	if err := listener.Init(ctx); err != nil {
		t.Fatalf("Init() unexpected error: %v", err)
	}

	go listener.StartListening(ctx)

	// Wait until subscription active
	select {
	case <-sub.subscribed:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for subscription to start")
	}

	// Update state in repository
	t1 := time.Date(2026, 3, 1, 14, 0, 0, 0, time.UTC)
	_ = repo.Set(ctx, &storage.PauseSchedulingState{
		SchedulingPaused: true,
		UpdatedAt:        t1,
	})

	// Emit broadcast event
	sub.emit(broadcast.EventSchedulingPaused)

	// Verify cache and gauge were updated
	time.Sleep(50 * time.Millisecond)
	if !cache.State().IsPaused {
		t.Errorf("IsPaused = false, want true after broadcast update")
	}
	if got := testutil.ToFloat64(schedulingPausedGauge); got != 1 {
		t.Errorf("xmc_actuator_scheduling_paused = %v, want 1", got)
	}
}

func TestListener_ReconnectAndResync(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sub := newMockSubscriber()
	sub.mu.Lock()
	sub.subscribeErr = errors.New("temporary redis disconnection")
	sub.mu.Unlock()
	repo := storagetest.NewFakePauseStateRepository()

	listener, cache := newTestListener(t, sub, repo)
	if err := listener.Init(ctx); err != nil {
		t.Fatalf("Init() unexpected error: %v", err)
	}

	// Initially active
	if cache.State().IsPaused {
		t.Errorf("IsPaused initially = true, want false")
	}

	// State changed while disconnected
	_ = repo.Set(ctx, &storage.PauseSchedulingState{
		SchedulingPaused: true,
	})

	go listener.StartListening(ctx)
	// Wait for first failure
	select {
	case <-sub.subscribed:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first subscribe attempt")
	}

	// Clear the error and update latest offset so reconnect succeeds
	sub.mu.Lock()
	sub.subscribeErr = nil
	sub.latestOffset = "200-0"
	sub.mu.Unlock()

	// Wait for reconnect subscription attempt
	select {
	case <-sub.subscribed:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for reconnect subscribe attempt")
	}

	// Verify cache was resynced from repo on reconnect
	time.Sleep(50 * time.Millisecond)
	if !cache.State().IsPaused {
		t.Errorf("IsPaused = false, want true after reconnect resync")
	}
	if listener.getStartID() != "200-0" {
		t.Errorf("getStartID() = %q, want %q after reconnect", listener.getStartID(), "200-0")
	}

	t.Run("retries_resync_until_database_and_offset_succeed_before_resubscribing", func(t *testing.T) {
		ctx2, cancel2 := context.WithCancel(context.Background())
		defer cancel2()

		sub2 := newMockSubscriber()
		sub2.mu.Lock()
		sub2.subscribeErr = errors.New("disconnect")
		sub2.mu.Unlock()
		repo2 := storagetest.NewFakePauseStateRepository()

		listener2, cache2 := newTestListener(t, sub2, repo2)
		if err := listener2.Init(ctx2); err != nil {
			t.Fatalf("Init() unexpected error: %v", err)
		}

		// Set offset error so resyncOnReconnect will fail after first Subscribe returns error
		sub2.mu.Lock()
		sub2.offsetErr = errors.New("offset temporarily unavailable")
		sub2.mu.Unlock()

		go listener2.StartListening(ctx2)

		// Consume the first subscribe attempt (which returns subscribeErr)
		select {
		case <-sub2.subscribed:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for first subscribe attempt")
		}

		// Clear subscribeErr while keeping offsetErr; verify Subscribe is NOT called during backoff/resync failures
		sub2.mu.Lock()
		sub2.subscribeErr = nil
		sub2.mu.Unlock()

		select {
		case <-sub2.subscribed:
			t.Fatal("Subscribe() was called while resyncOnReconnect was failing")
		case <-time.After(45 * time.Millisecond):
			// Expected: no Subscribe() call while resyncOnReconnect fails
		}

		// Now allow resyncOnReconnect to succeed and update database state
		_ = repo2.Set(ctx2, &storage.PauseSchedulingState{
			SchedulingPaused: true,
		})
		sub2.mu.Lock()
		sub2.offsetErr = nil
		sub2.latestOffset = "300-0"
		sub2.mu.Unlock()

		// Now Subscribe() should resume after resync succeeds
		select {
		case <-sub2.subscribed:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for subscribe to resume after resync succeeded")
		}

		if !cache2.State().IsPaused {
			t.Errorf("IsPaused = false, want true after resync succeeded")
		}
		if listener2.getStartID() != "300-0" {
			t.Errorf("getStartID() = %q, want %q", listener2.getStartID(), "300-0")
		}
	})
}

func TestListener_StartListening_GracefulShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	sub := newMockSubscriber()
	listener, _ := newTestListener(t, sub, storagetest.NewFakePauseStateRepository())

	_ = listener.Init(ctx)

	done := make(chan struct{})
	go func() {
		listener.StartListening(ctx)
		close(done)
	}()

	select {
	case <-sub.subscribed:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for subscription")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("StartListening() did not return after context cancellation")
	}
}

type mockCloserSubscriber struct {
	mockStreamSubscriber
	closeErr error
	closed   bool
}

func (m *mockCloserSubscriber) Close() error {
	m.closed = true
	return m.closeErr
}

func TestListener_Close(t *testing.T) {
	t.Run("non_closer_subscriber", func(t *testing.T) {
		sub := newMockSubscriber()
		listener, _ := newTestListener(t, sub, storagetest.NewFakePauseStateRepository())
		if err := listener.Close(); err != nil {
			t.Errorf("Close() error = %v, want nil", err)
		}
	})

	t.Run("closer_subscriber_success", func(t *testing.T) {
		sub := &mockCloserSubscriber{}
		listener, _ := newTestListener(t, sub, storagetest.NewFakePauseStateRepository())
		if err := listener.Close(); err != nil {
			t.Errorf("Close() error = %v, want nil", err)
		}
		if !sub.closed {
			t.Errorf("subscriber Close() was not called")
		}
	})

	t.Run("closer_subscriber_error", func(t *testing.T) {
		wantErr := errors.New("close failure")
		sub := &mockCloserSubscriber{closeErr: wantErr}
		listener, _ := newTestListener(t, sub, storagetest.NewFakePauseStateRepository())
		if err := listener.Close(); !errors.Is(err, wantErr) {
			t.Errorf("Close() error = %v, want %v", err, wantErr)
		}
		if !sub.closed {
			t.Errorf("subscriber Close() was not called")
		}
	})
}
