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
package server

import (
	"context"
	"fmt"
	"testing"

	googlespanner "golang/cloud_google_com/go/spanner/v/v1/spanner"
	"xmanager_cloud/internal/storage/spanner"

	"golang/cmp/cmp"
	"golang/protobuf/v2/testing/protocmp/protocmp"
	"task/go/status"
	"xmanager_cloud/storage/spanner/emulator/emulator"

	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
)

func TestRestartWorkUnitExecutions_HappyPath(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storageClient, err := spanner.NewStorage(essClient)
	if err != nil {
		t.Fatalf("spanner.NewStorage failed: %v", err)
	}

	handler := &WorkUnitHandler{
		baseHandler: baseHandler{
			storage: storageClient,
		},
	}

	xid := int64(123)
	wids := []int64{1, 2}
	spanner.CreateBlankExperiment(ctx, t, storageClient, xid)
	spanner.CreateBlankWorkUnitWithState(ctx, t, storageClient, xid, 1, essmsgpb.WorkUnitStatus_FAILED)
	spanner.CreateBlankWorkUnitWithState(ctx, t, storageClient, xid, 2, essmsgpb.WorkUnitStatus_STOPPED)

	initialExperiment, err := storageClient.FetchExperiment(ctx, xid)
	if err != nil {
		t.Fatalf("FetchExperiment(%d) failed: %v", xid, err)
	}
	if got, want := spanner.ChildStateCountInState(t, initialExperiment, essmsgpb.WorkUnitStatus_FAILED), int64(1); got != want {
		t.Errorf("Initial experiment state count for FAILED is %d, want %d", got, want)
	}
	if got, want := spanner.ChildStateCountInState(t, initialExperiment, essmsgpb.WorkUnitStatus_STOPPED), int64(1); got != want {
		t.Errorf("Initial experiment state count for STOPPED is %d, want %d", got, want)
	}
	if got, want := spanner.MessageSeverityCountInState(t, initialExperiment, essmsgpb.StructuredMessage_INFO), int64(2); got != want {
		t.Errorf("Initial experiment message severity count for INFO is %d, want %d", got, want)
	}

	restartedWLEs, err := handler.restartWorkUnitExecutions(ctx, "test@example.com", xid, wids)
	if err != nil {
		t.Fatalf("restartWorkUnitExecutions failed: %v", err)
	}

	if len(restartedWLEs) != 2 {
		t.Errorf("restartWorkUnitExecutions returned %d executions, want 2", len(restartedWLEs))
	}

	for _, wid := range wids {
		restartedWLE := restartedWLEs[wid]
		if restartedWLE == nil {
			t.Errorf("restartWorkUnitExecutions returned nil execution for wid %d", wid)
			continue
		}
		expectedName := fmt.Sprintf("experiments/123/workUnits/%d/executions/2", wid)
		if restartedWLE.GetName() != expectedName {
			t.Errorf("restartWorkUnitExecutions returned execution with name %q, want %q for wid %d", restartedWLE.GetName(), expectedName, wid)
		}
		if want := essmsgpb.WorkUnitStatus_RESTARTING; restartedWLE.GetStatus().GetState() != want {
			t.Errorf("restartWorkUnitExecutions returned execution with state %v, want %v for wid %d", restartedWLE.GetStatus().GetState(), want, wid)
		}

		// Verify work unit has new execution as current.
		wu, err := storageClient.FetchWorkUnit(ctx, xid, wid)
		if err != nil {
			t.Fatalf("FetchWorkUnit(%d, %d) failed: %v", xid, wid, err)
		}
		if diff := cmp.Diff(restartedWLE, wu.GetCurrentWorkUnitExecution(), protocmp.Transform()); diff != "" {
			t.Errorf("FetchWorkUnit(%d).GetCurrentWorkUnitExecution() mismatch (-want +got):\n%s", wid, diff)
		}
	}

	// Verify experiment status counts.
	exp, err := storageClient.FetchExperiment(ctx, xid)
	if err != nil {
		t.Fatalf("FetchExperiment(%d) failed: %v", xid, err)
	}
	if got, want := spanner.ChildStateCountInState(t, exp, essmsgpb.WorkUnitStatus_FAILED), int64(0); got != want {
		t.Errorf("Experiment state count for FAILED is %d, want %d", got, want)
	}
	if got, want := spanner.ChildStateCountInState(t, exp, essmsgpb.WorkUnitStatus_STOPPED), int64(0); got != want {
		t.Errorf("Experiment state count for STOPPED is %d, want %d", got, want)
	}
	if got, want := spanner.ChildStateCountInState(t, exp, essmsgpb.WorkUnitStatus_RESTARTING), int64(2); got != want {
		t.Errorf("Experiment state count for RESTARTING is %d, want %d", got, want)
	}
	if got, want := spanner.MessageSeverityCountInState(t, exp, essmsgpb.StructuredMessage_INFO), int64(2); got != want {
		t.Errorf("Experiment message severity count for INFO is %d, want %d", got, want)
	}

	// Verify WorkUnitStatusHistory records were added for restarted executions.
	stmt := googlespanner.Statement{
		SQL:    "SELECT COUNT(1) FROM WorkUnitStatusHistory WHERE Xid = @xid",
		Params: map[string]any{"xid": xid},
	}
	iter := essClient.Single().Query(ctx, stmt)
	defer iter.Stop()
	row, err := iter.Next()
	if err != nil {
		t.Fatalf("failed to query WorkUnitStatusHistory: %v", err)
	}
	var historyCount int64
	if err := row.Column(0, &historyCount); err != nil {
		t.Fatalf("failed to parse WorkUnitStatusHistory count: %v", err)
	}
	if historyCount == 0 {
		t.Errorf("expected WorkUnitStatusHistory records to be added for restarted work unit executions, got 0")
	}
}

func TestRestartWorkUnitExecutions_WorkUnitNotFound(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storageClient, err := spanner.NewStorage(essClient)
	if err != nil {
		t.Fatalf("spanner.NewStorage failed: %v", err)
	}

	handler := &WorkUnitHandler{
		baseHandler: baseHandler{
			storage: storageClient,
		},
	}

	xid := int64(456)
	wids := []int64{1}
	spanner.CreateBlankExperiment(ctx, t, storageClient, xid)

	_, err = handler.restartWorkUnitExecutions(ctx, "test@example.com", xid, wids)
	if status.ExtractCanonicalCode(err) != status.Internal {
		t.Errorf("restartWorkUnitExecutions for non-existent work unit returned error %v, want Internal", err)
	}
}
