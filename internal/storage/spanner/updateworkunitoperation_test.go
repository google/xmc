// Copyright 2025 Google LLC
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

	lropb "longrunning/operations_go_proto"
	anypb "protobuf/any_go_proto"
	fmpb "protobuf/field_mask_go_proto"
	statuspb "rpc/status_go_proto"
	"golang/cmp/cmp"
	"golang/protobuf/v2/testing/protocmp/protocmp"
	pb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/storage/spanner/emulator/emulator"
	"xmanager_cloud/storage/storage"
	"xmanager_cloud/validator/validator"
)

func TestUpdateWorkUnitOperation(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	s, err := NewStorage(essClient)
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}

	// 1. Create an Experiment and WorkUnit.
	xid := int64(123)
	wid := int64(1)
	createBlankExperiment(ctx, t, s, xid)
	createBlankWorkUnit(ctx, t, s, xid, wid)

	// 2. Add an operation.
	metadata := &pb.OperationMetadata{ActionType: pb.OperationMetadata_CREATE}
	op, err := s.AddWorkUnitOperation(ctx, xid, wid, metadata)
	if err != nil {
		t.Fatalf("AddWorkUnitOperation failed: %v", err)
	}

	// 3. Update the operation.
	response, err := anypb.New(&pb.WorkUnit{Name: "experiments/1/workUnits/1"})
	if err != nil {
		t.Fatalf("Failed to marshal response: %v", err)
	}

	xid, wid, oid, err := validator.ValidateOperationName(op.GetName())
	if err != nil {
		t.Fatalf("Failed to parse operation name: %v", err)
	}

	update := &storage.OperationUpdate{
		XID: xid,
		WID: wid,
		OID: oid,
		Operation: &lropb.Operation{
			Done:   true,
			Result: &lropb.Operation_Response{Response: response},
		},
		UpdateMask: &fmpb.FieldMask{Paths: []string{"done", "response"}},
	}
	updatedOp, err := s.UpdateWorkUnitOperation(ctx, update)
	if err != nil {
		t.Fatalf("UpdateWorkUnitOperation failed: %v", err)
	}

	// 4. Verify the update.
	if !updatedOp.Done {
		t.Errorf("UpdateWorkUnitOperation returned operation with Done=false, want true")
	}
	if updatedOp.GetError() != nil {
		t.Errorf("UpdateWorkUnitOperation returned operation with error %v, want nil", updatedOp.GetError())
	}
	if diff := cmp.Diff(response, updatedOp.GetResponse(), protocmp.Transform()); diff != "" {
		t.Errorf("UpdateWorkUnitOperation returned operation with unexpected response: diff (-want +got):\n%s", diff)
	}

	// 5. Verify with FetchWorkUnitOperation.
	gotOp, err := s.FetchWorkUnitOperation(ctx, xid, wid, oid)
	if err != nil {
		t.Fatalf("FetchWorkUnitOperation failed: %v", err)
	}
	if diff := cmp.Diff(updatedOp, gotOp, protocmp.Transform()); diff != "" {
		t.Errorf("FetchWorkUnitOperation returned diff after update (-want +got):\n%s", diff)
	}
}

func TestUpdateWorkUnitOperation_Error(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	s, err := NewStorage(essClient)
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}

	// 1. Create an Experiment and WorkUnit.
	xid := int64(123)
	wid := int64(1)
	createBlankExperiment(ctx, t, s, xid)
	createBlankWorkUnit(ctx, t, s, xid, wid)

	// 2. Add an operation.
	metadata := &pb.OperationMetadata{ActionType: pb.OperationMetadata_CREATE}
	op, err := s.AddWorkUnitOperation(ctx, xid, wid, metadata)
	if err != nil {
		t.Fatalf("AddWorkUnitOperation failed: %v", err)
	}

	// 3. Update the operation with an error.
	xid, wid, oid, err := validator.ValidateOperationName(op.GetName())
	if err != nil {
		t.Fatalf("Failed to parse operation name: %v", err)
	}

	errorStatus := &statuspb.Status{Code: 1, Message: "error"}
	update := &storage.OperationUpdate{
		XID: xid,
		WID: wid,
		OID: oid,
		Operation: &lropb.Operation{
			Done:   true,
			Result: &lropb.Operation_Error{Error: errorStatus},
		},
		UpdateMask: &fmpb.FieldMask{Paths: []string{"done", "error"}},
	}
	updatedOp, err := s.UpdateWorkUnitOperation(ctx, update)
	if err != nil {
		t.Fatalf("UpdateWorkUnitOperation failed: %v", err)
	}

	// 4. Verify the update.
	if !updatedOp.Done {
		t.Errorf("UpdateWorkUnitOperation returned operation with Done=false, want true")
	}
	if updatedOp.GetResponse() != nil {
		t.Errorf("UpdateWorkUnitOperation returned operation with response %v, want nil", updatedOp.GetResponse())
	}
	if diff := cmp.Diff(errorStatus, updatedOp.GetError(), protocmp.Transform()); diff != "" {
		t.Errorf("UpdateWorkUnitOperation returned operation with unexpected error: diff (-want +got):\n%s", diff)
	}
}
