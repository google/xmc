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

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	actuatortaskpb "xmanager_cloud/api/protos/task_queue"
	"xmanager_cloud/internal/experiment/experimenttest"
	"xmanager_cloud/internal/storage"
)

func mustMarshal(t *testing.T, msg proto.Message) []byte {
	t.Helper()
	b, err := proto.Marshal(msg)
	if err != nil {
		t.Fatalf("proto.Marshal() unexpected error: %v", err)
	}
	return b
}

func TestPausedTaskRoundTrip(t *testing.T) {
	createTime := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	completeTime := createTime.Add(time.Hour)
	task := &storage.PausedTask{
		XID: 100, WID: 200, TaskID: "task-1", Target: "experiments/100/workUnits/200/executions/3",
		Action:        &actuatortaskpb.Action{Action: &actuatortaskpb.Action_Restart{Restart: &actuatortaskpb.RestartAction{Author: proto.String("a")}}},
		OperationName: "op-1", TraceID: "trace-1", Priority: 7, CreateTime: createTime,
	}
	taskRow, err := PausedTaskToRow(task)
	if err != nil {
		t.Fatalf("PausedTaskToRow() unexpected error: %v", err)
	}
	got, err := PendingPausedTaskRowToTask(PendingPausedTaskRow{
		PausedTaskRow: taskRow, WorkUnitType: 1, WorkUnitPriority: 2, Notes: "notes", Tags: []string{"tag"},
		Urls:                [][]byte{mustMarshal(t, &essmsgpb.Url{Url: "https://example.com"})},
		Executable:          mustMarshal(t, &essmsgpb.Executable{}),
		ExecutionCreateTime: createTime, ExecutionUpdateTime: createTime, ExecutionCompleteTime: &completeTime, ExecutionID: 3,
		WorkUnitStatus:     mustMarshal(t, &essmsgpb.WorkUnitStatus{State: essmsgpb.WorkUnitStatus_SCHEDULING_PAUSED}),
		WorkUnitCreateTime: createTime, WorkUnitUpdateTime: completeTime,
		Parameters: mustMarshal(t, experimenttest.TrialParameters()), TrialID: proto.String("trial-1"),
	})
	if err != nil {
		t.Fatalf("PendingPausedTaskRowToTask() unexpected error: %v", err)
	}

	want := *task
	want.WorkUnit = &essmsgpb.WorkUnit{
		Name: "experiments/100/workUnits/200", WorkUnitType: 1, Priority: 2, Notes: "notes", Tags: []string{"tag"},
		Urls: []*essmsgpb.Url{{Url: "https://example.com"}}, Executable: &essmsgpb.Executable{},
		CurrentWorkUnitExecution: &essmsgpb.WorkUnitExecution{
			Name: "experiments/100/workUnits/200/executions/3", State: essmsgpb.WorkUnitExecution_ACTIVE,
			Metadata: &essmsgpb.WorkUnitExecutionMetadata{}, Status: &essmsgpb.WorkUnitStatus{State: essmsgpb.WorkUnitStatus_SCHEDULING_PAUSED},
			CreateTime: timestamppb.New(createTime), UpdateTime: timestamppb.New(createTime), CompleteTime: timestamppb.New(completeTime),
		},
		CreateTime: timestamppb.New(createTime), UpdateTime: timestamppb.New(completeTime),
		Parameters: experimenttest.TrialParameters(), TrialId: "trial-1",
	}
	if diff := cmp.Diff(&want, got, protocmp.Transform()); diff != "" {
		t.Errorf("round trip mismatch (-want +got):\n%s", diff)
	}
}

func TestPendingPausedTaskRowToTask_CorruptProto(t *testing.T) {
	corrupt := []byte{0xff}
	tests := []struct {
		name string
		row  PendingPausedTaskRow
	}{
		{name: "action", row: PendingPausedTaskRow{PausedTaskRow: PausedTaskRow{ActionProto: corrupt}}},
		{name: "url", row: PendingPausedTaskRow{Urls: [][]byte{corrupt}}},
		{name: "executable", row: PendingPausedTaskRow{Executable: corrupt}},
		{name: "execution_metadata", row: PendingPausedTaskRow{ExecutionMetadata: corrupt}},
		{name: "work_unit_status", row: PendingPausedTaskRow{WorkUnitStatus: corrupt}},
		{name: "parameters", row: PendingPausedTaskRow{Parameters: corrupt}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := PendingPausedTaskRowToTask(tt.row); err == nil {
				t.Error("PendingPausedTaskRowToTask() error = nil, want non-nil")
			}
		})
	}
}
