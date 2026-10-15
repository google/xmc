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
	"fmt"
	"io"
	"sync"
	"time"

	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/storage"
)

var _ io.Closer = (*Listener)(nil)

const (
	defaultReconnectBackoff = 500 * time.Millisecond
	defaultReadTimeout      = 10 * time.Second
)

// StreamSubscriber defines the stream operations required by the pause listener.
type StreamSubscriber interface {
	// LatestOffset queries the stream to locate the starting message ID.
	LatestOffset(ctx context.Context) (string, error)
	// Subscribe blocks listening for broadcast updates starting from startID.
	Subscribe(ctx context.Context, startID string, handler func(msg string)) error
}

// Params holds the dependencies for creating a Listener.
type Params struct {
	Cache                Cache
	Subscriber           StreamSubscriber
	PauseStateRepository storage.PauseStateRepository
	ReconnectBackoff     time.Duration
	ReadTimeout          time.Duration
}

// Listener is the background worker subscribing to broadcast updates to maintain the local pause cache.
type Listener struct {
	cache            Cache
	subscriber       StreamSubscriber
	pauseStateRepo   storage.PauseStateRepository
	reconnectBackoff time.Duration
	readTimeout      time.Duration
	mu               sync.RWMutex
	startID          string
}

// New creates a new Listener instance.
func New(p Params) (*Listener, error) {
	if p.Cache == nil {
		return nil, fmt.Errorf("cache cannot be nil")
	}
	if p.Subscriber == nil {
		return nil, fmt.Errorf("subscriber cannot be nil")
	}
	if p.PauseStateRepository == nil {
		return nil, fmt.Errorf("pause state repository cannot be nil")
	}

	backoff := p.ReconnectBackoff
	if backoff <= 0 {
		backoff = defaultReconnectBackoff
	}
	readTimeout := p.ReadTimeout
	if readTimeout <= 0 {
		readTimeout = defaultReadTimeout
	}

	return &Listener{
		cache:            p.Cache,
		subscriber:       p.Subscriber,
		pauseStateRepo:   p.PauseStateRepository,
		reconnectBackoff: backoff,
		readTimeout:      readTimeout,
		startID:          "0",
	}, nil
}

// Init performs the synchronous startup phase: queries Redis stream offset and reads Spanner cold-start state.
func (l *Listener) Init(ctx context.Context) error {
	startID, err := l.subscriber.LatestOffset(ctx)
	if err != nil {
		return fmt.Errorf("failed to query stream offset on startup: %w", err)
	}
	if startID == "" {
		startID = "0"
	}
	l.setStartID(startID)

	state, err := l.pauseStateRepo.Get(ctx)
	if err != nil {
		return fmt.Errorf("failed cold-start read of pause scheduling state: %w", err)
	}

	l.cache.Update(State{
		IsPaused:  state.SchedulingPaused,
		UpdatedAt: state.UpdatedAt,
	})
	logging.Info(ctx, "Seeded Actuator pause scheduling cache on startup",
		"is_paused", state.SchedulingPaused, "updated_at", state.UpdatedAt, "start_id", startID)
	return nil
}

// Close closes the underlying subscriber if it implements io.Closer.
func (l *Listener) Close() error {
	if closer, ok := l.subscriber.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}

// StartListening runs the background listener loop, handling subscription disconnections and resyncing on reconnect.
func (l *Listener) StartListening(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}

		handler := func(msg string) {
			l.handleBroadcastUpdate(ctx, msg)
		}

		startID := l.getStartID()
		err := l.subscriber.Subscribe(ctx, startID, handler)
		if ctx.Err() != nil {
			return
		}

		logging.Warn(ctx, "Redis broadcast subscription disconnected, attempting reconnect", "error", err)

		for {
			l.waitForBackoff(ctx)
			if ctx.Err() != nil {
				return
			}
			if err := l.resyncOnReconnect(ctx); err == nil {
				break
			}
		}
	}
}

func (l *Listener) waitForBackoff(ctx context.Context) {
	timer := time.NewTimer(l.reconnectBackoff)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

// handleBroadcastUpdate refreshes the local cache from storage. A failed or timed-out read is
// retried until it succeeds or ctx is done: the stream has already moved past this message, so
// giving up would leave the cache stale until the next broadcast or reconnect.
func (l *Listener) handleBroadcastUpdate(ctx context.Context, msg string) {
	logging.Info(ctx, "Received broadcast update notification", "message", msg)
	state, err := l.readPauseState(ctx)
	for err != nil {
		if ctx.Err() != nil {
			return
		}
		logging.Error(ctx, "Failed to query pause scheduling state on broadcast update, retrying",
			"error", err, "backoff", l.reconnectBackoff)
		l.waitForBackoff(ctx)
		state, err = l.readPauseState(ctx)
	}
	l.cache.Update(State{
		IsPaused:  state.SchedulingPaused,
		UpdatedAt: state.UpdatedAt,
	})
	logging.Info(ctx, "Updated local pause state from broadcast notification",
		"is_paused", state.SchedulingPaused, "updated_at", state.UpdatedAt)
}

// readPauseState bounds a single pause state read with readTimeout, so a hung call surfaces
// as a retryable error instead of stalling the caller's retry loop.
func (l *Listener) readPauseState(ctx context.Context) (*storage.PauseSchedulingState, error) {
	readCtx, cancel := context.WithTimeout(ctx, l.readTimeout)
	defer cancel()
	return l.pauseStateRepo.Get(readCtx)
}

func (l *Listener) resyncOnReconnect(ctx context.Context) error {
	latestOffset, err := l.subscriber.LatestOffset(ctx)
	if err != nil {
		logging.Warn(ctx, "Failed to query stream offset on Redis reconnect", "error", err)
		return err
	}
	if latestOffset == "" {
		latestOffset = "0"
	}

	state, err := l.readPauseState(ctx)
	if err != nil {
		logging.Error(ctx, "Failed to resync pause state from repository on Redis reconnect", "error", err)
		return err
	}

	l.cache.Update(State{
		IsPaused:  state.SchedulingPaused,
		UpdatedAt: state.UpdatedAt,
	})
	l.setStartID(latestOffset)
	logging.Info(ctx, "Resynced Actuator pause cache on Redis reconnect",
		"is_paused", state.SchedulingPaused, "updated_at", state.UpdatedAt, "start_id", latestOffset)
	return nil
}

func (l *Listener) getStartID() string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.startID
}

func (l *Listener) setStartID(id string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.startID = id
}
