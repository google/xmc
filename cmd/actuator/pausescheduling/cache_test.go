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
	"testing"
	"time"

	"golang/cmp/cmp"
)

func TestCache_InitialState(t *testing.T) {
	cache := NewCache()

	want := State{
		IsPaused:  false,
		UpdatedAt: time.Time{},
	}
	if diff := cmp.Diff(want, cache.State()); diff != "" {
		t.Errorf("State() mismatch (-want +got):\n%s", diff)
	}
}

func TestCache_UpdateAndAntiThrashing(t *testing.T) {
	cache := NewCache()

	t1 := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 3, 1, 13, 0, 0, 0, time.UTC)
	tOld := time.Date(2026, 3, 1, 11, 0, 0, 0, time.UTC)

	tests := []struct {
		name      string
		isPaused  bool
		updatedAt time.Time
		want      State
	}{
		{name: "pause_at_t1", isPaused: true, updatedAt: t1, want: State{IsPaused: true, UpdatedAt: t1}},
		{name: "unpause_at_t2", isPaused: false, updatedAt: t2, want: State{IsPaused: false, UpdatedAt: t2}},
		{name: "stale_pause_at_tOld_ignored", isPaused: true, updatedAt: tOld, want: State{IsPaused: false, UpdatedAt: t2}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cache.Update(State{IsPaused: tt.isPaused, UpdatedAt: tt.updatedAt})

			if diff := cmp.Diff(tt.want, cache.State()); diff != "" {
				t.Errorf("State() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestCache_ConcurrentAccess(t *testing.T) {
	cache := NewCache()
	baseTime := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		offset := time.Duration(i) * time.Millisecond
		go func(ts time.Time, paused bool) {
			defer wg.Done()
			cache.Update(State{IsPaused: paused, UpdatedAt: ts})
		}(baseTime.Add(offset), i%2 == 0)

		go func() {
			defer wg.Done()
			_ = cache.State()
		}()
	}
	wg.Wait()
}

func TestCache_ZeroTimestampHandling(t *testing.T) {
	t.Run("zero_timestamp_keeps_initial_zero_timestamp", func(t *testing.T) {
		cache := NewCache()
		cache.Update(State{IsPaused: true})
		got := cache.State()
		if !got.IsPaused {
			t.Errorf("IsPaused = false, want true")
		}
		if !got.UpdatedAt.IsZero() {
			t.Errorf("UpdatedAt = %v, expected zero time", got.UpdatedAt)
		}
	})

	t.Run("preserves_existing_non_zero_timestamp", func(t *testing.T) {
		cache := NewCache()
		t1 := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
		cache.Update(State{IsPaused: false, UpdatedAt: t1})
		cache.Update(State{IsPaused: true})

		got := cache.State()
		if !got.IsPaused {
			t.Errorf("IsPaused = false, want true")
		}
		if !got.UpdatedAt.Equal(t1) {
			t.Errorf("UpdatedAt = %v, want preserved %v", got.UpdatedAt, t1)
		}
	})
}
