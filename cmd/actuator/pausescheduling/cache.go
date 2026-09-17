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
	"sync"
	"time"
)

// State represents an atomic point-in-time view of the scheduling pause state.
type State struct {
	IsPaused  bool
	UpdatedAt time.Time
}

// Cache defines the thread-safe local cache interface for Actuator pause scheduling state.
type Cache interface {
	// State returns an atomic point-in-time view of the scheduling pause state.
	State() State
	// Update atomically updates is_paused and updated_at, preventing backwards state thrashing.
	Update(isPaused bool, updatedAt time.Time)
}

var _ Cache = (*localCache)(nil)

type localCache struct {
	mu        sync.RWMutex
	isPaused  bool
	updatedAt time.Time
}

// NewCache creates a new in-memory thread-safe local pause cache.
func NewCache() Cache {
	return &localCache{}
}

// State returns an atomic point-in-time view of the scheduling pause state.
func (c *localCache) State() State {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return State{
		IsPaused:  c.isPaused,
		UpdatedAt: c.updatedAt,
	}
}

// Update atomically updates is_paused and updated_at, preventing backwards state thrashing.
func (c *localCache) Update(isPaused bool, updatedAt time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Prevent state thrashing from older/stale updates
	if !updatedAt.IsZero() && !c.updatedAt.IsZero() && updatedAt.Before(c.updatedAt) {
		return
	}

	c.isPaused = isPaused
	if !updatedAt.IsZero() {
		c.updatedAt = updatedAt
	}
}
