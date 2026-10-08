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
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
	fieldmaskpb "google.golang.org/protobuf/types/known/fieldmaskpb"
	pb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	xidmsgpb "xmanager_cloud/xid_service/proto/messages_go_proto"
)

func TestMergeExperiments(t *testing.T) {
	current := &pb.Experiment{
		Title:    "Current Title",
		Priority: 100,
		Urls:     []*pb.Url{},
		Tags:     []string{"current_tag"},
		Notes:    "Current notes",
		Acls: &xidmsgpb.XidAcls{
			Writers: []*xidmsgpb.XidAcls_Role{
				{Principal: &xidmsgpb.XidAcls_Role_User{User: "current@example.com"}},
			},
		},
	}
	updated := &pb.Experiment{
		Title:    "Updated Title",
		Priority: 200,
		Urls:     []*pb.Url{{Url: "http://updated.com"}},
		Tags:     []string{"updated_tag"},
		Notes:    "Updated notes",
		Acls: &xidmsgpb.XidAcls{
			Writers: []*xidmsgpb.XidAcls_Role{
				{Principal: &xidmsgpb.XidAcls_Role_User{User: "updated@example.com"}},
			},
		},
	}

	testCases := []struct {
		name       string
		current    *pb.Experiment
		updated    *pb.Experiment
		updateMask *fieldmaskpb.FieldMask
		want       *pb.Experiment
		wantErr    bool
	}{
		{
			name:       "update acls",
			current:    current,
			updated:    updated,
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{"acls"}},
			want: &pb.Experiment{
				Title:    "Current Title",
				Priority: 100,
				Urls:     []*pb.Url{},
				Tags:     []string{"current_tag"},
				Notes:    "Current notes",
				Acls:     updated.Acls,
			},
		},
		{
			name:       "update title",
			current:    current,
			updated:    updated,
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{"title"}},
			want: &pb.Experiment{
				Title:    "Updated Title",
				Priority: 100,
				Urls:     []*pb.Url{},
				Tags:     []string{"current_tag"},
				Notes:    "Current notes",
				Acls:     current.Acls,
			},
		},
		{
			name:       "update priority",
			current:    current,
			updated:    updated,
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{"priority"}},
			want: &pb.Experiment{
				Title:    "Current Title",
				Priority: 200,
				Urls:     []*pb.Url{},
				Tags:     []string{"current_tag"},
				Notes:    "Current notes",
				Acls:     current.Acls,
			},
		},
		{
			name:       "update urls",
			current:    current,
			updated:    updated,
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{"urls"}},
			want: &pb.Experiment{
				Title:    "Current Title",
				Priority: 100,
				Urls:     []*pb.Url{{Url: "http://updated.com"}},
				Tags:     []string{"current_tag"},
				Notes:    "Current notes",
				Acls:     current.Acls,
			},
		},
		{
			name:       "update tags",
			current:    current,
			updated:    updated,
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{"tags"}},
			want: &pb.Experiment{
				Title:    "Current Title",
				Priority: 100,
				Urls:     []*pb.Url{},
				Tags:     []string{"updated_tag"},
				Notes:    "Current notes",
				Acls:     current.Acls,
			},
		},
		{
			name:       "update notes",
			current:    current,
			updated:    updated,
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{"notes"}},
			want: &pb.Experiment{
				Title:    "Current Title",
				Priority: 100,
				Urls:     []*pb.Url{},
				Tags:     []string{"current_tag"},
				Notes:    "Updated notes",
				Acls:     current.Acls,
			},
		},
		{
			name:       "update multiple fields",
			current:    current,
			updated:    updated,
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{"title", "priority"}},
			want: &pb.Experiment{
				Title:    "Updated Title",
				Priority: 200,
				Urls:     []*pb.Url{},
				Tags:     []string{"current_tag"},
				Notes:    "Current notes",
				Acls:     current.Acls,
			},
		},
		{
			name:       "empty mask",
			current:    current,
			updated:    updated,
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{}},
			wantErr:    true,
		},
		{
			name:       "invalid field",
			current:    current,
			updated:    updated,
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{"invalid_field"}},
			wantErr:    true,
		},
		{
			name:    "update title with empty",
			current: current,
			updated: &pb.Experiment{
				Title:    "",
				Priority: 200,
				Urls:     []*pb.Url{{Url: "http://updated.com"}},
				Tags:     []string{"updated_tag"},
				Notes:    "Updated notes",
				Acls:     updated.Acls,
			},
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{"title"}},
			wantErr:    true,
		},
		{
			name:       "nil current",
			current:    nil,
			updated:    updated,
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{"title"}},
			wantErr:    true,
		},
		{
			name:       "nil updated",
			current:    current,
			updated:    nil,
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{"title"}},
			wantErr:    true,
		},
		{
			name:    "nil urls",
			current: current,
			updated: &pb.Experiment{
				Title:    "Updated Title",
				Priority: 200,
				Urls:     nil,
				Tags:     []string{"updated_tag"},
				Notes:    "Updated notes",
				Acls:     updated.Acls,
			},
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{"urls"}},
			wantErr:    true,
		},
		{
			name:    "nil tags",
			current: current,
			updated: &pb.Experiment{
				Title:    "Updated Title",
				Priority: 200,
				Urls:     []*pb.Url{{Url: "http://updated.com"}},
				Tags:     nil,
				Notes:    "Updated notes",
				Acls:     updated.Acls,
			},
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{"tags"}},
			wantErr:    true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := MergeExperiments(tc.current, tc.updated, tc.updateMask)
			if tc.wantErr {
				if err == nil {
					t.Errorf("MergeExperiments() succeeded, want error")
				}
				return
			}

			if err != nil {
				t.Fatalf("MergeExperiments() failed: %v", err)
			}

			if diff := cmp.Diff(tc.want, got, protocmp.Transform()); diff != "" {
				t.Errorf("MergeExperiments() returned unexpected diff (-want +got):\n%s", diff)
			}
		})
	}
}

func TestExperimentRequiresXIDUpdate(t *testing.T) {
	expWithACLs := &pb.Experiment{
		Name: "experiments/123",
		Acls: &xidmsgpb.XidAcls{
			Writers: []*xidmsgpb.XidAcls_Role{
				{Principal: &xidmsgpb.XidAcls_Role_User{User: "test@example.com"}},
			},
		},
	}

	testCases := []struct {
		name           string
		exp            *pb.Experiment
		updateMask     *fieldmaskpb.FieldMask
		wantXidEntry   *xidmsgpb.XidEntry
		wantUpdateMask *fieldmaskpb.FieldMask
		wantErr        bool
	}{
		{
			name:       "acls in update mask",
			exp:        expWithACLs,
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{"acls", "title"}},
			wantXidEntry: &xidmsgpb.XidEntry{
				Name: proto.String("xids/123"),
				Acls: expWithACLs.Acls,
			},
			wantUpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"acls"}},
		},
		{
			name:       "acls not in update mask",
			exp:        expWithACLs,
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{"title"}},
			wantXidEntry: &xidmsgpb.XidEntry{
				Name: proto.String("xids/123"),
			},
			wantUpdateMask: &fieldmaskpb.FieldMask{Paths: []string{}},
		},
		{
			name:       "nil experiment",
			exp:        nil,
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{"acls"}},
			wantErr:    true,
		},
		{
			name:       "nil update mask",
			exp:        expWithACLs,
			updateMask: nil,
			wantErr:    true,
		},
		{
			name:       "empty update mask",
			exp:        expWithACLs,
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{}},
			wantErr:    true,
		},
		{
			name: "empty acls",
			exp: &pb.Experiment{
				Name: "experiments/123",
				Acls: &xidmsgpb.XidAcls{},
			},
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{"acls"}},
			wantErr:    true,
		},
		{
			name: "invalid experiment name",
			exp: &pb.Experiment{
				Name: "invalid-name",
				Acls: expWithACLs.Acls,
			},
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{"acls"}},
			wantErr:    true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			gotXidEntry, gotUpdateMask, err := ExperimentRequiresXIDUpdate(tc.exp, tc.updateMask)
			if tc.wantErr {
				if err == nil {
					t.Errorf("ExperimentRequiresXIDUpdate() succeeded, want error")
				}
				return
			}

			if err != nil {
				t.Fatalf("ExperimentRequiresXIDUpdate() failed: %v", err)
			}

			if diff := cmp.Diff(tc.wantXidEntry, gotXidEntry, protocmp.Transform()); diff != "" {
				t.Errorf("ExperimentRequiresXIDUpdate() returned unexpected XidEntry diff (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tc.wantUpdateMask, gotUpdateMask, protocmp.Transform()); diff != "" {
				t.Errorf("ExperimentRequiresXIDUpdate() returned unexpected update mask diff (-want +got):\n%s", diff)
			}
		})
	}
}
