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
	"xmanager_cloud/internal/auth"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/validator"

	xidapipb "xmanager_cloud/api/protos/xid"
	apipb "xmanager_cloud/experiment_state_server/proto/api_go_proto"
	msgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
)

func TestGetWorkUnit_HappyPath(t *testing.T) {
	ctx := context.Background()
	server, _ := createExperimentStateServer(ctx, t)

	now := time.Now()

	exp := &msgpb.Experiment{
		Title: "test experiment for getworkunit",
		Acls: &xidapipb.XidAcls{
			Owners: []*xidapipb.XidAcls_Role{{Principal: &xidapipb.XidAcls_Role_User{User: "test@example.com"}}},
		},
	}
	createdExp := mustCreateExperiment(ctx, t, server, exp)

	workUnit := &msgpb.WorkUnit{
		WorkUnitType: msgpb.WorkUnit_STANDARD,
		Executable:   testExecutable,
	}
	workUnitName := fmt.Sprintf("%s/workUnits/1", createdExp.GetName())
	_ = mustCreateWorkUnit(ctx, t, server, createdExp.GetName(), 1, workUnit)

	req := &apipb.GetWorkUnitRequest{Name: workUnitName}
	got, err := server.GetWorkUnit(ctx, req)
	if err != nil {
		t.Fatalf("GetWorkUnit() returned an unexpected error: %v", err)
	}

	xid, wid, err := validator.ValidateWorkUnitName(workUnitName)
	if err != nil {
		t.Fatalf("ValidateWorkUnitName() returned an unexpected error: %v", err)
	}
	workUnit.CurrentWorkUnitExecution = newWorkUnitExecution(xid, wid)

	opts := []cmp.Option{
		protocmp.Transform(),
		protocmp.IgnoreFields(&msgpb.WorkUnitExecution{}, "create_time", "update_time"),
		protocmp.IgnoreFields(&msgpb.WorkUnitStatus{}, "update_time", "state_update_time"),
	}
	if diff := cmp.Diff(workUnit, got, opts...); diff != "" {
		t.Errorf("GetWorkUnit() mismatch (-want +got):\n%s", diff)
	}

	if got.GetCreateTime().AsTime().Before(now) {
		t.Errorf("GetWorkUnit() returned a create time %v that is before the test start time %v", got.GetCreateTime().AsTime(), now)
	}
	if got.GetUpdateTime().AsTime().Before(now) {
		t.Errorf("GetWorkUnit() returned an update time %v that is before the test start time %v", got.GetUpdateTime().AsTime(), now)
	}
}

func TestGetWorkUnit_BadPaths(t *testing.T) {
	ctx := context.Background()
	server := newTestServer(t)

	testCases := []struct {
		name    string
		req     *apipb.GetWorkUnitRequest
		wantErr error
	}{
		{
			name:    "invalid_name",
			req:     &apipb.GetWorkUnitRequest{Name: "invalid-name"},
			wantErr: grpcerr.ErrInvalidArgument,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := server.GetWorkUnit(ctx, tc.req)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("GetWorkUnit(%v) returned error %v, want %v", tc.req, err, tc.wantErr)
			}
		})
	}
}

func TestGetWorkUnit_DependencyFailures(t *testing.T) {
	ctx := context.Background()

	goodReq := &apipb.GetWorkUnitRequest{Name: "experiments/123/workUnits/456"}

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
				FetchWorkUnitErr: fmt.Errorf("storage fetch failed"),
			},
			wantErr: grpcerr.ErrInternal,
		},
		{
			name: "storage_fetch_not_found",
			xidClient: &mockXidClient{
				checkUserAuthorizationResp: &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)},
			},
			storage: &storage.MockStorage{
				FetchWorkUnitErr: grpcerr.ErrNotFound,
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
			_, err := server.GetWorkUnit(ctx, goodReq)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("GetWorkUnit(%v) returned error %v, want %v", goodReq, err, tc.wantErr)
			}
		})
	}
}
