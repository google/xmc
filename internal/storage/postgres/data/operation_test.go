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

package postgresdata

import (
	"testing"
	"time"

	longrunningpb "cloud.google.com/go/longrunning/autogen/longrunningpb"
	"github.com/google/go-cmp/cmp"
	statuspb "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
	anypb "google.golang.org/protobuf/types/known/anypb"
	essmsgpb "xmanager_cloud/api/protos/experiment"
)

var testOperationTime = time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)

func mustMarshalAny(t *testing.T, msg proto.Message) *anypb.Any {
	t.Helper()
	a, err := anypb.New(msg)
	if err != nil {
		t.Fatalf("anypb.New() failed: %v", err)
	}
	return a
}

func TestOperationProtoAndRowRoundTrip(t *testing.T) {
	baseMetadata := &essmsgpb.OperationMetadata{
		ActionType: essmsgpb.OperationMetadata_CREATE,
		Target:     "experiments/1/workUnits/2",
	}
	responseWorkUnit := &essmsgpb.WorkUnit{Name: "experiments/1/workUnits/2", Notes: "ok"}
	errStatus := &statuspb.Status{Code: 3, Message: "invalid"}

	tests := []struct {
		name string
		op   *longrunningpb.Operation
	}{
		{
			name: "pending operation",
			op: &longrunningpb.Operation{
				Name:     "experiments/1/workUnits/2/operations/op-1",
				Done:     false,
				Metadata: mustMarshalAny(t, baseMetadata),
			},
		},
		{
			name: "completed operation with WorkUnit response",
			op: &longrunningpb.Operation{
				Name:     "experiments/1/workUnits/2/operations/op-2",
				Done:     true,
				Metadata: mustMarshalAny(t, baseMetadata),
				Result:   &longrunningpb.Operation_Response{Response: mustMarshalAny(t, responseWorkUnit)},
			},
		},
		{
			name: "failed operation with error status",
			op: &longrunningpb.Operation{
				Name:     "experiments/1/workUnits/2/operations/op-3",
				Done:     true,
				Metadata: mustMarshalAny(t, baseMetadata),
				Result:   &longrunningpb.Operation_Error{Error: errStatus},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			row, err := OperationProtoToRow(tc.op, testOperationTime)
			if err != nil {
				t.Fatalf("OperationProtoToRow() failed: %v", err)
			}
			if !row.CreateTime.Equal(testOperationTime) || !row.UpdateTime.Equal(testOperationTime) {
				t.Errorf("row timestamps = (%v, %v), want %v", row.CreateTime, row.UpdateTime, testOperationTime)
			}
			insertArgs := row.InsertArgs()
			if len(insertArgs) != 9 || insertArgs["createtime"] != testOperationTime {
				t.Errorf("row.InsertArgs() = %v, want 9 entries with matching createtime", insertArgs)
			}
			updateArgs := row.UpdateArgs()
			if len(updateArgs) != 8 || updateArgs["operationid"] != row.OperationID {
				t.Errorf("row.UpdateArgs() = %v, want 8 entries with matching operationid", updateArgs)
			}
			got, err := OperationRowToProto(row)
			if err != nil {
				t.Fatalf("OperationRowToProto() failed: %v", err)
			}
			if diff := cmp.Diff(tc.op, got, protocmp.Transform()); diff != "" {
				t.Errorf("round-trip Operation mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestOperationConversionErrors(t *testing.T) {
	corrupt := []byte{0xff, 0xff, 0xff}
	wrongAny := mustMarshalAny(t, &statuspb.Status{Code: 1})

	badRows := []OperationRow{
		{Xid: 1, WorkUnitID: 1, OperationID: "id", Status: []byte{}, Response: []byte{}},
		{Xid: 1, WorkUnitID: 1, OperationID: "id", OperationMetadata: corrupt},
		{Xid: 1, WorkUnitID: 1, OperationID: "id", Status: corrupt},
		{Xid: 1, WorkUnitID: 1, OperationID: "id", Response: corrupt},
	}
	for i, r := range badRows {
		if _, err := OperationRowToProto(r); err == nil {
			t.Errorf("OperationRowToProto(badRows[%d]) = nil, want error", i)
		}
	}

	badOps := []*longrunningpb.Operation{
		{Name: "invalid"},
		{Name: "experiments/1/workUnits/1/operations/op-1", Metadata: wrongAny},
		{Name: "experiments/1/workUnits/1/operations/op-1", Result: &longrunningpb.Operation_Response{Response: wrongAny}},
	}
	for i, op := range badOps {
		if _, err := OperationProtoToRow(op, testOperationTime); err == nil {
			t.Errorf("OperationProtoToRow(badOps[%d]) = nil, want error", i)
		}
	}
}
