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

package pausestate

import (
	"context"
	"fmt"
	"sync"
	"time"

	"task/go/status"
)

var _ Repository = (*Fake)(nil)

// Fake is an in-memory test double for Repository.
type Fake struct {
	mu    sync.RWMutex
	state *PauseSchedulingState
}

// NewFake creates a new in-memory Fake.
func NewFake() *Fake {
	return &Fake{}
}

func clonePauseSchedulingState(s *PauseSchedulingState) *PauseSchedulingState {
	if s == nil {
		return nil
	}
	cp := *s
	return &cp
}

// Get retrieves the current global scheduling pause state from memory.
// If uninitialized, it returns a safe default with SchedulingPaused = false.
func (f *Fake) Get(ctx context.Context) (*PauseSchedulingState, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	f.mu.RLock()
	defer f.mu.RUnlock()

	if f.state == nil {
		return DefaultPauseSchedulingState(), nil
	}
	return clonePauseSchedulingState(f.state), nil
}

// Set updates or initializes the global scheduling pause state in memory.
func (f *Fake) Set(ctx context.Context, state *PauseSchedulingState) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if state == nil {
		return fmt.Errorf("%w: state cannot be nil", status.ErrInvalidArgument)
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	stored := clonePauseSchedulingState(state)
	stored.UpdatedAt = time.Now().UTC()
	f.state = stored
	return nil
}

// Reset clears the stored state from memory (test helper).
func (f *Fake) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state = nil
}
