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

	"xmanager_cloud/internal/broadcast"
	"xmanager_cloud/internal/storage/spanner/pausestate"
	"xmanager_cloud/task_queue_redis/redisclient"

	"github.com/google/uuid"
)

var (
	// errBroadcastPublisherNotConfigured is returned when scheduling state changes are requested
	// but the broadcast publisher is unavailable, so actuators could not be notified.
	errBroadcastPublisherNotConfigured = errors.New("broadcast publisher is not configured")

	// errTaskQueuePublisherNotConfigured is returned when a resume is requested but the task queue
	// publisher is unavailable, so the resumption trigger could not be dispatched.
	errTaskQueuePublisherNotConfigured = errors.New("task queue publisher is not configured")
)

// PauseScheduler manages the pause scheduling domain logic, state persistence,
// broadcast notifications, and task queue trigger dispatching.
type PauseScheduler interface {
	// Pause sets the scheduling state to paused and clears resumption metadata.
	Pause(ctx context.Context) error

	// Resume clears the scheduling paused state, generates a new ResumptionRunID,
	// and clears the ResumptionWorkerID. It returns the newly generated ResumptionRunID.
	Resume(ctx context.Context) (string, error)

	// IsPaused returns whether scheduling is currently paused.
	IsPaused(ctx context.Context) (bool, error)
}

// PauseSchedulerParams holds the dependencies and configuration for PauseScheduler.
type PauseSchedulerParams struct {
	PauseStateRepository pausestate.Repository
	BroadcastPublisher   broadcast.Publisher
	TaskQueuePublisher   redisclient.Publisher
	BatchSize            int
	MaxBacklogThreshold  int
}

type schedulingResumeTrigger struct {
	ResumptionRunID     string `json:"resumption_run_id"`
	BatchSize           int    `json:"batch_size"`
	MaxBacklogThreshold int    `json:"max_backlog_threshold"`
}

type pauseScheduler struct {
	pauseStateRepository pausestate.Repository
	broadcastPublisher   broadcast.Publisher
	taskQueuePublisher   redisclient.Publisher
	batchSize            int
	maxBacklogThreshold  int
}

// NewPauseScheduler creates a new PauseScheduler with the provided dependencies and configuration.
func NewPauseScheduler(params PauseSchedulerParams) PauseScheduler {
	return &pauseScheduler{
		pauseStateRepository: params.PauseStateRepository,
		broadcastPublisher:   params.BroadcastPublisher,
		taskQueuePublisher:   params.TaskQueuePublisher,
		batchSize:            params.BatchSize,
		maxBacklogThreshold:  params.MaxBacklogThreshold,
	}
}

// Pause updates the global scheduling state to paused and broadcasts the pause event.
func (p *pauseScheduler) Pause(ctx context.Context) error {
	if p.broadcastPublisher == nil {
		return errBroadcastPublisherNotConfigured
	}
	state := &pausestate.PauseSchedulingState{
		SchedulingPaused:   true,
		ResumptionRunID:    "",
		ResumptionWorkerID: "",
	}
	if err := p.pauseStateRepository.Set(ctx, state); err != nil {
		return fmt.Errorf("failed to persist pause scheduling state: %w", err)
	}
	return p.broadcastPauseEvent(ctx)
}

// Resume updates the global scheduling state to unpaused with a newly generated run ID,
// broadcasts the resume event, and publishes the task queue resume trigger.
func (p *pauseScheduler) Resume(ctx context.Context) (string, error) {
	if p.broadcastPublisher == nil {
		return "", errBroadcastPublisherNotConfigured
	}
	if p.taskQueuePublisher == nil {
		return "", errTaskQueuePublisherNotConfigured
	}

	// TODO: Move state check and update into a Spanner read-write transaction.
	current, err := p.pauseStateRepository.Get(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to retrieve pause scheduling state: %w", err)
	}
	if current != nil && !current.SchedulingPaused {
		return current.ResumptionRunID, nil
	}

	resumptionRunID := uuid.NewString()
	state := &pausestate.PauseSchedulingState{
		SchedulingPaused:   false,
		ResumptionRunID:    resumptionRunID,
		ResumptionWorkerID: "",
	}
	if err := p.pauseStateRepository.Set(ctx, state); err != nil {
		return "", fmt.Errorf("failed to persist resume scheduling state: %w", err)
	}

	if err := p.broadcastResumeEvent(ctx, resumptionRunID); err != nil {
		return "", err
	}
	if err := p.publishResumeTrigger(ctx, resumptionRunID); err != nil {
		return "", err
	}

	return resumptionRunID, nil
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
	p.taskQueuePublisher.AddMessage(ctx, string(payload), "0")
	return nil
}

// IsPaused returns whether scheduling is currently paused according to repository state.
func (p *pauseScheduler) IsPaused(ctx context.Context) (bool, error) {
	state, err := p.pauseStateRepository.Get(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to retrieve pause scheduling state: %w", err)
	}
	if state == nil {
		return false, nil
	}
	return state.SchedulingPaused, nil
}
