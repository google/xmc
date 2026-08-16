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
	"strings"
	"testing"

	"golang/cmp/cmp"
	"golang/protobuf/v2/proto/proto"
	"golang/protobuf/v2/testing/protocmp/protocmp"
	"task/go/status"
	"xmanager_cloud/auth/auth"
	"xmanager_cloud/storage/storage"
	tq "xmanager_cloud/task_queue/client"
	"xmanager_cloud/validator/validator"

	apipb "xmanager_cloud/experiment_state_server/proto/api_go_proto"
	msgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	xidgrpcpb "xmanager_cloud/xid_service/proto/api_go_grpc_proto"
	xidapipb "xmanager_cloud/xid_service/proto/api_go_proto"
	xidmsgpb "xmanager_cloud/xid_service/proto/messages_go_proto"
)

func TestStopWorkUnit_HappyPath(t *testing.T) {
	ctx := context.Background()
	server, taskQueueClient := createExperimentStateServer(ctx, t)

	exp := &msgpb.Experiment{
		Title: "test experiment for stopworkunit",
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
	taskQueueClient.LastEnqueuedAction = nil // Clear state from CreateWorkUnit

	op, err := server.StopWorkUnit(ctx, &apipb.StopWorkUnitRequest{Name: workUnitName})
	if err != nil {
		t.Fatalf("StopWorkUnit() returned an unexpected error: %v", err)
	}
	if op.GetDone() {
		t.Errorf("StopWorkUnit() returned an operation with done=true")
	}
	if op.GetResponse() != nil {
		t.Errorf("StopWorkUnit() returned an operation with non-nil response")
	}
	if op.GetError() != nil {
		t.Errorf("StopWorkUnit() returned an operation with non-nil error")
	}
	var gotMetadata msgpb.OperationMetadata
	if err := op.GetMetadata().UnmarshalTo(&gotMetadata); err != nil {
		t.Errorf("Failed to unmarshal operation metadata: %v", err)
	}
	if !strings.HasPrefix(gotMetadata.GetTarget(), createdExp.GetName()+"/workUnits/") {
		t.Errorf("StopWorkUnit() operation metadata target %q does not start with experiment prefix", gotMetadata.GetTarget())
	}
	if gotMetadata.GetActionType() != msgpb.OperationMetadata_STOP {
		t.Errorf("StopWorkUnit() operation metadata action type got %q, want %q", gotMetadata.GetActionType(), msgpb.OperationMetadata_STOP)
	}
	if gotMetadata.GetOriginalRequester() != "test@example.com" {
		t.Errorf("StopWorkUnit() operation metadata original requester got %q, want %q", gotMetadata.GetOriginalRequester(), "test@example.com")
	}

	if taskQueueClient.LastEnqueuedAction == nil {
		t.Fatalf("StopWorkUnit() did not enqueue a task")
	}

	action := taskQueueClient.LastEnqueuedAction
	stopAction := action.GetDelete()
	if stopAction == nil {
		t.Fatalf("Task action is not Stop (Delete): %v", action)
	}

	if diff := cmp.Diff(taskQueueClient.LastEnqueuedWorkUnit, workUnit, protocmp.Transform()); diff != "" {
		t.Errorf("Enqueued stop action work unit mismatch (-want +got):\n%s", diff)
	}

	if taskQueueClient.LastEnqueuedTarget != taskQueueClient.LastEnqueuedWorkUnit.GetCurrentWorkUnitExecution().GetName() {
		t.Errorf("Enqueued stop action has wrong target name: got %s, want %s", taskQueueClient.LastEnqueuedTarget, taskQueueClient.LastEnqueuedWorkUnit.GetCurrentWorkUnitExecution().GetName())
	}

	// Check that the work unit execution was stopped.
	xid, _, err := validator.ValidateWorkUnitName(workUnitName)
	if err != nil {
		t.Fatalf("ValidateWorkUnitName() returned an unexpected error: %v", err)
	}
	workUnitExecution, err := server.WorkUnitHandler.storage.FetchCurrentWorkUnitExecution(ctx, xid, workUnitID)
	if err != nil {
		t.Fatalf("FetchCurrentWorkUnitExecution() returned an unexpected error: %v", err)
	}
	if workUnitExecution.GetStatus().GetState() != msgpb.WorkUnitStatus_STOPPING {
		t.Errorf("Work unit execution state got %v, want %v", workUnitExecution.GetStatus().GetState(), msgpb.WorkUnitStatus_STOPPING)
	}
}

func TestStopWorkUnit_BadPaths(t *testing.T) {
	ctx := context.Background()
	server := newTestServer(t)

	testCases := []struct {
		name    string
		req     *apipb.StopWorkUnitRequest
		wantErr error
	}{
		{
			name:    "invalid_name",
			req:     &apipb.StopWorkUnitRequest{Name: "invalid-name"},
			wantErr: status.ErrInvalidArgument,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := server.StopWorkUnit(ctx, tc.req)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("StopWorkUnit() got error %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestStopWorkUnit_DependencyFailures(t *testing.T) {
	ctx := context.Background()

	goodReq := &apipb.StopWorkUnitRequest{Name: "experiments/123/workUnits/456"}
	workUnit := &msgpb.WorkUnit{
		Name:                     "experiments/123/workUnits/456",
		WorkUnitType:             msgpb.WorkUnit_STANDARD,
		Executable:               testExecutable,
		CurrentWorkUnitExecution: &msgpb.WorkUnitExecution{},
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
			name: "xid_check_user_authorization_fails",
			xidClient: &mockXidClient{
				checkUserAuthorizationErr: status.ErrPermissionDenied,
			},
			storage: &storage.MockStorage{},
			wantErr: status.ErrPermissionDenied,
		},
		{
			name: "xid_check_user_authorization_unauthorized",
			xidClient: &mockXidClient{
				checkUserAuthorizationResp: &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(false)},
			},
			storage: &storage.MockStorage{},
			wantErr: status.ErrPermissionDenied,
		},
		{
			name: "storage_fetch_work_unit_fails",
			xidClient: &mockXidClient{
				checkUserAuthorizationResp: &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)},
			},
			storage: &storage.MockStorage{
				FetchWorkUnitErr: fmt.Errorf("storage fetch work unit failed"),
			},
			wantErr: status.ErrInternal,
		},
		{
			name: "storage_fetch_work_unit_not_found",
			xidClient: &mockXidClient{
				checkUserAuthorizationResp: &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)},
			},
			storage: &storage.MockStorage{
				FetchWorkUnitErr: status.ErrNotFound,
			},
			wantErr: status.ErrNotFound,
		},
		{
			name: "storage_add_operation_fails",
			xidClient: &mockXidClient{
				checkUserAuthorizationResp: &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)},
			},
			storage: &storage.MockStorage{
				WorkUnit:                workUnit,
				AddWorkUnitOperationErr: fmt.Errorf("Storage add operation failed"),
			},
			wantErr: status.ErrInternal,
		},
		{
			name: "storage_update_work_unit_executions_fails",
			xidClient: &mockXidClient{
				checkUserAuthorizationResp: &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)},
			},
			storage: &storage.MockStorage{
				WorkUnit:                    workUnit,
				UpdateWorkUnitExecutionsErr: fmt.Errorf("Storage update work unit executions failed"),
			},
			wantErr: status.ErrInternal,
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
			_, err := server.StopWorkUnit(ctx, goodReq)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("StopWorkUnit() got error %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestStopWorkUnit_TaskEnqueueFails(t *testing.T) {
	ctx := context.Background()

	workUnit := &msgpb.WorkUnit{
		Name:                     "experiments/123/workUnits/456",
		WorkUnitType:             msgpb.WorkUnit_STANDARD,
		Executable:               testExecutable,
		CurrentWorkUnitExecution: &msgpb.WorkUnitExecution{},
	}

	xidClient := &mockXidClient{
		checkUserAuthorizationResp: &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)},
	}
	storage := &storage.MockStorage{WorkUnit: workUnit}
	taskQueueClient := &tq.FakeClient{
		EnqueueErr: fmt.Errorf("task enqueue failed"),
	}

	server := newTestServer(t,
		withStorage(storage),
		withXidClient(xidClient),
		withWorkUnitActuator(taskQueueClient),
	)

	req := &apipb.StopWorkUnitRequest{Name: "experiments/123/workUnits/456"}
	_, err := server.StopWorkUnit(ctx, req)
	if !errors.Is(err, status.ErrInternal) {
		t.Errorf("StopWorkUnit() got error %v, want %v", err, status.ErrInternal)
	}
}
