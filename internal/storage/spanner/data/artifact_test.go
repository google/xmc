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

	timestamppb "protobuf/timestamp_go_proto"
	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	"golang/cmp/cmp"
	"golang/protobuf/v2/proto/proto"
	"golang/protobuf/v2/testing/protocmp/protocmp"
	pb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/storage/data/data"
	"xmanager_cloud/storage/spanner/schema/artifactschema"
)

var (
	testArtTime  = time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC)
	testArtifact = pb.Artifact{
		Name:   "artifacts/789",
		Parent: "experiments/123/workUnits/456",
		Url: &pb.Url{
			Url:         "http://test.com",
			Icon:        "test-icon",
			DisplayName: "test-display-name",
			Priority:    100,
		},
		Tags:           []string{"tag1", "tag2"},
		Notes:          "Test notes",
		LifecyclePhase: pb.Artifact_INPUT,
		DataType:       pb.Artifact_URL,
		MimeType:       "text/plain",
		CreateTime:     timestamppb.New(testArtTime),
		UpdateTime:     timestamppb.New(testArtTime),
	}
)

func TestArtifactRowToProto(t *testing.T) {
	keys := []string{"Xid", "WorkUnitId", "ArtifactId", "Url", "Icon", "DisplayName", "Priority", "Tags", "Notes", "LifecyclePhase", "DataType", "MimeType", "AdditionalInfo", "CreateTime", "UpdateTime"}
	values := []any{
		int64(123),
		int64(456),
		int64(789),
		testArtifact.Url.Url,
		testArtifact.Url.Icon,
		testArtifact.Url.DisplayName,
		int64(testArtifact.Url.Priority),
		testArtifact.Tags,
		testArtifact.Notes,
		testArtifact.LifecyclePhase,
		testArtifact.DataType,
		testArtifact.MimeType,
		testArtifact.AdditionalInfo,
		testArtTime,
		testArtTime,
	}

	row, err := spanner.NewRow(keys, values)
	if err != nil {
		t.Fatalf("Failed to create Spanner row: %v", err)
	}

	got, err := ArtifactRowToProto(row)
	if err != nil {
		t.Fatalf("Failed to convert Spanner row to Artifact proto: %v", err)
	}

	if diff := cmp.Diff(&testArtifact, got, protocmp.Transform()); diff != "" {
		t.Errorf("ArtifactRowToProto() returned unexpected diff (-want +got):\n%s", diff)
	}
}

func TestArtifactRowToProto_WithAdditionalInfo(t *testing.T) {
	additionalInfo := &pb.Artifact_AdditionalInfo{
		Title:   "Launch Command",
		Content: &pb.Artifact_AdditionalInfo_CodeBlock{CodeBlock: "python train.py --lr=0.01"},
	}
	want := proto.Clone(&testArtifact).(*pb.Artifact)
	want.AdditionalInfo = additionalInfo

	keys := []string{"Xid", "WorkUnitId", "ArtifactId", "Url", "Icon", "DisplayName", "Priority", "Tags", "Notes", "LifecyclePhase", "DataType", "MimeType", "AdditionalInfo", "CreateTime", "UpdateTime"}
	values := []any{
		int64(123),
		int64(456),
		int64(789),
		want.Url.Url,
		want.Url.Icon,
		want.Url.DisplayName,
		int64(want.Url.Priority),
		want.Tags,
		want.Notes,
		want.LifecyclePhase,
		want.DataType,
		want.MimeType,
		spanner.NullProtoMessage{ProtoMessageVal: additionalInfo, Valid: true},
		testArtTime,
		testArtTime,
	}

	row, err := spanner.NewRow(keys, values)
	if err != nil {
		t.Fatalf("Failed to create Spanner row: %v", err)
	}

	got, err := ArtifactRowToProto(row)
	if err != nil {
		t.Fatalf("Failed to convert Spanner row to Artifact proto: %v", err)
	}

	if diff := cmp.Diff(want, got, protocmp.Transform()); diff != "" {
		t.Errorf("ArtifactRowToProto() returned unexpected diff (-want +got):\n%s", diff)
	}
}

func TestArtifactRowToProto_MissingColumn(t *testing.T) {
	keys := []string{"Xid"}
	values := []any{int64(123)}
	row, err := spanner.NewRow(keys, values)
	if err != nil {
		t.Fatalf("spanner.NewRow(%v, %v) failed: %v", keys, values, err)
	}

	if _, err := ArtifactRowToProto(row); err == nil {
		t.Errorf("ArtifactRowToProto() with missing columns succeeded, want error")
	}
}

func TestArtifactProtoToInsertMutation(t *testing.T) {
	art := &testArtifact
	want := spanner.Insert(
		artifactschema.ArtifactTableName,
		artifactschema.ArtifactColumns,
		[]any{
			int64(123),
			spanner.NullInt64{Int64: 456, Valid: true},
			int64(789),
			art.Url.Url,
			art.Url.Icon,
			art.Url.DisplayName,
			int64(art.Url.Priority),
			art.Tags,
			art.Notes,
			art.LifecyclePhase,
			art.DataType,
			art.MimeType,
			art.AdditionalInfo,
			art.CreateTime.AsTime(),
			art.UpdateTime.AsTime(),
		},
	)
	got, err := ArtifactProtoToInsertMutation(art)
	if err != nil {
		t.Fatalf("ArtifactProtoToInsertMutation() returned unexpected error: %v", err)
	}
	if diff := cmp.Diff(want, got, protocmp.Transform(), cmp.AllowUnexported(spanner.Mutation{})); diff != "" {
		t.Errorf("ArtifactProtoToInsertMutation() returned unexpected diff (-want +got):\n%s", diff)
	}
}

func TestArtifactProtoToUpdateMutation(t *testing.T) {
	art := &testArtifact
	want := spanner.Update(
		artifactschema.ArtifactTableName,
		artifactschema.ArtifactUpdateColumns,
		[]any{
			data.ReverseBits(int64(123)),
			spanner.NullInt64{Int64: 456, Valid: true},
			int64(789),
			art.Url.Url,
			art.Url.Icon,
			art.Url.DisplayName,
			int64(art.Url.Priority),
			art.Tags,
			art.Notes,
			art.LifecyclePhase,
			art.DataType,
			art.MimeType,
			art.AdditionalInfo,
			art.UpdateTime.AsTime(),
		},
	)
	got, err := ArtifactProtoToUpdateMutation(art)
	if err != nil {
		t.Fatalf("ArtifactProtoToUpdateMutation() returned unexpected error: %v", err)
	}
	if diff := cmp.Diff(want, got, protocmp.Transform(), cmp.AllowUnexported(spanner.Mutation{})); diff != "" {
		t.Errorf("ArtifactProtoToUpdateMutation() returned unexpected diff (-want +got):\n%s", diff)
	}
}
