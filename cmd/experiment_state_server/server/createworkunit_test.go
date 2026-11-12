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

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
	jobsetv1alpha2 "sigs.k8s.io/jobset/api/jobset/v1alpha2"
	msgpb "xmanager_cloud/api/protos/experiment"
	tpb "xmanager_cloud/api/protos/task_queue"
	xidmsgpb "xmanager_cloud/api/protos/xid"
	"xmanager_cloud/cmd/experiment_state_server/config"
	"xmanager_cloud/internal/auth"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/security/securitytest"
	"xmanager_cloud/internal/storage"
	tq "xmanager_cloud/internal/task_queue"
	"xmanager_cloud/internal/task_queue_redis/redisclient"
	"xmanager_cloud/internal/toollibs/execution_environments"
	"xmanager_cloud/internal/validator"
	"xmanager_cloud/internal/workunit"
)

func TestCreateWorkUnit_HappyPath(t *testing.T) {
	ctx := context.Background()
	fakeRedis := redisclient.NewFakeClient()
	keyFunc := securitytest.MustCreateTestSigner(t)
	tokenProvider := tq.TokenProviderFunc(func(_ context.Context, _, _ string) (string, error) {
		return "mock-oidc-token", nil
	})
	realTaskQueueClient, err := tq.New(ctx, "test-project", "test-sa", "http://actuator.example.com", "test-audience", keyFunc, tq.DefaultSignatureTTLSeconds, "", tq.WithTokenProvider(tokenProvider), tq.WithRedisPublisher(fakeRedis))
	if err != nil {
		t.Fatalf("Failed to create task queue client: %v", err)
	}
	server, _ := createExperimentStateServer(ctx, t, withWorkUnitActuator(realTaskQueueClient))
	redisFake := fakeRedis

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
	req := &msgpb.CreateWorkUnitRequest{
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
	got, err := server.WorkUnitHandler.storage.FetchWorkUnit(ctx, xid, 1)
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
func (e *incompatibleEnv) InjectOnExperimentCreation(ctx context.Context, essServer msgpb.ExperimentStateServiceServer, experiment *msgpb.Experiment) (*msgpb.Experiment, error) {
	return experiment, nil
}
func (e *incompatibleEnv) InjectOnWorkUnitCreation(ctx context.Context, essServer msgpb.ExperimentStateServiceServer, workUnit *msgpb.WorkUnit) (*msgpb.WorkUnit, error) {
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
	req := &msgpb.CreateWorkUnitRequest{
		Parent:     "experiments/123",
		WorkUnitId: 1,
		WorkUnit:   goodWorkUnit,
	}

	xidClient := &mockXidClient{
		checkUserAuthorizationResp: &xidmsgpb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)},
	}

	server := newTestServer(t,
		withConfig(cfg),
		withXidClient(xidClient),
	)

	_, err := server.CreateWorkUnit(ctx, req)
	if !errors.Is(err, grpcerr.ErrInvalidArgument) {
		t.Errorf("CreateWorkUnit() = error %v, want %v", err, grpcerr.ErrInvalidArgument)
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
		req     *msgpb.CreateWorkUnitRequest
		wantErr error
	}{
		{
			name: "invalid_parent_name",
			req: &msgpb.CreateWorkUnitRequest{
				Parent:     "invalid-parent-name",
				WorkUnitId: 1,
				WorkUnit:   &msgpb.WorkUnit{},
			},
			wantErr: grpcerr.ErrInvalidArgument,
		},
		{
			name: "missing_work_unit",
			req: &msgpb.CreateWorkUnitRequest{
				Parent:     createdExp.GetName(),
				WorkUnitId: 1,
				WorkUnit:   nil,
			},
			wantErr: grpcerr.ErrInvalidArgument,
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
	goodReq := &msgpb.CreateWorkUnitRequest{
		Parent:     "experiments/123",
		WorkUnitId: 1,
		WorkUnit:   goodWorkUnit,
	}

	testCases := []struct {
		name          string
		req           *msgpb.CreateWorkUnitRequest
		authenticator auth.AuthorizerInterface
		xidClient     xidmsgpb.XidServiceClient
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
				checkUserAuthorizationResp: &xidmsgpb.CheckUserAuthorizationResponse{Authorized: proto.Bool(false)},
			},
			storage: &storage.MockStorage{},
			wantErr: grpcerr.ErrPermissionDenied,
		},
		{
			name: "unauthorized_user_with_malformed_payload_returns_permission_denied",
			xidClient: &mockXidClient{
				checkUserAuthorizationResp: &xidmsgpb.CheckUserAuthorizationResponse{Authorized: proto.Bool(false)},
			},
			storage: &storage.MockStorage{},
			req: &msgpb.CreateWorkUnitRequest{
				Parent:     "experiments/123",
				WorkUnitId: 1,
				WorkUnit:   nil,
			},
			wantErr: grpcerr.ErrPermissionDenied,
		},
		{
			name: "storage_create_work_unit_fails",
			xidClient: &mockXidClient{
				checkUserAuthorizationResp: &xidmsgpb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)},
			},
			storage: &storage.MockStorage{
				Experiment:        &msgpb.Experiment{Name: "experiments/123"},
				InsertWorkUnitErr: fmt.Errorf("Storage create work unit failed"),
			},
			wantErr: grpcerr.ErrInternal,
		},
		{
			name: "storage_create_work_unit_already_exists",
			xidClient: &mockXidClient{
				checkUserAuthorizationResp: &xidmsgpb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)},
			},
			storage: &storage.MockStorage{
				Experiment:        &msgpb.Experiment{Name: "experiments/123"},
				InsertWorkUnitErr: grpcerr.ErrAlreadyExists,
			},
			wantErr: grpcerr.ErrAlreadyExists,
		},
		{
			name: "storage_add_operation_fails",
			xidClient: &mockXidClient{
				checkUserAuthorizationResp: &xidmsgpb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)},
			},
			storage: &storage.MockStorage{
				Experiment:              &msgpb.Experiment{Name: "experiments/123"},
				AddWorkUnitOperationErr: fmt.Errorf("Storage add operation failed"),
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
			opts = append(opts, withConfig(cfg))
			server := newTestServer(t, opts...)
			req := goodReq
			if tc.req != nil {
				req = tc.req
			}
			_, err := server.CreateWorkUnit(ctx, proto.Clone(req).(*msgpb.CreateWorkUnitRequest))
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
	goodReq := &msgpb.CreateWorkUnitRequest{
		Parent:     "experiments/123",
		WorkUnitId: 1,
		WorkUnit:   goodWorkUnit,
	}

	xidClient := &mockXidClient{
		checkUserAuthorizationResp: &xidmsgpb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)},
	}
	taskQueueClient := &tq.FakeClient{
		EnqueueErr: fmt.Errorf("Task enqueue failed"),
	}

	server := newTestServer(t,
		withConfig(cfg),
		withXidClient(xidClient),
		withStorage(&storage.MockStorage{
			Experiment: &msgpb.Experiment{Name: "experiments/123"},
		}),
		withWorkUnitActuator(taskQueueClient),
	)

	_, err := server.CreateWorkUnit(ctx, proto.Clone(goodReq).(*msgpb.CreateWorkUnitRequest))
	if !errors.Is(err, grpcerr.ErrInternal) {
		t.Errorf("CreateWorkUnit() = error %v, want %v", err, grpcerr.ErrInternal)
	}
}

func TestCreateWorkUnit_StudyExperimentRejected(t *testing.T) {
	ctx := context.Background()
	st := &storage.MockStorage{
		Experiment: &msgpb.Experiment{Name: "experiments/123", ExperimentType: msgpb.Experiment_STUDY},
	}
	taskQueueClient := &tq.FakeClient{}
	server := newTestServer(t,
		withConfig(&config.Config{DefaultExecutionEnvironment: "gke"}),
		withXidClient(&mockXidClient{
			checkUserAuthorizationResp: &xidmsgpb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)},
		}),
		withStorage(st),
		withWorkUnitActuator(taskQueueClient),
	)
	req := &msgpb.CreateWorkUnitRequest{
		Parent:   "experiments/123",
		WorkUnit: &msgpb.WorkUnit{WorkUnitType: msgpb.WorkUnit_STANDARD, Executable: testExecutable},
	}

	_, err := server.CreateWorkUnit(ctx, req)

	if got := status.Code(err); got != codes.FailedPrecondition || !errors.Is(err, workunit.ErrStudyWorkUnitCreation) {
		t.Errorf("CreateWorkUnit() on a study = error %v (code %v), want %v wrapping ErrStudyWorkUnitCreation", err, got, codes.FailedPrecondition)
	}
	if st.UpdatedExperiment != nil {
		t.Errorf("CreateWorkUnit() on a study updated the experiment: %v", st.UpdatedExperiment)
	}
	if taskQueueClient.LastEnqueuedAction != nil {
		t.Errorf("CreateWorkUnit() on a study enqueued a task: %v", taskQueueClient.LastEnqueuedAction)
	}
}
