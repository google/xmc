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

package broadcast

import "time"

const (
	// EventSchedulingPaused is the identifier for the SchedulingPaused event.
	EventSchedulingPaused = "SchedulingPaused"

	// EventSchedulingResumed is the identifier for the SchedulingResumed event.
	EventSchedulingResumed = "SchedulingResumed"
)

// SchedulingPaused represents an event broadcast when scheduling is paused.
type SchedulingPaused struct {
	Event     string    `json:"event"`
	Timestamp time.Time `json:"timestamp"`
	UpdatedBy string    `json:"updated_by,omitempty"`
	Reason    string    `json:"reason,omitempty"`
}

// NewSchedulingPaused creates a new SchedulingPaused event with UTC timestamp.
func NewSchedulingPaused(updatedBy, reason string) SchedulingPaused {
	return SchedulingPaused{
		Event:     EventSchedulingPaused,
		Timestamp: time.Now().UTC(),
		UpdatedBy: updatedBy,
		Reason:    reason,
	}
}

// SchedulingResumed represents an event broadcast when scheduling is resumed.
type SchedulingResumed struct {
	Event           string    `json:"event"`
	ResumptionRunID string    `json:"resumption_run_id,omitempty"`
	Timestamp       time.Time `json:"timestamp"`
	UpdatedBy       string    `json:"updated_by,omitempty"`
}

// NewSchedulingResumed creates a new SchedulingResumed event with UTC timestamp.
func NewSchedulingResumed(resumptionRunID, updatedBy string) SchedulingResumed {
	return SchedulingResumed{
		Event:           EventSchedulingResumed,
		ResumptionRunID: resumptionRunID,
		Timestamp:       time.Now().UTC(),
		UpdatedBy:       updatedBy,
	}
}
