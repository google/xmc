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

	"golang/protobuf/v2/proto/proto"
	"task/go/status"

	"xmanager_cloud/auth/auth"
	"xmanager_cloud/experiment_state_server/config"
	apipb "xmanager_cloud/experiment_state_server/proto/api_go_proto"
	msgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/storage/storage"
	xidgrpcpb "xmanager_cloud/xid_service/proto/api_go_grpc_proto"
	xidapipb "xmanager_cloud/xid_service/proto/api_go_proto"
	xidmsgpb "xmanager_cloud/xid_service/proto/messages_go_proto"
)

func TestDeleteArtifact_HappyPath(t *testing.T) {
	ctx := context.Background()
	server, _ := createExperimentStateServer(ctx, t)
	exp := mustCreateExperiment(ctx, t, server, nil)

	artifact := &msgpb.Artifact{
		Url: &msgpb.Url{
			Url: "http://www.google.com",
		},
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
	}
	artifact = mustCreateArtifact(ctx, t, server, exp.GetName(), artifact)

	_, err := server.DeleteArtifact(ctx, &apipb.DeleteArtifactRequest{Name: artifact.GetName()})
	if err != nil {
		t.Fatalf("DeleteArtifact() returned an unexpected error: %v", err)
	}

	_, err = server.GetArtifact(ctx, &apipb.GetArtifactRequest{Name: artifact.GetName()})
	if !errors.Is(err, status.ErrNotFound) {
		t.Errorf("GetArtifact() after DeleteArtifact() returned error %v, want %v", err, status.ErrNotFound)
	}
}

func TestDeleteArtifact_BadPaths(t *testing.T) {
	ctx := context.Background()
	server, _ := createExperimentStateServer(ctx, t)
	exp := mustCreateExperiment(ctx, t, server, nil)
	_ = mustCreateArtifact(ctx, t, server, exp.GetName(), nil)

	testCases := []struct {
		name    string
		req     *apipb.DeleteArtifactRequest
		wantErr error
	}{
		{
			name:    "invalid_name",
			req:     &apipb.DeleteArtifactRequest{Name: "invalid-name"},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name:    "not_found",
			req:     &apipb.DeleteArtifactRequest{Name: "artifacts/123"},
			wantErr: status.ErrNotFound,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := server.DeleteArtifact(ctx, tc.req)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("DeleteArtifact() returned error %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestDeleteArtifact_DependencyFailures(t *testing.T) {
	ctx := context.Background()
	cfg := &config.Config{}

	goodReq := &apipb.DeleteArtifactRequest{Name: "artifacts/123"}
	artifact := &msgpb.Artifact{
		Name:   "artifacts/123",
		Parent: "experiments/456",
	}

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
			name: "xid_check_auth_fails",
			xidClient: &mockXidClient{
				checkUserAuthorizationErr: status.ErrPermissionDenied,
			},
			storage: &storage.MockStorage{
				Artifact: artifact,
			},
			wantErr: status.ErrPermissionDenied,
		},
		{
			name: "xid_check_auth_permission_denied",
			xidClient: &mockXidClient{
				checkUserAuthorizationResp: &xidapipb.CheckUserAuthorizationResponse{
					Authorized: proto.Bool(false),
				},
			},
			storage: &storage.MockStorage{
				Artifact: artifact,
			},
			wantErr: status.ErrPermissionDenied,
		},
		{
			name: "storage_delete_fails",
			xidClient: &mockXidClient{
				checkUserAuthorizationResp: &xidapipb.CheckUserAuthorizationResponse{
					Authorized: proto.Bool(true),
				},
			},
			storage: &storage.MockStorage{
				Artifact:          artifact,
				DeleteArtifactErr: fmt.Errorf("storage delete failed"),
			},
			wantErr: status.ErrInternal,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.authenticator == nil {
				tc.authenticator = createTestAuthorizer(ctx, t)
			}
			server := New(tc.storage, cfg, tc.authenticator, tc.xidClient, nil, nil)
			_, err := server.DeleteArtifact(ctx, goodReq)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("DeleteArtifact() returned error %v, want %v", err, tc.wantErr)
			}
		})
	}
}
