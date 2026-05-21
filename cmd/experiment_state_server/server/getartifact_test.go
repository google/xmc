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
package server

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"golang/cmp/cmp"
	"golang/protobuf/v2/proto/proto"
	"golang/protobuf/v2/testing/protocmp/protocmp"
	"xmanager_cloud/auth/auth"
	"xmanager_cloud/experiment_state_server/config"
	apipb "xmanager_cloud/experiment_state_server/proto/api_go_proto"
	msgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/storage/storage"
	xidgrpcpb "xmanager_cloud/xid_service/proto/api_go_grpc_proto"
	xidmsgpb "xmanager_cloud/xid_service/proto/messages_go_proto"
	"task/go/status"
)

func TestGetArtifact_HappyPath(t *testing.T) {
	ctx := context.Background()
	server, _ := createExperimentStateServer(ctx, t)
	exp := mustCreateExperiment(ctx, t, server, nil)

	artifact := &msgpb.Artifact{
		Url: &msgpb.Url{
			Url: "http://www.google.com",
		},
		Acls: &xidmsgpb.XidAcls{
			Writers: []*xidmsgpb.XidAcls_Role{{Principal: &xidmsgpb.XidAcls_Role_User{User: "test@example.com"}}},
		},
	}
	createdArtifact := mustCreateArtifact(ctx, t, server, exp.GetName(), artifact)

	req := &apipb.GetArtifactRequest{Name: createdArtifact.GetName()}
	got, err := server.GetArtifact(ctx, req)
	if err != nil {
		t.Fatalf("GetArtifact(name=%q) returned an unexpected error: %v", req.GetName(), err)
	}

	opts := []cmp.Option{
		protocmp.Transform(),
	}
	if diff := cmp.Diff(createdArtifact, got, opts...); diff != "" {
		t.Errorf("GetArtifact(name=%q) mismatch (-want +got):\n%s", req.GetName(), diff)
	}
}

func TestGetArtifact_BadPaths(t *testing.T) {
	ctx := context.Background()
	server, _ := createExperimentStateServer(ctx, t)

	testCases := []struct {
		name    string
		req     *apipb.GetArtifactRequest
		wantErr error
	}{
		{
			name:    "invalid_name",
			req:     &apipb.GetArtifactRequest{Name: "invalid-name"},
			wantErr: status.ErrInvalidArgument,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := server.GetArtifact(ctx, tc.req)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("GetArtifact(name=%q) returned error %v, want %v", tc.req.GetName(), err, tc.wantErr)
			}
		})
	}
}

func TestGetArtifact_DependencyFailures(t *testing.T) {
	ctx := context.Background()
	cfg := &config.Config{}

	goodReq := &apipb.GetArtifactRequest{Name: "artifacts/123"}

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
			name: "xid_get_fails",
			xidClient: &mockXidClient{
				getXidEntryErr: fmt.Errorf("XID get failed"),
			},
			storage: &storage.MockStorage{},
			wantErr: status.ErrInternal,
		},
		{
			name: "xid_get_permission_denied",
			xidClient: &mockXidClient{
				getXidEntryErr: status.ErrPermissionDenied,
			},
			storage: &storage.MockStorage{},
			wantErr: status.ErrPermissionDenied,
		},
		{
			name: "xid_get_not_found",
			xidClient: &mockXidClient{
				getXidEntryErr: status.ErrNotFound,
			},
			storage: &storage.MockStorage{},
			wantErr: status.ErrNotFound,
		},
		{
			name: "storage_fetch_fails",
			xidClient: &mockXidClient{
				getXidEntryResp: &xidmsgpb.XidEntry{Name: proto.String("xids/123")},
			},
			storage: &storage.MockStorage{
				FetchArtifactErr: fmt.Errorf("storage fetch failed"),
			},
			wantErr: status.ErrInternal,
		},
		{
			name: "storage_fetch_not_found",
			xidClient: &mockXidClient{
				getXidEntryResp: &xidmsgpb.XidEntry{Name: proto.String("xids/123")},
			},
			storage: &storage.MockStorage{
				FetchArtifactErr: status.ErrNotFound,
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
			_, err := server.GetArtifact(ctx, goodReq)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("GetArtifact(name=%q) returned error %v, want %v", goodReq.GetName(), err, tc.wantErr)
			}
		})
	}
}
