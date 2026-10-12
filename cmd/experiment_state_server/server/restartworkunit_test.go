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

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
	apipb "xmanager_cloud/experiment_state_server/proto/api_go_proto"
	msgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/auth"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/storage"
	tq "xmanager_cloud/internal/task_queue"
	"xmanager_cloud/internal/validator"
	xidapipb "xmanager_cloud/xid_service/proto/api_go_proto"
	xidmsgpb "xmanager_cloud/xid_service/proto/messages_go_proto"
)

func TestRestartWorkUnit_HappyPath(t *testing.T) {
	ctx := context.Background()
	server, taskQueueClient := createExperimentStateServer(ctx, t)

	exp := &msgpb.Experiment{
		Title: "test experiment for restartworkunit",
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

	op, err := server.RestartWorkUnit(ctx, &apipb.RestartWorkUnitRequest{Name: workUnitName})
	if err != nil {
		t.Fatalf("RestartWorkUnit() returned an unexpected error: %v", err)
	}
	if op.GetDone() {
		t.Errorf("RestartWorkUnit() returned an operation with done=true")
	}
	if op.GetResponse() != nil {
		t.Errorf("RestartWorkUnit() returned an operation with non-nil response")
	}
	if op.GetError() != nil {
		t.Errorf("RestartWorkUnit() returned an operation with non-nil error")
	}
	var gotMetadata msgpb.OperationMetadata
	if err := op.GetMetadata().UnmarshalTo(&gotMetadata); err != nil {
		t.Errorf("Failed to unmarshal operation metadata: %v", err)
	}
	if !strings.HasPrefix(gotMetadata.GetTarget(), createdExp.GetName()+"/workUnits/") {
		t.Errorf("RestartWorkUnit() operation metadata target %q does not start with experiment prefix", gotMetadata.GetTarget())
	}
	if gotMetadata.GetActionType() != msgpb.OperationMetadata_RESTART {
		t.Errorf("RestartWorkUnit() operation metadata action type got %q, want %q", gotMetadata.GetActionType(), msgpb.OperationMetadata_RESTART)
	}
	if gotMetadata.GetOriginalRequester() != "test@example.com" {
		t.Errorf("RestartWorkUnit() operation metadata original requester got %q, want %q", gotMetadata.GetOriginalRequester(), "test@example.com")
	}

	if taskQueueClient.LastEnqueuedAction == nil {
		t.Fatalf("RestartWorkUnit() did not enqueue a task")
	}

	action := taskQueueClient.LastEnqueuedAction
	restartAction := action.GetRestart()
	if restartAction == nil {
		t.Fatalf("Task action is not Restart: %v", action)
	}

	xid, err := validator.ValidateExperimentName(createdExp.GetName())
	if err != nil {
		t.Fatalf("ValidateExperimentName() returned an unexpected error: %v", err)
	}
	wantWorkUnitName := validator.ConstructWorkUnitName(xid, 1)
	if taskQueueClient.LastEnqueuedWorkUnit.GetName() != wantWorkUnitName {
		t.Errorf("Enqueued work unit has wrong name: got %q, want %q", taskQueueClient.LastEnqueuedWorkUnit.GetName(), wantWorkUnitName)
	}
	if taskQueueClient.LastEnqueuedWorkUnit.GetWorkUnitType() != workUnit.GetWorkUnitType() {
		t.Errorf("Enqueued work unit has wrong type: got %v, want %v", taskQueueClient.LastEnqueuedWorkUnit.GetWorkUnitType(), workUnit.GetWorkUnitType())
	}
	if diff := cmp.Diff(workUnit.GetExecutable(), taskQueueClient.LastEnqueuedWorkUnit.GetExecutable(), protocmp.Transform()); diff != "" {
		t.Errorf("Enqueued work unit executable mismatch (-want +got):\n%s", diff)
	}
	if taskQueueClient.LastEnqueuedWorkUnit.GetCurrentWorkUnitExecution() == nil {
		t.Errorf("Enqueued work unit has nil CurrentWorkUnitExecution")
	}

	// Check that the work unit execution was restarted.
	workUnitExecutions, err := server.WorkUnitHandler.storage.ListWorkUnitExecutions(ctx, xid, workUnitID)
	if err != nil {
		t.Fatalf("ListWorkUnitExecutions() returned an unexpected error: %v", err)
	}
	if len(workUnitExecutions) != 2 {
		t.Fatalf("ListWorkUnitExecutions() returned %d work unit executions, want 2", len(workUnitExecutions))
	}

	newWorkUnitExecution := workUnitExecutions[1]
	oldWorkUnitExecution := workUnitExecutions[0]

	if newWorkUnitExecution.GetStatus().GetState() != msgpb.WorkUnitStatus_INITIALIZING {
		t.Errorf("New work unit execution state is %v, want %v", newWorkUnitExecution.GetStatus().GetState(), msgpb.WorkUnitStatus_INITIALIZING)
	}
	if oldWorkUnitExecution.GetStatus().GetState() != msgpb.WorkUnitStatus_RESTARTING {
		t.Errorf("Old work unit execution state is %v, want %v", oldWorkUnitExecution.GetStatus().GetState(), msgpb.WorkUnitStatus_RESTARTING)
	}

	// Check that the new work unit execution has a different name and creation time.
	newExecution, err := server.WorkUnitHandler.storage.FetchCurrentWorkUnitExecution(ctx, xid, workUnitID)
	if err != nil {
		t.Fatalf("FetchWorkUnitExecution() returned an unexpected error: %v", err)
	}
	_, _, eid, err := validator.ValidateWorkUnitExecutionName(newExecution.GetName())
	if err != nil {
		t.Fatalf("ValidateWorkUnitExecutionName() returned an unexpected error: %v", err)
	}
	if eid != 2 {
		t.Errorf("New work unit execution has wrong execution ID: got %d, want 2", eid)
	}
}

func TestRestartWorkUnit_BadPaths(t *testing.T) {
	ctx := context.Background()
	server := newTestServer(t)

	testCases := []struct {
		name    string
		req     *apipb.RestartWorkUnitRequest
		wantErr error
	}{
		{
			name:    "invalid_name",
			req:     &apipb.RestartWorkUnitRequest{Name: "invalid-name"},
			wantErr: grpcerr.ErrInvalidArgument,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := server.RestartWorkUnit(ctx, tc.req)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("RestartWorkUnit() returned error %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestRestartWorkUnit_DependencyFailures(t *testing.T) {
	ctx := context.Background()

	goodReq := &apipb.RestartWorkUnitRequest{Name: "experiments/123/workUnits/456"}
	workUnit := &msgpb.WorkUnit{
		Name:                     "experiments/123/workUnits/456",
		WorkUnitType:             msgpb.WorkUnit_STANDARD,
		Executable:               testExecutable,
		CurrentWorkUnitExecution: &msgpb.WorkUnitExecution{Name: "experiments/123/workUnits/456/executions/1"},
	}

	testCases := []struct {
		name            string
		authenticator   auth.AuthorizerInterface
		xidClient       xidapipb.XidServiceClient
		storage         storage.Storage
		taskQueueClient *tq.FakeClient
		wantErr         error
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
			name: "storage_fetch_work_unit_fails",
			xidClient: &mockXidClient{
				checkUserAuthorizationResp: &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)},
			},
			storage: &storage.MockStorage{
				FetchWorkUnitErr: fmt.Errorf("storage fetch work unit failed"),
			},
			wantErr: grpcerr.ErrNotFound,
		},
		{
			name: "storage_fetch_work_unit_not_found",
			xidClient: &mockXidClient{
				checkUserAuthorizationResp: &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)},
			},
			storage: &storage.MockStorage{
				FetchWorkUnitErr: grpcerr.ErrNotFound,
			},
			wantErr: grpcerr.ErrNotFound,
		},
		{
			name: "storage_restart_work_unit_executions_fails",
			xidClient: &mockXidClient{
				checkUserAuthorizationResp: &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)},
			},
			storage: &storage.MockStorage{
				WorkUnit:            workUnit,
				RunInTransactionErr: fmt.Errorf("storage restart work unit executions failed"),
			},
			wantErr: grpcerr.ErrInternal,
		},
		{
			name: "storage_add_operation_fails",
			xidClient: &mockXidClient{
				checkUserAuthorizationResp: &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)},
			},
			storage: &storage.MockStorage{
				WorkUnit:                workUnit,
				Experiment:              &msgpb.Experiment{Name: "experiments/123"},
				AddWorkUnitOperationErr: fmt.Errorf("storage add operation failed"),
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
			if tc.taskQueueClient != nil {
				opts = append(opts, withWorkUnitActuator(tc.taskQueueClient))
			}
			server := newTestServer(t, opts...)
			_, err := server.RestartWorkUnit(ctx, goodReq)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("RestartWorkUnit() returned error %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestRestartWorkUnit_TaskEnqueueFails(t *testing.T) {
	ctx := context.Background()

	goodReq := &apipb.RestartWorkUnitRequest{Name: "experiments/123/workUnits/456"}
	workUnit := &msgpb.WorkUnit{
		Name:                     "experiments/123/workUnits/456",
		WorkUnitType:             msgpb.WorkUnit_STANDARD,
		Executable:               testExecutable,
		CurrentWorkUnitExecution: &msgpb.WorkUnitExecution{Name: "experiments/123/workUnits/456/executions/1"},
	}
	xidClient := &mockXidClient{
		checkUserAuthorizationResp: &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)},
	}
	storage := &storage.MockStorage{
		WorkUnit:   workUnit,
		Experiment: &msgpb.Experiment{Name: "experiments/123"},
	}
	taskQueueClient := &tq.FakeClient{
		EnqueueErr: fmt.Errorf("task enqueue failed"),
	}

	server := newTestServer(t,
		withStorage(storage),
		withXidClient(xidClient),
		withWorkUnitActuator(taskQueueClient),
	)
	_, err := server.RestartWorkUnit(ctx, goodReq)
	if !errors.Is(err, grpcerr.ErrInternal) {
		t.Errorf("RestartWorkUnit() returned error %v, want %v", err, grpcerr.ErrInternal)
	}
}
