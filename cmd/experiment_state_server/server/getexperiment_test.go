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
	"xmanager_cloud/experiment_state_server/config"
	"xmanager_cloud/internal/auth"
	"xmanager_cloud/storage/storage"

	apipb "xmanager_cloud/experiment_state_server/proto/api_go_proto"
	msgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	xidgrpcpb "xmanager_cloud/xid_service/proto/api_go_proto"
	xidmsgpb "xmanager_cloud/xid_service/proto/messages_go_proto"
)

func TestGetExperiment_HappyPath(t *testing.T) {
	ctx := context.Background()
	server, _ := createExperimentStateServer(ctx, t)

	// Create an experiment first to test GetExperiment
	exp := &msgpb.Experiment{
		Title: "test experiment",
		Acls: &xidmsgpb.XidAcls{
			Owners: []*xidmsgpb.XidAcls_Role{{Principal: &xidmsgpb.XidAcls_Role_User{User: "test@example.com"}}},
		},
	}
	createdExp := mustCreateExperiment(ctx, t, server, exp)

	req := &apipb.GetExperimentRequest{Name: createdExp.GetName()}
	got, err := server.GetExperiment(ctx, req)
	if err != nil {
		t.Fatalf("GetExperiment() returned an unexpected error: %v", err)
	}

	opts := []cmp.Option{
		protocmp.Transform(),
	}
	if diff := cmp.Diff(createdExp, got, opts...); diff != "" {
		t.Errorf("GetExperiment() mismatch (-want +got):\n%s", diff)
	}
}

func TestGetExperiment_BadPaths(t *testing.T) {
	ctx := context.Background()
	server, _ := createExperimentStateServer(ctx, t)

	testCases := []struct {
		name    string
		req     *apipb.GetExperimentRequest
		wantErr error
	}{
		{
			name:    "invalid_name",
			req:     &apipb.GetExperimentRequest{Name: "invalid-name"},
			wantErr: status.ErrInvalidArgument,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := server.GetExperiment(ctx, tc.req)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("GetExperiment(%v) returned error %v, want %v", tc.req, err, tc.wantErr)
			}
		})
	}
}

func TestGetExperiment_DependencyFailures(t *testing.T) {
	ctx := context.Background()
	cfg := &config.Config{}

	goodReq := &apipb.GetExperimentRequest{Name: "experiments/123"}

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
			name: "storage_fetch_fails",
			xidClient: &mockXidClient{
				getXidEntryResp: &xidmsgpb.XidEntry{Name: proto.String("xids/123")},
			},
			storage: &storage.MockStorage{
				FetchExperimentErr: fmt.Errorf("storage fetch failed"),
			},
			wantErr: status.ErrInternal,
		},
		{
			name: "storage_fetch_not_found",
			xidClient: &mockXidClient{
				getXidEntryResp: &xidmsgpb.XidEntry{Name: proto.String("xids/123")},
			},
			storage: &storage.MockStorage{
				FetchExperimentErr: status.ErrNotFound,
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
			_, err := server.GetExperiment(ctx, goodReq)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("GetExperiment(%v) returned error %v, want %v", goodReq, err, tc.wantErr)
			}
		})
	}
}
