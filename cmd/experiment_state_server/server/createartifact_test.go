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
	"time"

	"golang/cmp/cmp"
	"golang/protobuf/v2/proto/proto"
	"golang/protobuf/v2/testing/protocmp/protocmp"
	"task/go/status"
	"xmanager_cloud/auth/auth"
	"xmanager_cloud/experiment_state_server/config"
	apipb "xmanager_cloud/experiment_state_server/proto/api_go_proto"
	msgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/storage/storage"
	"xmanager_cloud/validator/validator"
	xidgrpcpb "xmanager_cloud/xid_service/proto/api_go_grpc_proto"
	xidapipb "xmanager_cloud/xid_service/proto/api_go_proto"
	xidmsgpb "xmanager_cloud/xid_service/proto/messages_go_proto"
)

func TestCreateArtifact_HappyPath(t *testing.T) {
	ctx := context.Background()
	server, _ := createExperimentStateServer(ctx, t)
	exp := mustCreateExperiment(ctx, t, server, nil)

	now := time.Now()
	artifact := &msgpb.Artifact{
		Acls: &xidmsgpb.XidAcls{
			Writers: []*xidmsgpb.XidAcls_Role{{Principal: &xidmsgpb.XidAcls_Role_User{User: "test@example.com"}}},
		},
		Tags:  []string{"tag1", "tag2"},
		Notes: "test notes",
		Url: &msgpb.Url{
			Url:         "http://www.google.com",
			DisplayName: "google",
			Icon:        "icon1",
			Priority:    1,
		},
		Payload: &msgpb.ArtifactPayload{
			Type: &msgpb.ArtifactPayload_Url{
				Url: &msgpb.UrlArtifact{
					Url:         "http://www.google.com",
					DisplayName: "google",
					Icon:        "icon1",
				},
			},
		},
	}
	req := &apipb.CreateArtifactRequest{
		Parent:   exp.GetName(),
		Artifact: artifact,
	}

	got, err := server.CreateArtifact(ctx, req)
	if err != nil {
		t.Fatalf("CreateArtifact() returned an unexpected error: %v", err)
	}

	artifact.Parent = exp.GetName()
	if got.GetName() == "" {
		t.Errorf("CreateArtifact() returned an empty artifact name")
	}
	if got.GetCreateTime() == nil || got.GetUpdateTime() == nil {
		t.Errorf("CreateArtifact() returned nil timestamps")
	}
	opts := []cmp.Option{
		protocmp.Transform(),
		protocmp.IgnoreFields(&msgpb.Artifact{}, "create_time", "update_time", "name"),
	}
	if diff := cmp.Diff(artifact, got, opts...); diff != "" {
		t.Errorf("CreateArtifact() with request %+v mismatch (-want +got):\n%s", req, diff)
	}
	if got.GetCreateTime().AsTime().Before(now) {
		t.Errorf("CreateArtifact() returned a create time %v that is before the test start time %v", got.GetCreateTime(), now)
	}
	if got.GetUpdateTime().AsTime().Before(now) {
		t.Errorf("CreateArtifact() returned an update time %v that is before the test start time %v", got.GetUpdateTime(), now)
	}
	aid, err := validator.ValidateArtifactName(got.GetName())
	if err != nil {
		t.Errorf("CreateArtifact() returned an invalid artifact name %s: %v", got.GetName(), err)
	}

	if aid <= 0 {
		t.Errorf("CreateArtifact() returned an invalid AID %d", aid)
	}
}

func TestCreateArtifact_FillNilValues(t *testing.T) {
	ctx := context.Background()
	server, _ := createExperimentStateServer(ctx, t)
	exp := mustCreateExperiment(ctx, t, server, nil)

	artifact := &msgpb.Artifact{
		Acls: &xidmsgpb.XidAcls{
			Writers: []*xidmsgpb.XidAcls_Role{{Principal: &xidmsgpb.XidAcls_Role_User{User: "test@example.com"}}},
		},
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
	}
	req := &apipb.CreateArtifactRequest{
		Parent:   exp.GetName(),
		Artifact: artifact,
	}

	got, err := server.CreateArtifact(ctx, req)
	if err != nil {
		t.Fatalf("CreateArtifact() returned an unexpected error: %v", err)
	}

	if got.GetTags() == nil {
		t.Errorf("CreateArtifact() returned nil tags, want empty slice")
	}
	if got.GetNotes() != "" {
		t.Errorf("CreateArtifact() returned notes %q, want empty string", got.GetNotes())
	}
}

func TestCreateArtifact_BadPaths(t *testing.T) {
	ctx := context.Background()
	server, _ := createExperimentStateServer(ctx, t)
	exp := mustCreateExperiment(ctx, t, server, nil)

	baseReq := &apipb.CreateArtifactRequest{
		Parent: exp.GetName(),
		Artifact: &msgpb.Artifact{
			Url: &msgpb.Url{Url: "http://www.google.com"},
		},
	}

	testCases := []struct {
		name    string
		req     *apipb.CreateArtifactRequest
		wantErr error
	}{
		{
			name: "missing_parent",
			req: proto.Modify(baseReq, func(r *apipb.CreateArtifactRequest) {
				r.Parent = ""
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "invalid_parent",
			req: proto.Modify(baseReq, func(r *apipb.CreateArtifactRequest) {
				r.Parent = "invalid-parent"
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "missing_artifact",
			req: proto.Modify(baseReq, func(r *apipb.CreateArtifactRequest) {
				r.Artifact = nil
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "missing_url",
			req: proto.Modify(baseReq, func(r *apipb.CreateArtifactRequest) {
				r.Artifact.Url = nil
			}),
			wantErr: status.ErrInvalidArgument,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := server.CreateArtifact(ctx, tc.req)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("CreateArtifact() returned error %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestCreateArtifact_DependencyFailures(t *testing.T) {
	ctx := context.Background()
	cfg := &config.Config{}
	authorizer := createTestAuthorizer(ctx, t)
	server, _ := createExperimentStateServer(ctx, t)
	exp := mustCreateExperiment(ctx, t, server, nil)

	goodArtifact := &msgpb.Artifact{
		Url: &msgpb.Url{Url: "http://www.google.com"},
		Payload: &msgpb.ArtifactPayload{
			Type: &msgpb.ArtifactPayload_Url{
				Url: &msgpb.UrlArtifact{
					Url: "http://www.google.com",
				},
			},
		},
		Acls: &xidmsgpb.XidAcls{
			Writers: []*xidmsgpb.XidAcls_Role{{Principal: &xidmsgpb.XidAcls_Role_User{User: "test@example.com"}}},
		},
	}
	goodReq := &apipb.CreateArtifactRequest{
		Parent:   exp.GetName(),
		Artifact: goodArtifact,
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
			name: "xid_creation_fails",
			xidClient: &mockXidClient{
				checkUserAuthorizationResp: &xidapipb.CheckUserAuthorizationResponse{
					Authorized: proto.Bool(true),
				},
				createXidEntryErr: fmt.Errorf("XID creation failed: %w", status.ErrInternal),
			},
			storage: &storage.MockStorage{},
			wantErr: status.ErrInternal,
		},
		{
			name: "storage_creation_fails",
			xidClient: &mockXidClient{
				checkUserAuthorizationResp: &xidapipb.CheckUserAuthorizationResponse{
					Authorized: proto.Bool(true),
				},
				createXidEntryResp: &xidmsgpb.XidEntry{Name: proto.String("xids/123")},
			},
			storage: &storage.MockStorage{
				CreateArtifactErr: fmt.Errorf("Storage creation failed"),
			},
			wantErr: status.ErrInternal,
		},
		{
			name: "invalid_xid_name",
			xidClient: &mockXidClient{
				checkUserAuthorizationResp: &xidapipb.CheckUserAuthorizationResponse{
					Authorized: proto.Bool(true),
				},
				createXidEntryResp: &xidmsgpb.XidEntry{Name: proto.String("invalid-xid-name")},
			},
			storage: &storage.MockStorage{},
			wantErr: status.ErrInternal,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var auth auth.AuthorizerInterface = authorizer
			if tc.authenticator != nil {
				auth = tc.authenticator
			}
			server := New(tc.storage, cfg, auth, tc.xidClient, nil)
			_, err := server.CreateArtifact(ctx, goodReq)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("%s: CreateArtifact() returned error %v, want %v", tc.name, err, tc.wantErr)
			}
		})
	}
}
