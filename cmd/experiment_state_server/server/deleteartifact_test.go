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

	"google.golang.org/protobuf/proto"
	msgpb "xmanager_cloud/api/protos/experiment"
	xidmsgpb "xmanager_cloud/api/protos/xid"
	"xmanager_cloud/internal/auth"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/storage"
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

	_, err := server.DeleteArtifact(ctx, &msgpb.DeleteArtifactRequest{Name: artifact.GetName()})
	if err != nil {
		t.Fatalf("DeleteArtifact() returned an unexpected error: %v", err)
	}

	_, err = server.GetArtifact(ctx, &msgpb.GetArtifactRequest{Name: artifact.GetName()})
	if !errors.Is(err, grpcerr.ErrNotFound) {
		t.Errorf("GetArtifact() after DeleteArtifact() returned error %v, want %v", err, grpcerr.ErrNotFound)
	}
}

func TestDeleteArtifact_BadPaths(t *testing.T) {
	ctx := context.Background()
	server, _ := createExperimentStateServer(ctx, t)
	exp := mustCreateExperiment(ctx, t, server, nil)
	_ = mustCreateArtifact(ctx, t, server, exp.GetName(), nil)

	testCases := []struct {
		name    string
		req     *msgpb.DeleteArtifactRequest
		wantErr error
	}{
		{
			name:    "invalid_name",
			req:     &msgpb.DeleteArtifactRequest{Name: "invalid-name"},
			wantErr: grpcerr.ErrInvalidArgument,
		},
		{
			name:    "not_found",
			req:     &msgpb.DeleteArtifactRequest{Name: "artifacts/123"},
			wantErr: grpcerr.ErrNotFound,
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

	goodReq := &msgpb.DeleteArtifactRequest{Name: "artifacts/123"}
	artifact := &msgpb.Artifact{
		Name:   "artifacts/123",
		Parent: "experiments/456",
	}

	testCases := []struct {
		name          string
		authenticator auth.AuthorizerInterface
		xidClient     xidmsgpb.XidServiceClient
		storage       storage.Storage
		wantErr       error
	}{
		{
			name:          "auth_fails",
			authenticator: &mockAuthenticator{err: grpcerr.ErrUnauthenticated},
			xidClient:     &mockXidClient{},
			storage:       &storage.MockStorage{},
			wantErr:       grpcerr.ErrUnauthenticated,
		},
		{
			name: "xid_check_auth_fails",
			xidClient: &mockXidClient{
				checkUserAuthorizationErr: grpcerr.ErrPermissionDenied,
			},
			storage: &storage.MockStorage{
				Artifact: artifact,
			},
			wantErr: grpcerr.ErrPermissionDenied,
		},
		{
			name: "xid_check_auth_permission_denied",
			xidClient: &mockXidClient{
				checkUserAuthorizationResp: &xidmsgpb.CheckUserAuthorizationResponse{
					Authorized: proto.Bool(false),
				},
			},
			storage: &storage.MockStorage{
				Artifact: artifact,
			},
			wantErr: grpcerr.ErrPermissionDenied,
		},
		{
			name: "storage_delete_fails",
			xidClient: &mockXidClient{
				checkUserAuthorizationResp: &xidmsgpb.CheckUserAuthorizationResponse{
					Authorized: proto.Bool(true),
				},
			},
			storage: &storage.MockStorage{
				Artifact:          artifact,
				DeleteArtifactErr: fmt.Errorf("storage delete failed"),
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
			_, err := server.DeleteArtifact(ctx, goodReq)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("DeleteArtifact() returned error %v, want %v", err, tc.wantErr)
			}
		})
	}
}
