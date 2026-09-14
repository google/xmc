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

// Package pausestate defines the entity, repository interface, and stub for pause scheduling state.
package pausestate

import (
	"time"
)

// TableName is the Spanner table name persisting the scheduling pause state.
const TableName = "PauseSchedulingState"

// Spanner column names for the PauseSchedulingState table.
const (
	ColID                 = "Id"
	ColSchedulingPaused   = "SchedulingPaused"
	ColResumptionRunID    = "ResumptionRunId"
	ColResumptionWorkerID = "ResumptionWorkerId"
	ColUpdatedAt          = "UpdatedAt"
)

// PauseSchedulingState represents the global scheduling pause state and resumption metadata.
type PauseSchedulingState struct {
	SchedulingPaused   bool      `spanner:"SchedulingPaused"`
	UpdatedAt          time.Time `spanner:"UpdatedAt"`
	ResumptionRunID    string    `spanner:"ResumptionRunId"`
	ResumptionWorkerID string    `spanner:"ResumptionWorkerId"`
}

// DefaultPauseSchedulingState returns a new PauseSchedulingState with default uninitialized values.
func DefaultPauseSchedulingState() *PauseSchedulingState {
	return &PauseSchedulingState{
		SchedulingPaused: false,
	}
}
