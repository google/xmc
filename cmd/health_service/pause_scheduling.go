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
	"fmt"
	"io"

	"xmanager_cloud/internal/broadcast"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/task_queue_redis/redisclient"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var schedulingPauseEventsTotal = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Name: "xmc_scheduling_pause_events_total",
		Help: "Total count of pause and resume operations triggered via the Health Service API",
	},
	[]string{"action", "status"},
)

func recordPauseSchedulingEvent(action string, err error) {
	statusLabel := "success"
	if err != nil {
		statusLabel = "error"
	}
	schedulingPauseEventsTotal.WithLabelValues(action, statusLabel).Inc()
}

var (
	// errStorageNotConfigured is returned when scheduling state operations are requested
	// but the lazy storage provider is not configured.
	errStorageNotConfigured = errors.New("storage provider is not configured")

	// errBroadcastPublisherNotConfigured is returned when scheduling state changes are requested
	// but the broadcast publisher is unavailable, so actuators could not be notified.
	errBroadcastPublisherNotConfigured = errors.New("broadcast publisher is not configured")

	// errTaskQueuePublisherNotConfigured is returned when a resume is requested but the task queue
	// publisher is unavailable, so the resumption trigger could not be dispatched.
	errTaskQueuePublisherNotConfigured = errors.New("task queue publisher is not configured")
)

// PauseSchedulingStorage provides the transactional and pause scheduling repositories
// required by PauseScheduler.
type PauseSchedulingStorage interface {
	storage.TransactionRunner
	storage.PauseSchedulingStore
}

// LazyStorageProvider provides lazy access to Spanner-backed pause scheduling storage.
type LazyStorageProvider interface {
	Storage(ctx context.Context) (PauseSchedulingStorage, error)
}

// PauseScheduler manages the pause scheduling domain logic, state persistence,
// broadcast notifications, and task queue trigger dispatching.
type PauseScheduler interface {
	io.Closer

	// Pause sets the scheduling state to paused and clears resumption metadata.
	Pause(ctx context.Context) error

	// Resume clears the scheduling paused state, generates a new ResumptionRunID (or reuses the
	// existing one if already unpaused), and clears the ResumptionWorkerID when newly resumed.
	// It returns the active ResumptionRunID.
	Resume(ctx context.Context) (string, error)

	// IsPaused returns whether scheduling is currently paused.
	IsPaused(ctx context.Context) (bool, error)
}

// PauseSchedulerParams holds the dependencies and configuration for PauseScheduler.
type PauseSchedulerParams struct {
	LazyStorage         LazyStorageProvider
	BroadcastPublisher  broadcast.Publisher
	TaskQueuePublisher  redisclient.Publisher
	BatchSize           int
	MaxBacklogThreshold int
}

type schedulingResumeTrigger struct {
	ResumptionRunID     string `json:"resumption_run_id"`
	BatchSize           int    `json:"batch_size"`
	MaxBacklogThreshold int    `json:"max_backlog_threshold"`
}

type pauseScheduler struct {
	lazyStorage         LazyStorageProvider
	broadcastPublisher  broadcast.Publisher
	taskQueuePublisher  redisclient.Publisher
	batchSize           int
	maxBacklogThreshold int
}

var _ PauseScheduler = (*pauseScheduler)(nil)

// NewPauseScheduler creates a new PauseScheduler with the provided dependencies and configuration.
func NewPauseScheduler(params PauseSchedulerParams) PauseScheduler {
	return &pauseScheduler{
		lazyStorage:         params.LazyStorage,
		broadcastPublisher:  params.BroadcastPublisher,
		taskQueuePublisher:  params.TaskQueuePublisher,
		batchSize:           params.BatchSize,
		maxBacklogThreshold: params.MaxBacklogThreshold,
	}
}

// Close closes any underlying resources that implement io.Closer, such as the broadcast publisher.
func (p *pauseScheduler) Close() error {
	if closer, ok := p.broadcastPublisher.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}

func (p *pauseScheduler) storage(ctx context.Context) (PauseSchedulingStorage, error) {
	if p.lazyStorage == nil {
		return nil, errStorageNotConfigured
	}
	st, err := p.lazyStorage.Storage(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize storage: %w", err)
	}
	return st, nil
}

// checkRedisAvailable pings Redis before any Spanner write, so a known Redis outage fails fast
// instead of committing state whose notifications cannot be published. Both publishers use the
// same Redis server, so pinging the task queue publisher (if configured) also covers the broadcast stream.
func (p *pauseScheduler) checkRedisAvailable(ctx context.Context) error {
	if p.taskQueuePublisher == nil {
		return nil
	}
	if err := p.taskQueuePublisher.Ping(ctx); err != nil {
		return fmt.Errorf("redis is unavailable: %w", err)
	}
	return nil
}

// Pause updates the global scheduling state to paused and broadcasts the pause event.
func (p *pauseScheduler) Pause(ctx context.Context) (err error) {
	defer func() {
		recordPauseSchedulingEvent("pause", err)
	}()

	if p.broadcastPublisher == nil {
		return errBroadcastPublisherNotConfigured
	}
	if err := p.checkRedisAvailable(ctx); err != nil {
		return err
	}
	st, err := p.storage(ctx)
	if err != nil {
		return err
	}

	err = st.RunInTransaction(ctx, func(txCtx context.Context, tx *storage.Transaction) error {
		state := &storage.PauseSchedulingState{
			SchedulingPaused:   true,
			ResumptionRunID:    "",
			ResumptionWorkerID: "",
		}
		if err := tx.PauseState.Set(txCtx, state); err != nil {
			return fmt.Errorf("failed to persist pause scheduling state: %w", err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	return p.broadcastPauseEvent(ctx)
}

// Resume updates the global scheduling state to unpaused with a new run ID (or reuses the existing
// run ID if already unpaused), broadcasts the resume event, and publishes the task queue trigger
// whenever the run has not yet been claimed by a worker.
func (p *pauseScheduler) Resume(ctx context.Context) (_ string, err error) {
	defer func() {
		recordPauseSchedulingEvent("resume", err)
	}()

	if p.broadcastPublisher == nil {
		return "", errBroadcastPublisherNotConfigured
	}
	if p.taskQueuePublisher == nil {
		return "", errTaskQueuePublisherNotConfigured
	}
	if err := p.checkRedisAvailable(ctx); err != nil {
		return "", err
	}
	st, err := p.storage(ctx)
	if err != nil {
		return "", err
	}

	resumptionRunID, dispatch, err := persistResumeState(ctx, st)
	if err != nil {
		return "", err
	}
	if !dispatch {
		return resumptionRunID, nil
	}

	if err := p.broadcastResumeEvent(ctx, resumptionRunID); err != nil {
		return "", err
	}
	if err := p.publishResumeTrigger(ctx, resumptionRunID); err != nil {
		return "", err
	}

	return resumptionRunID, nil
}

// persistResumeState unpauses scheduling with a new run ID, or keeps the current run ID when
// scheduling is already unpaused. It reports whether the resume notifications must be dispatched:
// always for a new run, and also for an existing run that no worker has claimed yet, because an
// earlier Resume may have committed state but failed to publish. Dispatching again is safe since
// the resumption worker fences duplicate triggers by run ID and worker ID.
func persistResumeState(ctx context.Context, st PauseSchedulingStorage) (string, bool, error) {
	var resumptionRunID string
	var dispatch bool
	err := st.RunInTransaction(ctx, func(txCtx context.Context, tx *storage.Transaction) error {
		resumptionRunID = ""
		dispatch = false

		current, err := tx.PauseState.Get(txCtx)
		if err != nil {
			return fmt.Errorf("failed to retrieve pause scheduling state: %w", err)
		}
		if current == nil || !current.SchedulingPaused {
			if current != nil {
				resumptionRunID = current.ResumptionRunID
				dispatch = current.ResumptionRunID != "" && current.ResumptionWorkerID == ""
			}
			return nil
		}

		resumptionRunID = uuid.NewString()
		dispatch = true
		state := &storage.PauseSchedulingState{
			SchedulingPaused:   false,
			ResumptionRunID:    resumptionRunID,
			ResumptionWorkerID: "",
		}
		if err := tx.PauseState.Set(txCtx, state); err != nil {
			return fmt.Errorf("failed to persist resume scheduling state: %w", err)
		}
		return nil
	})
	if err != nil {
		return "", false, err
	}
	return resumptionRunID, dispatch, nil
}

func (p *pauseScheduler) broadcastPauseEvent(ctx context.Context) error {
	event := broadcast.NewSchedulingPaused()
	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to marshal scheduling paused event: %w", err)
	}
	if err := p.broadcastPublisher.Publish(ctx, string(payload)); err != nil {
		return fmt.Errorf("failed to broadcast scheduling paused event: %w", err)
	}
	return nil
}

func (p *pauseScheduler) broadcastResumeEvent(ctx context.Context, resumptionRunID string) error {
	event := broadcast.NewSchedulingResumed(resumptionRunID)
	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to marshal scheduling resumed event: %w", err)
	}
	if err := p.broadcastPublisher.Publish(ctx, string(payload)); err != nil {
		return fmt.Errorf("failed to broadcast scheduling resumed event: %w", err)
	}
	return nil
}

func (p *pauseScheduler) publishResumeTrigger(ctx context.Context, resumptionRunID string) error {
	trigger := schedulingResumeTrigger{
		ResumptionRunID:     resumptionRunID,
		BatchSize:           p.batchSize,
		MaxBacklogThreshold: p.maxBacklogThreshold,
	}
	payload, err := json.Marshal(trigger)
	if err != nil {
		return fmt.Errorf("failed to marshal scheduling resume trigger: %w", err)
	}
	if err := p.taskQueuePublisher.AddMessage(ctx, string(payload), "0"); err != nil {
		return fmt.Errorf("failed to publish scheduling resume trigger: %w", err)
	}
	return nil
}

// IsPaused returns whether scheduling is currently paused according to repository state.
func (p *pauseScheduler) IsPaused(ctx context.Context) (bool, error) {
	st, err := p.storage(ctx)
	if err != nil {
		return false, err
	}
	state, err := st.PauseState().Get(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to retrieve pause scheduling state: %w", err)
	}
	if state == nil {
		return false, nil
	}
	return state.SchedulingPaused, nil
}
