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
	xidmsgpb "xmanager_cloud/api/protos/xid"
)

func TestXidProtoAndRowRoundTrip(t *testing.T) {
	now := timestamppb.New(time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC))
	ownerRole := &xidmsgpb.XidAcls_Role{
		Principal: &xidmsgpb.XidAcls_Role_User{User: "owner@example.com"},
	}

	tests := []struct {
		name  string
		entry *xidmsgpb.XidEntry
	}{
		{
			name: "root xid entry without parent",
			entry: &xidmsgpb.XidEntry{
				Name:       proto.String("xids/42"),
				Creator:    proto.String("creator@example.com"),
				Origin:     &xidmsgpb.OriginatingSystem{Endpoint: proto.String("https://endpoint.example.com")},
				Acls:       &xidmsgpb.XidAcls{Owners: []*xidmsgpb.XidAcls_Role{ownerRole}},
				CreateTime: now,
				UpdateTime: now,
			},
		},
		{
			name: "child xid entry with parent_xid",
			entry: &xidmsgpb.XidEntry{
				Name:       proto.String("xids/99"),
				ParentXid:  proto.String("xids/42"),
				Creator:    proto.String("creator@example.com"),
				Origin:     &xidmsgpb.OriginatingSystem{Endpoint: proto.String("https://endpoint.example.com")},
				Acls:       &xidmsgpb.XidAcls{},
				CreateTime: now,
				UpdateTime: now,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			row, err := XidProtoToRow(tc.entry)
			if err != nil {
				t.Fatalf("XidProtoToRow() failed: %v", err)
			}
			if row.Endpoint != tc.entry.GetOrigin().GetEndpoint() {
				t.Errorf("row.Endpoint = %q, want %q", row.Endpoint, tc.entry.GetOrigin().GetEndpoint())
			}
			if args := row.GetInsertArgs(); len(args) != 7 || args["endpoint"] != row.Endpoint {
				t.Errorf("row.GetInsertArgs() = %v, want 7 entries with matching endpoint", args)
			}
			if args := row.GetUpdateArgs(); len(args) != 3 || args["xid"] != row.Xid {
				t.Errorf("row.GetUpdateArgs() = %v, want 3 entries with matching xid", args)
			}
			got, err := XidRowToProto(row)
			if err != nil {
				t.Fatalf("XidRowToProto() failed: %v", err)
			}
			if diff := cmp.Diff(tc.entry, got, protocmp.Transform()); diff != "" {
				t.Errorf("round-trip XidEntry mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestXidConversionErrors(t *testing.T) {
	corrupt := []byte{0xff, 0xff, 0xff}
	badRows := []XidRow{
		{Xid: 1, OriginatingSystem: corrupt},
		{Xid: 1, Acls: corrupt},
	}
	for i, r := range badRows {
		if _, err := XidRowToProto(r); err == nil {
			t.Errorf("XidRowToProto(badRows[%d]) = nil, want error", i)
		}
	}

	badEntries := []*xidmsgpb.XidEntry{
		{Name: proto.String("invalid-name")},
		{ParentXid: proto.String("invalid-parent")},
	}
	for i, entry := range badEntries {
		if _, err := XidProtoToRow(entry); err == nil {
			t.Errorf("XidProtoToRow(badEntries[%d]) = nil, want error", i)
		}
	}
}
