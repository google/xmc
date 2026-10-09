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

// Package pausedtaskschema provides schema definitions for the PausedTasks table.
package pausedtaskschema

// TableName is the Spanner table name buffering tasks intercepted during a scheduling pause.
const TableName = "PausedTasks"

// UnprocessedIndexName is the null-filtered index ordering pending tasks for replay.
const UnprocessedIndexName = "PausedTasksUnprocessed"

// Spanner column names for the PausedTasks table.
const (
	ColXID              = "Xid"
	ColBitReversedXID   = "BitReversedXid"
	ColWorkUnitID       = "WorkUnitId"
	ColTaskID           = "TaskId"
	ColTarget           = "Target"
	ColActionProto      = "ActionProto"
	ColOperationName    = "OperationName"
	ColTraceID          = "TraceId"
	ColPriority         = "Priority"
	ColCreateTime       = "CreateTime"
	ColProcessedTime    = "ProcessedTime"
	ColResolutionStatus = "ResolutionStatus"
	ColResumptionRunID  = "ResumptionRunId"
	ColIsPending        = "IsPending"
)

// InsertColumns lists the PausedTasks columns written when inserting a new paused task.
var InsertColumns = []string{
	ColXID, ColWorkUnitID, ColTaskID, ColTarget, ColActionProto,
	ColOperationName, ColTraceID, ColPriority, ColCreateTime,
}

// ResolutionColumns starts with the primary key (including generated BitReversedXid) followed by
// the resolution columns updated when marking a paused task processed.
var ResolutionColumns = []string{
	ColBitReversedXID, ColWorkUnitID, ColTaskID,
	ColProcessedTime, ColResolutionStatus, ColResumptionRunID,
}
