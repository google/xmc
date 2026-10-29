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
	"math"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	essmsgpb "xmanager_cloud/api/protos/experiment"
)

var (
	testWUTime         = time.Date(2026, time.January, 3, 0, 0, 0, 0, time.UTC)
	testWUCompleteTime = time.Date(2026, time.January, 3, 1, 0, 0, 0, time.UTC)
)

func mustMarshalProto(t *testing.T, msg proto.Message) []byte {
	t.Helper()
	b, err := MarshalProto(msg)
	if err != nil {
		t.Fatalf("MarshalProto() failed: %v", err)
	}
	return b
}

func sampleWorkUnitProto() *essmsgpb.WorkUnit {
	return &essmsgpb.WorkUnit{
		Name:         "experiments/123/workUnits/456",
		WorkUnitType: essmsgpb.WorkUnit_STANDARD,
		Priority:     200,
		Notes:        "WorkUnit test notes.",
		Tags:         []string{"wu_tag1", "wu_tag2"},
		Urls:         []*essmsgpb.Url{{Url: "http://test.com", Icon: "test-icon", DisplayName: "test-display-name"}},
		Executable: &essmsgpb.Executable{
			SchedulingConstraints: &essmsgpb.SchedulingConstraints{QueueName: "test-queue"},
			Type:                  &essmsgpb.Executable_Jobset{Jobset: &essmsgpb.JobSetExecutable{Jobs: []*essmsgpb.KubernetesJob{{Name: "test-job"}}}},
		},
		CreateTime: timestamppb.New(testWUTime),
		UpdateTime: timestamppb.New(testWUTime.Add(time.Minute)),
		CurrentWorkUnitExecution: &essmsgpb.WorkUnitExecution{
			Name:  "experiments/123/workUnits/456/executions/789",
			State: essmsgpb.WorkUnitExecution_ACTIVE,
			Status: &essmsgpb.WorkUnitStatus{
				State:                 essmsgpb.WorkUnitStatus_RUNNING,
				UpdateTime:            timestamppb.New(testWUTime.Add(2 * time.Minute)),
				SystemMessage:         &essmsgpb.StructuredMessage{Severity: essmsgpb.StructuredMessage_WARNING, UserFacingText: "sys warning"},
				UserMessage:           &essmsgpb.StructuredMessage{Severity: essmsgpb.StructuredMessage_ERROR, UserFacingText: "user error"},
				SchedulingInformation: &essmsgpb.StructuredMessage{AuxiliaryInfo: &essmsgpb.AuxiliaryInfo{Detail: &essmsgpb.AuxiliaryInfo_QueueInfo{QueueInfo: &essmsgpb.QueueInfo{PositionInQueue: 42}}}},
			},
			Metadata: &essmsgpb.WorkUnitExecutionMetadata{
				ManagementCluster: &essmsgpb.ClusterInfo{Project: "p", Location: "l", ClusterName: "c"},
				ResourceName:      "test-resource-name",
			},
			CreateTime:   timestamppb.New(testWUTime.Add(3 * time.Minute)),
			UpdateTime:   timestamppb.New(testWUTime.Add(4 * time.Minute)),
			CompleteTime: timestamppb.New(testWUCompleteTime),
		},
	}
}

func TestWorkUnitRowAndCurrentExecutionRoundTrip(t *testing.T) {
	wu := sampleWorkUnitProto()
	exec := wu.GetCurrentWorkUnitExecution()
	row, err := CurrentWorkUnitExecutionProtoToRow(123, 456, 789, exec)
	if err != nil {
		t.Fatalf("CurrentWorkUnitExecutionProtoToRow() failed: %v", err)
	}
	wantRow := WorkUnitRow{
		Xid: 123, WorkUnitID: 456, ExecutionID: 789,
		ExecutionCreateTime: exec.GetCreateTime().AsTime(), ExecutionUpdateTime: exec.GetUpdateTime().AsTime(),
		ExecutionCompleteTime: &testWUCompleteTime, ExecutionMetadata: mustMarshalProto(t, exec.GetMetadata()),
		WorkUnitStatus: mustMarshalProto(t, exec.GetStatus()), ExecutionState: int64(essmsgpb.WorkUnitStatus_RUNNING),
		ExecutionStatusMessageSeverity: int64(essmsgpb.StructuredMessage_ERROR), PositionInQueue: 42,
		UpdateTime: exec.GetUpdateTime().AsTime(),
	}
	if diff := cmp.Diff(wantRow, row); diff != "" {
		t.Errorf("CurrentWorkUnitExecutionProtoToRow() mismatch (-want +got):\n%s", diff)
	}
	wantUpdateArgs := map[string]any{
		"xid": wantRow.Xid, "workunitid": wantRow.WorkUnitID, "executionid": wantRow.ExecutionID,
		"executioncreatetime": wantRow.ExecutionCreateTime, "executionupdatetime": wantRow.ExecutionUpdateTime,
		"executioncompletetime": wantRow.ExecutionCompleteTime, "executionmetadata": wantRow.ExecutionMetadata,
		"workunitstatus": wantRow.WorkUnitStatus, "executionstate": wantRow.ExecutionState,
		"executionstatusmessageseverity": wantRow.ExecutionStatusMessageSeverity,
		"positioninqueue":                wantRow.PositionInQueue, "updatetime": wantRow.UpdateTime,
	}
	if diff := cmp.Diff(wantUpdateArgs, map[string]any(row.GetCurrentExecutionUpdateArgs())); diff != "" {
		t.Errorf("GetCurrentExecutionUpdateArgs() mismatch (-want +got):\n%s", diff)
	}

	row.WorkUnitType, row.Priority, row.Tags, row.Notes = int64(wu.WorkUnitType), int64(wu.Priority), wu.Tags, wu.Notes
	row.Urls, row.Executable = [][]byte{mustMarshalProto(t, wu.Urls[0])}, mustMarshalProto(t, wu.Executable)
	row.CreateTime, row.UpdateTime = wu.CreateTime.AsTime(), wu.UpdateTime.AsTime()

	gotWU, err := WorkUnitRowToProto(row)
	if err != nil {
		t.Fatalf("WorkUnitRowToProto() failed: %v", err)
	}
	if diff := cmp.Diff(wu, gotWU, protocmp.Transform()); diff != "" {
		t.Errorf("WorkUnitRowToProto mismatch (-want +got):\n%s", diff)
	}
	gotCurExec, err := WorkUnitCurrentExecutionRowToProto(row)
	if err != nil {
		t.Fatalf("WorkUnitCurrentExecutionRowToProto() failed: %v", err)
	}
	if diff := cmp.Diff(wu.CurrentWorkUnitExecution, gotCurExec, protocmp.Transform()); diff != "" {
		t.Errorf("WorkUnitCurrentExecutionRowToProto mismatch (-want +got):\n%s", diff)
	}
}

func TestWorkUnitExecutionRoundTripAndDefaults(t *testing.T) {
	defaultRow, err := CurrentWorkUnitExecutionProtoToRow(1, 2, 3, &essmsgpb.WorkUnitExecution{
		Name:       "experiments/1/workUnits/2/executions/3",
		CreateTime: timestamppb.New(testWUTime),
		UpdateTime: timestamppb.New(testWUTime),
	})
	if err != nil {
		t.Fatalf("CurrentWorkUnitExecutionProtoToRow() failed: %v", err)
	}
	if defaultRow.PositionInQueue != math.MaxInt64 {
		t.Errorf("defaultRow.PositionInQueue = %d, want MaxInt64", defaultRow.PositionInQueue)
	}
	gotEmptyWU, err := WorkUnitRowToProto(defaultRow)
	if err != nil {
		t.Fatalf("WorkUnitRowToProto(defaultRow) failed: %v", err)
	}
	if gotEmptyWU.Tags == nil || len(gotEmptyWU.Tags) != 0 {
		t.Errorf("gotEmptyWU.Tags = %v, want non-nil empty slice", gotEmptyWU.Tags)
	}

	exec := sampleWorkUnitProto().CurrentWorkUnitExecution
	exec.State = essmsgpb.WorkUnitExecution_INACTIVE
	execRow, err := WorkUnitExecutionProtoToRow(exec)
	if err != nil {
		t.Fatalf("WorkUnitExecutionProtoToRow() failed: %v", err)
	}
	if len(execRow.GetInsertArgs()) != 8 {
		t.Errorf("len(execRow.GetInsertArgs()) = %d, want 8", len(execRow.GetInsertArgs()))
	}
	gotExec, err := WorkUnitExecutionRowToProto(execRow)
	if err != nil {
		t.Fatalf("WorkUnitExecutionRowToProto() failed: %v", err)
	}
	if diff := cmp.Diff(exec, gotExec, protocmp.Transform()); diff != "" {
		t.Errorf("WorkUnitExecution round-trip mismatch (-want +got):\n%s", diff)
	}
}

func TestWorkUnitConversionErrors(t *testing.T) {
	corrupt := []byte{0xff, 0xff, 0xff}
	if _, err := WorkUnitExecutionProtoToRow(&essmsgpb.WorkUnitExecution{Name: "invalid"}); err == nil {
		t.Error("WorkUnitExecutionProtoToRow(invalid) = nil, want error")
	}
	badWURows := []WorkUnitRow{
		{Xid: 1, WorkUnitID: 1, ExecutionID: 1, Urls: [][]byte{corrupt}},
		{Xid: 1, WorkUnitID: 1, ExecutionID: 1, Executable: corrupt},
		{Xid: 1, WorkUnitID: 1, ExecutionID: 1, ExecutionMetadata: corrupt},
		{Xid: 1, WorkUnitID: 1, ExecutionID: 1, WorkUnitStatus: corrupt},
	}
	for i, r := range badWURows {
		if _, err := WorkUnitRowToProto(r); err == nil {
			t.Errorf("WorkUnitRowToProto(badWURows[%d]) = nil, want error", i)
		}
	}
	if _, err := WorkUnitCurrentExecutionRowToProto(WorkUnitRow{Xid: 1, WorkUnitID: 1, ExecutionID: 1, WorkUnitStatus: corrupt}); err == nil {
		t.Error("WorkUnitCurrentExecutionRowToProto(corrupt) = nil, want error")
	}
}
