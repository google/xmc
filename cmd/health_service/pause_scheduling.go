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
	"fmt"

	"xmanager_cloud/internal/storage/spanner/pausestate"

	"github.com/google/uuid"
)

// PauseScheduler manages the pause scheduling domain logic and state persistence.
type PauseScheduler interface {
	// Pause sets the scheduling state to paused and clears resumption metadata.
	Pause(ctx context.Context) error

	// Resume clears the scheduling paused state, generates a new ResumptionRunID,
	// and clears the ResumptionWorkerID. It returns the newly generated ResumptionRunID.
	Resume(ctx context.Context) (string, error)

	// IsPaused returns whether scheduling is currently paused.
	IsPaused(ctx context.Context) (bool, error)
}

type pauseScheduler struct {
	repo pausestate.Repository
}

// NewPauseScheduler creates a new PauseScheduler backed by the given repository.
func NewPauseScheduler(repo pausestate.Repository) PauseScheduler {
	return &pauseScheduler{repo: repo}
}

// Pause updates the global scheduling state to paused.
func (p *pauseScheduler) Pause(ctx context.Context) error {
	state := &pausestate.PauseSchedulingState{
		SchedulingPaused:   true,
		ResumptionRunID:    "",
		ResumptionWorkerID: "",
	}
	if err := p.repo.Set(ctx, state); err != nil {
		return fmt.Errorf("failed to persist pause scheduling state: %w", err)
	}
	return nil
}

// Resume updates the global scheduling state to unpaused with a newly generated run ID.
func (p *pauseScheduler) Resume(ctx context.Context) (string, error) {
	// TODO: Move state check and update into a Spanner read-write transaction.
	current, err := p.repo.Get(ctx)
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
	if err := p.repo.Set(ctx, state); err != nil {
		return "", fmt.Errorf("failed to persist resume scheduling state: %w", err)
	}
	return resumptionRunID, nil
}

// IsPaused returns whether scheduling is currently paused according to repository state.
func (p *pauseScheduler) IsPaused(ctx context.Context) (bool, error) {
	state, err := p.repo.Get(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to retrieve pause scheduling state: %w", err)
	}
	if state == nil {
		return false, nil
	}
	return state.SchedulingPaused, nil
}
