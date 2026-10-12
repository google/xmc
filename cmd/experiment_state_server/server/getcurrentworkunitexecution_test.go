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
	"github.com/google/xmc/internal/auth"
	"github.com/google/xmc/internal/grpcerr"
	"github.com/google/xmc/internal/storage"
	"github.com/google/xmc/internal/validator"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"

	apipb "github.com/google/xmc/api/protos/experiment"
	xidapipb "github.com/google/xmc/api/protos/xid"
)

func TestGetCurrentWorkUnitExecution_HappyPath_Create(t *testing.T) {
	ctx := context.Background()
	server, _ := createExperimentStateServer(ctx, t)

	now := time.Now()

	exp := &apipb.Experiment{
		Title: "test experiment for getcurrentworkunitexecution",
		Acls: &xidapipb.XidAcls{
			Owners: []*xidapipb.XidAcls_Role{{Principal: &xidapipb.XidAcls_Role_User{User: "test@example.com"}}},
		},
	}
	createdExp := mustCreateExperiment(ctx, t, server, exp)

	workUnit := &apipb.WorkUnit{
		WorkUnitType: apipb.WorkUnit_STANDARD,
		Executable:   testExecutable,
	}
	workUnitName := fmt.Sprintf("%s/workUnits/1", createdExp.GetName())
	_ = mustCreateWorkUnit(ctx, t, server, createdExp.GetName(), 1, workUnit)

	xid, wid, err := validator.ValidateWorkUnitName(workUnitName)
	if err != nil {
		t.Fatalf("ValidateWorkUnitName() returned an unexpected error: %v", err)
	}
	workUnit.CurrentWorkUnitExecution = newWorkUnitExecution(xid, wid)

	req := &apipb.GetCurrentWorkUnitExecutionRequest{Name: workUnitName}
	got, err := server.GetCurrentWorkUnitExecution(ctx, req)
	if err != nil {
		t.Fatalf("GetCurrentWorkUnitExecution() returned an unexpected error: %v", err)
	}

	opts := []cmp.Option{
		protocmp.Transform(),
		protocmp.IgnoreFields(&apipb.WorkUnitExecution{}, "create_time", "update_time"),
		protocmp.IgnoreFields(&apipb.WorkUnitStatus{}, "update_time", "state_update_time"),
	}
	if diff := cmp.Diff(workUnit.GetCurrentWorkUnitExecution(), got, opts...); diff != "" {
		t.Errorf("GetCurrentWorkUnitExecution() mismatch (-want +got):\n%s", diff)
	}

	if got.GetCreateTime().AsTime().Before(now) {
		t.Errorf("GetCurrentWorkUnitExecution() returned a create time %v that is before the test start time %v", got.GetCreateTime().AsTime(), now)
	}
	if got.GetUpdateTime().AsTime().Before(now) {
		t.Errorf("GetCurrentWorkUnitExecution() returned an update time %v that is before the test start time %v", got.GetUpdateTime().AsTime(), now)
	}
	if got.GetStatus().GetUpdateTime().AsTime().Before(now) {
		t.Errorf("GetCurrentWorkUnitExecution() returned a status update time %v that is before the test start time %v", got.GetStatus().GetUpdateTime().AsTime(), now)
	}
	if got.GetStatus().GetStateUpdateTime().AsTime().Before(now) {
		t.Errorf("GetCurrentWorkUnitExecution() returned a status state update time %v that is before the test start time %v", got.GetStatus().GetStateUpdateTime().AsTime(), now)
	}
}

func TestGetCurrentWorkUnitExecution_BadPaths(t *testing.T) {
	ctx := context.Background()
	server := newTestServer(t)

	testCases := []struct {
		name    string
		req     *apipb.GetCurrentWorkUnitExecutionRequest
		wantErr error
	}{
		{
			name:    "invalid_name",
			req:     &apipb.GetCurrentWorkUnitExecutionRequest{Name: "invalid-name"},
			wantErr: grpcerr.ErrInvalidArgument,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := server.GetCurrentWorkUnitExecution(ctx, tc.req)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("GetCurrentWorkUnitExecution(%v) returned error %v, want %v", tc.req, err, tc.wantErr)
			}
		})
	}
}

func TestGetCurrentWorkUnitExecution_DependencyFailures(t *testing.T) {
	ctx := context.Background()

	goodReq := &apipb.GetCurrentWorkUnitExecutionRequest{Name: "experiments/123/workUnits/456"}

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
				FetchCurrentWorkUnitExecutionErr: fmt.Errorf("Storage fetch failed"),
			},
			wantErr: grpcerr.ErrInternal,
		},
		{
			name: "storage_fetch_not_found",
			xidClient: &mockXidClient{
				checkUserAuthorizationResp: &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)},
			},
			storage: &storage.MockStorage{
				FetchCurrentWorkUnitExecutionErr: grpcerr.ErrNotFound,
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
			_, err := server.GetCurrentWorkUnitExecution(ctx, goodReq)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("GetCurrentWorkUnitExecution(%v) returned error %v, want %v", goodReq, err, tc.wantErr)
			}
		})
	}
}
