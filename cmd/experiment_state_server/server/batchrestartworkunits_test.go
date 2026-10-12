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
	"sort"
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
	"xmanager_cloud/auth/auth"
	apipb "xmanager_cloud/experiment_state_server/proto/api_go_proto"
	msgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/prototest"
	"xmanager_cloud/storage/storage"
	tq "xmanager_cloud/task_queue/client"
	actuatortaskpb "xmanager_cloud/task_queue/proto/task_go_proto"
	"xmanager_cloud/validator/validator"
	xidgrpcpb "xmanager_cloud/xid_service/proto/api_go_grpc_proto"
	xidapipb "xmanager_cloud/xid_service/proto/api_go_proto"
	xidmsgpb "xmanager_cloud/xid_service/proto/messages_go_proto"
)

func TestBatchRestartWorkUnits_HappyPath(t *testing.T) {
	ctx := context.Background()
	server, taskQueueClient := createExperimentStateServer(ctx, t)

	exp := &msgpb.Experiment{
		Title: "test experiment for batchrestartworkunit",
		Acls: &xidmsgpb.XidAcls{
			Owners: []*xidmsgpb.XidAcls_Role{{Principal: &xidmsgpb.XidAcls_Role_User{User: "test@example.com"}}},
		},
	}
	createdExp := mustCreateExperiment(ctx, t, server, exp)

	workUnit1 := &msgpb.WorkUnit{
		WorkUnitType: msgpb.WorkUnit_STANDARD,
		Executable:   testExecutable,
	}
	workUnitID1 := int64(1)
	_ = mustCreateWorkUnit(ctx, t, server, createdExp.GetName(), workUnitID1, workUnit1)

	workUnit2 := &msgpb.WorkUnit{
		WorkUnitType: msgpb.WorkUnit_STANDARD,
		Executable:   testExecutable,
	}
	workUnitID2 := int64(2)
	_ = mustCreateWorkUnit(ctx, t, server, createdExp.GetName(), workUnitID2, workUnit2)
	taskQueueClient.EnqueuedTasks = nil // Clear state from CreateWorkUnit

	op, err := server.BatchRestartWorkUnits(ctx, &apipb.BatchRestartWorkUnitsRequest{Parent: createdExp.GetName()})
	if err != nil {
		t.Fatalf("BatchRestartWorkUnits() returned an unexpected error: %v", err)
	}
	if op.GetDone() {
		t.Errorf("BatchRestartWorkUnits() returned an operation with done=true")
	}
	if op.GetResponse() != nil {
		t.Errorf("BatchRestartWorkUnits() returned an operation with non-nil response")
	}
	if op.GetError() != nil {
		t.Errorf("BatchRestartWorkUnits() returned an operation with non-nil error")
	}
	var gotMetadata msgpb.OperationMetadata
	if err := op.GetMetadata().UnmarshalTo(&gotMetadata); err != nil {
		t.Errorf("Failed to unmarshal operation metadata: %v", err)
	}
	workUnitName1 := fmt.Sprintf("%s/workUnits/%d", createdExp.GetName(), workUnitID1)
	workUnitName2 := fmt.Sprintf("%s/workUnits/%d", createdExp.GetName(), workUnitID2)
	if gotMetadata.GetTarget() != workUnitName1 && gotMetadata.GetTarget() != workUnitName2 {
		t.Errorf("BatchRestartWorkUnits() operation metadata target got %q, want %q or %q", gotMetadata.GetTarget(), workUnitName1, workUnitName2)
	}
	if gotMetadata.GetActionType() != msgpb.OperationMetadata_RESTART {
		t.Errorf("BatchRestartWorkUnits() operation metadata action type got %q, want %q", gotMetadata.GetActionType(), msgpb.OperationMetadata_RESTART)
	}
	if gotMetadata.GetOriginalRequester() != "test@example.com" {
		t.Errorf("BatchRestartWorkUnits() operation metadata original requester got %q, want %q", gotMetadata.GetOriginalRequester(), "test@example.com")
	}

	if len(taskQueueClient.EnqueuedTasks) != 2 {
		t.Fatalf("BatchRestartWorkUnits() did not enqueue exactly two tasks, got %d", len(taskQueueClient.EnqueuedTasks))
	}

	for i, task := range taskQueueClient.EnqueuedTasks {
		action := task.Action
		if action == nil {
			t.Fatalf("Task action is nil for task %d", i)
		}

		restartAction := action.GetRestart()
		if restartAction == nil {
			t.Fatalf("Task action is not Restart for task %d: %v", i, action)
		}

		var wantWorkUnit *msgpb.WorkUnit
		if task.WorkUnit.GetName() == fmt.Sprintf("%s/workUnits/%d", createdExp.GetName(), workUnitID1) {
			wantWorkUnit = workUnit1
		} else if task.WorkUnit.GetName() == fmt.Sprintf("%s/workUnits/%d", createdExp.GetName(), workUnitID2) {
			wantWorkUnit = workUnit2
		} else {
			t.Errorf("Enqueued restart action for task %d has wrong work unit name: %s", i, task.WorkUnit.GetName())
			continue
		}

		gotXID, gotWID, gotEID, err := validator.ValidateWorkUnitExecutionName(task.Target)
		if err != nil {
			t.Fatalf("Failed to validate work unit execution name: %v", err)
		}

		wantXID, wantWID, err := validator.ValidateWorkUnitName(wantWorkUnit.GetName())
		if err != nil {
			t.Fatalf("Failed to validate work unit execution name: %v", err)
		}

		if gotXID != wantXID {
			t.Errorf("Enqueued restart action for task %d has wrong XID: got %d, want %d", i, gotXID, wantXID)
		}
		if gotWID != wantWID {
			t.Errorf("Enqueued restart action for task %d has wrong work unit ID: got %d, want %d", i, gotWID, wantWID)
		}
		if gotEID != 1 {
			t.Errorf("Enqueued restart action for task %d has wrong execution ID: got %d, want 1", i, gotEID)
		}

		if diff := cmp.Diff(task.WorkUnit, wantWorkUnit, protocmp.Transform(), protocmp.IgnoreFields(&msgpb.WorkUnit{}, "name", "create_time", "update_time", "current_work_unit_execution")); diff != "" {
			t.Errorf("Enqueued restart action for task %d mismatch (-want +got):\n%s", i, diff)
		}
	}

	// Check that the work unit executions were restarted.
	xid, err := validator.ValidateExperimentName(createdExp.GetName())
	if err != nil {
		t.Fatalf("ValidateExperimentName() returned an unexpected error: %v", err)
	}

	for _, workUnitID := range []int64{workUnitID1, workUnitID2} {
		workUnitExecutions, err := server.WorkUnitHandler.storage.ListWorkUnitExecutions(ctx, xid, workUnitID)
		if err != nil {
			t.Fatalf("ListWorkUnitExecutions() for work unit %d returned an unexpected error: %v", workUnitID, err)
		}
		if len(workUnitExecutions) != 2 {
			t.Fatalf("ListWorkUnitExecutions() for work unit %d returned %d work unit executions, want 2", workUnitID, len(workUnitExecutions))
		}

		newWorkUnitExecution := workUnitExecutions[1]
		oldWorkUnitExecution := workUnitExecutions[0]

		if newWorkUnitExecution.GetStatus().GetState() != msgpb.WorkUnitStatus_INITIALIZING {
			t.Errorf("New work unit execution state is %v, want %v", newWorkUnitExecution.GetStatus().GetState(), msgpb.WorkUnitStatus_INITIALIZING)
		}
		if oldWorkUnitExecution.GetStatus().GetState() != msgpb.WorkUnitStatus_RESTARTING {
			t.Errorf("Old work unit execution state is %v, want %v", oldWorkUnitExecution.GetStatus().GetState(), msgpb.WorkUnitStatus_RESTARTING)
		}

		newExecution, err := server.WorkUnitHandler.storage.FetchCurrentWorkUnitExecution(ctx, xid, workUnitID)
		if err != nil {
			t.Fatalf("FetchWorkUnitExecution() for work unit %d returned an unexpected error: %v", workUnitID, err)
		}
		_, _, eid, err := validator.ValidateWorkUnitExecutionName(newExecution.GetName())
		if err != nil {
			t.Fatalf("ValidateWorkUnitExecutionName() for work unit %d returned an unexpected error: %v", workUnitID, err)
		}
		if eid != 2 {
			t.Errorf("New work unit execution for work unit %d has wrong execution ID: got %d, want 2", workUnitID, eid)
		}
	}
}

func TestBatchRestartWorkUnits_FilterReturnsNoWorkUnits(t *testing.T) {
	ctx := context.Background()
	server, taskQueueClient := createExperimentStateServer(ctx, t)

	exp := &msgpb.Experiment{
		Title: "test experiment for batchrestartworkunit filter no work units",
		Acls: &xidmsgpb.XidAcls{
			Owners: []*xidmsgpb.XidAcls_Role{{Principal: &xidmsgpb.XidAcls_Role_User{User: "test@example.com"}}},
		},
	}
	createdExp := mustCreateExperiment(ctx, t, server, exp)

	workUnit1 := &msgpb.WorkUnit{
		WorkUnitType: msgpb.WorkUnit_STANDARD,
		Tags:         []string{"a"},
		Executable:   testExecutable,
	}
	workUnitID1 := int64(1)
	_ = mustCreateWorkUnit(ctx, t, server, createdExp.GetName(), workUnitID1, workUnit1)
	taskQueueClient.EnqueuedTasks = nil // Clear state from CreateWorkUnit

	op, err := server.BatchRestartWorkUnits(ctx, &apipb.BatchRestartWorkUnitsRequest{Parent: createdExp.GetName(), Filter: "tags:\"b\""})
	if err != nil {
		t.Fatalf("BatchRestartWorkUnits() returned an unexpected error: %v", err)
	}

	if !op.GetDone() {
		t.Errorf("BatchRestartWorkUnits() returned an operation with done=false")
	}

	if len(taskQueueClient.EnqueuedTasks) != 0 {
		t.Fatalf("BatchRestartWorkUnits() enqueued %d tasks, want 0", len(taskQueueClient.EnqueuedTasks))
	}
}

func TestBatchRestartWorkUnits_Filter(t *testing.T) {
	ctx := context.Background()
	server, taskQueueClient := createExperimentStateServer(ctx, t)

	exp := &msgpb.Experiment{
		Title: "test experiment for batchrestartworkunit filter",
		Acls: &xidmsgpb.XidAcls{
			Owners: []*xidmsgpb.XidAcls_Role{{Principal: &xidmsgpb.XidAcls_Role_User{User: "test@example.com"}}},
		},
	}
	createdExp := mustCreateExperiment(ctx, t, server, exp)

	baseStandardWorkUnit := &msgpb.WorkUnit{
		WorkUnitType: msgpb.WorkUnit_STANDARD,
		Executable:   testExecutable,
	}

	workUnit1 := prototest.Modify(baseStandardWorkUnit, func(wu *msgpb.WorkUnit) { wu.Tags = []string{"a"} })
	workUnitID1 := int64(1)
	_ = mustCreateWorkUnit(ctx, t, server, createdExp.GetName(), workUnitID1, workUnit1)

	workUnit2 := prototest.Modify(baseStandardWorkUnit, func(wu *msgpb.WorkUnit) { wu.Tags = []string{"a"} })
	workUnitID2 := int64(2)
	_ = mustCreateWorkUnit(ctx, t, server, createdExp.GetName(), workUnitID2, workUnit2)

	workUnit3 := prototest.Modify(baseStandardWorkUnit, func(wu *msgpb.WorkUnit) { wu.Tags = []string{"b"} })
	workUnitID3 := int64(3)
	_ = mustCreateWorkUnit(ctx, t, server, createdExp.GetName(), workUnitID3, workUnit3)
	taskQueueClient.EnqueuedTasks = nil // Clear state from CreateWorkUnit

	_, err := server.BatchRestartWorkUnits(ctx, &apipb.BatchRestartWorkUnitsRequest{Parent: createdExp.GetName(), Filter: "tags:\"a\""})
	if err != nil {
		t.Fatalf("BatchRestartWorkUnits() returned an unexpected error: %v", err)
	}

	if len(taskQueueClient.EnqueuedTasks) != 2 {
		t.Fatalf("BatchRestartWorkUnits() did not enqueue exactly two tasks, got %d", len(taskQueueClient.EnqueuedTasks))
	}

	var restartedWUIDs []int64
	for _, task := range taskQueueClient.EnqueuedTasks {
		_, wid, err := validator.ValidateWorkUnitName(task.WorkUnit.GetName())
		if err != nil {
			t.Fatalf("Failed to validate work unit name: %v", err)
		}
		restartedWUIDs = append(restartedWUIDs, wid)
	}

	sort.Slice(restartedWUIDs, func(i, j int) bool { return restartedWUIDs[i] < restartedWUIDs[j] })
	if diff := cmp.Diff([]int64{workUnitID1, workUnitID2}, restartedWUIDs); diff != "" {
		t.Errorf("BatchRestartWorkUnits() restarted wrong work units (-want +got):\n%s", diff)
	}
}

func TestBatchRestartWorkUnits_BadPaths(t *testing.T) {
	ctx := context.Background()
	server := newTestServer(t)

	testCases := []struct {
		name    string
		req     *apipb.BatchRestartWorkUnitsRequest
		wantErr error
	}{
		{
			name:    "invalid_parent_name",
			req:     &apipb.BatchRestartWorkUnitsRequest{Parent: "invalid-name"},
			wantErr: grpcerr.ErrInvalidArgument,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := server.BatchRestartWorkUnits(ctx, tc.req)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("BatchRestartWorkUnits() returned error %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestBatchRestartWorkUnits_DependencyFailures(t *testing.T) {
	ctx := context.Background()

	goodReq := &apipb.BatchRestartWorkUnitsRequest{Parent: "experiments/123"}
	workUnit := &msgpb.WorkUnit{
		Name:                     "experiments/123/workUnits/456",
		WorkUnitType:             msgpb.WorkUnit_STANDARD,
		Executable:               testExecutable,
		CurrentWorkUnitExecution: &msgpb.WorkUnitExecution{Name: "experiments/123/workUnits/456/executions/1"},
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
			name: "storage_list_work_units_fails",
			xidClient: &mockXidClient{
				checkUserAuthorizationResp: &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)},
			},
			storage: &storage.MockStorage{
				ListWorkUnitsErr: fmt.Errorf("storage list work units failed"),
			},
			wantErr: grpcerr.ErrInternal,
		},
		{
			name: "storage_restart_work_unit_executions_fails",
			xidClient: &mockXidClient{
				checkUserAuthorizationResp: &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)},
			},
			storage: &storage.MockStorage{
				ListWorkUnitsResp:   []*msgpb.WorkUnit{workUnit},
				RunInTransactionErr: fmt.Errorf("storage restart work unit executions failed"),
			},
			wantErr: grpcerr.ErrInternal,
		},
		{
			name: "storage_restart_work_unit_executions_misses_execution",
			xidClient: &mockXidClient{
				checkUserAuthorizationResp: &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)},
			},
			storage: &storage.MockStorage{
				ListWorkUnitsResp: []*msgpb.WorkUnit{workUnit},
				FetchWorkUnitErr:  fmt.Errorf("work unit missing"),
			},
			wantErr: grpcerr.ErrInternal,
		},
		{
			name: "storage_add_operation_fails",
			xidClient: &mockXidClient{
				checkUserAuthorizationResp: &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)},
			},
			storage: &storage.MockStorage{
				ListWorkUnitsResp:       []*msgpb.WorkUnit{workUnit},
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
			server := newTestServer(t, opts...)
			_, err := server.BatchRestartWorkUnits(ctx, goodReq)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("BatchRestartWorkUnits() returned error %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestBatchRestartWorkUnits_TaskEnqueueFails(t *testing.T) {
	ctx := context.Background()

	goodReq := &apipb.BatchRestartWorkUnitsRequest{Parent: "experiments/123"}
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
		ListWorkUnitsResp: []*msgpb.WorkUnit{workUnit},
		WorkUnit:          workUnit,
		Experiment:        &msgpb.Experiment{Name: "experiments/123"},
	}
	taskQueueClient := &tq.FakeClient{
		EnqueueErr: fmt.Errorf("task enqueue failed"),
	}

	server := newTestServer(t,
		withStorage(storage),
		withXidClient(xidClient),
		withWorkUnitActuator(taskQueueClient),
	)
	_, err := server.BatchRestartWorkUnits(ctx, goodReq)
	if !errors.Is(err, grpcerr.ErrInternal) {
		t.Errorf("BatchRestartWorkUnits() returned error %v, want %v", err, grpcerr.ErrInternal)
	}
}

func TestBatchRestartWorkUnits_PartialFailure(t *testing.T) {
	ctx := context.Background()
	server, taskQueueClient := createExperimentStateServer(ctx, t)

	exp := &msgpb.Experiment{
		Title: "test experiment for batchrestartworkunit partial failure",
		Acls: &xidmsgpb.XidAcls{
			Owners: []*xidmsgpb.XidAcls_Role{{Principal: &xidmsgpb.XidAcls_Role_User{User: "test@example.com"}}},
		},
	}
	createdExp := mustCreateExperiment(ctx, t, server, exp)

	workUnit1 := &msgpb.WorkUnit{
		WorkUnitType: msgpb.WorkUnit_STANDARD,
		Executable:   testExecutable,
	}
	workUnitID1 := int64(1)
	_ = mustCreateWorkUnit(ctx, t, server, createdExp.GetName(), workUnitID1, workUnit1)

	workUnit2 := &msgpb.WorkUnit{
		WorkUnitType: msgpb.WorkUnit_STANDARD,
		Executable:   testExecutable,
	}
	workUnitID2 := int64(2)
	_ = mustCreateWorkUnit(ctx, t, server, createdExp.GetName(), workUnitID2, workUnit2)
	taskQueueClient.EnqueuedTasks = nil // Clear state from CreateWorkUnit

	// Fail on second call to EnqueueWorkUnitTask.
	i := 0
	taskQueueClient.EnqueueFunc = func(ctx context.Context, target string, workUnit *msgpb.WorkUnit, action *actuatortaskpb.Action, operationName string) error {
		i++
		if i == 2 {
			return fmt.Errorf("task enqueue failed on second call")
		}
		return nil
	}

	_, err := server.BatchRestartWorkUnits(ctx, &apipb.BatchRestartWorkUnitsRequest{Parent: createdExp.GetName()})
	if err == nil {
		t.Fatalf("BatchRestartWorkUnits() with partial failure should return an error, but returned nil")
	}

	// It should have tried to enqueue 2 tasks.
	if len(taskQueueClient.EnqueuedTasks) != 2 {
		t.Fatalf("BatchRestartWorkUnits() should have attempted to enqueue 2 tasks, got %d", len(taskQueueClient.EnqueuedTasks))
	}

	// Check that work unit executions for both work units were restarted, even though one failed to enqueue.
	xid, err := validator.ValidateExperimentName(createdExp.GetName())
	if err != nil {
		t.Fatalf("ValidateExperimentName() returned an unexpected error: %v", err)
	}
	for _, workUnitID := range []int64{workUnitID1, workUnitID2} {
		newExecution, err := server.WorkUnitHandler.storage.FetchCurrentWorkUnitExecution(ctx, xid, workUnitID)
		if err != nil {
			t.Fatalf("FetchWorkUnitExecution() for work unit %d returned an unexpected error: %v", workUnitID, err)
		}
		_, _, eid, err := validator.ValidateWorkUnitExecutionName(newExecution.GetName())
		if err != nil {
			t.Fatalf("ValidateWorkUnitExecutionName() for work unit %d returned an unexpected error: %v", workUnitID, err)
		}
		if eid != 2 {
			t.Errorf("New work unit execution for work unit %d has wrong execution ID: got %d, want 2", workUnitID, eid)
		}
	}
}

func TestBatchRestartWorkUnits_NoCurrentExecutionFails(t *testing.T) {
	ctx := context.Background()
	wuWithoutExec := &msgpb.WorkUnit{
		Name: "experiments/1/workUnits/1",
	}
	mockStorage := &storage.MockStorage{
		Experiment:        &msgpb.Experiment{Name: "experiments/1"},
		WorkUnit:          wuWithoutExec,
		ListWorkUnitsResp: []*msgpb.WorkUnit{wuWithoutExec},
	}
	mockXid := &mockXidClient{
		checkUserAuthorizationResp: &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)},
	}
	server := newTestServer(t, withStorage(mockStorage), withXidClient(mockXid))

	_, err := server.BatchRestartWorkUnits(ctx, &apipb.BatchRestartWorkUnitsRequest{Parent: "experiments/1"})
	if err == nil {
		t.Fatal("BatchRestartWorkUnits() with work unit having no current execution want error, got nil")
	}
}
