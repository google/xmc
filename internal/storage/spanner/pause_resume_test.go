// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package spanner

import (
	"context"
	"testing"

	"xmanager_cloud/storage/spanner/emulator/emulator"

	"xmanager_cloud/storage/storage"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	taskpb "xmanager_cloud/task_queue/proto/task_go_proto"
	"golang/protobuf/v2/proto/proto"
)

func TestPauseResumeOperations(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	store, _ := NewStorage(essClient)


	// 1. Test GetSchedulingPause initially false
	paused, err := store.GetSchedulingPause(ctx)
	if err != nil {
		t.Fatalf("GetSchedulingPause initially failed: %v", err)
	}
	if paused {
		t.Errorf("Expected scheduling pause state to be initially false, got true")
	}

	// 2. Test UpdateSchedulingPause to true
	err = store.UpdateSchedulingPause(ctx, true)
	if err != nil {
		t.Fatalf("UpdateSchedulingPause to true failed: %v", err)
	}

	paused, err = store.GetSchedulingPause(ctx)
	if err != nil {
		t.Fatalf("GetSchedulingPause after update failed: %v", err)
	}
	if !paused {
		t.Errorf("Expected scheduling pause state to be true, got false")
	}

	// 3. Setup parent Experiment and WorkUnit to test BufferPausedTask
	xid := int64(1001)
	wid := int64(2001)

	_ = createBlankExperiment(ctx, t, store, xid)
	_ = createBlankWorkUnit(ctx, t, store, xid, wid)

	// 4. Test BufferPausedTask
	task := &storage.PausedTask{
		BitReversedXid: reverseBits(xid),
		WorkUnitID:     wid,
		TaskID:         "task-123",
		Target:         "target-server",
		Action: &taskpb.Action{
			Action: &taskpb.Action_Create{
				Create: &taskpb.CreateAction{
					Author: proto.String("test-user@google.com"),
				},
			},
		},
		OperationName:  "operation-restart",
		TraceID:        "trace-456",
	}

	err = store.BufferPausedTask(ctx, task, nil)
	if err != nil {
		t.Fatalf("BufferPausedTask failed: %v", err)
	}

	// Verify WorkUnit state is updated to GLOBAL_PAUSE
	wu, err := store.FetchWorkUnit(ctx, xid, wid)
	if err != nil {
		t.Fatalf("FetchWorkUnit failed: %v", err)
	}
	if wu.CurrentWorkUnitExecution.Status.State != essmsgpb.WorkUnitStatus_GLOBAL_PAUSE {
		t.Errorf("Expected WorkUnit state to be GLOBAL_PAUSE, got %s", wu.CurrentWorkUnitExecution.Status.State)
	}

	// 5. Test FetchPausedTasksBatch
	tasks, err := store.FetchPausedTasksBatch(ctx, 10)
	if err != nil {
		t.Fatalf("FetchPausedTasksBatch failed: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("Expected 1 paused task, got %d", len(tasks))
	}
	fetched := tasks[0]
	if fetched.TaskID != task.TaskID || fetched.Target != task.Target || !proto.Equal(fetched.Action, task.Action) || fetched.OperationName != task.OperationName || fetched.TraceID != task.TraceID {
		t.Errorf("Fetched task mismatch: %+v vs %+v", fetched, task)
	}

	// 6. Test DeletePausedTasks
	err = store.DeletePausedTasks(ctx, []storage.PausedTaskKey{
		{
			BitReversedXid: task.BitReversedXid,
			WorkUnitID:     task.WorkUnitID,
			TaskID:         task.TaskID,
		},
	})
	if err != nil {
		t.Fatalf("DeletePausedTasks failed: %v", err)
	}

	tasks, err = store.FetchPausedTasksBatch(ctx, 10)
	if err != nil {
		t.Fatalf("FetchPausedTasksBatch after delete failed: %v", err)
	}
	if len(tasks) != 0 {
		t.Errorf("Expected 0 paused tasks, got %d", len(tasks))
	}
}
