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
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"golang/grpc/credentials/insecure/insecure"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"

	"xmanager_cloud/auth/auth"
	"xmanager_cloud/cmd/experiment_state_server/toollibs"
	"xmanager_cloud/experiment_state_server/config"
	apipb "xmanager_cloud/experiment_state_server/proto/api_go_proto"
	msgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/prototest"
	"xmanager_cloud/storage/spanner/emulator/emulator"
	"xmanager_cloud/storage/spanner/spanner"
	"xmanager_cloud/storage/storage"
	tq "xmanager_cloud/task_queue/client"
	"xmanager_cloud/validator/validator"
	xidgrpcpb "xmanager_cloud/xid_service/proto/api_go_grpc_proto"
	xidapipb "xmanager_cloud/xid_service/proto/api_go_proto"
	xidmsgpb "xmanager_cloud/xid_service/proto/messages_go_proto"

	"golang/grpc/grpc"
)

func TestCreateArtifact_HappyPath(t *testing.T) {
	ctx := context.Background()
	server, _ := createExperimentStateServer(ctx, t)
	exp := mustCreateExperiment(ctx, t, server, nil)

	now := time.Now()
	artifact := &msgpb.Artifact{
		Acls: &xidmsgpb.XidAcls{
			Writers: []*xidmsgpb.XidAcls_Role{{Principal: &xidmsgpb.XidAcls_Role_User{User: "test@example.com"}}},
		},
		Tags:  []string{"tag1", "tag2"},
		Notes: "test notes",
		Url: &msgpb.Url{
			Url:         "http://www.google.com",
			DisplayName: "google",
			Icon:        "icon1",
			Priority:    1,
		},
		Payload: &msgpb.ArtifactPayload{
			Type: &msgpb.ArtifactPayload_Url{
				Url: &msgpb.UrlArtifact{
					Url:         "http://www.google.com",
					DisplayName: "google",
					Icon:        "icon1",
				},
			},
		},
	}
	req := &apipb.CreateArtifactRequest{
		Parent:   exp.GetName(),
		Artifact: artifact,
	}

	got, err := server.CreateArtifact(ctx, req)
	if err != nil {
		t.Fatalf("CreateArtifact() returned an unexpected error: %v", err)
	}

	artifact.Parent = exp.GetName()
	if got.GetName() == "" {
		t.Errorf("CreateArtifact() returned an empty artifact name")
	}
	if got.GetCreateTime() == nil || got.GetUpdateTime() == nil {
		t.Errorf("CreateArtifact() returned nil timestamps")
	}
	opts := []cmp.Option{
		protocmp.Transform(),
		protocmp.IgnoreFields(&msgpb.Artifact{}, "create_time", "update_time", "name"),
	}
	if diff := cmp.Diff(artifact, got, opts...); diff != "" {
		t.Errorf("CreateArtifact() with request %+v mismatch (-want +got):\n%s", req, diff)
	}
	if got.GetCreateTime().AsTime().Before(now) {
		t.Errorf("CreateArtifact() returned a create time %v that is before the test start time %v", got.GetCreateTime(), now)
	}
	if got.GetUpdateTime().AsTime().Before(now) {
		t.Errorf("CreateArtifact() returned an update time %v that is before the test start time %v", got.GetUpdateTime(), now)
	}
	aid, err := validator.ValidateArtifactName(got.GetName())
	if err != nil {
		t.Errorf("CreateArtifact() returned an invalid artifact name %s: %v", got.GetName(), err)
	}

	if aid <= 0 {
		t.Errorf("CreateArtifact() returned an invalid AID %d", aid)
	}
}

func TestCreateArtifact_FillNilValues(t *testing.T) {
	ctx := context.Background()
	server, _ := createExperimentStateServer(ctx, t)
	exp := mustCreateExperiment(ctx, t, server, nil)

	artifact := &msgpb.Artifact{
		Acls: &xidmsgpb.XidAcls{
			Writers: []*xidmsgpb.XidAcls_Role{{Principal: &xidmsgpb.XidAcls_Role_User{User: "test@example.com"}}},
		},
		Url: &msgpb.Url{
			Url: "http://www.google.com",
		},
		Payload: &msgpb.ArtifactPayload{
			Type: &msgpb.ArtifactPayload_Url{
				Url: &msgpb.UrlArtifact{
					Url: "http://www.google.com",
				},
			},
		},
	}
	req := &apipb.CreateArtifactRequest{
		Parent:   exp.GetName(),
		Artifact: artifact,
	}

	got, err := server.CreateArtifact(ctx, req)
	if err != nil {
		t.Fatalf("CreateArtifact() returned an unexpected error: %v", err)
	}

	if got.GetTags() == nil {
		t.Errorf("CreateArtifact() returned nil tags, want empty slice")
	}
	if got.GetNotes() != "" {
		t.Errorf("CreateArtifact() returned notes %q, want empty string", got.GetNotes())
	}
}

func TestCreateArtifact_BadPaths(t *testing.T) {
	ctx := context.Background()
	server, _ := createExperimentStateServer(ctx, t)
	exp := mustCreateExperiment(ctx, t, server, nil)

	baseReq := &apipb.CreateArtifactRequest{
		Parent: exp.GetName(),
		Artifact: &msgpb.Artifact{
			Url: &msgpb.Url{Url: "http://www.google.com"},
		},
	}

	testCases := []struct {
		name    string
		req     *apipb.CreateArtifactRequest
		wantErr error
	}{
		{
			name: "missing_parent",
			req: prototest.Modify(baseReq, func(r *apipb.CreateArtifactRequest) {
				r.Parent = ""
			}),
			wantErr: grpcerr.ErrInvalidArgument,
		},
		{
			name: "invalid_parent",
			req: prototest.Modify(baseReq, func(r *apipb.CreateArtifactRequest) {
				r.Parent = "invalid-parent"
			}),
			wantErr: grpcerr.ErrInvalidArgument,
		},
		{
			name: "missing_artifact",
			req: prototest.Modify(baseReq, func(r *apipb.CreateArtifactRequest) {
				r.Artifact = nil
			}),
			wantErr: grpcerr.ErrInvalidArgument,
		},
		{
			name: "missing_url",
			req: prototest.Modify(baseReq, func(r *apipb.CreateArtifactRequest) {
				r.Artifact.Url = nil
			}),
			wantErr: grpcerr.ErrInvalidArgument,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := server.CreateArtifact(ctx, tc.req)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("CreateArtifact() returned error %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestCreateArtifact_DependencyFailures(t *testing.T) {
	ctx := context.Background()
	server, _ := createExperimentStateServer(ctx, t)
	exp := mustCreateExperiment(ctx, t, server, nil)

	goodArtifact := &msgpb.Artifact{
		Url: &msgpb.Url{Url: "http://www.google.com"},
		Payload: &msgpb.ArtifactPayload{
			Type: &msgpb.ArtifactPayload_Url{
				Url: &msgpb.UrlArtifact{
					Url: "http://www.google.com",
				},
			},
		},
		Acls: &xidmsgpb.XidAcls{
			Writers: []*xidmsgpb.XidAcls_Role{{Principal: &xidmsgpb.XidAcls_Role_User{User: "test@example.com"}}},
		},
	}
	goodReq := &apipb.CreateArtifactRequest{
		Parent:   exp.GetName(),
		Artifact: goodArtifact,
	}

	testCases := []struct {
		name          string
		req           *apipb.CreateArtifactRequest
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
			name: "unauthorized_user_returns_permission_denied",
			xidClient: &mockXidClient{
				checkUserAuthorizationResp: &xidapipb.CheckUserAuthorizationResponse{
					Authorized: proto.Bool(false),
				},
			},
			storage: &storage.MockStorage{},
			wantErr: grpcerr.ErrPermissionDenied,
		},
		{
			name: "unauthorized_user_with_malformed_payload_returns_permission_denied",
			xidClient: &mockXidClient{
				checkUserAuthorizationResp: &xidapipb.CheckUserAuthorizationResponse{
					Authorized: proto.Bool(false),
				},
			},
			storage: &storage.MockStorage{},
			req: &apipb.CreateArtifactRequest{
				Parent:   "experiments/123",
				Artifact: nil,
			},
			wantErr: grpcerr.ErrPermissionDenied,
		},
		{
			name: "xid_creation_fails",
			xidClient: &mockXidClient{
				checkUserAuthorizationResp: &xidapipb.CheckUserAuthorizationResponse{
					Authorized: proto.Bool(true),
				},
				createXidEntryErr: fmt.Errorf("XID creation failed: %w", grpcerr.ErrInternal),
			},
			storage: &storage.MockStorage{},
			wantErr: grpcerr.ErrInternal,
		},
		{
			name: "storage_creation_fails",
			xidClient: &mockXidClient{
				checkUserAuthorizationResp: &xidapipb.CheckUserAuthorizationResponse{
					Authorized: proto.Bool(true),
				},
				createXidEntryResp: &xidmsgpb.XidEntry{Name: proto.String("xids/123")},
			},
			storage: &storage.MockStorage{
				CreateArtifactErr: fmt.Errorf("Storage creation failed"),
			},
			wantErr: grpcerr.ErrInternal,
		},
		{
			name: "invalid_xid_name",
			xidClient: &mockXidClient{
				checkUserAuthorizationResp: &xidapipb.CheckUserAuthorizationResponse{
					Authorized: proto.Bool(true),
				},
				createXidEntryResp: &xidmsgpb.XidEntry{Name: proto.String("invalid-xid-name")},
			},
			storage: &storage.MockStorage{},
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
			req := goodReq
			if tc.req != nil {
				req = tc.req
			}
			_, err := server.CreateArtifact(ctx, req)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("%s: CreateArtifact() returned error %v, want %v", tc.name, err, tc.wantErr)
			}
		})
	}
}

func TestCreateArtifact_ChartArtifact(t *testing.T) {
	ctx := context.Background()
	cfg := &config.Config{
		DefaultExecutionEnvironment: "gke",
	}
	auth := createTestAuthorizer(ctx, t)

	xidStorageClient, essStorageClient := emulator.Setup(ctx, t)
	storageClient, _ := spanner.NewStorage(essStorageClient)

	// Setup fake XID server
	xidAddr := createXidServer(ctx, t, xidStorageClient)
	cfg.XidServiceAddr = xidAddr.Addr().String()

	conn, err := grpc.NewClient(cfg.XidServiceAddr, []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	}...)
	if err != nil {
		t.Fatalf("Failed to create gRPC client for XID service: %v", err)
	}
	xidClient := xidgrpcpb.NewXidServiceClient(conn)
	taskQueueClient := &tq.FakeClient{}

	chartArtifact := &msgpb.Artifact{
		Url: &msgpb.Url{Url: "http://example.com/chart"},
		Payload: &msgpb.ArtifactPayload{
			Type: &msgpb.ArtifactPayload_Chart{
				Chart: &msgpb.ChartArtifact{
					Title:       "Test Chart",
					MetricNames: []string{"loss"},
				},
			},
		},
		Acls: &xidmsgpb.XidAcls{
			Writers: []*xidmsgpb.XidAcls_Role{{Principal: &xidmsgpb.XidAcls_Role_User{User: "test@example.com"}}},
		},
	}

	testCases := []struct {
		name       string
		clientErr  error
		wantStatus msgpb.ChartArtifact_Status
		wantErrMsg string
	}{
		{
			name:       "success",
			clientErr:  nil,
			wantStatus: msgpb.ChartArtifact_SUCCESS,
		},
		{
			name:       "failure",
			clientErr:  fmt.Errorf("fake dashboard error"),
			wantStatus: msgpb.ChartArtifact_FAILED,
			wantErrMsg: "fake dashboard error",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockDash := &mockDashboardClient{
				createChartErr: tc.clientErr,
			}
			var bgTaskWG sync.WaitGroup
			var obs []ArtifactObserver
			if mockDash != nil {
				toolClient := toollibs.NewDashboardToolClient(mockDash)
				obs = append(obs, NewDashboardObserver(toolClient, storageClient, &bgTaskWG))
			}
			server, err := New(Params{
				Config:            cfg,
				Storage:           storageClient,
				Authorizer:        auth,
				XidClient:         xidClient,
				WorkUnitActuator:  taskQueueClient,
				ArtifactObservers: obs,
			})
			if err != nil {
				t.Fatalf("Failed to create server: %v", err)
			}

			// Create parent Experiment
			exp := mustCreateExperiment(ctx, t, server, nil)
			mustCreateWorkUnit(ctx, t, server, exp.GetName(), 1, nil)
			req := &apipb.CreateArtifactRequest{
				Parent:   fmt.Sprintf("%s/workUnits/1", exp.GetName()),
				Artifact: proto.Clone(chartArtifact).(*msgpb.Artifact),
			}

			// Action
			got, err := server.CreateArtifact(ctx, req)
			if err != nil {
				t.Fatalf("CreateArtifact returned unexpected error: %v", err)
			}
			// Synchronize WaitGroup on the server to wait for async tasks
			bgTaskWG.Wait()

			gotFromDB, err := server.GetArtifact(ctx, &apipb.GetArtifactRequest{
				Name: got.GetName(),
			})
			if err != nil {
				t.Fatalf("Failed to fetch created artifact from DB: %v", err)
			}

			chartPayload := gotFromDB.GetPayload().GetChart()
			if chartPayload == nil {
				t.Fatal("Chart payload is nil")
			}

			if chartPayload.GetStatus() != tc.wantStatus {
				t.Errorf("Expected status %v, got %v", tc.wantStatus, chartPayload.GetStatus())
			}

			if tc.wantErrMsg != "" && chartPayload.GetErrorMessage() != tc.wantErrMsg {
				t.Errorf("Expected err msg %q, got %q", tc.wantErrMsg, chartPayload.GetErrorMessage())
			}
		})
	}
}

func TestCreateArtifact_ManagedXprofDefaults(t *testing.T) {
	ctx := context.Background()
	server, _ := createExperimentStateServer(ctx, t)
	exp := mustCreateExperiment(ctx, t, server, nil)
	mustCreateWorkUnit(ctx, t, server, exp.GetName(), 1, nil)

	tests := []struct {
		name         string
		parent       string
		initialArt   *msgpb.Artifact
		wantDisplay  string
		wantRunGroup string
	}{
		{
			name:   "empty_fields_on_work_unit_defaults_to_title_and_wid",
			parent: fmt.Sprintf("%s/workUnits/1", exp.GetName()),
			initialArt: &msgpb.Artifact{
				Payload: &msgpb.ArtifactPayload{
					Type: &msgpb.ArtifactPayload_ManagedXprof{
						ManagedXprof: &msgpb.ManagedXprofArtifact{
							DisplayName: "",
							RunGroup:    "",
						},
					},
				},
			},
			wantDisplay:  fmt.Sprintf("%s_1", exp.GetTitle()),
			wantRunGroup: exp.GetTitle(),
		},
		{
			name:   "explicit_display_name_defaults_run_group",
			parent: fmt.Sprintf("%s/workUnits/1", exp.GetName()),
			initialArt: &msgpb.Artifact{
				Payload: &msgpb.ArtifactPayload{
					Type: &msgpb.ArtifactPayload_ManagedXprof{
						ManagedXprof: &msgpb.ManagedXprofArtifact{
							DisplayName: "custom-display",
							RunGroup:    "",
						},
					},
				},
			},
			wantDisplay:  "custom-display",
			wantRunGroup: exp.GetTitle(),
		},
		{
			name:   "explicit_run_group_defaults_display_name",
			parent: fmt.Sprintf("%s/workUnits/1", exp.GetName()),
			initialArt: &msgpb.Artifact{
				Payload: &msgpb.ArtifactPayload{
					Type: &msgpb.ArtifactPayload_ManagedXprof{
						ManagedXprof: &msgpb.ManagedXprofArtifact{
							DisplayName: "",
							RunGroup:    "custom-group",
						},
					},
				},
			},
			wantDisplay:  fmt.Sprintf("%s_1", exp.GetTitle()),
			wantRunGroup: "custom-group",
		},
		{
			name:   "empty_fields_on_experiment_defaults_both_to_title",
			parent: exp.GetName(),
			initialArt: &msgpb.Artifact{
				Payload: &msgpb.ArtifactPayload{
					Type: &msgpb.ArtifactPayload_ManagedXprof{
						ManagedXprof: &msgpb.ManagedXprofArtifact{
							DisplayName: "",
							RunGroup:    "",
						},
					},
				},
			},
			wantDisplay:  exp.GetTitle(),
			wantRunGroup: exp.GetTitle(),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := server.CreateArtifact(ctx, &apipb.CreateArtifactRequest{
				Parent:   tc.parent,
				Artifact: tc.initialArt,
			})
			if err != nil {
				t.Fatalf("CreateArtifact() failed unexpectedly: %v", err)
			}
			mx := got.GetPayload().GetManagedXprof()
			if mx == nil {
				t.Fatalf("Expected ManagedXprof payload, got nil")
			}
			if mx.GetDisplayName() != tc.wantDisplay {
				t.Errorf("DisplayName = %q, want %q", mx.GetDisplayName(), tc.wantDisplay)
			}
			if mx.GetRunGroup() != tc.wantRunGroup {
				t.Errorf("RunGroup = %q, want %q", mx.GetRunGroup(), tc.wantRunGroup)
			}
		})
	}
}
