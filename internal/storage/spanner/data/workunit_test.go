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
	"slices"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/testing/protocmp"
	structpb "google.golang.org/protobuf/types/known/structpb"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	pb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/experiment/experimenttest"
	"xmanager_cloud/internal/prototest"
	"xmanager_cloud/internal/storage/data"
	"xmanager_cloud/internal/storage/spanner/schema/workunitschema"
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
			[]byte(nil),
			spanner.NullString{},
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
	row := trialRow(t, []byte(nil), spanner.NullString{})

	got, err := WorkUnitRowToProto(row)
	if err != nil {
		t.Fatalf("WorkUnitRowToProto() failed: %v", err)
	}

	want := &fullTestWorkUnit

	if diff := cmp.Diff(want, got, protocmp.Transform()); diff != "" {
		t.Errorf("WorkUnitRowToProto() returned unexpected diff (-want +got):\n%s", diff)
	}
}

// trialRow returns a WorkUnits row of fullTestWorkUnit with the given Parameters and TrialId column
// values.
func trialRow(t *testing.T, parameters []byte, trialID spanner.NullString) *spanner.Row {
	t.Helper()
	return workUnitRow(t, workunitschema.WorkUnitColumns, append(insertValuesPrefix(), parameters, trialID))
}

// workUnitRow returns a Spanner row with the given columns and values.
func workUnitRow(t *testing.T, keys []string, values []any) *spanner.Row {
	t.Helper()
	row, err := spanner.NewRow(keys, values)
	if err != nil {
		t.Fatalf("Failed to create Spanner row: %v", err)
	}
	return row
}

// trialWorkUnit returns fullTestWorkUnit as a study trial.
func trialWorkUnit() *pb.WorkUnit {
	return prototest.Modify(&fullTestWorkUnit, func(wu *pb.WorkUnit) {
		wu.Parameters = experimenttest.TrialParameters()
		wu.TrialId = "trial-7"
	})
}

func TestWorkUnitTrialFields_RoundTrip(t *testing.T) {
	tests := []struct {
		name       string
		parameters *structpb.Struct
		trialID    string
	}{
		{name: "trial", parameters: experimenttest.TrialParameters(), trialID: "trial-7"},
		{name: "empty parameters stay non-null", parameters: &structpb.Struct{}, trialID: "trial-8"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wu := prototest.Modify(&fullTestWorkUnit, func(wu *pb.WorkUnit) {
				wu.Parameters = tt.parameters
				wu.TrialId = tt.trialID
			})
			parameters, err := marshalParameters(wu.GetParameters())
			if err != nil || parameters == nil {
				t.Fatalf("marshalParameters() = (%v, %v), want non-nil bytes", parameters, err)
			}
			mutation, err := WorkUnitProtoToInsertMutation(wu)
			if err != nil {
				t.Fatalf("WorkUnitProtoToInsertMutation() failed: %v", err)
			}
			want := spanner.Insert(workunitschema.WorkUnitTableName, workunitschema.WorkUnitColumns, append(insertValuesPrefix(), parameters, spanner.NullString{StringVal: tt.trialID, Valid: true}))
			if diff := cmp.Diff(want, mutation, protocmp.Transform(), cmp.AllowUnexported(spanner.Mutation{})); diff != "" {
				t.Errorf("WorkUnitProtoToInsertMutation() diff (-want +got):\n%s", diff)
			}
			got, err := WorkUnitRowToProto(trialRow(t, parameters, spanner.NullString{StringVal: tt.trialID, Valid: true}))
			if err != nil {
				t.Fatalf("WorkUnitRowToProto() failed: %v", err)
			}
			if diff := cmp.Diff(wu, got, protocmp.Transform()); diff != "" {
				t.Errorf("round trip diff (-want +got):\n%s", diff)
			}
		})
	}
}

// insertValuesPrefix returns the insert mutation values of fullTestWorkUnit before Parameters.
func insertValuesPrefix() []any {
	wu := &fullTestWorkUnit
	return []any{
		int64(123), int64(456), wu.WorkUnitType, int64(wu.Priority), wu.Urls, wu.Tags, wu.Notes, wu.Executable,
		wu.CurrentWorkUnitExecution.CreateTime.AsTime(), wu.CurrentWorkUnitExecution.UpdateTime.AsTime(),
		spanner.NullTime{Time: wu.CurrentWorkUnitExecution.CompleteTime.AsTime(), Valid: true}, int64(789),
		wu.CurrentWorkUnitExecution.Metadata, wu.CurrentWorkUnitExecution.Status, wu.CreateTime.AsTime(), wu.UpdateTime.AsTime(),
	}
}

func TestWorkUnitTrialFields_Errors(t *testing.T) {
	invalidUTF8 := prototest.Modify(trialWorkUnit(), func(wu *pb.WorkUnit) {
		wu.Parameters.Fields["\xff"] = structpb.NewNumberValue(1)
	})
	if _, err := WorkUnitProtoToInsertMutation(invalidUTF8); err == nil || !strings.Contains(err.Error(), "failed to marshal Parameters") {
		t.Errorf("WorkUnitProtoToInsertMutation(invalid UTF-8 key) = %v, want marshal error", err)
	}
	if _, err := WorkUnitRowToProto(trialRow(t, []byte{0xff, 0xff}, spanner.NullString{})); err == nil || !strings.Contains(err.Error(), "failed to unmarshal column Parameters") {
		t.Errorf("WorkUnitRowToProto(corrupt Parameters) = %v, want unmarshal error", err)
	}
	keys := slices.Clone(workunitschema.WorkUnitColumns[:16])
	partial := workUnitRow(t, append(keys, "Parameters"), append(insertValuesPrefix(), []byte(nil)))
	if _, err := WorkUnitRowToProto(partial); err == nil || !strings.Contains(err.Error(), "TrialId") {
		t.Errorf("WorkUnitRowToProto(missing TrialId) = %v, want error", err)
	}
	withoutParameters := workUnitRow(t, keys, insertValuesPrefix())
	if _, err := WorkUnitRowToProto(withoutParameters); err == nil || !strings.Contains(err.Error(), "Parameters") {
		t.Errorf("WorkUnitRowToProto(missing Parameters) = %v, want error", err)
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
