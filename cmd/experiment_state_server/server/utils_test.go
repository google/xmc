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
	"net"
	"strings"
	"testing"
	"time"

	lropb "cloud.google.com/go/longrunning/autogen/longrunningpb"
	googlespanner "cloud.google.com/go/spanner"
	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"google.golang.org/api/cloudidentity/v1beta1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/reflection"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
	fmpb "google.golang.org/protobuf/types/known/fieldmaskpb"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	actuatortaskpb "xmanager_cloud/api/protos/task_queue"
	xidmsgpb "xmanager_cloud/api/protos/xid"
	"xmanager_cloud/cmd/experiment_state_server/config"
	xidconfig "xmanager_cloud/cmd/xid_service/config"
	xidserver "xmanager_cloud/cmd/xid_service/server"
	authz "xmanager_cloud/internal/auth"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/spanner"
	"xmanager_cloud/internal/storage/spanner/emulator"
	tq "xmanager_cloud/internal/task_queue"
	"xmanager_cloud/internal/task_queue_redis/redisclient"
	"xmanager_cloud/internal/validator"
)

var testExecutable = &essmsgpb.Executable{
	SchedulingConstraints: &essmsgpb.SchedulingConstraints{
		QueueName: "test-queue",
	},
	Type: &essmsgpb.Executable_Jobset{
		Jobset: &essmsgpb.JobSetExecutable{
			Jobs: []*essmsgpb.KubernetesJob{
				{
					Name:     "test-job",
					Replicas: 1,
					Spec: &essmsgpb.ExecutableSpec{
						DockerImage: "test-image",
						Resources: &essmsgpb.ResourceSet{
							Resources: &essmsgpb.ResourceSet_CpuResources{
								CpuResources: &essmsgpb.CpuResources{
									CpuMillicores: 1000,
									MemoryBytes:   1024 * 1024 * 1024,
								},
							},
						},
					},
				},
			},
		},
	},
}

type testAuthorizerWrapper struct {
	*authz.Authorizer
}

func (w *testAuthorizerWrapper) AuthenticateUserFromContext(ctx context.Context) (string, string, error) {
	return "test@example.com", "user_id", nil
}

func createTestAuthorizer(ctx context.Context, t *testing.T) authz.AuthorizerInterface {
	t.Helper()
	idService := &cloudidentity.Service{}
	authorizer, err := authz.NewAuthorizer(authz.AuthorizerParams{
		IDService:     idService,
		CacheTTL:      10 * time.Minute,
		CacheErrorTTL: time.Minute,
		IAPAudiences:  []string{"test-audience"},
		IAPIssuer:     authz.DefaultIAPIssuer,
		Admission:     authz.AdmitAll(),
	})
	if err != nil {
		t.Fatalf("Failed to create mock authorizer: %v", err)
	}
	return &testAuthorizerWrapper{Authorizer: authorizer}
}

type testServerOpt func(*Params)

func withStorage(s storage.Storage) testServerOpt {
	return func(p *Params) {
		p.Storage = s
	}
}

func withConfig(c *config.Config) testServerOpt {
	return func(p *Params) {
		p.Config = c
	}
}

func withAuthorizer(a authz.AuthorizerInterface) testServerOpt {
	return func(p *Params) {
		p.Authorizer = a
	}
}

func withXidClient(x xidmsgpb.XidServiceClient) testServerOpt {
	return func(p *Params) {
		p.XidClient = x
	}
}

func withWorkUnitActuator(actuator WorkUnitActuator) testServerOpt {
	return func(p *Params) {
		p.WorkUnitActuator = actuator
	}
}

func withStudyReconcilePublisher(publisher StudyReconcilePublisher) testServerOpt {
	return func(p *Params) {
		p.StudyReconcilePublisher = publisher
	}
}

func newTestServer(t *testing.T, opts ...testServerOpt) *Server {
	t.Helper()
	p := Params{}
	for _, opt := range opts {
		opt(&p)
	}
	if p.Config == nil {
		p.Config = &config.Config{}
	}
	if p.Storage == nil {
		p.Storage = &storage.MockStorage{}
	}
	if p.Authorizer == nil {
		p.Authorizer = createTestAuthorizer(context.Background(), t)
	}
	if p.XidClient == nil {
		p.XidClient = &mockXidClient{}
	}
	if p.WorkUnitActuator == nil {
		p.WorkUnitActuator = &tq.FakeClient{}
	}
	if p.StudyReconcilePublisher == nil {
		p.StudyReconcilePublisher = redisclient.NewFakeClient()
	}
	s, err := New(p)
	if err != nil {
		t.Fatalf("newTestServer failed: %v", err)
	}
	return s
}

func createExperimentStateServer(ctx context.Context, t *testing.T, opts ...testServerOpt) (*Server, *tq.FakeClient) {
	t.Helper()
	xidStorageClient, essStorageClient := emulator.Setup(ctx, t)
	storage, _ := spanner.NewStorage(essStorageClient)
	xidAddr := createXidServer(ctx, t, xidStorageClient)
	cfg := config.Config{
		EndpointURL:                 "http://localhost:8080",
		XidServiceAddr:              xidAddr.Addr().String(),
		DefaultExecutionEnvironment: "gke",
	}

	conn, err := grpc.NewClient(cfg.XidServiceAddr, []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	}...)
	if err != nil {
		t.Fatalf("Failed to create gRPC client for XID service: %v", err)
	}
	xidClient := xidmsgpb.NewXidServiceClient(conn)
	p := Params{
		Config:                  &cfg,
		Storage:                 storage,
		Authorizer:              createTestAuthorizer(ctx, t),
		XidClient:               xidClient,
		WorkUnitActuator:        &tq.FakeClient{},
		StudyReconcilePublisher: redisclient.NewFakeClient(),
	}
	for _, opt := range opts {
		opt(&p)
	}
	s, err := New(p)
	if err != nil {
		t.Fatalf("createExperimentStateServer failed: %v", err)
	}
	fakeActuator, _ := p.WorkUnitActuator.(*tq.FakeClient)
	return s, fakeActuator
}

func createXidServer(ctx context.Context, t *testing.T, xidClient *googlespanner.Client) net.Listener {
	t.Helper()
	storage, _ := spanner.NewStorage(xidClient)
	cfg := xidconfig.Config{}

	lis, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}

	s := grpc.NewServer(grpc.Creds(insecure.NewCredentials()))

	xidServer, err := xidserver.New(xidserver.Params{
		Config:     cfg,
		Authorizer: createTestAuthorizer(ctx, t),
		Storage:    storage,
	})
	if err != nil {
		t.Fatalf("failed to create xid server: %v", err)
	}
	xidmsgpb.RegisterXidServiceServer(s, xidServer)

	reflection.Register(s)

	healthServer := health.NewServer()
	healthpb.RegisterHealthServer(s, healthServer)

	healthServer.SetServingStatus("xmc.XidService", healthpb.HealthCheckResponse_SERVING)

	t.Logf("gRPC server listening at %v", lis.Addr())

	go func() {
		if err := s.Serve(lis); err != nil {
			t.Logf("Server exited with error: %v", err)
		}
	}()

	t.Cleanup(func() {
		s.Stop()
	})

	return lis
}

func mustCreateExperiment(ctx context.Context, t *testing.T, server *Server, exp *essmsgpb.Experiment) *essmsgpb.Experiment {
	t.Helper()
	if exp == nil {
		exp = &essmsgpb.Experiment{
			Title: "test experiment",
			Acls: &xidmsgpb.XidAcls{
				Owners: []*xidmsgpb.XidAcls_Role{{Principal: &xidmsgpb.XidAcls_Role_User{User: "test@example.com"}}},
			},
		}
	}
	createdExp, err := server.CreateExperiment(ctx, &essmsgpb.CreateExperimentRequest{Experiment: exp})
	if err != nil {
		t.Fatalf("CreateExperiment() returned an unexpected error: %v", err)
	}
	return createdExp
}

func mustCreateWorkUnit(ctx context.Context, t *testing.T, server *Server, parent string, workUnitID int64, workUnit *essmsgpb.WorkUnit) *lropb.Operation {
	t.Helper()
	if workUnit == nil {
		workUnit = &essmsgpb.WorkUnit{
			WorkUnitType: essmsgpb.WorkUnit_STANDARD,
			Executable: &essmsgpb.Executable{
				SchedulingConstraints: &essmsgpb.SchedulingConstraints{
					QueueName: "test-queue",
				},
				Type: &essmsgpb.Executable_Jobset{
					Jobset: &essmsgpb.JobSetExecutable{
						Jobs: []*essmsgpb.KubernetesJob{
							{
								Name:     "test-job",
								Replicas: 1,
								Spec: &essmsgpb.ExecutableSpec{
									DockerImage: "test-image",
									Resources: &essmsgpb.ResourceSet{
										Resources: &essmsgpb.ResourceSet_CpuResources{
											CpuResources: &essmsgpb.CpuResources{
												CpuMillicores: 1000,
												MemoryBytes:   1024 * 1024 * 1024,
											},
										},
									},
								},
							},
						},
					},
				},
			},
		}
	}
	op, err := server.CreateWorkUnit(ctx, &essmsgpb.CreateWorkUnitRequest{
		Parent:     parent,
		WorkUnitId: workUnitID,
		WorkUnit:   workUnit,
	})
	if err != nil {
		t.Fatalf("CreateWorkUnit() returned an unexpected error: %v", err)
	}
	if op.Done {
		t.Fatalf("CreateWorkUnit() returned an operation that was already done")
	}
	return op
}

func mustCreateArtifact(ctx context.Context, t *testing.T, server *Server, parent string, artifact *essmsgpb.Artifact) *essmsgpb.Artifact {
	t.Helper()
	if artifact == nil {
		artifact = &essmsgpb.Artifact{
			Url: &essmsgpb.Url{
				Url: "http://www.google.com",
			},
			Payload: &essmsgpb.ArtifactPayload{
				Type: &essmsgpb.ArtifactPayload_Url{
					Url: &essmsgpb.UrlArtifact{
						Url: "http://www.google.com",
					},
				},
			},
			Acls: &xidmsgpb.XidAcls{
				Writers: []*xidmsgpb.XidAcls_Role{{Principal: &xidmsgpb.XidAcls_Role_User{User: "test@example.com"}}},
			},
		}
	}
	createdArtifact, err := server.CreateArtifact(ctx, &essmsgpb.CreateArtifactRequest{
		Parent:   parent,
		Artifact: artifact,
	})
	if err != nil {
		t.Fatalf("CreateArtifact() returned an unexpected error: %v", err)
	}
	return createdArtifact
}

// mockXidClient is a mock implementation of the XidServiceClient.
type mockXidClient struct {
	xidmsgpb.XidServiceClient
	getXidEntryErr             error
	getXidEntryResp            *xidmsgpb.XidEntry
	getXidEntryCalls           int
	createXidEntryErr          error
	createXidEntryResp         *xidmsgpb.XidEntry
	createXidEntryCalls        int
	gotCreateXidEntryReq       *xidmsgpb.CreateXidEntryRequest
	checkUserAuthorizationErr  error
	checkUserAuthorizationResp *xidmsgpb.CheckUserAuthorizationResponse
	updateXidEntryErr          error
	updateXidEntryResp         *xidmsgpb.XidEntry
	listXidEntriesErr          error
	listXidEntriesResp         *xidmsgpb.ListXidEntriesResponse
	gotListXidEntriesReq       *xidmsgpb.ListXidEntriesRequest
}

func (m *mockXidClient) GetXidEntry(ctx context.Context, in *xidmsgpb.GetXidEntryRequest, opts ...grpc.CallOption) (*xidmsgpb.XidEntry, error) {
	m.getXidEntryCalls++
	return m.getXidEntryResp, m.getXidEntryErr
}
func (m *mockXidClient) CreateXidEntry(ctx context.Context, in *xidmsgpb.CreateXidEntryRequest, opts ...grpc.CallOption) (*xidmsgpb.XidEntry, error) {
	m.createXidEntryCalls++
	m.gotCreateXidEntryReq = in
	return m.createXidEntryResp, m.createXidEntryErr
}
func (m *mockXidClient) CheckUserAuthorization(ctx context.Context, in *xidmsgpb.CheckUserAuthorizationRequest, opts ...grpc.CallOption) (*xidmsgpb.CheckUserAuthorizationResponse, error) {
	return m.checkUserAuthorizationResp, m.checkUserAuthorizationErr
}
func (m *mockXidClient) UpdateXidEntry(ctx context.Context, in *xidmsgpb.UpdateXidEntryRequest, opts ...grpc.CallOption) (*xidmsgpb.XidEntry, error) {
	return m.updateXidEntryResp, m.updateXidEntryErr
}
func (m *mockXidClient) ListXidEntries(ctx context.Context, in *xidmsgpb.ListXidEntriesRequest, opts ...grpc.CallOption) (*xidmsgpb.ListXidEntriesResponse, error) {
	m.gotListXidEntriesReq = in
	return m.listXidEntriesResp, m.listXidEntriesErr
}

type mockAuthenticator struct {
	email string
	err   error
	// denyAuthorization makes IsUserAuthorized report the user as unauthorized.
	denyAuthorization   bool
	isUserAuthorizedErr error
	// gotIsUserAuthorizedRole records the role requested by the last IsUserAuthorized call.
	gotIsUserAuthorizedRole authz.Role
}

func (m *mockAuthenticator) AuthenticateUserFromContext(ctx context.Context) (string, string, error) {
	if m.err != nil {
		return "", "", m.err
	}
	if m.email != "" {
		return m.email, "user_id", nil
	}
	return "test@example.com", "user_id", nil
}

func (m *mockAuthenticator) IsUserAuthorized(ctx context.Context, user string, acls *xidmsgpb.XidAcls, role authz.Role) (bool, error) {
	m.gotIsUserAuthorizedRole = role
	if m.isUserAuthorizedErr != nil {
		return false, m.isUserAuthorizedErr
	}
	return !m.denyAuthorization, nil
}

func (m *mockAuthenticator) IsUserAuthorizedForChart(ctx context.Context, user string, acls *xidmsgpb.XidAcls) (bool, error) {
	return true, nil
}

func TestNewWorkUnitExecution(t *testing.T) {
	xid := int64(123)
	wid := int64(456)
	wue := newWorkUnitExecution(xid, wid)

	wantName := "experiments/123/workUnits/456/executions/1"
	if wue.GetName() != wantName {
		t.Errorf("newWorkUnitExecution() name = %q, want %q", wue.GetName(), wantName)
	}

	if wue.GetStatus().GetState() != essmsgpb.WorkUnitStatus_INITIALIZING {
		t.Errorf("newWorkUnitExecution() status.state = %v, want %v", wue.GetStatus().GetState(), essmsgpb.WorkUnitStatus_INITIALIZING)
	}

	if wue.GetState() != essmsgpb.WorkUnitExecution_ACTIVE {
		t.Errorf("newWorkUnitExecution() state = %v, want %v", wue.GetState(), essmsgpb.WorkUnitExecution_ACTIVE)
	}
}

func TestStopOperationOnError(t *testing.T) {
	ctx := context.Background()
	opName := fmt.Sprintf("experiments/%d/workUnits/%d/operations/%s", 123, 1, uuid.NewString())

	testCases := []struct {
		name                        string
		op                          *lropb.Operation
		err                         error
		updateWorkUnitOperationErr  error
		wantUpdateWorkUnitOperation bool
		wantDone                    bool
		wantCode                    codes.Code
		wantMessage                 string
	}{
		{
			name:                        "no_error",
			op:                          &lropb.Operation{Name: opName},
			err:                         nil,
			wantUpdateWorkUnitOperation: false,
		},
		{
			name:                        "with_error",
			op:                          &lropb.Operation{Name: opName},
			err:                         grpcerr.ErrInternal,
			wantUpdateWorkUnitOperation: true,
			wantDone:                    true,
			wantCode:                    codes.Internal,
		},
		{
			name:                        "invalid_op_name",
			op:                          &lropb.Operation{Name: "invalid/op/name"},
			err:                         grpcerr.ErrInternal,
			wantUpdateWorkUnitOperation: false,
		},
		{
			// The generated GetName getter is nil-safe, so a nil operation fails name validation
			// instead of panicking.
			name:                        "nil_operation",
			op:                          nil,
			err:                         grpcerr.ErrInternal,
			wantUpdateWorkUnitOperation: false,
		},
		{
			name:                        "error_without_status",
			op:                          &lropb.Operation{Name: opName},
			err:                         errors.New("failed to add message to redis"),
			wantUpdateWorkUnitOperation: true,
			wantDone:                    true,
			wantCode:                    codes.Internal,
			wantMessage:                 "failed to add message to redis",
		},
		{
			name:                        "update_op_error",
			op:                          &lropb.Operation{Name: opName},
			err:                         grpcerr.ErrInternal,
			updateWorkUnitOperationErr:  errors.New("storage fails"),
			wantUpdateWorkUnitOperation: true,
			wantDone:                    true,
			wantCode:                    codes.Internal,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			storage := &storage.MockStorage{
				UpdateWorkUnitOperationErr: tc.updateWorkUnitOperationErr,
			}
			server := newTestServer(t, withStorage(storage))

			server.stopOperationOnError(ctx, tc.op, tc.err)

			if !tc.wantUpdateWorkUnitOperation {
				if storage.UpdateWorkUnitOperationCalls != 0 {
					t.Errorf("stopOperationOnError() UpdateWorkUnitOperation calls = %d, want 0", storage.UpdateWorkUnitOperationCalls)
				}
				return
			}

			if storage.UpdateWorkUnitOperationCalls != 1 {
				t.Errorf("stopOperationOnError() UpdateWorkUnitOperation calls = %d, want 1", storage.UpdateWorkUnitOperationCalls)
			}
			if storage.GotOperationUpdate.Operation.GetDone() != tc.wantDone {
				t.Errorf("stopOperationOnError() updated operation with done = %v, want %v", storage.GotOperationUpdate.Operation.GetDone(), tc.wantDone)
			}
			if codes.Code(storage.GotOperationUpdate.Operation.GetError().GetCode()) != tc.wantCode {
				t.Errorf("stopOperationOnError() updated operation with code = %v, want %v", codes.Code(storage.GotOperationUpdate.Operation.GetError().GetCode()), tc.wantCode)
			}
			if tc.wantMessage != "" {
				if got := storage.GotOperationUpdate.Operation.GetError().GetMessage(); got != tc.wantMessage {
					t.Errorf("stopOperationOnError() updated operation with message = %q, want %q", got, tc.wantMessage)
				}
			}
			wantMask := &fmpb.FieldMask{Paths: []string{"done", "error"}}
			if diff := cmp.Diff(wantMask, storage.GotOperationUpdate.UpdateMask, protocmp.Transform()); diff != "" {
				t.Errorf("stopOperationOnError() updated operation with wrong update mask (-want +got):\n%s", diff)
			}
		})
	}
}

// checkOperationStopped checks that st recorded the work unit operation for xid and wid as done
// with an INTERNAL error, which is how stopOperationOnError closes an operation after a failure.
func checkOperationStopped(t *testing.T, st *storage.MockStorage, xid, wid int64) {
	t.Helper()
	got := st.GotOperationUpdate
	if got == nil {
		t.Fatalf("UpdateWorkUnitOperation() was not called, want the operation of experiments/%d/workUnits/%d stopped", xid, wid)
	}
	if got.XID != xid || got.WID != wid {
		t.Errorf("stopped operation of XID %d, WID %d, want XID %d, WID %d", got.XID, got.WID, xid, wid)
	}
	if !got.Operation.GetDone() {
		t.Error("stopped operation done = false, want true")
	}
	if code := codes.Code(got.Operation.GetError().GetCode()); code != codes.Internal {
		t.Errorf("stopped operation error code = %v, want %v", code, codes.Internal)
	}
	wantMask := &fmpb.FieldMask{Paths: []string{"done", "error"}}
	if diff := cmp.Diff(wantMask, got.UpdateMask, protocmp.Transform()); diff != "" {
		t.Errorf("stopped operation update mask mismatch (-want +got):\n%s", diff)
	}
}

// checkSystemMessage checks that the last execution st recorded for wid is wantExecution and
// carries the want system message. The message's create time is ignored.
func checkSystemMessage(t *testing.T, st *storage.MockStorage, wid int64, wantExecution string, want *essmsgpb.StructuredMessage) {
	t.Helper()
	got, ok := st.UpdatedWorkUnitExecutions[wid]
	if !ok {
		t.Fatalf("no work unit execution was written for WID %d, want %s with system message %v", wid, wantExecution, want)
	}
	if got.GetName() != wantExecution {
		t.Errorf("written work unit execution = %s, want %s", got.GetName(), wantExecution)
	}
	opts := []cmp.Option{protocmp.Transform(), protocmp.IgnoreFields(&essmsgpb.StructuredMessage{}, "create_time")}
	if diff := cmp.Diff(want, got.GetStatus().GetSystemMessage(), opts...); diff != "" {
		t.Errorf("written system message mismatch (-want +got):\n%s", diff)
	}
}

// fetchRecordedOperation parses opName and fetches the corresponding operation from st.
func fetchRecordedOperation(ctx context.Context, t *testing.T, st storage.Storage, opName string) (int64, *lropb.Operation) {
	t.Helper()
	xid, wid, oid, err := validator.ValidateOperationName(opName)
	if err != nil {
		t.Fatalf("ValidateOperationName(%q) failed: %v", opName, err)
	}
	op, err := st.FetchWorkUnitOperation(ctx, xid, wid, oid)
	if err != nil {
		t.Fatalf("FetchWorkUnitOperation(%q) failed: %v", opName, err)
	}
	return wid, op
}

// checkPartialEnqueueOperations verifies that after a 2-task batch where tasks[0] succeeded and
// tasks[1] failed to enqueue, only tasks[1] closed its operation and recorded wantFailSeverity.
func checkPartialEnqueueOperations(ctx context.Context, t *testing.T, st storage.Storage, xid int64, tasks []tq.EnqueuedTask, action string, wantFailSeverity essmsgpb.StructuredMessage_Severity) {
	t.Helper()
	okWID, okOp := fetchRecordedOperation(ctx, t, st, tasks[0].OperationName)
	if okOp.GetDone() || okOp.GetError() != nil {
		t.Errorf("succeeded %s operation %s = (done=%v, error=%v), want pending with nil error", action, okOp.GetName(), okOp.GetDone(), okOp.GetError())
	}
	okExec, err := st.FetchCurrentWorkUnitExecution(ctx, xid, okWID)
	if err != nil {
		t.Fatalf("FetchCurrentWorkUnitExecution(%d) failed: %v", okWID, err)
	}
	if sev := okExec.GetStatus().GetSystemMessage().GetSeverity(); sev == wantFailSeverity {
		t.Errorf("succeeded work unit %d execution system message severity = %v, want non-%v", okWID, sev, wantFailSeverity)
	}
	failWID, failOp := fetchRecordedOperation(ctx, t, st, tasks[1].OperationName)
	if !failOp.GetDone() || codes.Code(failOp.GetError().GetCode()) != codes.Internal {
		t.Errorf("failed %s operation %s = (done=%v, code=%v), want (done=true, code=%v)", action, failOp.GetName(), failOp.GetDone(), codes.Code(failOp.GetError().GetCode()), codes.Internal)
	}
	failExec, err := st.FetchCurrentWorkUnitExecution(ctx, xid, failWID)
	if err != nil {
		t.Fatalf("FetchCurrentWorkUnitExecution(%d) failed: %v", failWID, err)
	}
	wantMsg := &essmsgpb.StructuredMessage{
		Source:         essmsgpb.StructuredMessage_XMANAGER_BACKEND,
		Severity:       wantFailSeverity,
		UserFacingText: fmt.Sprintf("Failed to %s work unit. Please try again.", action),
		Parent:         validator.ConstructWorkUnitName(xid, failWID),
	}
	if diff := cmp.Diff(wantMsg, failExec.GetStatus().GetSystemMessage(), protocmp.Transform(), protocmp.IgnoreFields(&essmsgpb.StructuredMessage{}, "create_time")); diff != "" {
		t.Errorf("failed work unit %d system message mismatch (-want +got):\n%s", failWID, diff)
	}
}

// ctxHonoringStorage fails writes on a done context, as real storage does, so a test can tell
// whether compensation ran on the caller's context. It also records the deadline of each
// operation update.
type ctxHonoringStorage struct {
	*storage.MockStorage
	operationUpdateDeadlines *[]time.Time
}

func (s ctxHonoringStorage) RunInTransaction(ctx context.Context, fn func(ctx context.Context, tx *storage.Transaction) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.MockStorage.RunInTransaction(ctx, fn)
}

func (s ctxHonoringStorage) UpdateWorkUnitOperation(ctx context.Context, update *storage.OperationUpdate) (*lropb.Operation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	deadline, _ := ctx.Deadline()
	*s.operationUpdateDeadlines = append(*s.operationUpdateDeadlines, deadline)
	return s.MockStorage.UpdateWorkUnitOperation(ctx, update)
}

// TestEnqueueFailureCompensation_CallerCancelled checks that when the caller cancels while the
// task is enqueued and the enqueue fails, the compensation still lands: the operation is closed
// and the status message is written, under a bounded context.
func TestEnqueueFailureCompensation_CallerCancelled(t *testing.T) {
	storedWorkUnit := &essmsgpb.WorkUnit{
		Name:                     "experiments/123/workUnits/456",
		WorkUnitType:             essmsgpb.WorkUnit_STANDARD,
		Executable:               testExecutable,
		CurrentWorkUnitExecution: &essmsgpb.WorkUnitExecution{Name: "experiments/123/workUnits/456/executions/1"},
	}
	testCases := []struct {
		name           string
		storedWorkUnit *essmsgpb.WorkUnit
		call           func(ctx context.Context, s *Server) error
		wid            int64
		wantExecution  string
		wantSeverity   essmsgpb.StructuredMessage_Severity
		wantText       string
	}{
		{
			name: "create",
			call: func(ctx context.Context, s *Server) error {
				_, err := s.CreateWorkUnit(ctx, &essmsgpb.CreateWorkUnitRequest{
					Parent:     "experiments/123",
					WorkUnitId: 1,
					WorkUnit:   &essmsgpb.WorkUnit{WorkUnitType: essmsgpb.WorkUnit_STANDARD, Executable: testExecutable},
				})
				return err
			},
			wid:           1,
			wantExecution: "experiments/123/workUnits/1/executions/1",
			wantSeverity:  essmsgpb.StructuredMessage_FATAL,
			wantText:      "Work unit was created but could not be scheduled for execution. Please restart the work unit.",
		},
		{
			name:           "restart",
			storedWorkUnit: storedWorkUnit,
			call: func(ctx context.Context, s *Server) error {
				_, err := s.RestartWorkUnit(ctx, &essmsgpb.RestartWorkUnitRequest{Name: "experiments/123/workUnits/456"})
				return err
			},
			wid:           456,
			wantExecution: "experiments/123/workUnits/456/executions/2",
			wantSeverity:  essmsgpb.StructuredMessage_FATAL,
			wantText:      "Failed to restart work unit. Please try again.",
		},
		{
			name:           "stop",
			storedWorkUnit: storedWorkUnit,
			call: func(ctx context.Context, s *Server) error {
				_, err := s.StopWorkUnit(ctx, &essmsgpb.StopWorkUnitRequest{Name: "experiments/123/workUnits/456"})
				return err
			},
			wid:           456,
			wantExecution: "experiments/123/workUnits/456/executions/1",
			wantSeverity:  essmsgpb.StructuredMessage_ERROR,
			wantText:      "Failed to stop work unit. Please try again.",
		},
		{
			name:           "batch_restart",
			storedWorkUnit: storedWorkUnit,
			call: func(ctx context.Context, s *Server) error {
				_, err := s.BatchRestartWorkUnits(ctx, &essmsgpb.BatchRestartWorkUnitsRequest{Parent: "experiments/123"})
				return err
			},
			wid:           456,
			wantExecution: "experiments/123/workUnits/456/executions/2",
			wantSeverity:  essmsgpb.StructuredMessage_FATAL,
			wantText:      "Failed to restart work unit. Please try again.",
		},
		{
			name:           "batch_stop",
			storedWorkUnit: storedWorkUnit,
			call: func(ctx context.Context, s *Server) error {
				_, err := s.BatchStopWorkUnits(ctx, &essmsgpb.BatchStopWorkUnitsRequest{Parent: "experiments/123"})
				return err
			},
			wid:           456,
			wantExecution: "experiments/123/workUnits/456/executions/1",
			wantSeverity:  essmsgpb.StructuredMessage_ERROR,
			wantText:      "Failed to stop work unit. Please try again.",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			st := &storage.MockStorage{
				Experiment: &essmsgpb.Experiment{Name: "experiments/123"},
				WorkUnit:   tc.storedWorkUnit,
			}
			if tc.storedWorkUnit != nil {
				st.ListWorkUnitsResp = []*essmsgpb.WorkUnit{tc.storedWorkUnit}
			}
			var deadlines []time.Time
			actuator := &tq.FakeClient{
				EnqueueFunc: func(context.Context, string, *essmsgpb.WorkUnit, *actuatortaskpb.Action, string) error {
					cancel()
					return errors.New("task enqueue failed")
				},
			}
			server := newTestServer(t,
				withConfig(&config.Config{DefaultExecutionEnvironment: "gke"}),
				withXidClient(&mockXidClient{
					checkUserAuthorizationResp: &xidmsgpb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)},
				}),
				withStorage(ctxHonoringStorage{MockStorage: st, operationUpdateDeadlines: &deadlines}),
				withWorkUnitActuator(actuator),
			)

			if err := tc.call(ctx, server); !errors.Is(err, grpcerr.ErrInternal) {
				t.Errorf("%s returned error %v, want %v", tc.name, err, grpcerr.ErrInternal)
			}
			latestDeadline := time.Now().Add(compensationTimeout)

			checkOperationStopped(t, st, 123, tc.wid)
			checkSystemMessage(t, st, tc.wid, tc.wantExecution, &essmsgpb.StructuredMessage{
				Source:         essmsgpb.StructuredMessage_XMANAGER_BACKEND,
				Severity:       tc.wantSeverity,
				UserFacingText: tc.wantText,
				Parent:         validator.ConstructWorkUnitName(123, tc.wid),
			})
			for _, deadline := range deadlines {
				if deadline.IsZero() || deadline.After(latestDeadline) {
					t.Errorf("operation update deadline = %v, want a deadline of at most %v", deadline, compensationTimeout)
				}
			}
		})
	}
}

// TestCompensationContext_NestedSharesOuterBudget checks that compensation outlives the caller's
// cancellation, and that compensation nested in a bounded one, such as closing each operation of a
// failed batch, keeps the outer deadline instead of starting a new compensationTimeout.
func TestCompensationContext_NestedSharesOuterBudget(t *testing.T) {
	callerCtx, cancelCaller := context.WithCancel(context.Background())
	cancelCaller()
	const outerTimeout = time.Second

	outerCtx, cancelOuter := boundedCompensationContext(callerCtx, outerTimeout)
	defer cancelOuter()
	innerCtx, cancelInner := compensationContext(outerCtx)
	defer cancelInner()

	if err := outerCtx.Err(); err != nil {
		t.Fatalf("outer compensation context error = %v, want nil after the caller is cancelled", err)
	}
	outerDeadline, ok := outerCtx.Deadline()
	if !ok || outerDeadline.After(time.Now().Add(outerTimeout)) {
		t.Fatalf("outer compensation deadline = %v (set %t), want at most %v from now", outerDeadline, ok, outerTimeout)
	}
	if innerDeadline, ok := innerCtx.Deadline(); !ok || !innerDeadline.Equal(outerDeadline) {
		t.Errorf("nested compensation deadline = %v (set %t), want the outer deadline %v", innerDeadline, ok, outerDeadline)
	}
}

func TestPropagateIAPJWT(t *testing.T) {
	testCases := []struct {
		name         string
		jwt          string
		email        string
		wantOK       bool
		wantJWTOut   string
		wantEmailOut string
	}{
		{
			name:         "with jwt and email",
			jwt:          "some-jwt",
			email:        "test@example.com",
			wantOK:       true,
			wantJWTOut:   "some-jwt",
			wantEmailOut: "test@example.com",
		},
		{
			name:         "without jwt or email",
			jwt:          "",
			email:        "",
			wantOK:       false,
			wantJWTOut:   "",
			wantEmailOut: "",
		},
		{
			name:         "with jwt only",
			jwt:          "some-jwt",
			email:        "",
			wantOK:       true,
			wantJWTOut:   "some-jwt",
			wantEmailOut: "",
		},
		{
			name:         "with email only",
			jwt:          "",
			email:        "test@example.com",
			wantOK:       true,
			wantJWTOut:   "",
			wantEmailOut: "test@example.com",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			md := metadata.Pairs()
			if tc.jwt != "" {
				md.Set("x-goog-iap-jwt-assertion", tc.jwt)
			}
			if tc.email != "" {
				md.Set("x-goog-user-email", tc.email)
			}
			ctx := metadata.NewIncomingContext(context.Background(), md)

			outCtx := propagateIAPJWT(ctx)

			outMD, ok := metadata.FromOutgoingContext(outCtx)
			if !ok {
				if tc.wantOK {
					t.Fatal("expected outgoing metadata, but got none")
				}
				return
			}

			jwtValues := outMD.Get("x-goog-iap-jwt-assertion")
			if len(jwtValues) > 0 && !tc.wantOK {
				t.Errorf("expected no jwt in outgoing metadata, got %v", jwtValues)
			}
			if len(jwtValues) == 0 && tc.wantOK && tc.wantJWTOut != "" {
				t.Errorf("expected jwt in outgoing metadata, got none")
			}
			if len(jwtValues) > 0 && tc.wantOK && jwtValues[0] != tc.wantJWTOut {
				t.Errorf("wrong jwt in outgoing metadata, got %q, want %q", jwtValues[0], tc.wantJWTOut)
			}

			emailValues := outMD.Get("x-goog-user-email")
			if len(emailValues) > 0 && !tc.wantOK {
				t.Errorf("expected no email in outgoing metadata, got %v", emailValues)
			}
			if len(emailValues) == 0 && tc.wantOK && tc.wantEmailOut != "" {
				t.Errorf("expected email in outgoing metadata, got none")
			}
			if len(emailValues) > 0 && tc.wantOK && emailValues[0] != tc.wantEmailOut {
				t.Errorf("wrong email in outgoing metadata, got %q, want %q", emailValues[0], tc.wantEmailOut)
			}
		})
	}
}

func TestPropagateIAPJWT_NoIncomingMetadata(t *testing.T) {
	ctx := context.Background()
	outCtx := propagateIAPJWT(ctx)
	_, ok := metadata.FromOutgoingContext(outCtx)
	if ok {
		t.Errorf("expected no outgoing metadata, but got some")
	}
}

func TestHandleEnqueueActionError(t *testing.T) {
	ctx := context.Background()
	enqueueErr := errors.New("queue unreachable")

	testCases := []struct {
		name                        string
		workUnit                    *essmsgpb.WorkUnit
		updateWorkUnitExecutionsErr error
		wantErr                     bool
		wantValErr                  bool
	}{
		{
			name: "invalid_execution_name",
			workUnit: &essmsgpb.WorkUnit{
				Name: "experiments/123/workUnits/456",
				CurrentWorkUnitExecution: &essmsgpb.WorkUnitExecution{
					Name: "invalid-name",
				},
			},
			wantErr:    true,
			wantValErr: true,
		},
		{
			name: "valid_execution_name_success",
			workUnit: &essmsgpb.WorkUnit{
				Name: "experiments/123/workUnits/456",
				CurrentWorkUnitExecution: &essmsgpb.WorkUnitExecution{
					Name: "experiments/123/workUnits/456/executions/1",
				},
			},
			wantErr:    true,
			wantValErr: false,
		},
		{
			name: "valid_execution_name_status_update_error",
			workUnit: &essmsgpb.WorkUnit{
				Name: "experiments/123/workUnits/456",
				CurrentWorkUnitExecution: &essmsgpb.WorkUnitExecution{
					Name: "experiments/123/workUnits/456/executions/1",
				},
			},
			updateWorkUnitExecutionsErr: errors.New("db error"),
			wantErr:                     true,
			wantValErr:                  false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockStore := &storage.MockStorage{
				WorkUnit:                          tc.workUnit,
				Experiment:                        &essmsgpb.Experiment{Name: "experiments/123"},
				UpdateCurrentWorkUnitExecutionErr: tc.updateWorkUnitExecutionsErr,
			}
			server := newTestServer(t, withStorage(mockStore))

			err := server.handleEnqueueActionError(ctx, "stop", tc.workUnit, 123, 456, essmsgpb.StructuredMessage_ERROR, enqueueErr)
			if (err != nil) != tc.wantErr {
				t.Fatalf("handleEnqueueActionError() error = %v, wantErr %v", err, tc.wantErr)
			}
			wantSubstr := "failed to enqueue"
			if tc.wantValErr {
				wantSubstr = "failed to validate work unit execution name"
			}
			if !strings.Contains(err.Error(), wantSubstr) {
				t.Errorf("handleEnqueueActionError() error = %q, want substring %q", err.Error(), wantSubstr)
			}
			if !errors.Is(err, grpcerr.ErrInternal) {
				t.Errorf("handleEnqueueActionError() error = %v, want grpcerr.ErrInternal", err)
			}
		})
	}
}
