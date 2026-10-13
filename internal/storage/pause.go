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

package storage

import (
	"fmt"
	"time"

	actuatortaskpb "xmanager_cloud/api/protos/task_queue"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/grpcerr"
)

var (
	// ErrPausedTaskNotFound indicates the requested paused task does not exist.
	ErrPausedTaskNotFound = fmt.Errorf("paused task not found: %w", grpcerr.ErrNotFound)
	// ErrPausedTaskAlreadyExists indicates a task with the given key already exists.
	ErrPausedTaskAlreadyExists = fmt.Errorf("paused task already exists: %w", grpcerr.ErrAlreadyExists)
)

// PauseSchedulingState represents the global scheduling pause state and resumption metadata.
type PauseSchedulingState struct {
	SchedulingPaused   bool
	UpdatedAt          time.Time
	ResumptionRunID    string
	ResumptionWorkerID string
}

// DefaultPauseSchedulingState returns a new PauseSchedulingState with default uninitialized values.
func DefaultPauseSchedulingState() *PauseSchedulingState {
	return &PauseSchedulingState{
		SchedulingPaused: false,
	}
}

// ResolutionStatus represents the outcome of a paused task resolution during unpause resumption.
type ResolutionStatus string

const (
	ResolutionStatusReplayed ResolutionStatus = "replayed"
	ResolutionStatusSkipped  ResolutionStatus = "skipped"
)

// PausedTaskResolution contains the resolution details applied to a processed paused task.
type PausedTaskResolution struct {
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
