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
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/protobuf/testing/protocmp"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	essmsgpb "xmanager_cloud/api/protos/experiment"
)

var testStatusMessageTime = time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)

func TestStatusMessageProtoAndRowRoundTrip(t *testing.T) {
	eid := int64(555)
	prevMsg := &essmsgpb.StructuredMessage{UserFacingText: "previous text"}
	wuMsg := &essmsgpb.StructuredMessage{
		Parent:         "experiments/123/workUnits/456",
		Severity:       essmsgpb.StructuredMessage_INFO,
		Source:         essmsgpb.StructuredMessage_XMANAGER_CLIENT,
		UserFacingText: "Test message",
		UserFacingUrl:  &essmsgpb.Url{Url: "https://example.com", DisplayName: "Example"},
		CreateTime:     timestamppb.New(testStatusMessageTime),
		AuxiliaryInfo: &essmsgpb.AuxiliaryInfo{
			Detail: &essmsgpb.AuxiliaryInfo_StackTrace{StackTrace: "test stack trace"},
		},
	}
	expMsg := &essmsgpb.StructuredMessage{
		Parent:         "experiments/123",
		Severity:       essmsgpb.StructuredMessage_ERROR,
		Source:         essmsgpb.StructuredMessage_XMANAGER_BACKEND,
		UserFacingText: "Experiment message",
		CreateTime:     timestamppb.New(testStatusMessageTime),
	}

	tests := []struct {
		name        string
		msg         *essmsgpb.StructuredMessage
		messageID   int64
		eid         *int64
		previousMsg *essmsgpb.StructuredMessage
		want        *essmsgpb.StatusMessage
		wantRow     StatusMessageRow
	}{
		{
			name:        "work unit message with all fields",
			msg:         wuMsg,
			messageID:   789,
			eid:         &eid,
			previousMsg: prevMsg,
			want: &essmsgpb.StatusMessage{
				Name:        "experiments/123/workUnits/456/statusMessages/789",
				Content:     wuMsg,
				ExecutionId: 555,
			},
			wantRow: StatusMessageRow{
				Xid:            123,
				WorkUnitID:     pgtype.Int8{Int64: 456, Valid: true},
				MessageID:      789,
				ExecutionID:    pgtype.Int8{Int64: 555, Valid: true},
				Severity:       int64(essmsgpb.StructuredMessage_INFO),
				Source:         int64(essmsgpb.StructuredMessage_XMANAGER_CLIENT),
				UserFacingText: "Test message",
				CreateTime:     testStatusMessageTime,
			},
		},
		{
			name:      "experiment message with nullable fields unset",
			msg:       expMsg,
			messageID: 101,
			want: &essmsgpb.StatusMessage{
				Name:    "experiments/123/statusMessages/101",
				Content: expMsg,
			},
			wantRow: StatusMessageRow{
				Xid:            123,
				MessageID:      101,
				Severity:       int64(essmsgpb.StructuredMessage_ERROR),
				Source:         int64(essmsgpb.StructuredMessage_XMANAGER_BACKEND),
				UserFacingText: "Experiment message",
				CreateTime:     testStatusMessageTime,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			row, err := StatusMessageProtoToRow(tc.msg, tc.messageID, tc.eid, tc.previousMsg)
			if err != nil {
				t.Fatalf("StatusMessageProtoToRow() failed: %v", err)
			}
			if diff := cmp.Diff(tc.wantRow, row, cmpopts.IgnoreFields(StatusMessageRow{}, "UserFacingURL", "PreviousMessage", "AuxiliaryInfo")); diff != "" {
				t.Errorf("StatusMessageProtoToRow() row diff (-want +got):\n%s", diff)
			}
			if (tc.previousMsg == nil) != (row.PreviousMessage == nil) {
				t.Errorf("row.PreviousMessage nil = %v, want %v", row.PreviousMessage == nil, tc.previousMsg == nil)
			}
			if args := row.GetInsertArgs(); len(args) != 11 || args["messageid"] != tc.messageID {
				t.Errorf("row.GetInsertArgs() = %v, want 11 entries with messageid=%d", args, tc.messageID)
			}
			got, err := StatusMessageRowToProto(row)
			if err != nil {
				t.Fatalf("StatusMessageRowToProto() failed: %v", err)
			}
			if diff := cmp.Diff(tc.want, got, protocmp.Transform()); diff != "" {
				t.Errorf("StatusMessageRowToProto() diff (-want +got):\n%s", diff)
			}
		})
	}
}

func TestStatusMessageRowToProto_PreservesEmptyNonNilProtoBytes(t *testing.T) {
	row := StatusMessageRow{
		Xid:           123,
		MessageID:     1,
		UserFacingURL: []byte{},
		AuxiliaryInfo: []byte{},
		CreateTime:    testStatusMessageTime,
	}
	got, err := StatusMessageRowToProto(row)
	if err != nil {
		t.Fatalf("StatusMessageRowToProto() failed: %v", err)
	}
	if got.GetContent().GetUserFacingUrl() == nil {
		t.Error("UserFacingUrl = nil for empty non-nil byte slice, want empty *essmsgpb.Url")
	}
	if got.GetContent().GetAuxiliaryInfo() == nil {
		t.Error("AuxiliaryInfo = nil for empty non-nil byte slice, want empty *essmsgpb.AuxiliaryInfo")
	}
}

func TestStatusMessageProtoToRow_DefaultCreateTime(t *testing.T) {
	before := time.Now().UTC()
	row, err := StatusMessageProtoToRow(&essmsgpb.StructuredMessage{
		Parent:         "experiments/123",
		UserFacingText: "msg",
	}, 1, nil, nil)
	after := time.Now().UTC()
	if err != nil {
		t.Fatalf("StatusMessageProtoToRow() failed: %v", err)
	}
	if row.CreateTime.Location() != time.UTC || row.CreateTime.Before(before) || row.CreateTime.After(after) {
		t.Errorf("row.CreateTime = %v (loc=%v), want UTC between %v and %v", row.CreateTime, row.CreateTime.Location(), before, after)
	}
}

func TestStatusMessageConversionErrors(t *testing.T) {
	corrupt := []byte{0xff, 0xff, 0xff}

	if _, err := StatusMessageProtoToRow(&essmsgpb.StructuredMessage{Parent: "invalid"}, 1, nil, nil); err == nil {
		t.Error("StatusMessageProtoToRow(invalid parent) = nil, want error")
	}

	badRows := []StatusMessageRow{
		{Xid: 1, MessageID: 1, UserFacingURL: corrupt, CreateTime: testStatusMessageTime},
		{Xid: 1, MessageID: 1, AuxiliaryInfo: corrupt, CreateTime: testStatusMessageTime},
	}
	for i, r := range badRows {
		if _, err := StatusMessageRowToProto(r); err == nil {
			t.Errorf("StatusMessageRowToProto(badRows[%d]) = nil, want error", i)
		}
	}
}
