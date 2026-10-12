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

package resumption

import (
	essmsgpb "github.com/google/xmc/api/protos/experiment"
	"github.com/google/xmc/internal/storage"
	"github.com/google/xmc/internal/validator"
)

// TaskFilterReason explains why a paused task was skipped during unpause resumption.
type TaskFilterReason string

const (
	// ReasonNone indicates the task is valid and should be replayed.
	ReasonNone TaskFilterReason = ""
	// ReasonNewerExecutionExists indicates a newer execution exists on the parent WorkUnit.
	ReasonNewerExecutionExists TaskFilterReason = "newer execution ID exists on work unit"
	// ReasonUpdateWhilePaused indicates an update was buffered while the WorkUnit is in SCHEDULING_PAUSED state.
	ReasonUpdateWhilePaused TaskFilterReason = "update action while work unit is in SCHEDULING_PAUSED state"
	// ReasonStaleUpdatePriority indicates an update's priority does not match the live WorkUnit priority.
	ReasonStaleUpdatePriority TaskFilterReason = "update action priority does not match work unit priority"
	// ReasonTerminalState indicates the parent WorkUnit has transitioned to a terminal execution state.
	ReasonTerminalState TaskFilterReason = "work unit is in terminal execution state"
)

// ShouldSkipTask evaluates a paused task against its live WorkUnit state according to the 4 terminal rules:
// 1. Newer ExecutionId exists on the WorkUnit.
// 2. Terminal WorkUnit execution states (COMPLETED, STOPPED, STOPPING, FAILED).
// 3. Stale UpdateAction while WorkUnit is in SCHEDULING_PAUSED state.
// 4. Stale UpdateAction where task priority does not match WorkUnit priority.
//
// Returns (true, reason) if the task should be skipped, or (false, ReasonNone) if it should be replayed.
func ShouldSkipTask(task *storage.PausedTask) (bool, TaskFilterReason) {
	if hasNewerExecutionID(task) {
		return true, ReasonNewerExecutionExists
	}

	state := task.WorkUnit.GetCurrentWorkUnitExecution().GetStatus().GetState()
	if isTerminalState(state) {
		return true, ReasonTerminalState
	}

	isUpdate := task.Action != nil && task.Action.GetUpdate() != nil
	if isUpdate {
		if state == essmsgpb.WorkUnitStatus_SCHEDULING_PAUSED {
			return true, ReasonUpdateWhilePaused
		}
		if task.Priority != int64(task.WorkUnit.GetPriority()) {
			return true, ReasonStaleUpdatePriority
		}
	}

	return false, ReasonNone
}

func hasNewerExecutionID(task *storage.PausedTask) bool {
	taskEid, ok := parseExecutionID(task.Target)
	if !ok {
		return false
	}

	currentExec := task.WorkUnit.GetCurrentWorkUnitExecution()
	if currentExec == nil {
		return false
	}

	wuEid, ok := parseExecutionID(currentExec.GetName())
	if !ok {
		return false
	}

	// For RestartAction, Target is the old execution (EID N) being replaced by EID N+1.
	// Therefore, the restart is only obsolete if the WorkUnit has moved beyond N+1.
	if task.Action != nil && task.Action.GetRestart() != nil {
		return wuEid > (taskEid + 1)
	}

	return wuEid > taskEid
}

func parseExecutionID(name string) (int64, bool) {
	if name == "" {
		return 0, false
	}
	_, _, eid, err := validator.ValidateWorkUnitExecutionName(name)
	return eid, err == nil
}

func isTerminalState(state essmsgpb.WorkUnitStatus_WorkUnitState) bool {
	switch state {
	case essmsgpb.WorkUnitStatus_COMPLETED,
		essmsgpb.WorkUnitStatus_STOPPED,
		essmsgpb.WorkUnitStatus_STOPPING,
		essmsgpb.WorkUnitStatus_FAILED:
		return true
	default:
		return false
	}
}
