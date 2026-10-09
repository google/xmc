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
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
	"xmanager_cloud/auth/auth"
	apipb "xmanager_cloud/experiment_state_server/proto/api_go_proto"
	msgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/storage/storage"
	xidgrpcpb "xmanager_cloud/xid_service/proto/api_go_grpc_proto"
	xidapipb "xmanager_cloud/xid_service/proto/api_go_proto"
	xidmsgpb "xmanager_cloud/xid_service/proto/messages_go_proto"

	fieldmaskpb "google.golang.org/protobuf/types/known/fieldmaskpb"
)

func TestUpdateExperiment_HappyPath(t *testing.T) {
	ctx := context.Background()
	server, _ := createExperimentStateServer(ctx, t)

	updatedAcls := &xidmsgpb.XidAcls{
		Owners: []*xidmsgpb.XidAcls_Role{{Principal: &xidmsgpb.XidAcls_Role_AllAuthenticatedUsers{AllAuthenticatedUsers: true}}},
	}

	testCases := []struct {
		name       string
		updateExp  *msgpb.Experiment
		updateMask *fieldmaskpb.FieldMask
	}{
		{
			name: "update_title",
			updateExp: &msgpb.Experiment{
				Title: "new title",
			},
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{"title"}},
		},
		{
			name: "update_priority",
			updateExp: &msgpb.Experiment{
				Priority: 200,
			},
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{"priority"}},
		},
		{
			name: "update_urls",
			updateExp: &msgpb.Experiment{
				Urls: []*msgpb.Url{{Url: "http://new.url"}},
			},
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{"urls"}},
		},
		{
			name: "update_tags",
			updateExp: &msgpb.Experiment{
				Tags: []string{"new_tag"},
			},
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{"tags"}},
		},
		{
			name: "update_notes",
			updateExp: &msgpb.Experiment{
				Notes: "new notes",
			},
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{"notes"}},
		},
		{
			name: "update_acls",
			updateExp: &msgpb.Experiment{
				Acls: updatedAcls,
			},
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{"acls"}},
		},
		{
			name: "update_multiple",
			updateExp: &msgpb.Experiment{
				Title:    "another title",
				Priority: 300,
			},
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{"title", "priority"}},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			exp := &msgpb.Experiment{
				Title: "test experiment",
				Acls: &xidmsgpb.XidAcls{
					Owners: []*xidmsgpb.XidAcls_Role{{Principal: &xidmsgpb.XidAcls_Role_User{User: "test@example.com"}}},
				},
				Priority: 100,
				Urls:     []*msgpb.Url{},
				Tags:     []string{"tag1"},
				Notes:    "notes1",
			}
			createdExp := mustCreateExperiment(ctx, t, server, exp)

			tc.updateExp.Name = createdExp.GetName()

			req := &apipb.UpdateExperimentRequest{
				Experiment: tc.updateExp,
				UpdateMask: tc.updateMask,
			}
			got, err := server.UpdateExperiment(ctx, req)
			if err != nil {
				t.Fatalf("UpdateExperiment() returned an unexpected error: %v", err)
			}

			// Check updated fields in response.
			wantExp := proto.Clone(createdExp).(*msgpb.Experiment)
			for _, path := range tc.updateMask.Paths {
				switch path {
				case "title":
					wantExp.Title = tc.updateExp.Title
				case "priority":
					wantExp.Priority = tc.updateExp.Priority
				case "urls":
					wantExp.Urls = tc.updateExp.Urls
				case "tags":
					wantExp.Tags = tc.updateExp.Tags
				case "notes":
					wantExp.Notes = tc.updateExp.Notes
				case "acls":
					wantExp.Acls = tc.updateExp.Acls
				}
			}

			if diff := cmp.Diff(wantExp, got, protocmp.Transform(), protocmp.IgnoreFields(&msgpb.Experiment{}, "create_time", "update_time")); diff != "" {
				t.Errorf("UpdateExperiment() returned diff (-want +got):\n%s", diff)
			}

			// Verify with GetExperiment that update persisted and non-updated fields are unchanged.
			getReq := &apipb.GetExperimentRequest{Name: createdExp.GetName()}
			gotGet, err := server.GetExperiment(ctx, getReq)
			if err != nil {
				t.Fatalf("GetExperiment() returned an unexpected error: %v", err)
			}

			if diff := cmp.Diff(wantExp, gotGet, protocmp.Transform(), protocmp.IgnoreFields(&msgpb.Experiment{}, "create_time", "update_time")); diff != "" {
				t.Errorf("GetExperiment() returned diff (-want +got):\n%s", diff)
			}
		})
	}
}

func TestUpdateExperiment_BadPaths(t *testing.T) {
	ctx := context.Background()
	xidClient := &mockXidClient{
		getXidEntryResp:            &xidmsgpb.XidEntry{Name: proto.String("xids/123")},
		checkUserAuthorizationResp: &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)},
	}
	server := newTestServer(t,
		withXidClient(xidClient),
	)

	testCases := []struct {
		name    string
		req     *apipb.UpdateExperimentRequest
		wantErr error
	}{
		{
			name: "invalid_name",
			req: &apipb.UpdateExperimentRequest{
				Experiment: &msgpb.Experiment{Name: "invalid-name"},
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"title"}},
			},
			wantErr: grpcerr.ErrInvalidArgument,
		},
		{
			name: "empty_mask",
			req: &apipb.UpdateExperimentRequest{
				Experiment: &msgpb.Experiment{Name: "experiments/123"},
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{}},
			},
			wantErr: grpcerr.ErrInternal,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := server.UpdateExperiment(ctx, tc.req)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("UpdateExperiment() returned error %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestUpdateExperiment_DependencyFailures(t *testing.T) {
	ctx := context.Background()

	goodReq := &apipb.UpdateExperimentRequest{
		Experiment: &msgpb.Experiment{Name: "experiments/123", Title: "new title"},
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"title"}},
	}
	goodReqWithAcls := &apipb.UpdateExperimentRequest{
		Experiment: &msgpb.Experiment{
			Name: "experiments/123",
			Acls: &xidmsgpb.XidAcls{},
		},
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"acls"}},
	}

	testCases := []struct {
		name          string
		authenticator auth.AuthorizerInterface
		req           *apipb.UpdateExperimentRequest
		xidClient     xidgrpcpb.XidServiceClient
		storage       storage.Storage
		wantErr       error
	}{
		{
			name:          "auth_fails",
			authenticator: &mockAuthenticator{err: grpcerr.ErrUnauthenticated},
			req:           goodReq,
			xidClient:     &mockXidClient{},
			storage:       &storage.MockStorage{},
			wantErr:       grpcerr.ErrUnauthenticated,
		},
		{
			name: "xid_get_fails",
			req:  goodReq,
			xidClient: &mockXidClient{
				getXidEntryErr:             fmt.Errorf("XID get failed"),
				checkUserAuthorizationResp: &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)},
			},
			storage: &storage.MockStorage{},
			wantErr: grpcerr.ErrInternal,
		},
		{
			name: "xid_get_permission_denied",
			req:  goodReq,
			xidClient: &mockXidClient{
				checkUserAuthorizationResp: &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(false)},
			},
			storage: &storage.MockStorage{},
			wantErr: grpcerr.ErrPermissionDenied,
		},
		{
			name: "storage_update_fails",
			req:  goodReq,
			xidClient: &mockXidClient{
				checkUserAuthorizationResp: &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)},
				getXidEntryResp:            &xidmsgpb.XidEntry{Name: proto.String("xids/123")},
			},
			storage: &storage.MockStorage{
				UpdateExperimentErr: fmt.Errorf("storage update failed"),
			},
			wantErr: grpcerr.ErrInternal,
		},
		{
			name: "xid_update_fails",
			req:  goodReqWithAcls,
			xidClient: &mockXidClient{
				checkUserAuthorizationResp: &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)},
				getXidEntryResp:            &xidmsgpb.XidEntry{Name: proto.String("xids/123")},
				updateXidEntryErr:          fmt.Errorf("XID update failed"),
			},
			storage: &storage.MockStorage{
				Experiment: &msgpb.Experiment{Name: "experiments/123"},
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
			_, err := server.UpdateExperiment(ctx, tc.req)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("UpdateExperiment() returned error %v, want %v", err, tc.wantErr)
			}
		})
	}
}
