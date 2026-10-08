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
	"testing"
	"time"

	googlespanner "cloud.google.com/go/spanner"
	"github.com/google/go-cmp/cmp"
	"golang/google_api/cloudidentity/v1beta1/cloudidentity"
	"golang/grpc/credentials/insecure/insecure"
	"golang/grpc/grpc"
	"golang/grpc/health/health"
	"golang/grpc/metadata/metadata"
	"golang/grpc/reflection/reflection"
	"golang/uuid/uuid"
	"google.golang.org/protobuf/testing/protocmp"
	healthgrpc "grpc_proto/health_go_grpc"
	healthpb "grpc_proto/health_go_proto"
	lropb "longrunning/operations_go_proto"
	fmpb "protobuf/field_mask_go_proto"
	"task/go/status"
	authz "xmanager_cloud/auth/auth"
	"xmanager_cloud/experiment_state_server/config"
	apipb "xmanager_cloud/experiment_state_server/proto/api_go_proto"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/storage/spanner/emulator/emulator"
	"xmanager_cloud/storage/spanner/spanner"
	"xmanager_cloud/storage/storage"
	tq "xmanager_cloud/task_queue/client"
	xidconfig "xmanager_cloud/xid_service/config"
	xidgrpcpb "xmanager_cloud/xid_service/proto/api_go_grpc_proto"
	xidapipb "xmanager_cloud/xid_service/proto/api_go_proto"
	xidmsgpb "xmanager_cloud/xid_service/proto/messages_go_proto"
	xidserver "xmanager_cloud/xid_service/server/server"
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
	authorizer, err := authz.NewAuthorizer(ctx, idService, 10*time.Minute, 1*time.Minute, []string{"test-audience"}, authz.DefaultIAPIssuer, nil)
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

func withXidClient(x xidgrpcpb.XidServiceClient) testServerOpt {
	return func(p *Params) {
		p.XidClient = x
	}
}

func withWorkUnitActuator(actuator WorkUnitActuator) testServerOpt {
	return func(p *Params) {
		p.WorkUnitActuator = actuator
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
	xidClient := xidgrpcpb.NewXidServiceClient(conn)
	p := Params{
		Config:           &cfg,
		Storage:          storage,
		Authorizer:       createTestAuthorizer(ctx, t),
		XidClient:        xidClient,
		WorkUnitActuator: &tq.FakeClient{},
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
	xidgrpcpb.RegisterXidServiceServer(s, xidServer)

	reflection.Register(s)

	healthServer := health.NewServer()
	healthgrpc.RegisterHealthServer(s, healthServer)

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
	createdExp, err := server.CreateExperiment(ctx, &apipb.CreateExperimentRequest{Experiment: exp})
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
	op, err := server.CreateWorkUnit(ctx, &apipb.CreateWorkUnitRequest{
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
	createdArtifact, err := server.CreateArtifact(ctx, &apipb.CreateArtifactRequest{
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
	xidgrpcpb.XidServiceClient
	getXidEntryErr             error
	getXidEntryResp            *xidmsgpb.XidEntry
	createXidEntryErr          error
	createXidEntryResp         *xidmsgpb.XidEntry
	checkUserAuthorizationErr  error
	checkUserAuthorizationResp *xidapipb.CheckUserAuthorizationResponse
	updateXidEntryErr          error
	updateXidEntryResp         *xidmsgpb.XidEntry
}

func (m *mockXidClient) GetXidEntry(ctx context.Context, in *xidapipb.GetXidEntryRequest, opts ...grpc.CallOption) (*xidmsgpb.XidEntry, error) {
	return m.getXidEntryResp, m.getXidEntryErr
}
func (m *mockXidClient) CreateXidEntry(ctx context.Context, in *xidapipb.CreateXidEntryRequest, opts ...grpc.CallOption) (*xidmsgpb.XidEntry, error) {
	return m.createXidEntryResp, m.createXidEntryErr
}
func (m *mockXidClient) CheckUserAuthorization(ctx context.Context, in *xidapipb.CheckUserAuthorizationRequest, opts ...grpc.CallOption) (*xidapipb.CheckUserAuthorizationResponse, error) {
	return m.checkUserAuthorizationResp, m.checkUserAuthorizationErr
}
func (m *mockXidClient) UpdateXidEntry(ctx context.Context, in *xidapipb.UpdateXidEntryRequest, opts ...grpc.CallOption) (*xidmsgpb.XidEntry, error) {
	return m.updateXidEntryResp, m.updateXidEntryErr
}

type mockAuthenticator struct {
	err error
}

func (m *mockAuthenticator) AuthenticateUserFromContext(ctx context.Context) (string, string, error) {
	if m.err != nil {
		return "", "", m.err
	}
	return "test@example.com", "user_id", nil
}

func (m *mockAuthenticator) IsUserAuthorized(ctx context.Context, user string, acls *xidmsgpb.XidAcls, role authz.Role) (bool, error) {
	return true, nil
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
	opName := fmt.Sprintf("experiments/%d/workUnits/%d/operations/%s", 123, 1, uuid.New())

	testCases := []struct {
		name                        string
		op                          *lropb.Operation
		err                         error
		updateWorkUnitOperationErr  error
		wantUpdateWorkUnitOperation bool
		wantDone                    bool
		wantCode                    status.Code
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
			err:                         status.ErrInternal,
			wantUpdateWorkUnitOperation: true,
			wantDone:                    true,
			wantCode:                    status.Internal,
		},
		{
			name:                        "invalid_op_name",
			op:                          &lropb.Operation{Name: "invalid/op/name"},
			err:                         status.ErrInternal,
			wantUpdateWorkUnitOperation: false,
		},
		{
			name:                        "update_op_error",
			op:                          &lropb.Operation{Name: opName},
			err:                         status.ErrInternal,
			updateWorkUnitOperationErr:  errors.New("storage fails"),
			wantUpdateWorkUnitOperation: true,
			wantDone:                    true,
			wantCode:                    status.Internal,
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
			if status.Code(storage.GotOperationUpdate.Operation.GetError().GetCode()) != tc.wantCode {
				t.Errorf("stopOperationOnError() updated operation with code = %v, want %v", status.Code(storage.GotOperationUpdate.Operation.GetError().GetCode()), tc.wantCode)
			}
			wantMask := &fmpb.FieldMask{Paths: []string{"done", "error"}}
			if diff := cmp.Diff(wantMask, storage.GotOperationUpdate.UpdateMask, protocmp.Transform()); diff != "" {
				t.Errorf("stopOperationOnError() updated operation with wrong update mask (-want +got):\n%s", diff)
			}
		})
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
				UpdateWorkUnitExecutionsErr: tc.updateWorkUnitExecutionsErr,
			}
			server := newTestServer(t, withStorage(mockStore))

			err := server.handleEnqueueActionError(ctx, "stop", tc.workUnit, 123, 456, essmsgpb.StructuredMessage_ERROR, enqueueErr)
			if (err != nil) != tc.wantErr {
				t.Fatalf("handleEnqueueActionError() error = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantValErr {
				if !errors.Is(err, status.ErrInternal) {
					t.Errorf("handleEnqueueActionError() error = %v, want status.ErrInternal", err)
				}
			} else {
				if !errors.Is(err, status.ErrInternal) {
					t.Errorf("handleEnqueueActionError() error = %v, want status.ErrInternal", err)
				}
			}
		})
	}
}
