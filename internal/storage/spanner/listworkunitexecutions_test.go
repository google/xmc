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
package spanner

import (
	"context"
	"testing"

	"golang/cmp/cmp"
	"golang/protobuf/v2/testing/protocmp/protocmp"
	"task/go/status"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/storage/spanner/emulator/emulator"
	"xmanager_cloud/validator/validator"
)

func TestListWorkUnitExecutions(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storage, err := NewStorage(essClient)
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}

	// Create experiment.
	createBlankExperiment(ctx, t, storage, 123)

	// Create work unit.
	createdWu := createBlankWorkUnit(ctx, t, storage, 123, 1)

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

	// Now restart work unit to create a second execution.
	_, err = storage.RestartWorkUnitExecutions(ctx, "test-restarter", xid, []int64{wid})
	if err != nil {
		t.Fatalf("RestartWorkUnitExecutions failed: %v", err)
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
	storage, err := NewStorage(essClient)
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}

	// Create experiment.
	createBlankExperiment(ctx, t, storage, 123)

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
			if got, want := status.ExtractCanonicalCode(err), status.NotFound; got != want {
				t.Errorf("ListWorkUnitExecutions(%d, %d) returned error %v, want %v", tc.xid, tc.wid, err, want)
			}
		})
	}
}
