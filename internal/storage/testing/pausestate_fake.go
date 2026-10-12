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

// Package storagetest provides in-memory test doubles for storage repositories.
package storagetest

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/xmc/internal/grpcerr"
	"github.com/google/xmc/internal/storage"
)

var _ storage.PauseStateRepository = (*FakePauseStateRepository)(nil)
var _ storage.PauseStateTxRepository = (*FakePauseStateRepository)(nil)

// FakePauseStateRepository is an in-memory test double for storage.PauseStateRepository.
type FakePauseStateRepository struct {
	mu     sync.RWMutex
	state  *storage.PauseSchedulingState
	GetErr error
	SetErr error
}

// NewFakePauseStateRepository creates a new in-memory FakePauseStateRepository.
func NewFakePauseStateRepository() *FakePauseStateRepository {
	return &FakePauseStateRepository{}
}

// NewFakePauseStateRepositoryWithState returns a fake pre-seeded with a copy of state.
func NewFakePauseStateRepositoryWithState(state *storage.PauseSchedulingState) *FakePauseStateRepository {
	stored := clonePauseSchedulingState(state)
	if stored != nil && stored.UpdatedAt.IsZero() {
		stored.UpdatedAt = time.Now().UTC()
	}
	return &FakePauseStateRepository{state: stored}
}

func clonePauseSchedulingState(s *storage.PauseSchedulingState) *storage.PauseSchedulingState {
	if s == nil {
		return nil
	}
	cp := *s
	return &cp
}

// Get retrieves the current global scheduling pause state from memory.
// If uninitialized, it returns a safe default with SchedulingPaused = false.
func (f *FakePauseStateRepository) Get(ctx context.Context) (*storage.PauseSchedulingState, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	f.mu.RLock()
	defer f.mu.RUnlock()

	if f.GetErr != nil {
		return nil, f.GetErr
	}
	if f.state == nil {
		return storage.DefaultPauseSchedulingState(), nil
	}
	return clonePauseSchedulingState(f.state), nil
}

// Set updates or initializes the global scheduling pause state in memory.
func (f *FakePauseStateRepository) Set(ctx context.Context, state *storage.PauseSchedulingState) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if state == nil {
		return fmt.Errorf("%w: state cannot be nil", grpcerr.ErrInvalidArgument)
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	if f.SetErr != nil {
		return f.SetErr
	}
	stored := clonePauseSchedulingState(state)
	stored.UpdatedAt = time.Now().UTC()
	f.state = stored
	return nil
}

// Reset clears the stored state from memory (test helper).
func (f *FakePauseStateRepository) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state = nil
}
