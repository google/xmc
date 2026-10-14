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
	xidmsgpb "xmanager_cloud/api/protos/xid"
	pb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/prototest"
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
			want:       prototest.Modify(current, func(x *pb.Artifact) { x.Acls = updated.Acls }),
		},
		{
			name:       "update url",
			current:    current,
			updated:    updated,
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{"url"}},
			want:       prototest.Modify(current, func(x *pb.Artifact) { x.Url = updated.Url; x.Priority = 200 }),
		},
		{
			name:       "update priority",
			current:    current,
			updated:    updated,
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{"priority"}},
			want:       prototest.Modify(current, func(x *pb.Artifact) { x.Priority = 200; x.Url.Priority = 200 }),
		},
		{
			name:       "update tags",
			current:    current,
			updated:    updated,
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{"tags"}},
			want:       prototest.Modify(current, func(x *pb.Artifact) { x.Tags = updated.Tags }),
		},
		{
			name:       "update notes",
			current:    current,
			updated:    updated,
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{"notes"}},
			want:       prototest.Modify(current, func(x *pb.Artifact) { x.Notes = updated.Notes }),
		},
		{
			name:       "update lifecycle_phase",
			current:    current,
			updated:    updated,
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{"lifecycle_phase"}},
			want:       prototest.Modify(current, func(x *pb.Artifact) { x.LifecyclePhase = updated.LifecyclePhase }),
		},
		{
			name:       "update data_type",
			current:    current,
			updated:    updated,
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{"data_type"}},
			want:       prototest.Modify(current, func(x *pb.Artifact) { x.DataType = updated.DataType }),
		},
		{
			name:       "update mime_type",
			current:    current,
			updated:    updated,
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{"mime_type"}},
			want:       prototest.Modify(current, func(x *pb.Artifact) { x.MimeType = updated.MimeType }),
		},
		{
			name:       "update multiple fields",
			current:    current,
			updated:    updated,
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{"notes", "tags"}},
			want:       prototest.Modify(current, func(x *pb.Artifact) { x.Tags = updated.Tags; x.Notes = updated.Notes }),
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

func TestArtifactSearchableContent(t *testing.T) {
	testCases := []struct {
		name    string
		payload *pb.ArtifactPayload
		want    string
	}{
		{name: "nil payload", payload: nil, want: ""},
		{name: "empty payload", payload: &pb.ArtifactPayload{}, want: ""},
		{
			name:    "managed xprof",
			payload: &pb.ArtifactPayload{Type: &pb.ArtifactPayload_ManagedXprof{ManagedXprof: &pb.ManagedXprofArtifact{DisplayName: "dn", RunGroup: "rg"}}},
			want:    "dn rg",
		},
		{
			name:    "managed xprof nil message",
			payload: &pb.ArtifactPayload{Type: &pb.ArtifactPayload_ManagedXprof{}},
			want:    "",
		},
		{
			name:    "code source skips empty fields",
			payload: &pb.ArtifactPayload{Type: &pb.ArtifactPayload_CodeSource{CodeSource: &pb.CodeSourceArtifact{Title: "t", CodeBlock: "cb", CodeUrl: "u"}}},
			want:    "t cb u",
		},
		{
			name:    "text",
			payload: &pb.ArtifactPayload{Type: &pb.ArtifactPayload_Text{Text: &pb.TextArtifact{Title: "t", Text: "body"}}},
			want:    "t body",
		},
		{
			name:    "url",
			payload: &pb.ArtifactPayload{Type: &pb.ArtifactPayload_Url{Url: &pb.UrlArtifact{Url: "http://x", DisplayName: "X"}}},
			want:    "http://x X",
		},
		{
			name:    "chart",
			payload: &pb.ArtifactPayload{Type: &pb.ArtifactPayload_Chart{Chart: &pb.ChartArtifact{Title: "c", MetricNames: []string{"acc", "loss"}}}},
			want:    "c acc loss",
		},
		{
			name:    "chart without title",
			payload: &pb.ArtifactPayload{Type: &pb.ArtifactPayload_Chart{Chart: &pb.ChartArtifact{MetricNames: []string{"acc"}}}},
			want:    "acc",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ArtifactSearchableContent(tc.payload); got != tc.want {
				t.Errorf("ArtifactSearchableContent() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestArtifactPriority(t *testing.T) {
	testCases := []struct {
		name string
		art  *pb.Artifact
		want int64
	}{
		{name: "nil artifact", art: nil, want: 0},
		{name: "top-level priority wins", art: &pb.Artifact{Priority: 5, Url: &pb.Url{Priority: 9}}, want: 5},
		{name: "falls back to url priority", art: &pb.Artifact{Url: &pb.Url{Priority: 9}}, want: 9},
		{name: "unset defaults to zero", art: &pb.Artifact{}, want: 0},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ArtifactPriority(tc.art); got != tc.want {
				t.Errorf("ArtifactPriority() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestArtifactTags(t *testing.T) {
	testCases := []struct {
		name string
		art  *pb.Artifact
		want []string
	}{
		{name: "nil tags default to empty slice", art: &pb.Artifact{}, want: []string{}},
		{name: "tags preserved", art: &pb.Artifact{Tags: []string{"a", "b"}}, want: []string{"a", "b"}},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := ArtifactTags(tc.art)
			if got == nil {
				t.Fatalf("ArtifactTags() = nil, want non-nil slice")
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("ArtifactTags() returned unexpected diff (-want +got):\n%s", diff)
			}
		})
	}
}
