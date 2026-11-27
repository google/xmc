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
	"xmanager_cloud/internal/auth"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/storage"
	tq "xmanager_cloud/internal/task_queue"
	"xmanager_cloud/internal/validator"

	apipb "xmanager_cloud/api/protos/experiment"
	xidapipb "xmanager_cloud/api/protos/xid"
)

func TestStopWorkUnit_HappyPath(t *testing.T) {
	ctx := context.Background()
	server, taskQueueClient := createExperimentStateServer(ctx, t)

	exp := &apipb.Experiment{
		Title: "test experiment for stopworkunit",
		Acls: &xidapipb.XidAcls{
			Owners: []*xidapipb.XidAcls_Role{{Principal: &xidapipb.XidAcls_Role_User{User: "test@example.com"}}},
		},
	}
	createdExp := mustCreateExperiment(ctx, t, server, exp)

	workUnit := &apipb.WorkUnit{
		WorkUnitType: apipb.WorkUnit_STANDARD,
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
	var gotMetadata apipb.OperationMetadata
	if err := op.GetMetadata().UnmarshalTo(&gotMetadata); err != nil {
		t.Errorf("Failed to unmarshal operation metadata: %v", err)
	}
	if !strings.HasPrefix(gotMetadata.GetTarget(), createdExp.GetName()+"/workUnits/") {
		t.Errorf("StopWorkUnit() operation metadata target %q does not start with experiment prefix", gotMetadata.GetTarget())
	}
	if gotMetadata.GetActionType() != apipb.OperationMetadata_STOP {
		t.Errorf("StopWorkUnit() operation metadata action type got %q, want %q", gotMetadata.GetActionType(), apipb.OperationMetadata_STOP)
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
	if workUnitExecution.GetStatus().GetState() != apipb.WorkUnitStatus_STOPPING {
		t.Errorf("Work unit execution state got %v, want %v", workUnitExecution.GetStatus().GetState(), apipb.WorkUnitStatus_STOPPING)
	}
}

func TestStopWorkUnit_SchedulingPausedTransitionsDirectlyToStopped(t *testing.T) {
	ctx := context.Background()
	server, taskQueueClient := createExperimentStateServer(ctx, t)

	exp := &apipb.Experiment{
		Title: "test experiment for stopworkunit from scheduling paused",
		Acls: &xidapipb.XidAcls{
			Owners: []*xidapipb.XidAcls_Role{{Principal: &xidapipb.XidAcls_Role_User{User: "test@example.com"}}},
		},
	}
	createdExp := mustCreateExperiment(ctx, t, server, exp)

	workUnit := &apipb.WorkUnit{
		WorkUnitType: apipb.WorkUnit_STANDARD,
		Executable:   testExecutable,
	}
	workUnitID := int64(1)
	workUnitName := fmt.Sprintf("%s/workUnits/%d", createdExp.GetName(), workUnitID)
	_ = mustCreateWorkUnit(ctx, t, server, createdExp.GetName(), workUnitID, workUnit)

	xid, _, err := validator.ValidateWorkUnitName(workUnitName)
	if err != nil {
		t.Fatalf("ValidateWorkUnitName() returned an unexpected error: %v", err)
	}
	// Transition the work unit to SCHEDULING_PAUSED first.
	mustSetWorkUnitState(ctx, t, server, xid, workUnitID, apipb.WorkUnitStatus_SCHEDULING_PAUSED)
	taskQueueClient.LastEnqueuedAction = nil

	if _, err := server.StopWorkUnit(ctx, &apipb.StopWorkUnitRequest{Name: workUnitName}); err != nil {
		t.Fatalf("StopWorkUnit() returned an unexpected error: %v", err)
	}

	if taskQueueClient.LastEnqueuedAction == nil || taskQueueClient.LastEnqueuedAction.GetDelete() == nil {
		t.Fatalf("StopWorkUnit() did not enqueue a DeleteAction: %v", taskQueueClient.LastEnqueuedAction)
	}

	workUnitExecution, err := server.WorkUnitHandler.storage.FetchCurrentWorkUnitExecution(ctx, xid, workUnitID)
	if err != nil {
		t.Fatalf("FetchCurrentWorkUnitExecution() returned an unexpected error: %v", err)
	}
	if workUnitExecution.GetStatus().GetState() != apipb.WorkUnitStatus_STOPPED {
		t.Errorf("Work unit execution state got %v, want %v", workUnitExecution.GetStatus().GetState(), apipb.WorkUnitStatus_STOPPED)
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
			wantErr: grpcerr.ErrInvalidArgument,
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
	workUnit := &apipb.WorkUnit{
		Name:                     "experiments/123/workUnits/456",
		WorkUnitType:             apipb.WorkUnit_STANDARD,
		Executable:               testExecutable,
		CurrentWorkUnitExecution: &apipb.WorkUnitExecution{Name: "experiments/123/workUnits/456/executions/1"},
	}

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
			name: "storage_fetch_work_unit_fails",
			xidClient: &mockXidClient{
				checkUserAuthorizationResp: &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)},
			},
			storage: &storage.MockStorage{
				FetchWorkUnitErr: fmt.Errorf("storage fetch work unit failed"),
			},
			wantErr: grpcerr.ErrInternal,
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
			name: "storage_add_operation_fails",
			xidClient: &mockXidClient{
				checkUserAuthorizationResp: &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)},
			},
			storage: &storage.MockStorage{
				WorkUnit:                workUnit,
				Experiment:              &apipb.Experiment{Name: "experiments/123"},
				AddWorkUnitOperationErr: fmt.Errorf("Storage add operation failed"),
			},
			wantErr: grpcerr.ErrInternal,
		},
		{
			name: "storage_update_work_unit_executions_fails",
			xidClient: &mockXidClient{
				checkUserAuthorizationResp: &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)},
			},
			storage: &storage.MockStorage{
				WorkUnit:                          workUnit,
				Experiment:                        &apipb.Experiment{Name: "experiments/123"},
				UpdateCurrentWorkUnitExecutionErr: fmt.Errorf("Storage update work unit executions failed"),
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
			_, err := server.StopWorkUnit(ctx, goodReq)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("StopWorkUnit() got error %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestStopWorkUnit_TaskEnqueueFails(t *testing.T) {
	ctx := context.Background()

	workUnit := &apipb.WorkUnit{
		Name:                     "experiments/123/workUnits/456",
		WorkUnitType:             apipb.WorkUnit_STANDARD,
		Executable:               testExecutable,
		CurrentWorkUnitExecution: &apipb.WorkUnitExecution{Name: "experiments/123/workUnits/456/executions/1"},
	}

	xidClient := &mockXidClient{
		checkUserAuthorizationResp: &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)},
	}
	storage := &storage.MockStorage{
		WorkUnit:   workUnit,
		Experiment: &apipb.Experiment{Name: "experiments/123"},
	}
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
	if !errors.Is(err, grpcerr.ErrInternal) {
		t.Errorf("StopWorkUnit() got error %v, want %v", err, grpcerr.ErrInternal)
	}
	if got := len(taskQueueClient.EnqueuedTasks); got != 1 {
		t.Fatalf("StopWorkUnit() attempted %d enqueues, want 1", got)
	}
	checkOperationStopped(t, storage, 123, 456)
	checkSystemMessage(t, storage, 456, "experiments/123/workUnits/456/executions/1", &apipb.StructuredMessage{
		Source:         apipb.StructuredMessage_XMANAGER_BACKEND,
		Severity:       apipb.StructuredMessage_ERROR,
		UserFacingText: "Failed to stop work unit. Please try again.",
		Parent:         "experiments/123/workUnits/456",
	})
}

func TestStopWorkUnit_ByCurrentState(t *testing.T) {
	ctx := context.Background()
	server, taskQueueClient := createExperimentStateServer(ctx, t)

	exp := &apipb.Experiment{
		Title: "test experiment for stopworkunit by state",
		Acls: &xidapipb.XidAcls{
			Owners: []*xidapipb.XidAcls_Role{{Principal: &xidapipb.XidAcls_Role_User{User: "test@example.com"}}},
		},
	}
	createdExp := mustCreateExperiment(ctx, t, server, exp)
	xid, err := validator.ValidateExperimentName(createdExp.GetName())
	if err != nil {
		t.Fatalf("ValidateExperimentName() returned an unexpected error: %v", err)
	}

	testCases := []struct {
		name            string
		wid             int64
		state           apipb.WorkUnitStatus_WorkUnitState
		wantDone        bool
		wantEmptyOpName bool
		wantEnqueued    bool
		wantFinalState  apipb.WorkUnitStatus_WorkUnitState
	}{
		{
			name:            "completed_is_noop",
			wid:             1,
			state:           apipb.WorkUnitStatus_COMPLETED,
			wantDone:        true,
			wantEmptyOpName: true,
			wantEnqueued:    false,
			wantFinalState:  apipb.WorkUnitStatus_COMPLETED,
		},
		{
			name:            "failed_enqueues_delete_and_preserves_failed_state",
			wid:             2,
			state:           apipb.WorkUnitStatus_FAILED,
			wantDone:        false,
			wantEmptyOpName: false,
			wantEnqueued:    true,
			wantFinalState:  apipb.WorkUnitStatus_FAILED,
		},
		{
			name:            "stopped_is_noop",
			wid:             3,
			state:           apipb.WorkUnitStatus_STOPPED,
			wantDone:        true,
			wantEmptyOpName: true,
			wantEnqueued:    false,
			wantFinalState:  apipb.WorkUnitStatus_STOPPED,
		},
		{
			name:            "stopping_re_enqueues_delete",
			wid:             4,
			state:           apipb.WorkUnitStatus_STOPPING,
			wantDone:        false,
			wantEmptyOpName: false,
			wantEnqueued:    true,
			wantFinalState:  apipb.WorkUnitStatus_STOPPING,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			workUnit := &apipb.WorkUnit{
				WorkUnitType: apipb.WorkUnit_STANDARD,
				Executable:   testExecutable,
			}
			workUnitName := fmt.Sprintf("%s/workUnits/%d", createdExp.GetName(), tc.wid)
			_ = mustCreateWorkUnit(ctx, t, server, createdExp.GetName(), tc.wid, workUnit)
			mustSetWorkUnitState(ctx, t, server, xid, tc.wid, tc.state)
			taskQueueClient.LastEnqueuedAction = nil
			taskQueueClient.EnqueuedTasks = nil

			op, err := server.StopWorkUnit(ctx, &apipb.StopWorkUnitRequest{Name: workUnitName})
			if err != nil {
				t.Fatalf("StopWorkUnit() returned an unexpected error: %v", err)
			}
			if op.GetDone() != tc.wantDone {
				t.Errorf("StopWorkUnit() returned op.GetDone()=%v, want %v", op.GetDone(), tc.wantDone)
			}
			if (op.GetName() == "") != tc.wantEmptyOpName {
				t.Errorf("StopWorkUnit() returned op.GetName()=%q, wantEmptyOpName=%v", op.GetName(), tc.wantEmptyOpName)
			}
			if tc.wantEnqueued {
				if taskQueueClient.LastEnqueuedAction == nil || taskQueueClient.LastEnqueuedAction.GetDelete() == nil {
					t.Errorf("StopWorkUnit() did not enqueue DeleteAction for state %v: %v", tc.state, taskQueueClient.LastEnqueuedAction)
				}
			} else if taskQueueClient.LastEnqueuedAction != nil || len(taskQueueClient.EnqueuedTasks) != 0 {
				t.Errorf("StopWorkUnit() enqueued task for state %v, want no task enqueued", tc.state)
			}

			gotExec, err := server.WorkUnitHandler.storage.FetchCurrentWorkUnitExecution(ctx, xid, tc.wid)
			if err != nil {
				t.Fatalf("FetchCurrentWorkUnitExecution() returned an unexpected error: %v", err)
			}
			if gotExec.GetStatus().GetState() != tc.wantFinalState {
				t.Errorf("Work unit execution state got %v, want %v", gotExec.GetStatus().GetState(), tc.wantFinalState)
			}
		})
	}
}

func TestStopWorkUnit_SkippedStateDoesNotAddOperation(t *testing.T) {
	ctx := context.Background()

	for _, state := range []apipb.WorkUnitStatus_WorkUnitState{
		apipb.WorkUnitStatus_COMPLETED,
		apipb.WorkUnitStatus_STOPPED,
	} {
		t.Run(state.String(), func(t *testing.T) {
			workUnit := &apipb.WorkUnit{
				Name:         "experiments/123/workUnits/456",
				WorkUnitType: apipb.WorkUnit_STANDARD,
				Executable:   testExecutable,
				CurrentWorkUnitExecution: &apipb.WorkUnitExecution{
					Name:   "experiments/123/workUnits/456/executions/1",
					Status: &apipb.WorkUnitStatus{State: state},
				},
			}
			xidClient := &mockXidClient{
				checkUserAuthorizationResp: &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)},
			}
			mockStore := &storage.MockStorage{
				WorkUnit:                workUnit,
				Experiment:              &apipb.Experiment{Name: "experiments/123"},
				AddWorkUnitOperationErr: fmt.Errorf("unexpected AddWorkUnitOperation call"),
			}
			taskQueueClient := &tq.FakeClient{}

			server := newTestServer(t,
				withStorage(mockStore),
				withXidClient(xidClient),
				withWorkUnitActuator(taskQueueClient),
			)

			op, err := server.StopWorkUnit(ctx, &apipb.StopWorkUnitRequest{Name: "experiments/123/workUnits/456"})
			if err != nil {
				t.Fatalf("StopWorkUnit() returned unexpected error: %v", err)
			}
			if !op.GetDone() {
				t.Errorf("StopWorkUnit() returned op.GetDone()=false, want true")
			}
			if op.GetName() != "" {
				t.Errorf("StopWorkUnit() returned op.GetName()=%q, want empty", op.GetName())
			}
			if len(taskQueueClient.EnqueuedTasks) != 0 {
				t.Errorf("StopWorkUnit() enqueued %d tasks, want 0", len(taskQueueClient.EnqueuedTasks))
			}
		})
	}
}
