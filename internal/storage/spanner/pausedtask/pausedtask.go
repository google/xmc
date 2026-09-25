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

// Package pausedtask defines the entity, repository interface, and stub for paused tasks.
package pausedtask

import (
	"time"

	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	actuatortaskpb "xmanager_cloud/task_queue/proto/task_go_proto"
)

// ResolutionStatus represents the outcome of a paused task resolution during unpause resumption.
type ResolutionStatus string

const (
	ResolutionStatusReplayed ResolutionStatus = "replayed"
	ResolutionStatusSkipped  ResolutionStatus = "skipped"
)

// TaskResolution contains the resolution details applied to a processed paused task.
type TaskResolution struct {
	XID              int64
	WID              int64
	TaskID           string
	ResolutionStatus ResolutionStatus
	ResumptionRunID  string
	ProcessedTime    time.Time
}

// PausedTask represents a task intercepted and buffered while scheduling is paused.
type PausedTask struct {
	XID              int64
	WID              int64
	TaskID           string
	Target           string
	Action           *actuatortaskpb.Action
	WorkUnit         *essmsgpb.WorkUnit
	OperationName    string
	TraceID          string
	Priority         int64
	CreateTime       time.Time
	ProcessedTime    *time.Time
	ResolutionStatus ResolutionStatus
	ResumptionRunID  string
}
