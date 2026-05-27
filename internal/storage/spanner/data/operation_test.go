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
package spannerdata

import (
	"testing"
	"time"

	lropb "longrunning/operations_go_proto"
	anypb "protobuf/any_go_proto"
	statuspb "rpc/status_go_proto"
	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	"golang/cmp/cmp"
	"golang/protobuf/v2/proto/proto"
	"golang/protobuf/v2/testing/protocmp/protocmp"
	pb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/storage/data/data"
	"xmanager_cloud/storage/spanner/schema/operationschema"
)

var (
	testOperationTime = time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC)
)

func mustMarshalAny(t *testing.T, pb proto.Message) *anypb.Any {
	t.Helper()
	any, err := anypb.New(pb)
	if err != nil {
		t.Fatalf("Failed to marshal proto to Any: %v", err)
	}
	return any
}

func TestOperationRowToProto(t *testing.T) {
	response := &pb.WorkUnit{Name: "experiments/1/workUnits/1"}
	responseBytes, err := proto.Marshal(response)
	if err != nil {
		t.Fatalf("Failed to marshal response: %v", err)
	}

	keys := []string{"Xid", "WorkUnitId", "OperationId", "Done", "Status", "Response", "OperationMetadata"}
	var status *statuspb.Status
	values := []any{
		int64(1),
		int64(1),
		"abc",
		true,
		spanner.NullProtoMessage{Valid: true, ProtoMessageVal: status},
		responseBytes,
		&pb.OperationMetadata{ActionType: pb.OperationMetadata_CREATE},
	}

	row, err := spanner.NewRow(keys, values)
	if err != nil {
		t.Fatalf("Failed to create Spanner row: %v", err)
	}

	got, err := OperationRowToProto(row)
	if err != nil {
		t.Fatalf("Failed to convert Spanner row to Operation proto: %v", err)
	}

	want := &lropb.Operation{
		Name: "experiments/1/workUnits/1/operations/abc",
		Done: true,
		Metadata: mustMarshalAny(t, &pb.OperationMetadata{
			ActionType: pb.OperationMetadata_CREATE,
		}),
		Result: &lropb.Operation_Response{Response: mustMarshalAny(t, response)},
	}
	if diff := cmp.Diff(want, got, protocmp.Transform()); diff != "" {
		t.Errorf("OperationRowToProto() returned unexpected diff (-want +got):\n%s", diff)
	}
}

func TestOperationRowToProto_MissingColumn(t *testing.T) {
	keys := []string{"Xid"}
	values := []any{int64(1)}
	row, err := spanner.NewRow(keys, values)
	if err != nil {
		t.Fatalf("spanner.NewRow(%v, %v) failed: %v", keys, values, err)
	}

	if _, err := OperationRowToProto(row); err == nil {
		t.Errorf("OperationRowToProto() with missing columns succeeded, want error")
	}
}

func TestOperationProtoToInsertMutation(t *testing.T) {
	op := &lropb.Operation{
		Name: "experiments/1/workUnits/1/operations/abc",
		Done: true,
		Metadata: mustMarshalAny(t, &pb.OperationMetadata{
			ActionType: pb.OperationMetadata_CREATE,
		}),
		Result: &lropb.Operation_Error{Error: &statuspb.Status{Code: 1, Message: "error"}},
	}
	want := spanner.Insert(
		operationschema.OperationsTable,
		operationschema.OperationColumns,
		[]any{
			int64(1),
			int64(1),
			"abc",
			true,
			op.GetError(),
			(*pb.WorkUnit)(nil),
			&pb.OperationMetadata{ActionType: pb.OperationMetadata_CREATE},
			testOperationTime,
			testOperationTime,
		},
	)

	got, err := OperationProtoToInsertMutation(op, testOperationTime)
	if err != nil {
		t.Fatalf("OperationProtoToInsertMutation() returned unexpected error: %v", err)
	}

	if diff := cmp.Diff(want, got, protocmp.Transform(), cmp.AllowUnexported(spanner.Mutation{})); diff != "" {
		t.Errorf("OperationProtoToInsertMutation() returned unexpected diff (-want +got):\n%s", diff)
	}
}

func TestOperationProtoToUpdateMutation(t *testing.T) {
	op := &lropb.Operation{
		Name: "experiments/1/workUnits/1/operations/abc",
		Done: true,
		Metadata: mustMarshalAny(t, &pb.OperationMetadata{
			ActionType: pb.OperationMetadata_CREATE,
		}),
		Result: &lropb.Operation_Error{Error: &statuspb.Status{Code: 1, Message: "error"}},
	}
	want := spanner.Update(
		operationschema.OperationsTable,
		operationschema.OperationUpdateColumns,
		[]any{
			data.ReverseBits(1),
			int64(1),
			"abc",
			true,
			op.GetError(),
			(*pb.WorkUnit)(nil),
			&pb.OperationMetadata{ActionType: pb.OperationMetadata_CREATE},
			testOperationTime,
		},
	)

	got, err := OperationProtoToUpdateMutation(op, testOperationTime)
	if err != nil {
		t.Fatalf("OperationProtoToUpdateMutation() returned unexpected error: %v", err)
	}

	if diff := cmp.Diff(want, got, protocmp.Transform(), cmp.AllowUnexported(spanner.Mutation{})); diff != "" {
		t.Errorf("OperationProtoToUpdateMutation() returned unexpected diff (-want +got):\n%s", diff)
	}
}
