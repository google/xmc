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
	"xmanager_cloud/cmd/experiment_state_server/config"
	"xmanager_cloud/internal/auth"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/storage"

	apipb "xmanager_cloud/api/protos/experiment"
	xidapipb "xmanager_cloud/api/protos/xid"
)

func TestGetExperimentStatus_HappyPath(t *testing.T) {
	ctx := context.Background()
	server, _ := createExperimentStateServer(ctx, t)

	// Create an experiment first to test GetExperimentStatus
	exp := &apipb.Experiment{
		Title: "test experiment",
		Acls: &xidapipb.XidAcls{
			Owners: []*xidapipb.XidAcls_Role{{Principal: &xidapipb.XidAcls_Role_User{User: "test@example.com"}}},
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
	server := newTestServer(t)

	testCases := []struct {
		name    string
		req     *apipb.GetExperimentStatusRequest
		wantErr error
	}{
		{
			name:    "invalid_name",
			req:     &apipb.GetExperimentStatusRequest{Name: "invalid-name"},
			wantErr: grpcerr.ErrInvalidArgument,
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

	goodReq := &apipb.GetExperimentStatusRequest{Name: "experiments/123"}

	testCases := []struct {
		name          string
		authenticator auth.AuthorizerInterface
		xidClient     xidapipb.XidServiceClient
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
			name: "xid_check_user_authorization_fails",
			xidClient: &mockXidClient{
				checkUserAuthorizationErr: grpcerr.ErrPermissionDenied,
			},
			storage: &storage.MockStorage{},
			wantErr: grpcerr.ErrPermissionDenied,
		},
		{
			name: "xid_check_user_authorization_unauthorized",
			xidClient: &mockXidClient{
				checkUserAuthorizationResp: &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(false)},
			},
			storage: &storage.MockStorage{},
			wantErr: grpcerr.ErrPermissionDenied,
		},
		{
			name: "storage_fetch_fails",
			xidClient: &mockXidClient{
				checkUserAuthorizationResp: &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)},
			},
			storage: &storage.MockStorage{
				FetchExperimentStatusErr: fmt.Errorf("storage fetch failed"),
			},
			wantErr: grpcerr.ErrInternal,
		},
		{
			name: "storage_fetch_not_found",
			xidClient: &mockXidClient{
				checkUserAuthorizationResp: &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)},
			},
			storage: &storage.MockStorage{
				FetchExperimentStatusErr: grpcerr.ErrNotFound,
			},
			wantErr: grpcerr.ErrNotFound,
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
			_, err := server.GetExperimentStatus(ctx, goodReq)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("GetExperimentStatus(%v) returned error %v, want %v", goodReq, err, tc.wantErr)
			}
		})
	}
}

func TestGetExperimentStatus_DriftWarningPropagation(t *testing.T) {
	ctx := context.Background()
	req := &apipb.GetExperimentStatusRequest{Name: "experiments/123"}
	compName := "redpanda"
	observed := int64(2)
	mockStorage := &storage.MockStorage{
		ExperimentStatus: &apipb.ExperimentStatus{
			ActiveState: apipb.ExperimentStatus_ACTIVE,
		},
		DriftedComponents: []storage.ComponentDiff{
			{
				ComponentID:        202,
				Name:               &compName,
				LockedGeneration:   1,
				ObservedGeneration: &observed,
			},
		},
	}
	xidClient := &mockXidClient{
		checkUserAuthorizationResp: &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)},
	}
	server := newTestServer(t,
		withConfig(&config.Config{EnableComponents: true}),
		withStorage(mockStorage),
		withXidClient(xidClient),
	)

	got, err := server.GetExperimentStatus(ctx, req)
	if err != nil {
		t.Fatalf("GetExperimentStatus(%v) returned unexpected error: %v", req, err)
	}
	want := &apipb.StructuredMessage{
		Severity:       apipb.StructuredMessage_WARNING,
		Source:         apipb.StructuredMessage_XMANAGER_BACKEND,
		UserFacingText: "Component drift detected: live component generations diverge from snapshots: redpanda (snapshot generation 1 != live observed generation 2)",
	}
	if diff := cmp.Diff(want, got.GetStructuredStatusMessage(), protocmp.Transform(), protocmp.IgnoreFields(&apipb.StructuredMessage{}, "create_time")); diff != "" {
		t.Errorf("GetExperimentStatus() StructuredStatusMessage mismatch (-want +got):\n%s", diff)
	}
}
