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

package workunit_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/timestamppb"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/spanner"
	"xmanager_cloud/internal/storage/spanner/emulator"
	"xmanager_cloud/internal/task_queue_redis/redisclient"
	"xmanager_cloud/internal/workunit"
)

func TestRedisTerminalObserver_EmitsOnlyOnNewTerminalTransitions(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	st, err := spanner.NewStorage(essClient)
	if err != nil {
		t.Fatalf("spanner.NewStorage() error: %v", err)
	}

	const xid = 1
	spanner.CreateBlankExperiment(ctx, t, st, xid)
	spanner.CreateBlankWorkUnit(ctx, t, st, xid, 1)

	fakeRedis := redisclient.NewFakeClient()
	updater := workunit.NewUpdaterWithObserver(st, workunit.NewRedisTerminalObserver(fakeRedis))

	// Non-terminal transition INITIALIZING -> RUNNING with MlflowRunId: must NOT publish.
	_, err = updater.UpdateWorkUnitExecutions(ctx, xid, []*storage.WorkUnitExecutionUpdate{{
		WID: 1, EID: 1,
		WorkUnitExecution: &essmsgpb.WorkUnitExecution{
			Name:     "experiments/1/workUnits/1/executions/1",
			Status:   &essmsgpb.WorkUnitStatus{State: essmsgpb.WorkUnitStatus_RUNNING, StateUpdateTime: timestamppb.New(time.Unix(10, 0))},
			Metadata: &essmsgpb.WorkUnitExecutionMetadata{MlflowRunId: "run-1"},
		},
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state", "status.state_update_time", "metadata.mlflow_run_id"}},
	}})
	if err != nil {
		t.Fatalf("UpdateWorkUnitExecutions(RUNNING) error: %v", err)
	}
	if len(fakeRedis.Messages) != 0 {
		t.Fatalf("expected 0 messages for non-terminal transition, got %d", len(fakeRedis.Messages))
	}

	// Transition RUNNING -> COMPLETED: should publish once.
	_, err = updater.UpdateWorkUnitExecutions(ctx, xid, []*storage.WorkUnitExecutionUpdate{{
		WID: 1, EID: 1,
		WorkUnitExecution: &essmsgpb.WorkUnitExecution{
			Name:   "experiments/1/workUnits/1/executions/1",
			Status: &essmsgpb.WorkUnitStatus{State: essmsgpb.WorkUnitStatus_COMPLETED, StateUpdateTime: timestamppb.New(time.Unix(20, 0))},
		},
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state", "status.state_update_time"}},
	}})
	if err != nil {
		t.Fatalf("UpdateWorkUnitExecutions(COMPLETED) error: %v", err)
	}
	if len(fakeRedis.Messages) != 1 {
		t.Fatalf("expected 1 published message, got %d", len(fakeRedis.Messages))
	}
	var got workunit.MLflowSyncPayload
	if err := json.Unmarshal([]byte(fakeRedis.Messages[0]), &got); err != nil {
		t.Fatalf("json.Unmarshal() error: %v", err)
	}
	if got != (workunit.MLflowSyncPayload{XID: 1, WID: 1, EID: 1}) {
		t.Errorf("published payload = %+v, want {1 1 1}", got)
	}

	// Re-updating an already-COMPLETED work unit (e.g. mixed stop): must NOT publish again.
	_, err = updater.UpdateWorkUnitExecutions(ctx, xid, []*storage.WorkUnitExecutionUpdate{{
		WID: 1, EID: 1,
		WorkUnitExecution: &essmsgpb.WorkUnitExecution{
			Name:   "experiments/1/workUnits/1/executions/1",
			Status: &essmsgpb.WorkUnitStatus{State: essmsgpb.WorkUnitStatus_STOPPED, StateUpdateTime: timestamppb.New(time.Unix(30, 0))},
		},
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state", "status.state_update_time"}},
	}})
	if err != nil {
		t.Fatalf("UpdateWorkUnitExecutions(STOPPED on COMPLETED) error: %v", err)
	}
	if len(fakeRedis.Messages) != 1 {
		t.Errorf("expected still 1 message after terminal no-op, got %d", len(fakeRedis.Messages))
	}
}
