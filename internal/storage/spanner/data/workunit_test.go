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
	pb "github.com/google/xmc/api/protos/experiment"
	"github.com/google/xmc/internal/storage/data"
	"github.com/google/xmc/internal/storage/spanner/schema/workunitschema"
	"google.golang.org/protobuf/testing/protocmp"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
)

var (
	testURL = pb.Url{
		Url:         "http://test.com",
		Icon:        "test-icon",
		DisplayName: "test-display-name",
	}

	testWorkUnit = pb.WorkUnit{
		Name:     "experiments/123/workUnits/456",
		Priority: 200,
		Notes:    "WorkUnit test notes.",
		Tags:     []string{"wu_tag1", "wu_tag2"},
		Urls: []*pb.Url{
			&testURL,
		},
		WorkUnitType: pb.WorkUnit_STANDARD,
		Executable: &pb.Executable{
			SchedulingConstraints: &pb.SchedulingConstraints{
				QueueName: "test-queue",
			},
			Type: &pb.Executable_Jobset{
				Jobset: &pb.JobSetExecutable{
					Jobs: []*pb.KubernetesJob{
						{
							Name: "test-job",
						},
					},
				},
			},
		},
		CurrentWorkUnitExecution: &pb.WorkUnitExecution{
			Name:  "experiments/123/workUnits/456/executions/789",
			State: pb.WorkUnitExecution_ACTIVE,
			Status: &pb.WorkUnitStatus{
				State: pb.WorkUnitStatus_RUNNING,
			},
			Metadata: &pb.WorkUnitExecutionMetadata{
				ManagementCluster: &pb.ClusterInfo{
					Project:     "test-project",
					Location:    "test-location",
					ClusterName: "test-cluster",
				},
				ResourceName: "test-resource-name",
			},
			CreateTime:   timestamppb.New(testWorkUnitTime),
			UpdateTime:   timestamppb.New(testWorkUnitTime),
			CompleteTime: timestamppb.New(testWorkUnitCompleteTime),
		},
	}
	testWorkUnitTime         = time.Date(2024, time.January, 3, 0, 0, 0, 0, time.UTC)
	testWorkUnitCompleteTime = time.Date(2024, time.January, 3, 1, 0, 0, 0, time.UTC)
	fullTestWorkUnit         = pb.WorkUnit{
		Name:                     testWorkUnit.Name,
		Priority:                 testWorkUnit.Priority,
		Notes:                    testWorkUnit.Notes,
		Tags:                     testWorkUnit.Tags,
		Urls:                     testWorkUnit.Urls,
		WorkUnitType:             testWorkUnit.WorkUnitType,
		Executable:               testWorkUnit.Executable,
		CreateTime:               timestamppb.New(testWorkUnitTime),
		UpdateTime:               timestamppb.New(testWorkUnitTime),
		CurrentWorkUnitExecution: testWorkUnit.CurrentWorkUnitExecution,
	}
)

func TestWorkUnitProtoToInsertMutation(t *testing.T) {
	xid := int64(123)
	wid := int64(456)
	eid := int64(789)
	wu := &fullTestWorkUnit

	want := spanner.Insert(
		workunitschema.WorkUnitTableName,
		workunitschema.WorkUnitColumns,
		[]any{
			xid,
			wid,
			wu.WorkUnitType,
			int64(wu.Priority),
			wu.Urls,
			wu.Tags,
			wu.Notes,
			wu.Executable,
			wu.CurrentWorkUnitExecution.CreateTime.AsTime(),
			wu.CurrentWorkUnitExecution.UpdateTime.AsTime(),
			spanner.NullTime{Time: wu.CurrentWorkUnitExecution.CompleteTime.AsTime(), Valid: true},
			eid,
			wu.CurrentWorkUnitExecution.Metadata,
			wu.CurrentWorkUnitExecution.Status,
			wu.CreateTime.AsTime(),
			wu.UpdateTime.AsTime(),
		},
	)

	got, err := WorkUnitProtoToInsertMutation(wu)
	if err != nil {
		t.Fatalf("WorkUnitProtoToInsertMutation() returned unexpected error: %v", err)
	}

	if diff := cmp.Diff(want, got, protocmp.Transform(), cmp.AllowUnexported(spanner.Mutation{})); diff != "" {
		t.Errorf("WorkUnitProtoToInsertMutation() returned unexpected diff (-want +got):\n%s", diff)
	}
}

func TestWorkUnitProtoToUpdateMutation(t *testing.T) {
	xid := int64(123)
	wid := int64(456)
	wu := &fullTestWorkUnit

	want := spanner.Update(
		workunitschema.WorkUnitTableName,
		workunitschema.WorkUnitUpdateColumns,
		[]any{
			data.ReverseBits(xid),
			wid,
			int64(wu.Priority),
			wu.Urls,
			wu.Tags,
			wu.Notes,
			wu.UpdateTime.AsTime(),
		},
	)

	got, err := WorkUnitProtoToUpdateMutation(wu)
	if err != nil {
		t.Fatalf("WorkUnitProtoToUpdateMutation() returned unexpected error: %v", err)
	}

	if diff := cmp.Diff(want, got, protocmp.Transform(), cmp.AllowUnexported(spanner.Mutation{})); diff != "" {
		t.Errorf("WorkUnitProtoToUpdateMutation() returned unexpected diff (-want +got):\n%s", diff)
	}
}

func TestWorkUnitProtoToUpdateMutation_InvalidName(t *testing.T) {
	wu := &pb.WorkUnit{Name: "invalid"}
	_, err := WorkUnitProtoToUpdateMutation(wu)
	if err == nil {
		t.Errorf("WorkUnitProtoToUpdateMutation() with invalid name succeeded, want error")
	}
}

func TestWorkUnitRowToProto(t *testing.T) {
	keys := []string{"Xid", "WorkUnitId", "WorkUnitType", "Priority", "Urls", "Tags", "Notes", "Executable", "ExecutionCreateTime", "ExecutionUpdateTime", "ExecutionCompleteTime", "ExecutionId", "ExecutionMetadata", "WorkUnitStatus", "CreateTime", "UpdateTime"}
	values := []any{
		int64(123),
		int64(456),
		fullTestWorkUnit.WorkUnitType,
		int64(fullTestWorkUnit.Priority),
		fullTestWorkUnit.Urls,
		fullTestWorkUnit.Tags,
		fullTestWorkUnit.Notes,
		fullTestWorkUnit.Executable,
		fullTestWorkUnit.CurrentWorkUnitExecution.CreateTime.AsTime(),
		fullTestWorkUnit.CurrentWorkUnitExecution.UpdateTime.AsTime(),
		spanner.NullTime{Time: fullTestWorkUnit.CurrentWorkUnitExecution.CompleteTime.AsTime(), Valid: true},
		int64(789),
		fullTestWorkUnit.CurrentWorkUnitExecution.Metadata,
		fullTestWorkUnit.CurrentWorkUnitExecution.Status,
		fullTestWorkUnit.CreateTime.AsTime(),
		fullTestWorkUnit.UpdateTime.AsTime(),
	}
	row, err := spanner.NewRow(keys, values)
	if err != nil {
		t.Fatalf("Failed to create Spanner row: %v", err)
	}

	got, err := WorkUnitRowToProto(row)
	if err != nil {
		t.Fatalf("WorkUnitRowToProto() failed: %v", err)
	}

	want := &fullTestWorkUnit

	if diff := cmp.Diff(want, got, protocmp.Transform()); diff != "" {
		t.Errorf("WorkUnitRowToProto() returned unexpected diff (-want +got):\n%s", diff)
	}
}

func TestWorkUnitRowToProto_MissingColumn(t *testing.T) {
	keys := []string{"Xid"}
	values := []any{int64(123)}
	row, err := spanner.NewRow(keys, values)
	if err != nil {
		t.Fatalf("spanner.NewRow(%v, %v) failed: %v", keys, values, err)
	}

	if _, err := WorkUnitRowToProto(row); err == nil {
		t.Errorf("WorkUnitRowToProto() with missing columns succeeded, want error")
	}
}
