// Copyright 2025 Google LLC
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
package workunit_test

import (
	"context"
	"testing"
	"xmanager_cloud/internal/storage/spanner"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/testing/protocmp"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	corestorage "xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/spanner/emulator"
	"xmanager_cloud/validator/validator"
)

func TestListWorkUnitExecutions(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storage, err := spanner.NewStorage(essClient)
	if err != nil {
		t.Fatalf("spanner.NewStorage failed: %v", err)
	}

	// Create experiment.
	spanner.CreateBlankExperiment(ctx, t, storage, 123)

	// Create work unit.
	createdWu := spanner.CreateBlankWorkUnit(ctx, t, storage, 123, 1)

	xid, wid, err := validator.ValidateWorkUnitName(createdWu.GetName())
	if err != nil {
		t.Fatalf("Could not parse XID, WID from name %q: %v", createdWu.GetName(), err)
	}

	gotWues, err := storage.ListWorkUnitExecutions(ctx, xid, wid)
	if err != nil {
		t.Fatalf("ListWorkUnitExecutions failed: %v", err)
	}

	if len(gotWues) != 1 {
		t.Fatalf("ListWorkUnitExecutions(%d, %d) returned %d executions, want 1", xid, wid, len(gotWues))
	}

	wantWue := createdWu.CurrentWorkUnitExecution
	wantWue.State = essmsgpb.WorkUnitExecution_ACTIVE
	if diff := cmp.Diff(wantWue, gotWues[0], protocmp.Transform()); diff != "" {
		t.Errorf("ListWorkUnitExecutions(%d, %d) returned diff (-want +got):\n%s", xid, wid, diff)
	}

	// Now archive the first execution and create a second execution using a transaction.
	err = storage.RunInTransaction(ctx, func(txCtx context.Context, tx *corestorage.Transaction) error {
		firstExec := createdWu.CurrentWorkUnitExecution
		firstExec.State = essmsgpb.WorkUnitExecution_INACTIVE
		if err := tx.WorkUnits.SaveWorkUnitExecution(txCtx, firstExec); err != nil {
			return err
		}
		now := timestamppb.Now()
		secondExec := &essmsgpb.WorkUnitExecution{
			Name:       "experiments/123/workUnits/1/executions/2",
			State:      essmsgpb.WorkUnitExecution_ACTIVE,
			Metadata:   &essmsgpb.WorkUnitExecutionMetadata{},
			Status:     &essmsgpb.WorkUnitStatus{},
			CreateTime: now,
			UpdateTime: now,
		}
		return tx.WorkUnits.UpdateCurrentWorkUnitExecution(txCtx, xid, wid, 2, secondExec)
	})
	if err != nil {
		t.Fatalf("archiving and creating second execution failed: %v", err)
	}

	gotWues, err = storage.ListWorkUnitExecutions(ctx, xid, wid)
	if err != nil {
		t.Fatalf("ListWorkUnitExecutions after restart failed: %v", err)
	}

	if len(gotWues) != 2 {
		t.Fatalf("ListWorkUnitExecutions(%d, %d) after restart returned %d executions, want 2", xid, wid, len(gotWues))
	}

	// The first execution should be State=ACTIVE and eid=2, and second should be INACTIVE and eid=1.
	if gotWues[0].GetState() != essmsgpb.WorkUnitExecution_ACTIVE {
		t.Errorf("ListWorkUnitExecutions[0] has state %v, want ACTIVE", gotWues[0].GetState())
	}
	_, _, eid0, err := validator.ValidateWorkUnitExecutionName(gotWues[0].GetName())
	if err != nil || eid0 != 2 {
		t.Errorf("ListWorkUnitExecutions[0] has eid %d, want 2", eid0)
	}

	if gotWues[1].GetState() != essmsgpb.WorkUnitExecution_INACTIVE {
		t.Errorf("ListWorkUnitExecutions[1] has state %v, want INACTIVE", gotWues[1].GetState())
	}
	_, _, eid1, err := validator.ValidateWorkUnitExecutionName(gotWues[1].GetName())
	if err != nil || eid1 != 1 {
		t.Errorf("ListWorkUnitExecutions[1] has eid %d, want 1", eid1)
	}
}

func TestListWorkUnitExecutions_NotFound(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storage, err := spanner.NewStorage(essClient)
	if err != nil {
		t.Fatalf("spanner.NewStorage failed: %v", err)
	}

	// Create experiment.
	spanner.CreateBlankExperiment(ctx, t, storage, 123)

	testCases := []struct {
		name string
		xid  int64
		wid  int64
	}{
		{
			name: "work unit not found",
			xid:  123,
			wid:  999,
		},
		{
			name: "experiment not found",
			xid:  999,
			wid:  1,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := storage.ListWorkUnitExecutions(ctx, tc.xid, tc.wid)
			if got, want := status.Code(err), codes.NotFound; got != want {
				t.Errorf("ListWorkUnitExecutions(%d, %d) returned error %v, want %v", tc.xid, tc.wid, err, want)
			}
		})
	}
}
