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
package server

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	msgpb "github.com/google/xmc/api/protos/experiment"
	xidmsgpb "github.com/google/xmc/api/protos/xid"
	"github.com/google/xmc/internal/auth"
	"github.com/google/xmc/internal/grpcerr"
	"github.com/google/xmc/internal/storage"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"

	fieldmaskpb "google.golang.org/protobuf/types/known/fieldmaskpb"
)

func TestUpdateArtifact_HappyPath(t *testing.T) {
	ctx := context.Background()
	server, _ := createExperimentStateServer(ctx, t)
	exp := mustCreateExperiment(ctx, t, server, nil)

	updatedAcls := &xidmsgpb.XidAcls{
		Owners: []*xidmsgpb.XidAcls_Role{{Principal: &xidmsgpb.XidAcls_Role_AllAuthenticatedUsers{AllAuthenticatedUsers: true}}},
	}

	testCases := []struct {
		name       string
		update     *msgpb.Artifact
		updateMask *fieldmaskpb.FieldMask
		want       *msgpb.Artifact
	}{
		{
			name: "update_url",
			update: &msgpb.Artifact{
				Url: &msgpb.Url{Url: "http://new.url"},
			},
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{"url"}},
			want: &msgpb.Artifact{
				Url: &msgpb.Url{Url: "http://new.url"},
			},
		},
		{
			name: "update_tags",
			update: &msgpb.Artifact{
				Tags: []string{"new_tag"},
			},
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{"tags"}},
			want: &msgpb.Artifact{
				Tags: []string{"new_tag"},
			},
		},
		{
			name: "update_notes",
			update: &msgpb.Artifact{
				Notes: "new notes",
			},
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{"notes"}},
			want: &msgpb.Artifact{
				Notes: "new notes",
			},
		},
		{
			name: "update_acls",
			update: &msgpb.Artifact{
				Acls: updatedAcls,
			},
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{"acls"}},
			want: &msgpb.Artifact{
				Acls: updatedAcls,
			},
		},
		{
			name: "update_multiple",
			update: &msgpb.Artifact{
				Url:   &msgpb.Url{Url: "http://another.url"},
				Notes: "another note",
			},
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{"url", "notes"}},
			want: &msgpb.Artifact{
				Url:   &msgpb.Url{Url: "http://another.url"},
				Notes: "another note",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			artifact := &msgpb.Artifact{
				Url: &msgpb.Url{Url: "http://www.google.com"},
				Payload: &msgpb.ArtifactPayload{
					Type: &msgpb.ArtifactPayload_Url{
						Url: &msgpb.UrlArtifact{
							Url: "http://www.google.com",
						},
					},
				},
				Acls: &xidmsgpb.XidAcls{
					Owners: []*xidmsgpb.XidAcls_Role{{Principal: &xidmsgpb.XidAcls_Role_User{User: "test@example.com"}}},
				},
				Tags:  []string{"tag1"},
				Notes: "notes1",
			}
			createdArtifact := mustCreateArtifact(ctx, t, server, exp.GetName(), artifact)

			tc.update.Name = createdArtifact.GetName()

			req := &msgpb.UpdateArtifactRequest{
				Artifact:   tc.update,
				UpdateMask: tc.updateMask,
			}
			got, err := server.UpdateArtifact(ctx, req)
			if err != nil {
				t.Fatalf("UpdateArtifact(%q) returned an unexpected error: %v", createdArtifact.GetName(), err)
			}

			// Check updated fields in response.
			for _, path := range tc.updateMask.Paths {
				switch path {
				case "url":
					if diff := cmp.Diff(tc.want.GetUrl(), got.GetUrl(), protocmp.Transform()); diff != "" {
						t.Errorf("UpdateArtifact(%q) url mismatch (-want +got):\n%s", createdArtifact.GetName(), diff)
					}
				case "tags":
					if diff := cmp.Diff(tc.want.GetTags(), got.GetTags(), protocmp.Transform()); diff != "" {
						t.Errorf("UpdateArtifact(%q) tags mismatch (-want +got):\n%s", createdArtifact.GetName(), diff)
					}
				case "notes":
					if got.GetNotes() != tc.want.GetNotes() {
						t.Errorf("UpdateArtifact(%q) notes = %v, want %v", createdArtifact.GetName(), got.GetNotes(), tc.want.GetNotes())
					}
				case "acls":
					if diff := cmp.Diff(tc.want.GetAcls(), got.GetAcls(), protocmp.Transform()); diff != "" {
						t.Errorf("UpdateArtifact(%q) acls mismatch (-want +got):\n%s", createdArtifact.GetName(), diff)
					}
				}
			}

			// Verify with GetArtifact that update persisted and non-updated fields are unchanged.
			getReq := &msgpb.GetArtifactRequest{Name: createdArtifact.GetName()}
			gotGet, err := server.GetArtifact(ctx, getReq)
			if err != nil {
				t.Fatalf("GetArtifact(%q) returned an unexpected error: %v", createdArtifact.GetName(), err)
			}

			for _, path := range tc.updateMask.Paths {
				switch path {
				case "url":
					if diff := cmp.Diff(tc.want.GetUrl(), gotGet.GetUrl(), protocmp.Transform()); diff != "" {
						t.Errorf("GetArtifact(%q) url mismatch (-want +got):\n%s", createdArtifact.GetName(), diff)
					}
				case "tags":
					if diff := cmp.Diff(tc.want.GetTags(), gotGet.GetTags(), protocmp.Transform()); diff != "" {
						t.Errorf("GetArtifact(%q) tags mismatch (-want +got):\n%s", createdArtifact.GetName(), diff)
					}
				case "notes":
					if gotGet.GetNotes() != tc.want.GetNotes() {
						t.Errorf("GetArtifact(%q) notes = %v, want %v", createdArtifact.GetName(), gotGet.GetNotes(), tc.want.GetNotes())
					}
				case "acls":
					if diff := cmp.Diff(tc.want.GetAcls(), gotGet.GetAcls(), protocmp.Transform()); diff != "" {
						t.Errorf("GetArtifact(%q) acls mismatch (-want +got):\n%s", createdArtifact.GetName(), diff)
					}
				}
			}
		})
	}
}

func TestUpdateArtifact_BadPaths(t *testing.T) {
	ctx := context.Background()
	xidClient := &mockXidClient{
		getXidEntryResp:            &xidmsgpb.XidEntry{Name: proto.String("xids/123")},
		checkUserAuthorizationResp: &xidmsgpb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)},
	}
	server := newTestServer(t,
		withStorage(&storage.MockStorage{
			Artifact: &msgpb.Artifact{
				Name:   "artifacts/123",
				Parent: "experiments/123",
			},
		}),
		withXidClient(xidClient),
	)

	testCases := []struct {
		name    string
		req     *msgpb.UpdateArtifactRequest
		wantErr error
	}{
		{
			name: "invalid_name",
			req: &msgpb.UpdateArtifactRequest{
				Artifact: &msgpb.Artifact{
					Name:   "invalid-name",
					Parent: "experiments/123",
				},
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"url"}},
			},
			wantErr: grpcerr.ErrInvalidArgument,
		},
		{
			name: "empty_mask",
			req: &msgpb.UpdateArtifactRequest{
				Artifact: &msgpb.Artifact{
					Name:   "artifacts/123",
					Parent: "experiments/123",
				},
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{}},
			},
			wantErr: grpcerr.ErrInvalidArgument,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := server.UpdateArtifact(ctx, tc.req)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("UpdateArtifact(%q) returned error %v, want %v", tc.req.GetArtifact().GetName(), err, tc.wantErr)
			}
		})
	}
}

func TestUpdateArtifact_DependencyFailures(t *testing.T) {
	ctx := context.Background()

	goodReq := &msgpb.UpdateArtifactRequest{
		Artifact:   &msgpb.Artifact{Name: "artifacts/123", Parent: "experiments/123", Notes: "new notes"},
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"notes"}},
	}
	goodReqWithAcls := &msgpb.UpdateArtifactRequest{
		Artifact: &msgpb.Artifact{
			Name:   "artifacts/123",
			Parent: "experiments/123",
			Acls:   &xidmsgpb.XidAcls{},
		},
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"acls"}},
	}

	testCases := []struct {
		name          string
		authenticator auth.AuthorizerInterface
		req           *msgpb.UpdateArtifactRequest
		xidClient     xidmsgpb.XidServiceClient
		storage       storage.Storage
		wantErr       error
	}{
		{
			name:          "auth_fails",
			authenticator: &mockAuthenticator{err: grpcerr.ErrUnauthenticated},
			req:           goodReq,
			xidClient:     &mockXidClient{},
			storage: &storage.MockStorage{
				Artifact: &msgpb.Artifact{
					Name:   "artifacts/123",
					Parent: "experiments/123",
				},
			},
			wantErr: grpcerr.ErrUnauthenticated,
		},
		{
			name: "xid_get_fails",
			req:  goodReq,
			xidClient: &mockXidClient{
				checkUserAuthorizationResp: &xidmsgpb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)},
				getXidEntryErr:             fmt.Errorf("XID get failed"),
			},
			storage: &storage.MockStorage{
				Artifact: &msgpb.Artifact{
					Name:   "artifacts/123",
					Parent: "experiments/123",
				},
			},
			wantErr: grpcerr.ErrInternal,
		},
		{
			name: "xid_check_permission_denied",
			req:  goodReq,
			xidClient: &mockXidClient{
				checkUserAuthorizationResp: &xidmsgpb.CheckUserAuthorizationResponse{Authorized: proto.Bool(false)},
			},
			storage: &storage.MockStorage{
				Artifact: &msgpb.Artifact{
					Name:   "artifacts/123",
					Parent: "experiments/123",
				},
			},
			wantErr: grpcerr.ErrPermissionDenied,
		},
		{
			name: "xid_get_permission_denied_on_non_acl_update",
			req:  goodReq, // Updates notes, not acls.
			xidClient: &mockXidClient{
				checkUserAuthorizationResp: &xidmsgpb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)},
				getXidEntryErr:             grpcerr.ErrPermissionDenied,
			},
			storage: &storage.MockStorage{
				Artifact: &msgpb.Artifact{
					Name:   "artifacts/123",
					Parent: "experiments/123",
				},
			},
			wantErr: grpcerr.ErrPermissionDenied,
		},
		{
			name: "storage_update_fails",
			req:  goodReq,
			xidClient: &mockXidClient{
				checkUserAuthorizationResp: &xidmsgpb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)},
			},
			storage: &storage.MockStorage{
				Artifact: &msgpb.Artifact{
					Name:   "artifacts/123",
					Parent: "experiments/123",
				},
				UpdateArtifactErr: fmt.Errorf("Storage update failed"),
			},
			wantErr: grpcerr.ErrInternal,
		},
		{
			name: "xid_update_fails",
			req:  goodReqWithAcls,
			xidClient: &mockXidClient{
				checkUserAuthorizationResp: &xidmsgpb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)},
				updateXidEntryErr:          fmt.Errorf("XID update failed"),
			},
			storage: &storage.MockStorage{
				Artifact: &msgpb.Artifact{
					Name:   "artifacts/123",
					Parent: "experiments/123",
				},
			},
			wantErr: grpcerr.ErrInternal,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var opts []testServerOpt
			if tc.storage != nil {
				opts = append(opts, withStorage(tc.storage))
			}
			if tc.authenticator != nil {
				opts = append(opts, withAuthorizer(tc.authenticator))
			}
			if tc.xidClient != nil {
				opts = append(opts, withXidClient(tc.xidClient))
			}
			server := newTestServer(t, opts...)
			_, err := server.UpdateArtifact(ctx, tc.req)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("UpdateArtifact(%q) returned error %v, want %v", tc.req.GetArtifact().GetName(), err, tc.wantErr)
			}
		})
	}
}
