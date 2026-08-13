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

	"golang/cmp/cmp"
	"golang/protobuf/v2/proto/proto"
	"golang/protobuf/v2/testing/protocmp/protocmp"
	"task/go/status"
	"xmanager_cloud/auth/auth"
	"xmanager_cloud/experiment_state_server/config"
	"xmanager_cloud/storage/storage"

	apipb "xmanager_cloud/experiment_state_server/proto/api_go_proto"
	msgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	xidgrpcpb "xmanager_cloud/xid_service/proto/api_go_grpc_proto"
	xidapipb "xmanager_cloud/xid_service/proto/api_go_proto"
	xidmsgpb "xmanager_cloud/xid_service/proto/messages_go_proto"
)

func TestGetExperimentStatus_HappyPath(t *testing.T) {
	ctx := context.Background()
	server, _ := createExperimentStateServer(ctx, t)

	// Create an experiment first to test GetExperimentStatus
	exp := &msgpb.Experiment{
		Title: "test experiment",
		Acls: &xidmsgpb.XidAcls{
			Owners: []*xidmsgpb.XidAcls_Role{{Principal: &xidmsgpb.XidAcls_Role_User{User: "test@example.com"}}},
		},
	}
	createdExp := mustCreateExperiment(ctx, t, server, exp)

	req := &apipb.GetExperimentStatusRequest{Name: createdExp.GetName()}
	got, err := server.GetExperimentStatus(ctx, req)
	if err != nil {
		t.Fatalf("GetExperimentStatus(%v) returned an unexpected error: %v", req, err)
	}

	opts := []cmp.Option{
		protocmp.Transform(),
	}
	if diff := cmp.Diff(createdExp.GetStatus(), got, opts...); diff != "" {
		t.Errorf("GetExperimentStatus(%v) mismatch (-want +got):\n%s", req, diff)
	}
}

func TestGetExperimentStatus_BadPaths(t *testing.T) {
	ctx := context.Background()
	server, _ := createExperimentStateServer(ctx, t)

	testCases := []struct {
		name    string
		req     *apipb.GetExperimentStatusRequest
		wantErr error
	}{
		{
			name:    "invalid_name",
			req:     &apipb.GetExperimentStatusRequest{Name: "invalid-name"},
			wantErr: status.ErrInvalidArgument,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := server.GetExperimentStatus(ctx, tc.req)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("GetExperimentStatus(%v) returned error %v, want %v", tc.req, err, tc.wantErr)
			}
		})
	}
}

func TestGetExperimentStatus_DependencyFailures(t *testing.T) {
	ctx := context.Background()
	cfg := &config.Config{}

	goodReq := &apipb.GetExperimentStatusRequest{Name: "experiments/123"}

	testCases := []struct {
		name          string
		authenticator auth.AuthorizerInterface
		xidClient     xidgrpcpb.XidServiceClient
		storage       storage.Storage
		wantErr       error
	}{
		{
			name:          "auth_fails",
			authenticator: &mockAuthenticator{err: status.ErrUnauthenticated},
			xidClient:     &mockXidClient{},
			storage:       &storage.MockStorage{},
			wantErr:       status.ErrUnauthenticated,
		},
		{
			name: "xid_check_user_authorization_fails",
			xidClient: &mockXidClient{
				checkUserAuthorizationErr: status.ErrPermissionDenied,
			},
			storage: &storage.MockStorage{},
			wantErr: status.ErrPermissionDenied,
		},
		{
			name: "xid_check_user_authorization_unauthorized",
			xidClient: &mockXidClient{
				checkUserAuthorizationResp: &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(false)},
			},
			storage: &storage.MockStorage{},
			wantErr: status.ErrPermissionDenied,
		},
		{
			name: "storage_fetch_fails",
			xidClient: &mockXidClient{
				checkUserAuthorizationResp: &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)},
			},
			storage: &storage.MockStorage{
				FetchExperimentStatusErr: fmt.Errorf("storage fetch failed"),
			},
			wantErr: status.ErrInternal,
		},
		{
			name: "storage_fetch_not_found",
			xidClient: &mockXidClient{
				checkUserAuthorizationResp: &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)},
			},
			storage: &storage.MockStorage{
				FetchExperimentStatusErr: status.ErrNotFound,
			},
			wantErr: status.ErrNotFound,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.authenticator == nil {
				tc.authenticator = createTestAuthorizer(ctx, t)
			}
			server := New(tc.storage, cfg, tc.authenticator, tc.xidClient, nil)
			_, err := server.GetExperimentStatus(ctx, goodReq)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("GetExperimentStatus(%v) returned error %v, want %v", goodReq, err, tc.wantErr)
			}
		})
	}
}
