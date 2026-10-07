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
	"fmt"
	"testing"

	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/storage"
	actuatortaskpb "xmanager_cloud/task_queue/proto/task_go_proto"
)

func newTestWorkUnit(name string, eid int64, state essmsgpb.WorkUnitStatus_WorkUnitState, priority essmsgpb.Priority) *essmsgpb.WorkUnit {
	execName := ""
	if eid > 0 {
		execName = fmt.Sprintf("%s/executions/%d", name, eid)
	}
	return &essmsgpb.WorkUnit{
		Name:     name,
		Priority: priority,
		CurrentWorkUnitExecution: &essmsgpb.WorkUnitExecution{
			Name: execName,
			Status: &essmsgpb.WorkUnitStatus{
				State: state,
			},
		},
	}
}

func TestShouldSkipTask(t *testing.T) {
	const (
		targetE1 = "experiments/1/workUnits/1/executions/1"
		targetE2 = "experiments/1/workUnits/1/executions/2"
		wuName   = "experiments/1/workUnits/1"
	)

	createAction := &actuatortaskpb.Action{Action: &actuatortaskpb.Action_Create{Create: &actuatortaskpb.CreateAction{}}}
	updateAction := &actuatortaskpb.Action{Action: &actuatortaskpb.Action_Update{Update: &actuatortaskpb.UpdateAction{}}}
	restartAction := &actuatortaskpb.Action{Action: &actuatortaskpb.Action_Restart{Restart: &actuatortaskpb.RestartAction{}}}

	tests := []struct {
		name       string
		task       *storage.PausedTask
		wantSkip   bool
		wantReason TaskFilterReason
	}{
		{
			name: "rule 1: skips when newer execution ID exists on work unit (CreateAction)",
			task: &storage.PausedTask{
				TaskID:   "task-1",
				Target:   targetE1,
				Action:   createAction,
				Priority: int64(essmsgpb.Priority_NORMAL),
				WorkUnit: newTestWorkUnit(wuName, 2, essmsgpb.WorkUnitStatus_RUNNING, essmsgpb.Priority_NORMAL),
			},
			wantSkip:   true,
			wantReason: ReasonNewerExecutionExists,
		},
		{
			name: "rule 1: does not skip when execution ID matches work unit (CreateAction)",
			task: &storage.PausedTask{
				TaskID:   "task-1",
				Target:   targetE2,
				Action:   createAction,
				Priority: int64(essmsgpb.Priority_NORMAL),
				WorkUnit: newTestWorkUnit(wuName, 2, essmsgpb.WorkUnitStatus_RUNNING, essmsgpb.Priority_NORMAL),
			},
			wantSkip:   false,
			wantReason: ReasonNone,
		},
		{
			name: "rule 1: does not skip RestartAction when WorkUnit is on expected next execution (EID+1)",
			task: &storage.PausedTask{
				TaskID:   "task-1",
				Target:   targetE1,
				Action:   restartAction,
				Priority: int64(essmsgpb.Priority_NORMAL),
				WorkUnit: newTestWorkUnit(wuName, 2, essmsgpb.WorkUnitStatus_RUNNING, essmsgpb.Priority_NORMAL),
			},
			wantSkip:   false,
			wantReason: ReasonNone,
		},
		{
			name: "rule 1: skips RestartAction when WorkUnit has superseded beyond next execution (> EID+1)",
			task: &storage.PausedTask{
				TaskID:   "task-1",
				Target:   targetE1,
				Action:   restartAction,
				Priority: int64(essmsgpb.Priority_NORMAL),
				WorkUnit: newTestWorkUnit(wuName, 3, essmsgpb.WorkUnitStatus_RUNNING, essmsgpb.Priority_NORMAL),
			},
			wantSkip:   true,
			wantReason: ReasonNewerExecutionExists,
		},
		{
			name: "rule 2: skips UpdateAction when WorkUnit is in SCHEDULING_PAUSED state",
			task: &storage.PausedTask{
				TaskID:   "task-1",
				Target:   targetE1,
				Action:   updateAction,
				Priority: int64(essmsgpb.Priority_NORMAL),
				WorkUnit: newTestWorkUnit(wuName, 1, essmsgpb.WorkUnitStatus_SCHEDULING_PAUSED, essmsgpb.Priority_NORMAL),
			},
			wantSkip:   true,
			wantReason: ReasonUpdateWhilePaused,
		},
		{
			name: "rule 2: does not skip CreateAction when WorkUnit is in SCHEDULING_PAUSED state",
			task: &storage.PausedTask{
				TaskID:   "task-1",
				Target:   targetE1,
				Action:   createAction,
				Priority: int64(essmsgpb.Priority_NORMAL),
				WorkUnit: newTestWorkUnit(wuName, 1, essmsgpb.WorkUnitStatus_SCHEDULING_PAUSED, essmsgpb.Priority_NORMAL),
			},
			wantSkip:   false,
			wantReason: ReasonNone,
		},
		{
			name: "rule 2: does not skip RestartAction when WorkUnit is in SCHEDULING_PAUSED state",
			task: &storage.PausedTask{
				TaskID:   "task-1",
				Target:   targetE1,
				Action:   restartAction,
				Priority: int64(essmsgpb.Priority_NORMAL),
				WorkUnit: newTestWorkUnit(wuName, 2, essmsgpb.WorkUnitStatus_SCHEDULING_PAUSED, essmsgpb.Priority_NORMAL),
			},
			wantSkip:   false,
			wantReason: ReasonNone,
		},
		{
			name: "rule 3: skips UpdateAction when task priority does not match WorkUnit priority",
			task: &storage.PausedTask{
				TaskID:   "task-1",
				Target:   targetE1,
				Action:   updateAction,
				Priority: int64(essmsgpb.Priority_LOW),
				WorkUnit: newTestWorkUnit(wuName, 1, essmsgpb.WorkUnitStatus_RUNNING, essmsgpb.Priority_HIGH),
			},
			wantSkip:   true,
			wantReason: ReasonStaleUpdatePriority,
		},
		{
			name: "rule 3: does not skip UpdateAction when task priority matches WorkUnit priority",
			task: &storage.PausedTask{
				TaskID:   "task-1",
				Target:   targetE1,
				Action:   updateAction,
				Priority: int64(essmsgpb.Priority_HIGH),
				WorkUnit: newTestWorkUnit(wuName, 1, essmsgpb.WorkUnitStatus_RUNNING, essmsgpb.Priority_HIGH),
			},
			wantSkip:   false,
			wantReason: ReasonNone,
		},
		{
			name: "rule 3: does not skip CreateAction when task priority differs from WorkUnit priority",
			task: &storage.PausedTask{
				TaskID:   "task-1",
				Target:   targetE1,
				Action:   createAction,
				Priority: int64(essmsgpb.Priority_LOW),
				WorkUnit: newTestWorkUnit(wuName, 1, essmsgpb.WorkUnitStatus_RUNNING, essmsgpb.Priority_HIGH),
			},
			wantSkip:   false,
			wantReason: ReasonNone,
		},
		{
			name: "rule 4: skips when WorkUnit is COMPLETED",
			task: &storage.PausedTask{
				TaskID:   "task-1",
				Target:   targetE1,
				Action:   createAction,
				Priority: int64(essmsgpb.Priority_NORMAL),
				WorkUnit: newTestWorkUnit(wuName, 1, essmsgpb.WorkUnitStatus_COMPLETED, essmsgpb.Priority_NORMAL),
			},
			wantSkip:   true,
			wantReason: ReasonTerminalState,
		},
		{
			name: "rule 4: skips when WorkUnit is STOPPED",
			task: &storage.PausedTask{
				TaskID:   "task-1",
				Target:   targetE1,
				Action:   createAction,
				Priority: int64(essmsgpb.Priority_NORMAL),
				WorkUnit: newTestWorkUnit(wuName, 1, essmsgpb.WorkUnitStatus_STOPPED, essmsgpb.Priority_NORMAL),
			},
			wantSkip:   true,
			wantReason: ReasonTerminalState,
		},
		{
			name: "rule 4: skips when WorkUnit is STOPPING",
			task: &storage.PausedTask{
				TaskID:   "task-1",
				Target:   targetE1,
				Action:   createAction,
				Priority: int64(essmsgpb.Priority_NORMAL),
				WorkUnit: newTestWorkUnit(wuName, 1, essmsgpb.WorkUnitStatus_STOPPING, essmsgpb.Priority_NORMAL),
			},
			wantSkip:   true,
			wantReason: ReasonTerminalState,
		},
		{
			name: "rule 4: skips when WorkUnit is FAILED",
			task: &storage.PausedTask{
				TaskID:   "task-1",
				Target:   targetE1,
				Action:   createAction,
				Priority: int64(essmsgpb.Priority_NORMAL),
				WorkUnit: newTestWorkUnit(wuName, 1, essmsgpb.WorkUnitStatus_FAILED, essmsgpb.Priority_NORMAL),
			},
			wantSkip:   true,
			wantReason: ReasonTerminalState,
		},
		{
			name: "rule 4 takes precedence over rule 3: UpdateAction on STOPPED WorkUnit with priority mismatch",
			task: &storage.PausedTask{
				TaskID:   "task-1",
				Target:   targetE1,
				Action:   updateAction,
				Priority: int64(essmsgpb.Priority_LOW),
				WorkUnit: newTestWorkUnit(wuName, 1, essmsgpb.WorkUnitStatus_STOPPED, essmsgpb.Priority_HIGH),
			},
			wantSkip:   true,
			wantReason: ReasonTerminalState,
		},
		{
			name: "rule 4: does not skip when WorkUnit is in non-terminal states",
			task: &storage.PausedTask{
				TaskID:   "task-1",
				Target:   targetE1,
				Action:   createAction,
				Priority: int64(essmsgpb.Priority_NORMAL),
				WorkUnit: newTestWorkUnit(wuName, 1, essmsgpb.WorkUnitStatus_INITIALIZING, essmsgpb.Priority_NORMAL),
			},
			wantSkip:   false,
			wantReason: ReasonNone,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotSkip, gotReason := ShouldSkipTask(tt.task)
			if gotSkip != tt.wantSkip {
				t.Errorf("ShouldSkipTask() gotSkip = %v, want %v", gotSkip, tt.wantSkip)
			}
			if gotReason != tt.wantReason {
				t.Errorf("ShouldSkipTask() gotReason = %q, want %q", gotReason, tt.wantReason)
			}
		})
	}
}
