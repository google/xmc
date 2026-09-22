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
	"testing"
	"time"

	googlespanner "golang/cloud_google_com/go/spanner/v/v1/spanner"
	"task/go/status"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/storage/spanner"
	"xmanager_cloud/storage/spanner/emulator/emulator"
)

func TestRepository_ExecutionOperations_HappyPath(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storageClient, err := spanner.NewStorage(essClient)
	if err != nil {
		t.Fatalf("spanner.NewStorage failed: %v", err)
	}

	xid := int64(100)
	wid := int64(1)
	spanner.CreateBlankExperiment(ctx, t, storageClient, xid)
	wu := spanner.CreateBlankWorkUnit(ctx, t, storageClient, xid, wid)

	now := time.Now()
	err = storageClient.RunInTransaction(ctx, func(txCtx context.Context) error {
		// 1. Insert archived execution
		firstExec := wu.GetCurrentWorkUnitExecution()
		firstExec.State = essmsgpb.WorkUnitExecution_INACTIVE
		if err := storageClient.SaveWorkUnitExecution(txCtx, firstExec); err != nil {
			return err
		}

		// 2. Save status history
		prevState := essmsgpb.WorkUnitStatus_STARTING
		newState := essmsgpb.WorkUnitStatus_RUNNING
		resources := map[string]int64{"cpu": 2}
		eid := int64(2)
		if err := storageClient.SaveWorkUnitStatusHistory(txCtx, xid, wid, &eid, "default-queue", &prevState, &newState, resources, now); err != nil {
			return err
		}

		// 3. Update current execution
		secondExec := &essmsgpb.WorkUnitExecution{
			Name:  "experiments/100/workUnits/1/executions/2",
			State: essmsgpb.WorkUnitExecution_ACTIVE,
			Status: &essmsgpb.WorkUnitStatus{
				State: essmsgpb.WorkUnitStatus_RUNNING,
			},
		}
		return storageClient.UpdateCurrentWorkUnitExecution(txCtx, xid, wid, 2, secondExec)
	})
	if err != nil {
		t.Fatalf("RunInTransaction failed: %v", err)
	}

	// Verify current execution was updated
	fetchedWu, err := storageClient.FetchWorkUnit(ctx, xid, wid)
	if err != nil {
		t.Fatalf("FetchWorkUnit failed: %v", err)
	}
	if fetchedWu.GetCurrentWorkUnitExecution().GetName() != "experiments/100/workUnits/1/executions/2" {
		t.Errorf("Current execution name = %q, want %q", fetchedWu.GetCurrentWorkUnitExecution().GetName(), "experiments/100/workUnits/1/executions/2")
	}

	// Verify history was written
	stmt := googlespanner.Statement{
		SQL:    "SELECT COUNT(1) FROM WorkUnitStatusHistory WHERE Xid = @xid",
		Params: map[string]any{"xid": xid},
	}
	iter := essClient.Single().Query(ctx, stmt)
	defer iter.Stop()
	row, err := iter.Next()
	if err != nil {
		t.Fatalf("query WorkUnitStatusHistory failed: %v", err)
	}
	var count int64
	if err := row.Column(0, &count); err != nil {
		t.Fatalf("parse count failed: %v", err)
	}
	if count == 0 {
		t.Error("expected WorkUnitStatusHistory record, got 0")
	}
}

func TestRepository_ExecutionOperations_RequiresTransaction(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storageClient, err := spanner.NewStorage(essClient)
	if err != nil {
		t.Fatalf("spanner.NewStorage failed: %v", err)
	}

	exec := &essmsgpb.WorkUnitExecution{Name: "experiments/1/workUnits/1/executions/1"}
	if err := storageClient.SaveWorkUnitExecution(ctx, exec); status.ExtractCanonicalCode(err) != status.FailedPrecondition {
		t.Errorf("SaveWorkUnitExecution outside tx returned %v, want FailedPrecondition", err)
	}
	if err := storageClient.UpdateCurrentWorkUnitExecution(ctx, 1, 1, 1, exec); status.ExtractCanonicalCode(err) != status.FailedPrecondition {
		t.Errorf("UpdateCurrentWorkUnitExecution outside tx returned %v, want FailedPrecondition", err)
	}
	if err := storageClient.SaveWorkUnitStatusHistory(ctx, 1, 1, nil, "", nil, nil, nil, time.Now()); status.ExtractCanonicalCode(err) != status.FailedPrecondition {
		t.Errorf("SaveWorkUnitStatusHistory outside tx returned %v, want FailedPrecondition", err)
	}
}
