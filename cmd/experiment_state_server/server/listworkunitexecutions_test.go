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

	"golang/protobuf/v2/proto/proto"
	"xmanager_cloud/auth/auth"
	"xmanager_cloud/experiment_state_server/config"
	apipb "xmanager_cloud/experiment_state_server/proto/api_go_proto"
	msgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/storage/storage"
	xidgrpcpb "xmanager_cloud/xid_service/proto/api_go_grpc_proto"
	xidmsgpb "xmanager_cloud/xid_service/proto/messages_go_proto"
	"task/go/status"
)

func TestListWorkUnitExecutions_HappyPath(t *testing.T) {
	ctx := context.Background()
	server, _ := createExperimentStateServer(ctx, t)

	exp := &msgpb.Experiment{
		Title: "test experiment for listworkunitexecutions",
		Acls: &xidmsgpb.XidAcls{
			Owners: []*xidmsgpb.XidAcls_Role{{Principal: &xidmsgpb.XidAcls_Role_User{User: "test@example.com"}}},
		},
	}
	createdExp := mustCreateExperiment(ctx, t, server, exp)

	workUnit := &msgpb.WorkUnit{
		WorkUnitType: msgpb.WorkUnit_STANDARD,
		Executable:   testExecutable,
	}
	workUnitID := int64(1)
	workUnitName := fmt.Sprintf("%s/workUnits/%d", createdExp.GetName(), workUnitID)
	_ = mustCreateWorkUnit(ctx, t, server, createdExp.GetName(), workUnitID, workUnit)

	req := &apipb.ListWorkUnitExecutionsRequest{Parent: workUnitName}
	resp, err := server.ListWorkUnitExecutions(ctx, req)
	if err != nil {
		t.Fatalf("ListWorkUnitExecutions() returned an unexpected error: %v", err)
	}

	if len(resp.GetWorkUnitExecutions()) != 1 {
		t.Errorf("ListWorkUnitExecutions() returned %d work unit executions, want 1", len(resp.GetWorkUnitExecutions()))
	}

	// Restart work unit to create a historical execution.
	_, err = server.RestartWorkUnit(ctx, &apipb.RestartWorkUnitRequest{Name: workUnitName})
	if err != nil {
		t.Fatalf("RestartWorkUnit() returned an unexpected error: %v", err)
	}

	resp, err = server.ListWorkUnitExecutions(ctx, req)
	if err != nil {
		t.Fatalf("ListWorkUnitExecutions() returned an unexpected error: %v", err)
	}

	if len(resp.GetWorkUnitExecutions()) != 2 {
		t.Errorf("ListWorkUnitExecutions() returned %d work unit executions, want 2", len(resp.GetWorkUnitExecutions()))
	}

	// Restart work unit again.
	_, err = server.RestartWorkUnit(ctx, &apipb.RestartWorkUnitRequest{Name: workUnitName})
	if err != nil {
		t.Fatalf("RestartWorkUnit() returned an unexpected error: %v", err)
	}

	resp, err = server.ListWorkUnitExecutions(ctx, req)
	if err != nil {
		t.Fatalf("ListWorkUnitExecutions() returned an unexpected error: %v", err)
	}

	if len(resp.GetWorkUnitExecutions()) != 3 {
		t.Errorf("ListWorkUnitExecutions() returned %d work unit executions, want 3", len(resp.GetWorkUnitExecutions()))
	}
}

func TestListWorkUnitExecutions_BadPaths(t *testing.T) {
	ctx := context.Background()
	authorizer := createTestAuthorizer(ctx, t)
	cfg := &config.Config{}
	server := New(&storage.MockStorage{}, cfg, authorizer, &mockXidClient{}, nil)

	testCases := []struct {
		name    string
		req     *apipb.ListWorkUnitExecutionsRequest
		wantErr error
	}{
		{
			name:    "invalid_parent",
			req:     &apipb.ListWorkUnitExecutionsRequest{Parent: "invalid-parent"},
			wantErr: status.ErrInvalidArgument,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := server.ListWorkUnitExecutions(ctx, tc.req)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("ListWorkUnitExecutions(%v) returned error %v, want %v", tc.req, err, tc.wantErr)
			}
		})
	}
}

func TestListWorkUnitExecutions_DependencyFailures(t *testing.T) {
	ctx := context.Background()
	cfg := &config.Config{}

	goodReq := &apipb.ListWorkUnitExecutionsRequest{Parent: "experiments/123/workUnits/456"}

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
			name: "storage_list_historical_fails",
			xidClient: &mockXidClient{
				getXidEntryResp: &xidmsgpb.XidEntry{Name: proto.String("xids/123")},
			},
			storage: &storage.MockStorage{
				WorkUnitExecution:         &msgpb.WorkUnitExecution{}, // for fetch current
				ListWorkUnitExecutionsErr: fmt.Errorf("Storage list historical failed"),
			},
			wantErr: status.ErrInternal,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.authenticator == nil {
				tc.authenticator = createTestAuthorizer(ctx, t)
			}
			server := New(tc.storage, cfg, tc.authenticator, tc.xidClient, nil)
			_, err := server.ListWorkUnitExecutions(ctx, goodReq)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("ListWorkUnitExecutions(%v) returned error %v, want %v", goodReq, err, tc.wantErr)
			}
		})
	}
}
