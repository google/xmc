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
package spannerdata

import (
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
	timestamppb "protobuf/timestamp_go_proto"
	pb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/storage/data/data"
)

var (
	testWorkUnitExecution = pb.WorkUnitExecution{
		Name:  "experiments/123/workUnits/456/executions/789",
		State: pb.WorkUnitExecution_INACTIVE,
		Status: &pb.WorkUnitStatus{
			State: pb.WorkUnitStatus_COMPLETED,
		},
		Metadata: &pb.WorkUnitExecutionMetadata{
			ManagementCluster: &pb.ClusterInfo{
				Project:     "test-project",
				Location:    "test-location",
				ClusterName: "test-cluster",
			},
			ResourceName: "test-resource-name",
		},
	}
	testWorkUnitExecutionTime         = time.Date(2024, time.January, 4, 0, 0, 0, 0, time.UTC)
	testWorkUnitExecutionCompleteTime = time.Date(2024, time.January, 4, 1, 0, 0, 0, time.UTC)
	fullTestWorkUnitExecution         = pb.WorkUnitExecution{
		Name:         testWorkUnitExecution.Name,
		State:        testWorkUnitExecution.State,
		Status:       testWorkUnitExecution.Status,
		Metadata:     testWorkUnitExecution.Metadata,
		CreateTime:   timestamppb.New(testWorkUnitExecutionTime),
		UpdateTime:   timestamppb.New(testWorkUnitExecutionTime),
		CompleteTime: timestamppb.New(testWorkUnitExecutionCompleteTime),
	}
)

func TestWorkUnitExecutionProtoToInsertMutation(t *testing.T) {
	xid := int64(123)
	wid := int64(456)
	eid := int64(789)
	exec := &fullTestWorkUnitExecution
	want := spanner.Insert(
		"WorkUnitExecutions",
		[]string{
			"Xid",
			"WorkUnitId",
			"ExecutionId",
			"ExecutionMetadata",
			"WorkUnitStatus",
			"CreateTime",
			"UpdateTime",
			"CompleteTime",
		},
		[]any{
			xid,
			wid,
			eid,
			exec.Metadata,
			exec.Status,
			exec.GetCreateTime().AsTime(),
			exec.GetUpdateTime().AsTime(),
			spanner.NullTime{Time: exec.GetCompleteTime().AsTime(), Valid: true},
		},
	)
	got, err := WorkUnitExecutionProtoToInsertMutation(exec)
	if err != nil {
		t.Fatalf("WorkUnitExecutionProtoToInsertMutation() returned unexpected error: %v", err)
	}
	if diff := cmp.Diff(want, got, protocmp.Transform(), cmp.AllowUnexported(spanner.Mutation{})); diff != "" {
		t.Errorf("WorkUnitExecutionProtoToInsertMutation() returned unexpected diff (-want +got):\n%s", diff)
	}
}

func TestWorkUnitExecutionRowToProto(t *testing.T) {
	keys := []string{"Xid", "WorkUnitId", "ExecutionId", "ExecutionMetadata", "WorkUnitStatus", "CreateTime", "UpdateTime", "CompleteTime"}
	values := []any{
		int64(123),
		int64(456),
		int64(789),
		fullTestWorkUnitExecution.Metadata,
		fullTestWorkUnitExecution.Status,
		fullTestWorkUnitExecution.CreateTime.AsTime(),
		fullTestWorkUnitExecution.UpdateTime.AsTime(),
		spanner.NullTime{Time: fullTestWorkUnitExecution.CompleteTime.AsTime(), Valid: true},
	}
	row, err := spanner.NewRow(keys, values)
	if err != nil {
		t.Fatalf("Failed to create Spanner row: %v", err)
	}

	got, err := WorkUnitExecutionRowToProto(row)
	if err != nil {
		t.Fatalf("WorkUnitExecutionRowToProto() failed: %v", err)
	}

	if diff := cmp.Diff(&fullTestWorkUnitExecution, got, protocmp.Transform()); diff != "" {
		t.Errorf("WorkUnitExecutionRowToProto() returned unexpected diff (-want +got):\n%s", diff)
	}
}

func TestWorkUnitExecutionRowToProto_MissingColumn(t *testing.T) {
	keys := []string{"Xid"}
	values := []any{int64(123)}
	row, err := spanner.NewRow(keys, values)
	if err != nil {
		t.Fatalf("spanner.NewRow(%v, %v) failed: %v", keys, values, err)
	}

	if _, err := WorkUnitExecutionRowToProto(row); err == nil {
		t.Errorf("WorkUnitExecutionRowToProto() with missing columns succeeded, want error")
	}
}

func TestWorkUnitCurrentExecutionRowToProto(t *testing.T) {
	keys := []string{"Xid", "WorkUnitId", "ExecutionId", "ExecutionCreateTime", "ExecutionMetadata", "WorkUnitStatus"}
	status := proto.Clone(testWorkUnitExecution.Status).(*pb.WorkUnitStatus)
	status.UpdateTime = timestamppb.New(testWorkUnitExecutionTime)
	values := []any{
		int64(123),
		int64(456),
		int64(789),
		testWorkUnitExecutionTime,
		testWorkUnitExecution.Metadata,
		status,
	}

	row, err := spanner.NewRow(keys, values)
	if err != nil {
		t.Fatalf("Failed to create Spanner row: %v", err)
	}

	got, err := WorkUnitCurrentExecutionRowToProto(row)
	if err != nil {
		t.Fatalf("Failed to convert Spanner row to WorkUnitExecution proto: %v", err)
	}

	// WorkUnitCurrentExecutionRowToProto always sets state to ACTIVE.
	want := &pb.WorkUnitExecution{
		Name:       "experiments/123/workUnits/456/executions/789",
		State:      pb.WorkUnitExecution_ACTIVE,
		Status:     status,
		Metadata:   testWorkUnitExecution.Metadata,
		CreateTime: timestamppb.New(testWorkUnitExecutionTime),
		UpdateTime: status.UpdateTime,
	}

	if diff := cmp.Diff(want, got, protocmp.Transform()); diff != "" {
		t.Errorf("WorkUnitCurrentExecutionRowToProto() returned unexpected diff (-want +got):\n%s", diff)
	}
}

func TestWorkUnitCurrentExecutionRowToProto_MissingColumn(t *testing.T) {
	keys := []string{"Xid"}
	values := []any{int64(123)}
	row, err := spanner.NewRow(keys, values)
	if err != nil {
		t.Fatalf("spanner.NewRow(%v, %v) failed: %v", keys, values, err)
	}

	if _, err := WorkUnitCurrentExecutionRowToProto(row); err == nil {
		t.Errorf("WorkUnitCurrentExecutionRowToProto() with missing columns succeeded, want error")
	}
}

func TestCurrentWorkUnitExecutionProtoToUpdateMutation(t *testing.T) {
	xid := int64(123)
	wid := int64(456)
	eid := int64(789)
	tm := time.Now()
	exec := &pb.WorkUnitExecution{
		CreateTime: timestamppb.New(tm),
		UpdateTime: timestamppb.New(tm),
		Metadata: &pb.WorkUnitExecutionMetadata{
			ManagementCluster: &pb.ClusterInfo{
				Project: "test-project",
			},
		},
		Status: &pb.WorkUnitStatus{
			State: pb.WorkUnitStatus_RUNNING,
		},
	}
	want := spanner.Update(
		"WorkUnits",
		[]string{"BitReversedXid", "WorkUnitId", "ExecutionId", "ExecutionCreateTime", "ExecutionUpdateTime", "ExecutionCompleteTime", "ExecutionMetadata", "WorkUnitStatus", "UpdateTime"},
		[]any{
			data.ReverseBits(xid),
			wid,
			eid,
			exec.CreateTime.AsTime(),
			exec.UpdateTime.AsTime(),
			spanner.NullTime{Valid: false},
			exec.Metadata,
			exec.Status,
			exec.UpdateTime.AsTime(),
		},
	)
	got := CurrentWorkUnitExecutionProtoToUpdateMutation(xid, wid, eid, exec)
	if diff := cmp.Diff(want, got, protocmp.Transform(), cmp.AllowUnexported(spanner.Mutation{})); diff != "" {
		t.Errorf("CurrentWorkUnitExecutionProtoToMutation() returned unexpected diff (-want +got):\n%s", diff)
	}
}
