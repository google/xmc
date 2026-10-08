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
package data

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/testing/protocmp"
	fmpb "protobuf/field_mask_go_proto"
	pb "xmanager_cloud/xid_service/proto/messages_go_proto"
)

func TestMergeXIDEntries(t *testing.T) {
	current := &pb.XidEntry{
		Acls: &pb.XidAcls{
			Owners: []*pb.XidAcls_Role{
				{Principal: &pb.XidAcls_Role_User{User: "user1"}},
			},
		},
	}
	updated := &pb.XidEntry{
		Acls: &pb.XidAcls{
			Owners: []*pb.XidAcls_Role{
				{Principal: &pb.XidAcls_Role_User{User: "user2"}},
			},
		},
	}
	testCases := []struct {
		name       string
		current    *pb.XidEntry
		updated    *pb.XidEntry
		updateMask *fmpb.FieldMask
		want       *pb.XidEntry
		wantErr    bool
	}{
		{
			name:       "update acls",
			current:    current,
			updated:    updated,
			updateMask: &fmpb.FieldMask{Paths: []string{"acls"}},
			want: &pb.XidEntry{
				Acls: updated.Acls,
			},
		},
		{
			name:       "empty mask",
			current:    current,
			updated:    updated,
			updateMask: &fmpb.FieldMask{Paths: []string{}},
			wantErr:    true,
		},
		{
			name:       "invalid field",
			current:    current,
			updated:    updated,
			updateMask: &fmpb.FieldMask{Paths: []string{"invalid_field"}},
			wantErr:    true,
		},
		{
			name:       "update acls with nil",
			current:    current,
			updated:    &pb.XidEntry{Acls: nil},
			updateMask: &fmpb.FieldMask{Paths: []string{"acls"}},
			wantErr:    true,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := MergeXIDEntries(tc.current, tc.updated, tc.updateMask)
			if tc.wantErr {
				if err == nil {
					t.Errorf("MergeXIDEntries() succeeded, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("MergeXIDEntries() failed: %v", err)
			}
			if diff := cmp.Diff(tc.want, got, protocmp.Transform()); diff != "" {
				t.Errorf("MergeXIDEntries() returned unexpected diff (-want +got):\n%s", diff)
			}
		})
	}
}
