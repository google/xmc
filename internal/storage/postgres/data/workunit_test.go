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

func TestWorkUnitProtoAndRowRoundTrip(t *testing.T) {
	wu := sampleWorkUnitProto()
	exec := wu.GetCurrentWorkUnitExecution()
	execRow, err := CurrentWorkUnitExecutionProtoToRow(123, 456, 789, exec)
	if err != nil {
		t.Fatalf("CurrentWorkUnitExecutionProtoToRow() failed: %v", err)
	}
	wantExecRow := WorkUnitRow{
		Xid: 123, WorkUnitID: 456, ExecutionID: 789,
		ExecutionCreateTime: exec.GetCreateTime().AsTime(), ExecutionUpdateTime: exec.GetUpdateTime().AsTime(),
		ExecutionCompleteTime: &testWUCompleteTime, ExecutionMetadata: mustMarshalProto(t, exec.GetMetadata()),
		WorkUnitStatus: mustMarshalProto(t, exec.GetStatus()), ExecutionState: int64(essmsgpb.WorkUnitStatus_RUNNING),
		ExecutionStatusMessageSeverity: int64(essmsgpb.StructuredMessage_ERROR), PositionInQueue: 42,
		UpdateTime: exec.GetUpdateTime().AsTime(),
	}
	if diff := cmp.Diff(wantExecRow, execRow); diff != "" {
		t.Errorf("CurrentWorkUnitExecutionProtoToRow() mismatch (-want +got):\n%s", diff)
	}
	wantUpdateArgs := map[string]any{
		"xid": wantExecRow.Xid, "workunitid": wantExecRow.WorkUnitID, "executionid": wantExecRow.ExecutionID,
		"executioncreatetime": wantExecRow.ExecutionCreateTime, "executionupdatetime": wantExecRow.ExecutionUpdateTime,
		"executioncompletetime": wantExecRow.ExecutionCompleteTime, "executionmetadata": wantExecRow.ExecutionMetadata,
		"workunitstatus": wantExecRow.WorkUnitStatus, "executionstate": wantExecRow.ExecutionState,
		"executionstatusmessageseverity": wantExecRow.ExecutionStatusMessageSeverity,
		"positioninqueue":                wantExecRow.PositionInQueue, "updatetime": wantExecRow.UpdateTime,
	}
	if diff := cmp.Diff(wantUpdateArgs, map[string]any(execRow.GetCurrentExecutionUpdateArgs())); diff != "" {
		t.Errorf("GetCurrentExecutionUpdateArgs() mismatch (-want +got):\n%s", diff)
	}

	row, err := WorkUnitProtoToRow(wu)
	if err != nil {
		t.Fatalf("WorkUnitProtoToRow() failed: %v", err)
	}
	wantInsertArgs := map[string]any{
		"xid": int64(123), "workunitid": int64(456), "workunittype": int64(essmsgpb.WorkUnit_STANDARD), "priority": int64(200),
		"urls": [][]byte{mustMarshalProto(t, wu.GetUrls()[0])}, "tags": []string{"wu_tag1", "wu_tag2"}, "notes": "WorkUnit test notes.",
		"executable":          mustMarshalProto(t, wu.GetExecutable()),
		"executioncreatetime": testWUTime.Add(3 * time.Minute), "executionupdatetime": testWUTime.Add(4 * time.Minute),
		"executioncompletetime": &testWUCompleteTime, "executionid": int64(789),
		"executionmetadata": mustMarshalProto(t, exec.GetMetadata()), "workunitstatus": mustMarshalProto(t, exec.GetStatus()),
		"queuename": "test-queue", "executionstate": int64(essmsgpb.WorkUnitStatus_RUNNING),
		"executionstatusmessageseverity": int64(essmsgpb.StructuredMessage_ERROR), "positioninqueue": int64(42),
		"createtime": testWUTime, "updatetime": testWUTime.Add(time.Minute),
	}
	if diff := cmp.Diff(wantInsertArgs, map[string]any(row.GetInsertArgs())); diff != "" {
		t.Errorf("GetInsertArgs() mismatch (-want +got):\n%s", diff)
	}

	gotWU, err := WorkUnitRowToProto(row)
	if err != nil {
		t.Fatalf("WorkUnitRowToProto() failed: %v", err)
	}
	if diff := cmp.Diff(wu, gotWU, protocmp.Transform()); diff != "" {
		t.Errorf("WorkUnit round-trip mismatch (-want +got):\n%s", diff)
	}
	gotCurExec, err := WorkUnitCurrentExecutionRowToProto(row)
	if err != nil {
		t.Fatalf("WorkUnitCurrentExecutionRowToProto() failed: %v", err)
	}
	if diff := cmp.Diff(wu.CurrentWorkUnitExecution, gotCurExec, protocmp.Transform()); diff != "" {
		t.Errorf("WorkUnitCurrentExecutionRowToProto mismatch (-want +got):\n%s", diff)
	}

	gotUpdating, err := UpdatingWorkUnitRowToProto(row)
	if err != nil {
		t.Fatalf("UpdatingWorkUnitRowToProto() failed: %v", err)
	}
	wantUpdatingExec := sampleWorkUnitProto().CurrentWorkUnitExecution
	wantUpdatingExec.State = essmsgpb.WorkUnitExecution_INACTIVE
	wantUpdating := &essmsgpb.WorkUnit{
		Name:                     wu.GetName(),
		Executable:               wu.GetExecutable(),
		CurrentWorkUnitExecution: wantUpdatingExec,
	}
	if diff := cmp.Diff(wantUpdating, gotUpdating, protocmp.Transform()); diff != "" {
		t.Errorf("UpdatingWorkUnitRowToProto() mismatch (-want +got):\n%s", diff)
	}
}

func TestWorkUnitUpdateAndExecutionRoundTrip(t *testing.T) {
	wu := &essmsgpb.WorkUnit{
		Name:       "experiments/123/workUnits/456",
		Priority:   10,
		Notes:      "updated",
		UpdateTime: timestamppb.New(testWUTime),
		CurrentWorkUnitExecution: &essmsgpb.WorkUnitExecution{
			Name:       "experiments/123/workUnits/456/executions/1",
			CreateTime: timestamppb.New(testWUTime),
			UpdateTime: timestamppb.New(testWUTime),
		},
	}
	defaultRow, err := WorkUnitProtoToRow(wu)
	if err != nil {
		t.Fatalf("WorkUnitProtoToRow() failed: %v", err)
	}
	if defaultRow.PositionInQueue != math.MaxInt64 || len(defaultRow.Tags) != 0 || defaultRow.Tags == nil {
		t.Errorf("defaultRow = (pos=%d, tags=%v), want MaxInt64 and non-nil empty slice", defaultRow.PositionInQueue, defaultRow.Tags)
	}
	wu.CurrentWorkUnitExecution.Status = &essmsgpb.WorkUnitStatus{
		SchedulingInformation: &essmsgpb.StructuredMessage{
			AuxiliaryInfo: &essmsgpb.AuxiliaryInfo{
				Detail: &essmsgpb.AuxiliaryInfo_QueueInfo{QueueInfo: &essmsgpb.QueueInfo{PositionInQueue: 0}},
			},
		},
	}
	zeroQueuePosRow, err := WorkUnitProtoToRow(wu)
	if err != nil {
		t.Fatalf("WorkUnitProtoToRow(PositionInQueue=0) failed: %v", err)
	}
	if zeroQueuePosRow.PositionInQueue != math.MaxInt64 {
		t.Errorf("zeroQueuePosRow.PositionInQueue = %d, want MaxInt64 (%d)", zeroQueuePosRow.PositionInQueue, int64(math.MaxInt64))
	}
	defaultRow.Tags = nil
	gotEmptyWU, err := WorkUnitRowToProto(defaultRow)
	if err != nil {
		t.Fatalf("WorkUnitRowToProto(defaultRow) failed: %v", err)
	}
	if gotEmptyWU.Tags == nil || len(gotEmptyWU.Tags) != 0 {
		t.Errorf("gotEmptyWU.Tags = %v, want non-nil empty slice", gotEmptyWU.Tags)
	}

	updateRow, err := WorkUnitProtoToUpdateRow(wu)
	if err != nil {
		t.Fatalf("WorkUnitProtoToUpdateRow() failed: %v", err)
	}
	wantUpdateRow := WorkUnitRow{
		Xid: 123, WorkUnitID: 456, Priority: 10,
		Urls: [][]byte{}, Tags: []string{}, Notes: "updated", UpdateTime: testWUTime,
	}
	if diff := cmp.Diff(wantUpdateRow, updateRow); diff != "" {
		t.Errorf("WorkUnitProtoToUpdateRow() mismatch (-want +got):\n%s", diff)
	}
	wantMetaUpdateArgs := map[string]any{
		"xid": int64(123), "workunitid": int64(456), "priority": int64(10),
		"urls": [][]byte{}, "tags": []string{}, "notes": "updated", "updatetime": testWUTime,
	}
	if diff := cmp.Diff(wantMetaUpdateArgs, map[string]any(updateRow.GetUpdateArgs())); diff != "" {
		t.Errorf("GetUpdateArgs() mismatch (-want +got):\n%s", diff)
	}

	exec := sampleWorkUnitProto().CurrentWorkUnitExecution
	exec.State = essmsgpb.WorkUnitExecution_INACTIVE
	execRow, err := WorkUnitExecutionProtoToRow(exec)
	if err != nil {
		t.Fatalf("WorkUnitExecutionProtoToRow() failed: %v", err)
	}
	wantInsertArgs := map[string]any{
		"xid": int64(123), "workunitid": int64(456), "executionid": int64(789),
		"executionmetadata": mustMarshalProto(t, exec.GetMetadata()), "workunitstatus": mustMarshalProto(t, exec.GetStatus()),
		"createtime": exec.GetCreateTime().AsTime(), "updatetime": exec.GetUpdateTime().AsTime(), "completetime": &testWUCompleteTime,
	}
	if diff := cmp.Diff(wantInsertArgs, map[string]any(execRow.GetInsertArgs())); diff != "" {
		t.Errorf("GetInsertArgs() mismatch (-want +got):\n%s", diff)
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
	if _, err := WorkUnitProtoToRow(&essmsgpb.WorkUnit{}); err == nil {
		t.Error("WorkUnitProtoToRow(invalid) = nil, want error")
	}
	if _, err := WorkUnitProtoToUpdateRow(&essmsgpb.WorkUnit{Name: "invalid"}); err == nil {
		t.Error("WorkUnitProtoToUpdateRow(invalid) = nil, want error")
	}
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
	for i, r := range []WorkUnitRow{
		{Xid: 1, WorkUnitID: 1, ExecutionID: 1, Executable: corrupt},
		{Xid: 1, WorkUnitID: 1, ExecutionID: 1, WorkUnitStatus: corrupt},
	} {
		if _, err := UpdatingWorkUnitRowToProto(r); err == nil {
			t.Errorf("UpdatingWorkUnitRowToProto(bad[%d]) = nil, want error", i)
		}
	}
}
