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

	"golang/cmp/cmp"
	"golang/protobuf/v2/proto/proto"
	"golang/protobuf/v2/testing/protocmp/protocmp"
	"task/go/status"
	"xmanager_cloud/auth/auth"
	"xmanager_cloud/experiment_state_server/config"
	"xmanager_cloud/storage/storage"
	tq "xmanager_cloud/task_queue/client"
	"xmanager_cloud/validator/validator"

	apipb "xmanager_cloud/experiment_state_server/proto/api_go_proto"
	msgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	actuatortaskpb "xmanager_cloud/task_queue/proto/task_go_proto"
	xidgrpcpb "xmanager_cloud/xid_service/proto/api_go_grpc_proto"
	xidapipb "xmanager_cloud/xid_service/proto/api_go_proto"
	xidmsgpb "xmanager_cloud/xid_service/proto/messages_go_proto"
)

func TestBatchStopWorkUnits_HappyPath(t *testing.T) {
	ctx := context.Background()
	server, taskQueueClient := createExperimentStateServer(ctx, t)

	exp := &msgpb.Experiment{
		Title: "test experiment for batchstopworkunit",
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

	op, err := server.BatchStopWorkUnits(ctx, &apipb.BatchStopWorkUnitsRequest{Parent: createdExp.GetName()})
	if err != nil {
		t.Fatalf("BatchStopWorkUnits() returned an unexpected error: %v", err)
	}
	if op.GetDone() {
		t.Errorf("BatchStopWorkUnits() returned an operation with done=true")
	}
	if op.GetResponse() != nil {
		t.Errorf("BatchStopWorkUnits() returned an operation with non-nil response")
	}
	if op.GetError() != nil {
		t.Errorf("BatchStopWorkUnits() returned an operation with non-nil error")
	}
	var gotMetadata msgpb.OperationMetadata
	if err := op.GetMetadata().UnmarshalTo(&gotMetadata); err != nil {
		t.Errorf("Failed to unmarshal operation metadata: %v", err)
	}
	workUnitName1 := fmt.Sprintf("%s/workUnits/%d", createdExp.GetName(), workUnitID1)
	workUnitName2 := fmt.Sprintf("%s/workUnits/%d", createdExp.GetName(), workUnitID2)
	if gotMetadata.GetTarget() != workUnitName1 && gotMetadata.GetTarget() != workUnitName2 {
		t.Errorf("BatchStopWorkUnits() operation metadata target got %q, want %q or %q", gotMetadata.GetTarget(), workUnitName1, workUnitName2)
	}
	if gotMetadata.GetActionType() != msgpb.OperationMetadata_STOP {
		t.Errorf("BatchStopWorkUnits() operation metadata action type got %q, want %q", gotMetadata.GetActionType(), msgpb.OperationMetadata_STOP)
	}
	if gotMetadata.GetOriginalRequester() != "test@example.com" {
		t.Errorf("BatchStopWorkUnits() operation metadata original requester got %q, want %q", gotMetadata.GetOriginalRequester(), "test@example.com")
	}

	if len(taskQueueClient.EnqueuedTasks) != 2 {
		t.Fatalf("BatchStopWorkUnits() did not enqueue exactly two tasks, got %d", len(taskQueueClient.EnqueuedTasks))
	}

	for i, task := range taskQueueClient.EnqueuedTasks {
		action := task.Action
		if action == nil {
			t.Fatalf("Task action is nil for task %d", i)
		}

		stopAction := action.GetDelete()
		if stopAction == nil {
			t.Fatalf("Task action is not Stop (Delete) for task %d: %v", i, action)
		}

		var wantWorkUnit *msgpb.WorkUnit
		if task.WorkUnit.GetName() == fmt.Sprintf("%s/workUnits/%d", createdExp.GetName(), workUnitID1) {
			wantWorkUnit = workUnit1
		} else if task.WorkUnit.GetName() == fmt.Sprintf("%s/workUnits/%d", createdExp.GetName(), workUnitID2) {
			wantWorkUnit = workUnit2
		} else {
			t.Errorf("Enqueued stop action for task %d has wrong work unit name: %s", i, task.WorkUnit.GetName())
			continue
		}

		if diff := cmp.Diff(task.WorkUnit, wantWorkUnit, protocmp.Transform(), protocmp.IgnoreFields(&msgpb.WorkUnit{}, "name", "create_time", "update_time", "current_work_unit_execution")); diff != "" {
			t.Errorf("Enqueued stop action for task %d mismatch (-want +got):\n%s", i, diff)
		}

		// Check that the target name is the current execution.
		if task.Target != task.WorkUnit.GetCurrentWorkUnitExecution().GetName() {
			t.Errorf("Enqueued stop action for task %d has wrong target name: got %s, want %s", i, task.Target, task.WorkUnit.GetCurrentWorkUnitExecution().GetName())
		}
	}

	// Check that the work unit executions were stopped.
	xid, err := validator.ValidateExperimentName(createdExp.GetName())
	if err != nil {
		t.Fatalf("ValidateExperimentName() returned an unexpected error: %v", err)
	}

	for _, workUnitID := range []int64{workUnitID1, workUnitID2} {
		workUnitExecution, err := server.storage.FetchCurrentWorkUnitExecution(ctx, xid, workUnitID)
		if err != nil {
			t.Fatalf("FetchCurrentWorkUnitExecution() for work unit %d returned an unexpected error: %v", workUnitID, err)
		}
		if workUnitExecution.GetStatus().GetState() != msgpb.WorkUnitStatus_STOPPING {
			t.Errorf("Work unit %d execution state got %v, want %v", workUnitID, workUnitExecution.GetStatus().GetState(), msgpb.WorkUnitStatus_STOPPING)
		}
	}
}

func TestBatchStopWorkUnits_Filter(t *testing.T) {
	ctx := context.Background()
	server, taskQueueClient := createExperimentStateServer(ctx, t)

	exp := &msgpb.Experiment{
		Title: "test experiment for batchstopworkunit filter",
		Acls: &xidmsgpb.XidAcls{
			Owners: []*xidmsgpb.XidAcls_Role{{Principal: &xidmsgpb.XidAcls_Role_User{User: "test@example.com"}}},
		},
	}
	createdExp := mustCreateExperiment(ctx, t, server, exp)

	baseStandardWorkUnit := &msgpb.WorkUnit{
		WorkUnitType: msgpb.WorkUnit_STANDARD,
		Executable:   testExecutable,
	}

	workUnit1 := proto.Modify(baseStandardWorkUnit, func(wu *msgpb.WorkUnit) { wu.Tags = []string{"a"} })
	workUnitID1 := int64(1)
	_ = mustCreateWorkUnit(ctx, t, server, createdExp.GetName(), workUnitID1, workUnit1)

	workUnit2 := proto.Modify(baseStandardWorkUnit, func(wu *msgpb.WorkUnit) { wu.Tags = []string{"a"} })
	workUnitID2 := int64(2)
	_ = mustCreateWorkUnit(ctx, t, server, createdExp.GetName(), workUnitID2, workUnit2)

	workUnit3 := proto.Modify(baseStandardWorkUnit, func(wu *msgpb.WorkUnit) { wu.Tags = []string{"b"} })
	workUnitID3 := int64(3)
	_ = mustCreateWorkUnit(ctx, t, server, createdExp.GetName(), workUnitID3, workUnit3)
	taskQueueClient.EnqueuedTasks = nil // Clear state from CreateWorkUnit

	_, err := server.BatchStopWorkUnits(ctx, &apipb.BatchStopWorkUnitsRequest{Parent: createdExp.GetName(), Filter: "tags:\"a\""})
	if err != nil {
		t.Fatalf("BatchStopWorkUnits() returned an unexpected error: %v", err)
	}

	if len(taskQueueClient.EnqueuedTasks) != 2 {
		t.Fatalf("BatchStopWorkUnits() did not enqueue exactly two tasks, got %d", len(taskQueueClient.EnqueuedTasks))
	}

	var stoppedWIDs []int64
	for _, task := range taskQueueClient.EnqueuedTasks {
		_, wid, err := validator.ValidateWorkUnitName(task.WorkUnit.GetName())
		if err != nil {
			t.Fatalf("Failed to validate work unit name: %v", err)
		}
		stoppedWIDs = append(stoppedWIDs, wid)
	}

	sort.Slice(stoppedWIDs, func(i, j int) bool { return stoppedWIDs[i] < stoppedWIDs[j] })
	if diff := cmp.Diff([]int64{workUnitID1, workUnitID2}, stoppedWIDs); diff != "" {
		t.Errorf("BatchStopWorkUnits() stopped wrong work units (-want +got):\n%s", diff)
	}
}

func TestBatchStopWorkUnits_BadPaths(t *testing.T) {
	ctx := context.Background()
	authorizer := createTestAuthorizer(ctx, t)
	cfg := &config.Config{}
	server := New(&storage.MockStorage{}, cfg, authorizer, &mockXidClient{}, nil)

	testCases := []struct {
		name    string
		req     *apipb.BatchStopWorkUnitsRequest
		wantErr error
	}{
		{
			name:    "invalid_parent_name",
			req:     &apipb.BatchStopWorkUnitsRequest{Parent: "invalid-name"},
			wantErr: status.ErrInvalidArgument,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := server.BatchStopWorkUnits(ctx, tc.req)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("BatchStopWorkUnits() got error %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestBatchStopWorkUnits_DependencyFailures(t *testing.T) {
	ctx := context.Background()
	cfg := &config.Config{}

	goodReq := &apipb.BatchStopWorkUnitsRequest{Parent: "experiments/123"}
	workUnit := &msgpb.WorkUnit{
		Name:         "experiments/123/workUnits/456",
		WorkUnitType: msgpb.WorkUnit_STANDARD,
		Executable:   testExecutable,
		CurrentWorkUnitExecution: &msgpb.WorkUnitExecution{
			Name: "experiments/123/workUnits/456/executions/1",
		},
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
			name: "storage_list_work_units_fails",
			xidClient: &mockXidClient{
				checkUserAuthorizationResp: &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)},
			},
			storage: &storage.MockStorage{
				ListWorkUnitsErr: fmt.Errorf("storage list work units failed"),
			},
			wantErr: status.ErrInternal,
		},
		{
			name: "storage_add_operation_fails",
			xidClient: &mockXidClient{
				checkUserAuthorizationResp: &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)},
			},
			storage: &storage.MockStorage{
				ListWorkUnitsResp:       []*msgpb.WorkUnit{workUnit},
				AddWorkUnitOperationErr: fmt.Errorf("storage add operation failed"),
			},
			wantErr: status.ErrInternal,
		},
		{
			name: "storage_update_work_unit_executions_fails",
			xidClient: &mockXidClient{
				checkUserAuthorizationResp: &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)},
			},
			storage: &storage.MockStorage{
				ListWorkUnitsResp:           []*msgpb.WorkUnit{workUnit},
				UpdateWorkUnitExecutionsErr: fmt.Errorf("Storage update work unit executions failed"),
			},
			wantErr: status.ErrInternal,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.authenticator == nil {
				tc.authenticator = createTestAuthorizer(ctx, t)
			}
			taskQueueClient := &tq.FakeClient{}
			server := New(tc.storage, cfg, tc.authenticator, tc.xidClient, taskQueueClient)
			_, err := server.BatchStopWorkUnits(ctx, goodReq)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("BatchStopWorkUnits() got error %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestBatchStopWorkUnits_TaskEnqueueFails(t *testing.T) {
	ctx := context.Background()
	cfg := &config.Config{}

	workUnit := &msgpb.WorkUnit{
		Name:         "experiments/123/workUnits/456",
		WorkUnitType: msgpb.WorkUnit_STANDARD,
		Executable:   testExecutable,
		CurrentWorkUnitExecution: &msgpb.WorkUnitExecution{
			Name: "experiments/123/workUnits/456/executions/1",
		},
	}

	xidClient := &mockXidClient{
		checkUserAuthorizationResp: &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)},
	}
	storage := &storage.MockStorage{ListWorkUnitsResp: []*msgpb.WorkUnit{workUnit}}
	taskQueueClient := &tq.FakeClient{
		EnqueueErr: fmt.Errorf("task enqueue failed"),
	}

	authenticator := createTestAuthorizer(ctx, t)
	server := New(storage, cfg, authenticator, xidClient, taskQueueClient)

	req := &apipb.BatchStopWorkUnitsRequest{Parent: "experiments/123"}
	_, err := server.BatchStopWorkUnits(ctx, req)
	if !errors.Is(err, status.ErrInternal) {
		t.Errorf("BatchStopWorkUnits() got error %v, want %v", err, status.ErrInternal)
	}
}

func TestBatchStopWorkUnits_PartialFailure(t *testing.T) {
	ctx := context.Background()
	server, taskQueueClient := createExperimentStateServer(ctx, t)

	exp := &msgpb.Experiment{
		Title: "test experiment for batchstopworkunit partial failure",
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

	_, err := server.BatchStopWorkUnits(ctx, &apipb.BatchStopWorkUnitsRequest{Parent: createdExp.GetName()})
	if err == nil {
		t.Fatalf("BatchStopWorkUnits() with partial failure should return an error, but returned nil")
	}

	// It should have tried to enqueue 2 tasks.
	if len(taskQueueClient.EnqueuedTasks) != 2 {
		t.Fatalf("BatchStopWorkUnits() should have attempted to enqueue 2 tasks, got %d", len(taskQueueClient.EnqueuedTasks))
	}

	// Check that one work unit had its status updated.
	xid, err := validator.ValidateExperimentName(createdExp.GetName())
	if err != nil {
		t.Fatalf("ValidateExperimentName() returned an unexpected error: %v", err)
	}

	wu1Exec, err := server.storage.FetchCurrentWorkUnitExecution(ctx, xid, workUnitID1)
	if err != nil {
		t.Fatalf("FetchCurrentWorkUnitExecution() for work unit 1 returned an unexpected error: %v", err)
	}
	wu2Exec, err := server.storage.FetchCurrentWorkUnitExecution(ctx, xid, workUnitID2)
	if err != nil {
		t.Fatalf("FetchCurrentWorkUnitExecution() for work unit 2 returned an unexpected error: %v", err)
	}

	// If work unit 1 was processed first, it should be enqueued and its status updated to STOPPING.
	// If work unit 2 was processed first, it should be enqueued and its status updated to STOPPING.
	// The other work unit should fail to enqueue and its status should remain INITIALIZING.
	// Since we can't guarantee order, we check that one is STOPPING and one is INITIALIZING.
	states := map[msgpb.WorkUnitStatus_WorkUnitState]int{}
	states[wu1Exec.GetStatus().GetState()]++
	states[wu2Exec.GetStatus().GetState()]++
	if states[msgpb.WorkUnitStatus_INITIALIZING] != 1 || states[msgpb.WorkUnitStatus_STOPPING] != 1 {
		t.Errorf("Work unit execution states got %v and %v, want one INITIALIZING and one STOPPING", wu1Exec.GetStatus().GetState(), wu2Exec.GetStatus().GetState())
	}
}

func TestBatchStopWorkUnits_FilterReturnsNoWorkUnits(t *testing.T) {
	ctx := context.Background()
	server, taskQueueClient := createExperimentStateServer(ctx, t)

	exp := &msgpb.Experiment{
		Title: "test experiment for batchstopworkunit filter no work units",
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

	op, err := server.BatchStopWorkUnits(ctx, &apipb.BatchStopWorkUnitsRequest{Parent: createdExp.GetName(), Filter: "tags:\"b\""})
	if err != nil {
		t.Fatalf("BatchStopWorkUnits() returned an unexpected error: %v", err)
	}

	if !op.GetDone() {
		t.Errorf("BatchStopWorkUnits() returned an operation with done=false")
	}

	if len(taskQueueClient.EnqueuedTasks) != 0 {
		t.Fatalf("BatchStopWorkUnits() enqueued %d tasks, want 0", len(taskQueueClient.EnqueuedTasks))
	}
}
