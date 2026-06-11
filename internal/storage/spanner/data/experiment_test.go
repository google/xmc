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

	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	"golang/cmp/cmp"
	"golang/protobuf/v2/testing/protocmp/protocmp"
	timestamppb "protobuf/timestamp_go_proto"
	pb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/storage/data/data"
	"xmanager_cloud/storage/spanner/schema/experimentschema"
)

var (
	testExpTime         = time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC)
	testExpCompleteTime = time.Date(2024, time.January, 1, 1, 0, 0, 0, time.UTC)
	testExperiment      = pb.Experiment{
		Name:     "experiments/123",
		Title:    "Test Experiment",
		Creator:  "test-user",
		Priority: 100,
		Urls: []*pb.Url{
			{Url: "http://test.com", Icon: "test-icon", DisplayName: "test-display-name"},
		},
		Tags:  []string{"tag1", "tag2"},
		Notes: "Test notes",
		Resources: []*pb.ResourceSet{
			{
				Resources: &pb.ResourceSet_CpuResources{
					CpuResources: &pb.CpuResources{
						CpuMillicores: 1000,
						MemoryBytes:   1024,
					},
				},
			},
		},
		Status: &pb.ExperimentStatus{
			ActiveState: pb.ExperimentStatus_ACTIVE,
			LaunchState: pb.ExperimentStatus_LAUNCHING,
		},
		CreateTime:   timestamppb.New(testExpTime),
		UpdateTime:   timestamppb.New(testExpTime),
		CompleteTime: timestamppb.New(testExpCompleteTime),
	}
)

func TestExperimentRowToProto(t *testing.T) {
	keys := []string{"Xid", "Title", "Creator", "Priority", "Urls", "Notes", "Tags", "Resources", "Status", "CreateTime", "UpdateTime", "CompleteTime"}
	values := []any{
		int64(123),
		testExperiment.Title,
		testExperiment.Creator,
		int64(testExperiment.Priority),
		testExperiment.Urls,
		testExperiment.Notes,
		testExperiment.Tags,
		testExperiment.Resources,
		testExperiment.Status,
		testExpTime,
		testExpTime,
		testExpCompleteTime,
	}

	row, err := spanner.NewRow(keys, values)
	if err != nil {
		t.Fatalf("Failed to create Spanner row: %v", err)
	}

	got, err := ExperimentRowToProto(row)
	if err != nil {
		t.Fatalf("Failed to convert Spanner row to Experiment proto: %v", err)
	}

	if diff := cmp.Diff(&testExperiment, got, protocmp.Transform()); diff != "" {
		t.Errorf("ExperimentRowToProto() returned unexpected diff (-want +got):\n%s", diff)
	}
}

func TestExperimentRowToProto_MissingColumn(t *testing.T) {
	keys := []string{"Xid"}
	values := []any{int64(123)}
	row, err := spanner.NewRow(keys, values)
	if err != nil {
		t.Fatalf("spanner.NewRow(%v, %v) failed: %v", keys, values, err)
	}

	if _, err := ExperimentRowToProto(row); err == nil {
		t.Errorf("ExperimentRowToProto() with missing columns succeeded, want error")
	}
}

func TestExperimentProtoToInsertMutation(t *testing.T) {
	xid := int64(123)
	exp := &testExperiment
	want := spanner.Insert(
		experimentschema.ExperimentTableName,
		experimentschema.ExperimentColumns,
		[]any{
			xid,
			exp.Title,
			exp.Creator,
			int64(exp.Priority),
			exp.Urls,
			exp.Tags,
			exp.Notes,
			exp.Resources,
			exp.Status,
			exp.CreateTime.AsTime(),
			exp.UpdateTime.AsTime(),
			spanner.NullTime{Time: exp.CompleteTime.AsTime(), Valid: true},
		},
	)
	got, err := ExperimentProtoToInsertMutation(exp)
	if err != nil {
		t.Fatalf("ExperimentProtoToInsertMutation() returned unexpected error: %v", err)
	}
	if diff := cmp.Diff(want, got, protocmp.Transform(), cmp.AllowUnexported(spanner.Mutation{})); diff != "" {
		t.Errorf("ExperimentProtoToInsertMutation() returned unexpected diff (-want +got):\n%s", diff)
	}
}

func TestExperimentProtoToUpdateMutation(t *testing.T) {
	xid := int64(123)
	exp := &testExperiment
	want := spanner.Update(
		experimentschema.ExperimentTableName,
		experimentschema.ExperimentUpdateColumns,
		[]any{
			data.ReverseBits(xid),
			exp.Title,
			int64(exp.Priority),
			exp.Urls,
			exp.Tags,
			exp.Notes,
			exp.UpdateTime.AsTime(),
		},
	)
	got, err := ExperimentProtoToUpdateMutation(exp)
	if err != nil {
		t.Fatalf("ExperimentProtoToUpdateMutation() returned unexpected error: %v", err)
	}
	if diff := cmp.Diff(want, got, protocmp.Transform(), cmp.AllowUnexported(spanner.Mutation{})); diff != "" {
		t.Errorf("ExperimentProtoToUpdateMutation() returned unexpected diff (-want +got):\n%s", diff)
	}
}

func TestExperimentProtoToStatusUpdateMutation(t *testing.T) {
	xid := int64(123)
	exp := &testExperiment
	want := spanner.Update(
		experimentschema.ExperimentTableName,
		experimentschema.ExperimentStatusUpdateColumns,
		[]any{
			data.ReverseBits(xid),
			exp.Status,
			exp.Status.UpdateTime.AsTime(),
			spanner.NullTime{Time: exp.CompleteTime.AsTime(), Valid: true},
		},
	)
	got, err := ExperimentProtoToStatusUpdateMutation(exp)
	if err != nil {
		t.Fatalf("ExperimentProtoToStatusUpdateMutation() returned unexpected error: %v", err)
	}
	if diff := cmp.Diff(want, got, protocmp.Transform(), cmp.AllowUnexported(spanner.Mutation{})); diff != "" {
		t.Errorf("ExperimentProtoToStatusUpdateMutation() returned unexpected diff (-want +got):\n%s", diff)
	}
}

func TestExperimentProtoToResourcesUpdateMutation(t *testing.T) {
	xid := int64(123)
	exp := &testExperiment
	want := spanner.Update(
		experimentschema.ExperimentTableName,
		experimentschema.ExperimentResourcesUpdateColumns,
		[]any{
			data.ReverseBits(xid),
			exp.Resources,
			exp.UpdateTime.AsTime(),
		},
	)
	got, err := ExperimentProtoToResourcesUpdateMutation(exp)
	if err != nil {
		t.Fatalf("ExperimentProtoToResourcesUpdateMutation() returned unexpected error: %v", err)
	}
	if diff := cmp.Diff(want, got, protocmp.Transform(), cmp.AllowUnexported(spanner.Mutation{})); diff != "" {
		t.Errorf("ExperimentProtoToResourcesUpdateMutation() returned unexpected diff (-want +got):\n%s", diff)
	}
}
