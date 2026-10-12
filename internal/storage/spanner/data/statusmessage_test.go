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
	"google.golang.org/protobuf/testing/protocmp"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	pb "xmanager_cloud/api/protos/experiment"
)

var (
	testTime    = time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC)
	testMessage = pb.StructuredMessage{
		Parent:         "experiments/123/workUnits/456",
		Severity:       pb.StructuredMessage_INFO,
		Source:         pb.StructuredMessage_XMANAGER_CLIENT,
		UserFacingText: "Test message",
		CreateTime:     timestamppb.New(testTime),
		AuxiliaryInfo: &pb.AuxiliaryInfo{
			Detail: &pb.AuxiliaryInfo_StackTrace{StackTrace: "test stack trace"},
		},
	}
)

func TestStatusMessageRowToProto(t *testing.T) {
	keys := []string{"MessageId", "Xid", "WorkUnitId", "ExecutionId", "Severity", "Source", "UserFacingText", "UserFacingUrl", "CreateTime", "AuxiliaryInfo"}
	values := []any{
		int64(789),
		int64(123),
		spanner.NullInt64{Int64: 456, Valid: true},
		spanner.NullInt64{Int64: 1, Valid: true},
		int64(pb.StructuredMessage_INFO),
		int64(pb.StructuredMessage_XMANAGER_CLIENT),
		"Test message",
		(*pb.Url)(nil),
		testTime,
		testMessage.AuxiliaryInfo,
	}

	row, err := spanner.NewRow(keys, values)
	if err != nil {
		t.Fatalf("Failed to create Spanner row: %v", err)
	}

	got, err := StatusMessageRowToProto(row)
	if err != nil {
		t.Fatalf("StatusMessageRowToProto() returned unexpected error: %v", err)
	}

	want := &pb.StatusMessage{
		Name:        "experiments/123/workUnits/456/statusMessages/789",
		Content:     &testMessage,
		ExecutionId: 1,
	}

	if diff := cmp.Diff(want, got, protocmp.Transform()); diff != "" {
		t.Errorf("StatusMessageRowToProto() returned unexpected diff (-want +got):\n%s", diff)
	}
}

func TestStatusMessageProtoToInsertMutation_AllFields(t *testing.T) {
	testEID := int64(555)
	testPrevMsg := &pb.StructuredMessage{UserFacingText: "previous text"}
	testURL := &pb.Url{Url: "http://google.com", DisplayName: "Google"}

	testAux := &pb.AuxiliaryInfo{
		Detail: &pb.AuxiliaryInfo_StackTrace{StackTrace: "test stack trace"},
	}

	testMsg := &pb.StructuredMessage{
		Parent:         "experiments/123/workUnits/456",
		Severity:       pb.StructuredMessage_INFO,
		Source:         pb.StructuredMessage_XMANAGER_CLIENT,
		UserFacingText: "Test message",
		UserFacingUrl:  testURL,
		CreateTime:     timestamppb.New(testTime),
		AuxiliaryInfo:  testAux,
	}

	want := spanner.Insert(
		"StatusMessages",
		[]string{"Xid", "WorkUnitId", "ExecutionId", "MessageId", "Severity", "Source", "UserFacingText", "UserFacingUrl", "CreateTime", "PreviousMessage", "AuxiliaryInfo"},
		[]any{
			int64(123),
			spanner.NullInt64{Int64: 456, Valid: true},
			spanner.NullInt64{Int64: 555, Valid: true},
			int64(789),
			int64(pb.StructuredMessage_INFO),
			int64(pb.StructuredMessage_XMANAGER_CLIENT),
			"Test message",
			testURL,
			testTime,
			testPrevMsg,
			testAux,
		},
	)

	got, err := StatusMessageProtoToInsertMutation(testMsg, 789, &testEID, testPrevMsg)
	if err != nil {
		t.Fatalf("StatusMessageProtoToInsertMutation() error = %v", err)
	}

	if diff := cmp.Diff(want, got, protocmp.Transform(), cmp.AllowUnexported(spanner.Mutation{})); diff != "" {
		t.Errorf("StatusMessageProtoToInsertMutation() diff (-want +got):\n%s", diff)
	}
}

func TestStatusMessageProtoToInsertMutation_NullableFields(t *testing.T) {
	testMsg := &pb.StructuredMessage{
		Parent:         "experiments/123",
		Severity:       pb.StructuredMessage_INFO,
		Source:         pb.StructuredMessage_XMANAGER_CLIENT,
		UserFacingText: "Test message",
		CreateTime:     timestamppb.New(testTime),
	}

	want := spanner.Insert(
		"StatusMessages",
		[]string{"Xid", "WorkUnitId", "ExecutionId", "MessageId", "Severity", "Source", "UserFacingText", "UserFacingUrl", "CreateTime", "PreviousMessage", "AuxiliaryInfo"},
		[]any{
			int64(123),
			spanner.NullInt64{Valid: false},
			spanner.NullInt64{Valid: false},
			int64(789),
			int64(pb.StructuredMessage_INFO),
			int64(pb.StructuredMessage_XMANAGER_CLIENT),
			"Test message",
			(*pb.Url)(nil),
			testTime,
			(*pb.StructuredMessage)(nil),
			(*pb.AuxiliaryInfo)(nil),
		},
	)

	got, err := StatusMessageProtoToInsertMutation(testMsg, 789, nil, nil)
	if err != nil {
		t.Fatalf("StatusMessageProtoToInsertMutation() returned unexpected error: %v", err)
	}

	if diff := cmp.Diff(want, got, protocmp.Transform(), cmp.AllowUnexported(spanner.Mutation{})); diff != "" {
		t.Errorf("StatusMessageProtoToInsertMutation() returned unexpected diff (-want +got):\n%s", diff)
	}
}
