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
)

// Repository defines operations for persisting and retrieving the scheduling pause state.
type Repository interface {
	// Get retrieves the current global scheduling pause state.
	// If uninitialized, it returns a safe default with SchedulingPaused = false.
	Get(ctx context.Context) (*PauseSchedulingState, error)

	// Set updates or initializes the global scheduling pause state.
	Set(ctx context.Context, state *PauseSchedulingState) error
}
