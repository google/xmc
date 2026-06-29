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

	"golang/cmp/cmp"
	"golang/protobuf/v2/proto/proto"
	"golang/protobuf/v2/testing/protocmp/protocmp"
	fieldmaskpb "protobuf/field_mask_go_proto"
	pb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	xidmsgpb "xmanager_cloud/xid_service/proto/messages_go_proto"
)

func TestMergeArtifacts(t *testing.T) {
	current := &pb.Artifact{
		Url:            &pb.Url{Url: "http://current.com", Priority: 100},
		Priority:       100,
		Tags:           []string{"current_tag"},
		Notes:          "Current notes",
		LifecyclePhase: pb.Artifact_INPUT,
		DataType:       pb.Artifact_URL,
		MimeType:       "text/plain",
		Acls: &xidmsgpb.XidAcls{
			Writers: []*xidmsgpb.XidAcls_Role{
				{Principal: &xidmsgpb.XidAcls_Role_User{User: "current@example.com"}},
			},
		},
	}
	updated := &pb.Artifact{
		Url:            &pb.Url{Url: "http://updated.com", Priority: 200},
		Priority:       200,
		Tags:           []string{"updated_tag"},
		Notes:          "Updated notes",
		LifecyclePhase: pb.Artifact_OUTPUT,
		DataType:       pb.Artifact_FILE,
		MimeType:       "application/pdf",
		Acls: &xidmsgpb.XidAcls{
			Writers: []*xidmsgpb.XidAcls_Role{
				{Principal: &xidmsgpb.XidAcls_Role_User{User: "updated@example.com"}},
			},
		},
	}

	testCases := []struct {
		name       string
		current    *pb.Artifact
		updated    *pb.Artifact
		updateMask *fieldmaskpb.FieldMask
		want       *pb.Artifact
		wantErr    bool
	}{
		{
			name:       "update acls",
			current:    current,
			updated:    updated,
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{"acls"}},
			want: &pb.Artifact{
				Url:            current.Url,
				Priority:       100,
				Tags:           current.Tags,
				Notes:          current.Notes,
				LifecyclePhase: current.LifecyclePhase,
				DataType:       current.DataType,
				MimeType:       current.MimeType,
				Acls:           updated.Acls,
			},
		},
		{
			name:       "update url",
			current:    current,
			updated:    updated,
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{"url"}},
			want: &pb.Artifact{
				Url:            updated.Url,
				Priority:       200,
				Tags:           current.Tags,
				Notes:          current.Notes,
				LifecyclePhase: current.LifecyclePhase,
				DataType:       current.DataType,
				MimeType:       current.MimeType,
				Acls:           current.Acls,
			},
		},
		{
			name:       "update priority",
			current:    current,
			updated:    updated,
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{"priority"}},
			want: &pb.Artifact{
				Url: &pb.Url{
					Url:         current.Url.Url,
					Icon:        current.Url.Icon,
					DisplayName: current.Url.DisplayName,
					Priority:    200,
				},
				Priority:       200,
				Tags:           current.Tags,
				Notes:          current.Notes,
				LifecyclePhase: current.LifecyclePhase,
				DataType:       current.DataType,
				MimeType:       current.MimeType,
				Acls:           current.Acls,
			},
		},
		{
			name:       "update tags",
			current:    current,
			updated:    updated,
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{"tags"}},
			want: &pb.Artifact{
				Url:            current.Url,
				Priority:       100,
				Tags:           updated.Tags,
				Notes:          current.Notes,
				LifecyclePhase: current.LifecyclePhase,
				DataType:       current.DataType,
				MimeType:       current.MimeType,
				Acls:           current.Acls,
			},
		},
		{
			name:       "update notes",
			current:    current,
			updated:    updated,
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{"notes"}},
			want: &pb.Artifact{
				Url:            current.Url,
				Priority:       100,
				Tags:           current.Tags,
				Notes:          updated.Notes,
				LifecyclePhase: current.LifecyclePhase,
				DataType:       current.DataType,
				MimeType:       current.MimeType,
				Acls:           current.Acls,
			},
		},
		{
			name:       "update lifecycle_phase",
			current:    current,
			updated:    updated,
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{"lifecycle_phase"}},
			want: &pb.Artifact{
				Url:            current.Url,
				Priority:       100,
				Tags:           current.Tags,
				Notes:          current.Notes,
				LifecyclePhase: updated.LifecyclePhase,
				DataType:       current.DataType,
				MimeType:       current.MimeType,
				Acls:           current.Acls,
			},
		},
		{
			name:       "update data_type",
			current:    current,
			updated:    updated,
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{"data_type"}},
			want: &pb.Artifact{
				Url:            current.Url,
				Priority:       100,
				Tags:           current.Tags,
				Notes:          current.Notes,
				LifecyclePhase: current.LifecyclePhase,
				DataType:       updated.DataType,
				MimeType:       current.MimeType,
				Acls:           current.Acls,
			},
		},
		{
			name:       "update mime_type",
			current:    current,
			updated:    updated,
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{"mime_type"}},
			want: &pb.Artifact{
				Url:            current.Url,
				Priority:       100,
				Tags:           current.Tags,
				Notes:          current.Notes,
				LifecyclePhase: current.LifecyclePhase,
				DataType:       current.DataType,
				MimeType:       updated.MimeType,
				Acls:           current.Acls,
			},
		},
		{
			name:       "update multiple fields",
			current:    current,
			updated:    updated,
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{"notes", "tags"}},
			want: &pb.Artifact{
				Url:            current.Url,
				Priority:       100,
				Tags:           updated.Tags,
				Notes:          updated.Notes,
				LifecyclePhase: current.LifecyclePhase,
				DataType:       current.DataType,
				MimeType:       current.MimeType,
				Acls:           current.Acls,
			},
		},
		{
			name: "update payload",
			current: &pb.Artifact{
				Url: current.Url,
				Payload: &pb.ArtifactPayload{
					Type: &pb.ArtifactPayload_Text{
						Text: &pb.TextArtifact{
							Title: "current-title",
							Text:  "current-text",
						},
					},
				},
			},
			updated: &pb.Artifact{
				Url: current.Url,
				Payload: &pb.ArtifactPayload{
					Type: &pb.ArtifactPayload_Text{
						Text: &pb.TextArtifact{
							Title: "updated-title",
							Text:  "updated-text",
						},
					},
				},
			},
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{"payload"}},
			want: &pb.Artifact{
				Url: current.Url,
				Payload: &pb.ArtifactPayload{
					Type: &pb.ArtifactPayload_Text{
						Text: &pb.TextArtifact{
							Title: "updated-title",
							Text:  "updated-text",
						},
					},
				},
			},
		},
		{
			name: "update test payload",
			current: &pb.Artifact{
				Url: current.Url,
				Payload: &pb.ArtifactPayload{
					Type: &pb.ArtifactPayload_TestArtifact{
						TestArtifact: &pb.TestArtifact{
							Title:   "current-title",
							Command: "current-command",
						},
					},
				},
			},
			updated: &pb.Artifact{
				Url: current.Url,
				Payload: &pb.ArtifactPayload{
					Type: &pb.ArtifactPayload_TestArtifact{
						TestArtifact: &pb.TestArtifact{
							Title:   "updated-title",
							Command: "updated-command",
						},
					},
				},
			},
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{"payload"}},
			want: &pb.Artifact{
				Url: current.Url,
				Payload: &pb.ArtifactPayload{
					Type: &pb.ArtifactPayload_TestArtifact{
						TestArtifact: &pb.TestArtifact{
							Title:   "updated-title",
							Command: "updated-command",
						},
					},
				},
			},
		},
		{
			name:       "empty mask",
			current:    current,
			updated:    updated,
			updateMask: &fieldmaskpb.FieldMask{Paths: nil},
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
			name:       "nil current",
			current:    nil,
			updated:    updated,
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{"notes"}},
			wantErr:    true,
		},
		{
			name:       "nil updated",
			current:    current,
			updated:    nil,
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{"notes"}},
			wantErr:    true,
		},
		{
			name:    "nil url",
			current: current,
			updated: &pb.Artifact{
				Url: nil,
			},
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{"url"}},
			wantErr:    true,
		},
		{
			name:    "nil tags",
			current: current,
			updated: &pb.Artifact{
				Tags: nil,
			},
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{"tags"}},
			wantErr:    true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := MergeArtifacts(tc.current, tc.updated, tc.updateMask)
			if tc.wantErr {
				if err == nil {
					t.Errorf("MergeArtifacts() succeeded, want error")
				}
				return
			}

			if err != nil {
				t.Fatalf("MergeArtifacts() failed: %v", err)
			}

			if diff := cmp.Diff(tc.want, got, protocmp.Transform()); diff != "" {
				t.Errorf("MergeArtifacts() returned unexpected diff (-want +got):\n%s", diff)
			}
		})
	}
}

func TestArtifactRequiresXIDUpdate(t *testing.T) {
	artWithACLs := &pb.Artifact{
		Name: "artifacts/123",
		Acls: &xidmsgpb.XidAcls{
			Writers: []*xidmsgpb.XidAcls_Role{
				{Principal: &xidmsgpb.XidAcls_Role_User{User: "test@example.com"}},
			},
		},
	}

	testCases := []struct {
		name           string
		art            *pb.Artifact
		updateMask     *fieldmaskpb.FieldMask
		wantXidEntry   *xidmsgpb.XidEntry
		wantUpdateMask *fieldmaskpb.FieldMask
		wantErr        bool
	}{
		{
			name:       "acls in update mask",
			art:        artWithACLs,
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{"acls", "notes"}},
			wantXidEntry: &xidmsgpb.XidEntry{
				Name: proto.String("xids/123"),
				Acls: artWithACLs.Acls,
			},
			wantUpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"acls"}},
		},
		{
			name:       "acls not in update mask",
			art:        artWithACLs,
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{"notes"}},
			wantXidEntry: &xidmsgpb.XidEntry{
				Name: proto.String("xids/123"),
			},
			wantUpdateMask: &fieldmaskpb.FieldMask{Paths: nil},
		},
		{
			name:       "nil artifact",
			art:        nil,
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{"acls"}},
			wantErr:    true,
		},
		{
			name:       "nil update mask",
			art:        artWithACLs,
			updateMask: nil,
			wantErr:    true,
		},
		{
			name:       "empty update mask",
			art:        artWithACLs,
			updateMask: &fieldmaskpb.FieldMask{Paths: nil},
			wantErr:    true,
		},
		{
			name: "invalid artifact name",
			art: &pb.Artifact{
				Name: "invalid-name",
				Acls: artWithACLs.Acls,
			},
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{"acls"}},
			wantErr:    true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			gotXidEntry, gotUpdateMask, err := ArtifactRequiresXIDUpdate(tc.art, tc.updateMask)
			if tc.wantErr {
				if err == nil {
					t.Errorf("ArtifactRequiresXIDUpdate() succeeded, want error")
				}
				return
			}

			if err != nil {
				t.Fatalf("ArtifactRequiresXIDUpdate() failed: %v", err)
			}

			if diff := cmp.Diff(tc.wantXidEntry, gotXidEntry, protocmp.Transform()); diff != "" {
				t.Errorf("ArtifactRequiresXIDUpdate() returned unexpected XidEntry diff (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tc.wantUpdateMask, gotUpdateMask, protocmp.Transform()); diff != "" {
				t.Errorf("ArtifactRequiresXIDUpdate() returned unexpected update mask diff (-want +got):\n%s", diff)
			}
		})
	}
}
