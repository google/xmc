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

// Package pausestateschema contains the schema constants for the PauseSchedulingState table.
package pausestateschema

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

// ReadColumns is the list of columns fetched when reading the singleton row.
var ReadColumns = []string{
	ColSchedulingPaused,
	ColResumptionRunID,
	ColResumptionWorkerID,
	ColUpdatedAt,
}

// WriteColumns is the list of columns written when upserting the singleton row.
var WriteColumns = []string{
	ColID,
	ColSchedulingPaused,
	ColResumptionRunID,
	ColResumptionWorkerID,
	ColUpdatedAt,
}
