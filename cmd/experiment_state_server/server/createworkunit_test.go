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
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"golang/cmp/cmp"
	"golang/protobuf/v2/encoding/protojson/protojson"
	"golang/protobuf/v2/proto/proto"
	"golang/protobuf/v2/testing/protocmp/protocmp"
	"task/go/status"

	jobsetv1alpha2 "sigs.k8s.io/jobset/api/jobset/v1alpha2"

	"xmanager_cloud/auth/auth"
	"xmanager_cloud/experiment_state_server/config"
	spb "xmanager_cloud/experiment_state_server/proto/api_go_grpc_proto"
	apipb "xmanager_cloud/experiment_state_server/proto/api_go_proto"
	msgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/storage/storage"
	tq "xmanager_cloud/task_queue/client"
	tpb "xmanager_cloud/task_queue/proto/task_go_proto"
	"xmanager_cloud/toollibs/execution_environments/executionenvironments"
	"xmanager_cloud/validator/validator"
	xidgrpcpb "xmanager_cloud/xid_service/proto/api_go_grpc_proto"
	xidapipb "xmanager_cloud/xid_service/proto/api_go_proto"
	xidmsgpb "xmanager_cloud/xid_service/proto/messages_go_proto"
)

func TestCreateWorkUnit_HappyPath(t *testing.T) {
	ctx := context.Background()
	cleanup := tq.MockGenerateOIDCTokenForTest()
	defer cleanup()
	server, _ := createExperimentStateServer(ctx, t)
	realTaskQueueClient := tq.CreateTaskQueueClient(ctx, t)
	server.taskQueueClient = realTaskQueueClient
	redisFake := tq.GetFakeRedisClient(realTaskQueueClient)

	exp := &msgpb.Experiment{
		Title: "test experiment for createworkunit",
		Acls: &xidmsgpb.XidAcls{
			Owners: []*xidmsgpb.XidAcls_Role{{Principal: &xidmsgpb.XidAcls_Role_User{User: "test@example.com"}}},
		},
	}
	createdExp := mustCreateExperiment(ctx, t, server, exp)

	workUnit := &msgpb.WorkUnit{
		WorkUnitType: msgpb.WorkUnit_STANDARD,
		Executable:   testExecutable,
	}
	op := mustCreateWorkUnit(ctx, t, server, createdExp.GetName(), 1, workUnit)
	if op.GetDone() {
		t.Errorf("CreateWorkUnit() op.GetDone() = true, want false")
	}
	if op.GetResponse() != nil {
		t.Errorf("CreateWorkUnit() op.GetResponse() = %v, want nil", op.GetResponse())
	}
	if op.GetError() != nil {
		t.Errorf("CreateWorkUnit() op.GetError() = %v, want nil", op.GetError())
	}

	if len(redisFake.Messages) != 1 {
		t.Fatalf("CreateWorkUnit() enqueued %d tasks, want 1", len(redisFake.Messages))
	}

	var redisTask struct {
		Payload   []byte `json:"payload"`
		Signature string `json:"signature"`
		OidcToken string `json:"oidcToken"`
	}
	if err := json.Unmarshal([]byte(redisFake.Messages[0]), &redisTask); err != nil {
		t.Fatalf("Failed to unmarshal redis message: %v", err)
	}

	if redisTask.OidcToken == "" {
		t.Errorf("CreateWorkUnit() enqueued task with empty OidcToken")
	}

	var payload tpb.ActuatorPayload
	if err := protojson.Unmarshal(redisTask.Payload, &payload); err != nil {
		t.Fatalf("Failed to unmarshal task body: %v", err)
	}

	action := payload.GetAction()
	if action == nil {
		t.Fatalf("CreateWorkUnit() payload action = nil, want non-nil")
	}

	createAction := action.GetCreate()
	if createAction == nil {
		t.Fatalf("CreateWorkUnit() payload action type = %T, want Create", action.GetAction())
	}

	xid, err := validator.ValidateExperimentName(createdExp.GetName())
	if err != nil {
		t.Fatalf("ValidateExperimentName() returned an unexpected error: %v", err)
	}

	var gotMetadata msgpb.OperationMetadata
	if err := op.GetMetadata().UnmarshalTo(&gotMetadata); err != nil {
		t.Errorf("Failed to unmarshal operation metadata: %v", err)
	}
	wantTarget := validator.ConstructWorkUnitName(xid, 1)
	if gotMetadata.GetTarget() != wantTarget {
		t.Errorf("CreateWorkUnit() metadata target = %q, want %q", gotMetadata.GetTarget(), wantTarget)
	}
	if gotMetadata.GetActionType() != msgpb.OperationMetadata_CREATE {
		t.Errorf("CreateWorkUnit() metadata action type = %q, want %q", gotMetadata.GetActionType(), msgpb.OperationMetadata_CREATE)
	}
	if gotMetadata.GetOriginalRequester() != "test@example.com" {
		t.Errorf("CreateWorkUnit() metadata original requester = %q, want %q", gotMetadata.GetOriginalRequester(), "test@example.com")
	}

	wantWorkUnitName := validator.ConstructWorkUnitName(xid, 1)
	if payload.GetWorkUnit().GetName() != wantWorkUnitName {
		t.Errorf("CreateWorkUnit() enqueued work unit name = %q, want %q", payload.GetWorkUnit().GetName(), wantWorkUnitName)
	}
	if payload.GetWorkUnit().GetWorkUnitType() != workUnit.GetWorkUnitType() {
		t.Errorf("CreateWorkUnit() enqueued work unit type = %v, want %v", payload.GetWorkUnit().GetWorkUnitType(), workUnit.GetWorkUnitType())
	}
	if diff := cmp.Diff(workUnit.GetExecutable(), payload.GetWorkUnit().GetExecutable(), protocmp.Transform()); diff != "" {
		t.Errorf("CreateWorkUnit() enqueued work unit executable mismatch (-want +got):\n%s", diff)
	}
	if payload.GetWorkUnit().GetCurrentWorkUnitExecution() == nil {
		t.Errorf("CreateWorkUnit() enqueued work unit CurrentWorkUnitExecution = nil, want non-nil")
	}
}

func TestCreateWorkUnit_ZeroWorkUnitID(t *testing.T) {
	ctx := context.Background()
	server, taskQueueClient := createExperimentStateServer(ctx, t)

	exp := &msgpb.Experiment{
		Title: "test experiment for createworkunit zero id",
		Acls: &xidmsgpb.XidAcls{
			Owners: []*xidmsgpb.XidAcls_Role{{Principal: &xidmsgpb.XidAcls_Role_User{User: "test@example.com"}}},
		},
	}
	createdExp := mustCreateExperiment(ctx, t, server, exp)

	workUnit := &msgpb.WorkUnit{
		WorkUnitType: msgpb.WorkUnit_STANDARD,
		Executable:   testExecutable,
	}
	req := &apipb.CreateWorkUnitRequest{
		Parent:     createdExp.GetName(),
		WorkUnitId: 0,
		WorkUnit:   workUnit,
	}
	op, err := server.CreateWorkUnit(ctx, req)
	if err != nil {
		t.Fatalf("CreateWorkUnit() = error %v, want nil", err)
	}

	xid, err := validator.ValidateExperimentName(createdExp.GetName())
	if err != nil {
		t.Fatalf("ValidateExperimentName() returned an unexpected error: %v", err)
	}

	// Since we are using an emulator, the first auto-generated ID should be 1.
	// (Assuming this is the first work unit for this experiment)
	wantTarget := validator.ConstructWorkUnitName(xid, 1)

	var gotMetadata msgpb.OperationMetadata
	if err := op.GetMetadata().UnmarshalTo(&gotMetadata); err != nil {
		t.Errorf("CreateWorkUnit with WorkUnitId 0: Failed to unmarshal operation metadata: %v", err)
	}
	if gotMetadata.GetTarget() != wantTarget {
		t.Errorf("CreateWorkUnit(WorkUnitId 0) metadata target = %q, want %q", gotMetadata.GetTarget(), wantTarget)
	}

	if taskQueueClient.LastEnqueuedAction == nil {
		t.Fatalf("CreateWorkUnit(WorkUnitId 0) did not enqueue a task")
	}

	if taskQueueClient.LastEnqueuedWorkUnit.GetName() != wantTarget {
		t.Errorf("CreateWorkUnit(WorkUnitId 0) enqueued work unit name = %q, want %q", taskQueueClient.LastEnqueuedWorkUnit.GetName(), wantTarget)
	}
}

func TestCreateWorkUnit_FillNilValues(t *testing.T) {
	ctx := context.Background()
	server, _ := createExperimentStateServer(ctx, t)

	exp := &msgpb.Experiment{
		Title: "test experiment for createworkunit",
		Acls: &xidmsgpb.XidAcls{
			Owners: []*xidmsgpb.XidAcls_Role{{Principal: &xidmsgpb.XidAcls_Role_User{User: "test@example.com"}}},
		},
	}
	createdExp := mustCreateExperiment(ctx, t, server, exp)

	workUnit := &msgpb.WorkUnit{
		WorkUnitType: msgpb.WorkUnit_STANDARD,
		Executable:   testExecutable,
	}
	_ = mustCreateWorkUnit(ctx, t, server, createdExp.GetName(), 1, workUnit)

	xid, err := validator.ValidateExperimentName(createdExp.GetName())
	if err != nil {
		t.Fatalf("ValidateExperimentName() returned an unexpected error: %v", err)
	}
	got, err := server.storage.FetchWorkUnit(ctx, xid, 1)
	if err != nil {
		t.Fatalf("FetchWorkUnit() returned an unexpected error: %v", err)
	}

	if got.GetTags() == nil {
		t.Errorf("CreateWorkUnit() stored work unit tags = nil, want empty slice")
	}
	if got.GetUrls() == nil {
		t.Errorf("CreateWorkUnit() stored work unit urls = nil, want empty slice")
	}
	if got.GetNotes() != "" {
		t.Errorf("CreateWorkUnit() stored work unit notes = %q, want empty string", got.GetNotes())
	}
}

type incompatibleEnv struct{}

func (e *incompatibleEnv) Name() string {
	return "incompatible"
}
func (e *incompatibleEnv) Description() string {
	return ""
}
func (e *incompatibleEnv) IsCompatibleWithWorkUnit(workUnit *msgpb.WorkUnit) (bool, string) {
	return false, "incompatible with everything!"
}
func (e *incompatibleEnv) InjectOnJobSetCreation(ctx context.Context, workUnit *msgpb.WorkUnit, jobSet *jobsetv1alpha2.JobSet) (*jobsetv1alpha2.JobSet, error) {
	return jobSet, nil
}
func (e *incompatibleEnv) InjectOnExperimentCreation(ctx context.Context, essServer spb.ExperimentStateServiceServer, experiment *msgpb.Experiment) (*msgpb.Experiment, error) {
	return experiment, nil
}
func (e *incompatibleEnv) InjectOnWorkUnitCreation(ctx context.Context, essServer spb.ExperimentStateServiceServer, workUnit *msgpb.WorkUnit) (*msgpb.WorkUnit, error) {
	return workUnit, nil
}

func TestCreateWorkUnit_IncompatibleWorkUnit(t *testing.T) {
	ctx := context.Background()
	cfg := &config.Config{DefaultExecutionEnvironment: "incompatible"}
	if _, ok := executionenvironments.Lookup("incompatible"); !ok {
		executionenvironments.Register(&incompatibleEnv{})
	}

	goodWorkUnit := &msgpb.WorkUnit{
		WorkUnitType: msgpb.WorkUnit_STANDARD,
		Executable:   testExecutable,
	}
	req := &apipb.CreateWorkUnitRequest{
		Parent:     "experiments/123",
		WorkUnitId: 1,
		WorkUnit:   goodWorkUnit,
	}

	xidClient := &mockXidClient{
		checkUserAuthorizationResp: &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)},
	}
	storage := &storage.MockStorage{}
	taskQueueClient := &tq.FakeClient{}

	authenticator := createTestAuthorizer(ctx, t)
	server := New(storage, cfg, authenticator, xidClient, taskQueueClient)

	_, err := server.CreateWorkUnit(ctx, req)
	if !errors.Is(err, status.ErrInvalidArgument) {
		t.Errorf("CreateWorkUnit() = error %v, want %v", err, status.ErrInvalidArgument)
	}

	if !strings.Contains(err.Error(), "incompatible") {
		t.Errorf("CreateWorkUnit() = error %v, want error to contain 'incompatible'", err)
	}
}

func TestCreateWorkUnit_BadPaths(t *testing.T) {
	ctx := context.Background()
	server, _ := createExperimentStateServer(ctx, t)

	exp := &msgpb.Experiment{
		Title: "test experiment for createworkunit bad paths",
		Acls: &xidmsgpb.XidAcls{
			Owners: []*xidmsgpb.XidAcls_Role{{Principal: &xidmsgpb.XidAcls_Role_User{User: "test@example.com"}}},
		},
	}
	createdExp := mustCreateExperiment(ctx, t, server, exp)

	testCases := []struct {
		name    string
		req     *apipb.CreateWorkUnitRequest
		wantErr error
	}{
		{
			name: "invalid_parent_name",
			req: &apipb.CreateWorkUnitRequest{
				Parent:     "invalid-parent-name",
				WorkUnitId: 1,
				WorkUnit:   &msgpb.WorkUnit{},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "missing_work_unit",
			req: &apipb.CreateWorkUnitRequest{
				Parent:     createdExp.GetName(),
				WorkUnitId: 1,
				WorkUnit:   nil,
			},
			wantErr: status.ErrInvalidArgument,
		},
		// zero_work_unit_id removed as it is now valid
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := server.CreateWorkUnit(ctx, tc.req)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("CreateWorkUnit() = error %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestCreateWorkUnit_DependencyFailures(t *testing.T) {
	ctx := context.Background()
	cfg := &config.Config{DefaultExecutionEnvironment: "gke"}

	goodWorkUnit := &msgpb.WorkUnit{
		WorkUnitType: msgpb.WorkUnit_STANDARD,
		Executable:   testExecutable,
	}
	goodReq := &apipb.CreateWorkUnitRequest{
		Parent:     "experiments/123",
		WorkUnitId: 1,
		WorkUnit:   goodWorkUnit,
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
			name: "storage_create_work_unit_fails",
			xidClient: &mockXidClient{
				checkUserAuthorizationResp: &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)},
			},
			storage: &storage.MockStorage{
				CreateWorkUnitErr: fmt.Errorf("Storage create work unit failed"),
			},
			wantErr: status.ErrInternal,
		},
		{
			name: "storage_create_work_unit_already_exists",
			xidClient: &mockXidClient{
				checkUserAuthorizationResp: &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)},
			},
			storage: &storage.MockStorage{
				CreateWorkUnitErr: status.ErrAlreadyExists,
			},
			wantErr: status.ErrAlreadyExists,
		},
		{
			name: "storage_add_operation_fails",
			xidClient: &mockXidClient{
				checkUserAuthorizationResp: &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)},
			},
			storage: &storage.MockStorage{
				AddWorkUnitOperationErr: fmt.Errorf("Storage add operation failed"),
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
			_, err := server.CreateWorkUnit(ctx, proto.Clone(goodReq).(*apipb.CreateWorkUnitRequest))
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("CreateWorkUnit() = error %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestCreateWorkUnit_TaskEnqueueFails(t *testing.T) {
	ctx := context.Background()
	cfg := &config.Config{DefaultExecutionEnvironment: "gke"}

	goodWorkUnit := &msgpb.WorkUnit{
		WorkUnitType: msgpb.WorkUnit_STANDARD,
		Executable:   testExecutable,
	}
	goodReq := &apipb.CreateWorkUnitRequest{
		Parent:     "experiments/123",
		WorkUnitId: 1,
		WorkUnit:   goodWorkUnit,
	}

	xidClient := &mockXidClient{
		checkUserAuthorizationResp: &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)},
	}
	storage := &storage.MockStorage{}
	taskQueueClient := &tq.FakeClient{
		EnqueueErr: fmt.Errorf("Task enqueue failed"),
	}

	authenticator := createTestAuthorizer(ctx, t)
	server := New(storage, cfg, authenticator, xidClient, taskQueueClient)

	_, err := server.CreateWorkUnit(ctx, proto.Clone(goodReq).(*apipb.CreateWorkUnitRequest))
	if !errors.Is(err, status.ErrInternal) {
		t.Errorf("CreateWorkUnit() = error %v, want %v", err, status.ErrInternal)
	}
}
