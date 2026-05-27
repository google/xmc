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
	"xmanager_cloud/storage/data/data"
	"xmanager_cloud/storage/spanner/schema/xidschema"
	pb "xmanager_cloud/xid_service/proto/messages_go_proto"
)

var (
	testXidEntry = pb.XidEntry{
		Creator: proto.String("test-user"),
		Origin: &pb.OriginatingSystem{
			Endpoint: proto.String("test-endpoint"),
		},
		Acls: &pb.XidAcls{
			Owners: []*pb.XidAcls_Role{
				&pb.XidAcls_Role{
					Principal: &pb.XidAcls_Role_User{
						User: "test-user1",
					},
				},
			},
			Writers: []*pb.XidAcls_Role{
				&pb.XidAcls_Role{
					Principal: &pb.XidAcls_Role_User{
						User: "test-user2",
					},
				},
			},
			Readers: []*pb.XidAcls_Role{
				&pb.XidAcls_Role{
					Principal: &pb.XidAcls_Role_User{
						User: "test-user3",
					},
				},
			},
		},
		CreateTime: timestamppb.New(time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC)),
		UpdateTime: timestamppb.New(time.Date(2024, time.January, 2, 0, 0, 0, 0, time.UTC)),
	}
)

func TestXidRoundTrip(t *testing.T) {
	xidEntryWithNullTimestamps := proto.Clone(&testXidEntry).(*pb.XidEntry)
	xidEntryWithNullTimestamps.CreateTime = nil
	xidEntryWithNullTimestamps.UpdateTime = nil

	xidEntryWithParentXid := proto.Clone(&testXidEntry).(*pb.XidEntry)
	xidEntryWithParentXid.ParentXid = proto.String("xids/42")

	testCases := []struct {
		name     string
		xidEntry *pb.XidEntry
		xid      int64
		wantName string
	}{
		{
			name:     "with timestamps",
			xidEntry: &testXidEntry,
			xid:      1,
			wantName: "xids/1",
		},
		{
			name:     "with null timestamps",
			xidEntry: xidEntryWithNullTimestamps,
			xid:      2,
			wantName: "xids/2",
		},
		{
			name:     "with parent xid",
			xidEntry: xidEntryWithParentXid,
			xid:      3,
			wantName: "xids/3",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var keys []string
			var values []any

			xidParams, err := XIDProtoToParamsMap(tc.xidEntry)
			if err != nil {
				t.Fatalf("Failed to convert XidEntry proto to params map: %v", err)
			}

			for k := range xidParams {
				keys = append(keys, k)
				values = append(values, xidParams[k])
			}

			keys = append(keys, "Xid")
			values = append(values, tc.xid)

			row, err := spanner.NewRow(keys, values)
			if err != nil {
				t.Fatalf("Failed to create Spanner row: %v", err)
			}

			got, err := XIDRowToProto(row)
			if err != nil {
				t.Fatalf("Failed to convert Spanner row to XidEntry proto: %v", err)
			}

			if diff := cmp.Diff(tc.xidEntry, got, protocmp.Transform(), protocmp.IgnoreFields(&pb.XidEntry{}, "name")); diff != "" {
				t.Errorf("XidRowToProto() returned unexpected diff (-want +got):\n%s", diff)
			}

			if got.GetName() != tc.wantName {
				t.Errorf("XidRowToProto() returned unexpected name: got %q, want %q", got.GetName(), tc.wantName)
			}
		})
	}
}

func TestXIDRowToProto_MissingColumn(t *testing.T) {
	keys := []string{"Xid"}
	values := []any{int64(123)}
	row, err := spanner.NewRow(keys, values)
	if err != nil {
		t.Fatalf("spanner.NewRow(%v, %v) failed: %v", keys, values, err)
	}

	if _, err := XIDRowToProto(row); err == nil {
		t.Errorf("XIDRowToProto() with missing columns succeeded, want error")
	}
}

func TestXIDEntryToMutation(t *testing.T) {
	xid := int64(123)
	entry := &testXidEntry
	want := spanner.Update(
		xidschema.XIDTableName,
		[]string{
			"BitReversedXid",
			"Acls",
			"UpdateTime",
		},
		[]any{
			data.ReverseBits(xid),
			entry.Acls,
			entry.UpdateTime.AsTime(),
		},
	)
	got := XIDEntryToMutation(xid, entry)
	if diff := cmp.Diff(want, got, protocmp.Transform(), cmp.AllowUnexported(spanner.Mutation{})); diff != "" {
		t.Errorf("XIDEntryToMutation() returned unexpected diff (-want +got):\n%s", diff)
	}
}
