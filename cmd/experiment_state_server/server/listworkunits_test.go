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

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
	xidapipb "xmanager_cloud/api/protos/xid"
	"xmanager_cloud/cmd/experiment_state_server/config"
	apipb "xmanager_cloud/experiment_state_server/proto/api_go_proto"
	msgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/auth"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/spanner"
	"xmanager_cloud/internal/storage/spanner/emulator"
)

func TestListWorkUnits_HappyPath(t *testing.T) {
	ctx := context.Background()
	server, _ := createExperimentStateServer(ctx, t)

	exp := mustCreateExperiment(ctx, t, server, nil)
	for i := 1; i <= 3; i++ {
		mustCreateWorkUnit(ctx, t, server, exp.GetName(), int64(i), nil)
	}

	wusResp, err := server.ListWorkUnits(ctx, &apipb.ListWorkUnitsRequest{Parent: exp.GetName(), PageSize: 10, OrderBy: "created"})
	if err != nil {
		t.Fatalf("ListWorkUnits() for all WUs returned an unexpected error: %v", err)
	}
	allWUs := wusResp.GetWorkUnits()
	if len(allWUs) != 3 {
		t.Fatalf("ListWorkUnits() for all WUs returned %d work units, want 3", len(allWUs))
	}

	// List first page.
	resp1, err := server.ListWorkUnits(ctx, &apipb.ListWorkUnitsRequest{Parent: exp.GetName(), PageSize: 2, OrderBy: "created"})
	if err != nil {
		t.Fatalf("ListWorkUnits() returned an unexpected error: %v", err)
	}
	if len(resp1.GetWorkUnits()) != 2 {
		t.Errorf("ListWorkUnits() returned %d work units, want 2", len(resp1.GetWorkUnits()))
	}
	if resp1.GetNextPageToken() == "" {
		t.Errorf("ListWorkUnits() returned empty next_page_token, want non-empty")
	}

	// List second page.
	resp2, err := server.ListWorkUnits(ctx, &apipb.ListWorkUnitsRequest{Parent: exp.GetName(), PageSize: 2, PageToken: resp1.GetNextPageToken(), OrderBy: "created"})
	if err != nil {
		t.Fatalf("ListWorkUnits() returned an unexpected error: %v", err)
	}
	if len(resp2.GetWorkUnits()) != 1 {
		t.Errorf("ListWorkUnits() returned %d work units, want 1", len(resp2.GetWorkUnits()))
	}
	if resp2.GetNextPageToken() != "" {
		t.Errorf("ListWorkUnits() returned non-empty next_page_token, want empty")
	}

	cmpOpts := []cmp.Option{
		protocmp.Transform(),
		protocmp.IgnoreFields(&msgpb.WorkUnit{}, "create_time", "update_time", "current_work_unit_execution"),
	}

	// Check all experiments are returned in order.
	if diff := cmp.Diff(allWUs[0], resp1.GetWorkUnits()[0], cmpOpts...); diff != "" {
		t.Errorf("ListWorkUnits() page 1 work unit 1 returned unexpected diff (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(allWUs[1], resp1.GetWorkUnits()[1], cmpOpts...); diff != "" {
		t.Errorf("ListWorkUnits() page 1 work unit 2 returned unexpected diff (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(allWUs[2], resp2.GetWorkUnits()[0], cmpOpts...); diff != "" {
		t.Errorf("ListWorkUnits() page 2 work unit 1 returned unexpected diff (-want +got):\n%s", diff)
	}
}

func TestSearchWorkUnits_HappyPath(t *testing.T) {
	ctx := context.Background()
	server, _ := createExperimentStateServer(ctx, t)

	exp := mustCreateExperiment(ctx, t, server, nil)
	for i := 1; i <= 3; i++ {
		mustCreateWorkUnit(ctx, t, server, exp.GetName(), int64(i), nil)
	}

	time.Sleep(workUnitSearchStaleness + 100*time.Millisecond)

	wusResp, err := server.SearchWorkUnits(ctx, &apipb.ListWorkUnitsRequest{Parent: exp.GetName(), PageSize: 10, OrderBy: "created"})
	if err != nil {
		t.Fatalf("SearchWorkUnits() for all WUs returned an unexpected error: %v", err)
	}
	allWUs := wusResp.GetWorkUnits()
	if len(allWUs) != 3 {
		t.Fatalf("SearchWorkUnits() for all WUs returned %d work units, want 3", len(allWUs))
	}

	// List first page.
	resp1, err := server.SearchWorkUnits(ctx, &apipb.ListWorkUnitsRequest{Parent: exp.GetName(), PageSize: 2, OrderBy: "created"})
	if err != nil {
		t.Fatalf("SearchWorkUnits() returned an unexpected error: %v", err)
	}
	if len(resp1.GetWorkUnits()) != 2 {
		t.Errorf("SearchWorkUnits() returned %d work units, want 2", len(resp1.GetWorkUnits()))
	}
	if resp1.GetNextPageToken() == "" {
		t.Errorf("SearchWorkUnits() returned empty next_page_token, want non-empty")
	}

	// List second page.
	resp2, err := server.SearchWorkUnits(ctx, &apipb.ListWorkUnitsRequest{Parent: exp.GetName(), PageSize: 2, PageToken: resp1.GetNextPageToken(), OrderBy: "created"})
	if err != nil {
		t.Fatalf("SearchWorkUnits() returned an unexpected error: %v", err)
	}
	if len(resp2.GetWorkUnits()) != 1 {
		t.Errorf("SearchWorkUnits() returned %d work units, want 1", len(resp2.GetWorkUnits()))
	}
	if resp2.GetNextPageToken() != "" {
		t.Errorf("SearchWorkUnits() returned non-empty next_page_token, want empty")
	}

	cmpOpts := []cmp.Option{
		protocmp.Transform(),
		protocmp.IgnoreFields(&msgpb.WorkUnit{}, "create_time", "update_time", "current_work_unit_execution"),
	}

	// Check all experiments are returned in order.
	if diff := cmp.Diff(allWUs[0], resp1.GetWorkUnits()[0], cmpOpts...); diff != "" {
		t.Errorf("SearchWorkUnits() page 1 work unit 1 returned unexpected diff (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(allWUs[1], resp1.GetWorkUnits()[1], cmpOpts...); diff != "" {
		t.Errorf("SearchWorkUnits() page 1 work unit 2 returned unexpected diff (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(allWUs[2], resp2.GetWorkUnits()[0], cmpOpts...); diff != "" {
		t.Errorf("SearchWorkUnits() page 2 work unit 1 returned unexpected diff (-want +got):\n%s", diff)
	}
}

func TestListWorkUnits_BadPaths(t *testing.T) {
	ctx := context.Background()
	server := newTestServer(t)

	testCases := []struct {
		name    string
		req     *apipb.ListWorkUnitsRequest
		wantErr error
	}{
		{
			name:    "invalid_page_size",
			req:     &apipb.ListWorkUnitsRequest{PageSize: -1, Parent: "experiments/1"},
			wantErr: grpcerr.ErrInvalidArgument,
		},
		{
			name:    "invalid_parent_name",
			req:     &apipb.ListWorkUnitsRequest{PageSize: 10, Parent: "invalid-parent"},
			wantErr: grpcerr.ErrInvalidArgument,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := server.ListWorkUnits(ctx, tc.req)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("ListWorkUnits(%v) returned error %v, want %v", tc.req, err, tc.wantErr)
			}
		})
	}
}

func TestListWorkUnits_DependencyFailures(t *testing.T) {
	ctx := context.Background()

	goodReq := &apipb.ListWorkUnitsRequest{PageSize: 10, Parent: "experiments/1"}

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
			name:          "check_auth_fails",
			authenticator: createTestAuthorizer(ctx, t),
			xidClient:     &mockXidClient{checkUserAuthorizationErr: grpcerr.ErrPermissionDenied},
			storage:       &storage.MockStorage{},
			wantErr:       grpcerr.ErrPermissionDenied,
		},
		{
			name:          "storage_fails_grpc_invalid_arg",
			authenticator: createTestAuthorizer(ctx, t),
			xidClient: &mockXidClient{
				checkUserAuthorizationResp: &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)},
			},
			storage: &storage.MockStorage{ListWorkUnitsErr: grpcerr.ErrInvalidArgument},
			wantErr: grpcerr.ErrInvalidArgument,
		},
		{
			name:          "storage_fails_grpc_other",
			authenticator: createTestAuthorizer(ctx, t),
			xidClient: &mockXidClient{
				checkUserAuthorizationResp: &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)},
			},
			storage: &storage.MockStorage{ListWorkUnitsErr: grpcerr.ErrPermissionDenied},
			wantErr: grpcerr.ErrInternal,
		},
		{
			name:          "storage_fails_non_grpc",
			authenticator: createTestAuthorizer(ctx, t),
			xidClient: &mockXidClient{
				checkUserAuthorizationResp: &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)},
			},
			storage: &storage.MockStorage{ListWorkUnitsErr: fmt.Errorf("some other error")},
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
			_, err := server.ListWorkUnits(ctx, goodReq)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("ListWorkUnits(%v) returned error %v, want %v", goodReq, err, tc.wantErr)
			}
		})
	}
}

func TestListWorkUnits_PermissionDenied(t *testing.T) {
	ctx := context.Background()
	server, mockXidClient := createExperimentStateServerWithMockXid(ctx, t)

	// Allow experiment creation to succeed.
	mockXidClient.checkUserAuthorizationResp = &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)}
	exp := mustCreateExperiment(ctx, t, server, nil)

	// Set up the mock to deny access for the ListWorkUnits call.
	mockXidClient.checkUserAuthorizationResp = &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(false)}
	req := &apipb.ListWorkUnitsRequest{Parent: exp.GetName(), PageSize: 10}
	_, err := server.ListWorkUnits(ctx, req)
	if !errors.Is(err, grpcerr.ErrPermissionDenied) {
		t.Errorf("ListWorkUnits(%v) returned error %v, want %v", req, err, grpcerr.ErrPermissionDenied)
	}
}

func createExperimentStateServerWithMockXid(ctx context.Context, t *testing.T) (*Server, *mockXidClient) {
	t.Helper()
	_, essStorageClient := emulator.Setup(ctx, t)
	storage, _ := spanner.NewStorage(essStorageClient)
	cfg := &config.Config{
		EndpointURL:                 "http://localhost:8080",
		DefaultExecutionEnvironment: "gke",
	}

	xidClient := &mockXidClient{
		createXidEntryResp: &xidapipb.XidEntry{Name: proto.String("xids/1")},
	}
	server := newTestServer(t,
		withStorage(storage),
		withConfig(cfg),
		withXidClient(xidClient),
	)
	return server, xidClient
}
