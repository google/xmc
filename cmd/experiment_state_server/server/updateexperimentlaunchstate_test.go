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
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	fieldmaskpb "google.golang.org/protobuf/types/known/fieldmaskpb"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	apipb "xmanager_cloud/experiment_state_server/proto/api_go_proto"
	msgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/auth"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/validator/validator"
	xidapipb "xmanager_cloud/xid_service/proto/api_go_proto"
	xidmsgpb "xmanager_cloud/xid_service/proto/messages_go_proto"
)

func TestUpdateExperimentLaunchState_HappyPath(t *testing.T) {
	ctx := context.Background()
	server, _ := createExperimentStateServer(ctx, t)

	exp := &msgpb.Experiment{
		Title: "test experiment for updateexperimentlaunchstate",
		Acls: &xidmsgpb.XidAcls{
			Owners: []*xidmsgpb.XidAcls_Role{{Principal: &xidmsgpb.XidAcls_Role_User{User: "test@example.com"}}},
		},
	}

	for _, state := range []msgpb.ExperimentStatus_LaunchState{msgpb.ExperimentStatus_COMPLETED, msgpb.ExperimentStatus_FAILED} {
		t.Run(state.String(), func(t *testing.T) {
			createdExp := mustCreateExperiment(ctx, t, server, exp)

			req := &apipb.UpdateExperimentLaunchStateRequest{
				Name:        createdExp.GetName(),
				LaunchState: state,
			}
			got, err := server.UpdateExperimentLaunchState(ctx, req)
			if err != nil {
				t.Fatalf("UpdateExperimentLaunchState() returned an unexpected error: %v", err)
			}

			if got.Status.GetLaunchState() != state {
				t.Errorf("UpdateExperimentLaunchState() returned experiment with launch state %v, want %v", got.GetStatus().GetLaunchState(), state)
			}
		})
	}
}

func TestUpdateExperimentLaunchState_BadPaths(t *testing.T) {
	ctx := context.Background()
	server := newTestServer(t)

	testCases := []struct {
		name    string
		req     *apipb.UpdateExperimentLaunchStateRequest
		wantErr error
	}{
		{
			name: "invalid_name",
			req: &apipb.UpdateExperimentLaunchStateRequest{
				Name:        "invalid-name",
				LaunchState: msgpb.ExperimentStatus_COMPLETED,
			},
			wantErr: grpcerr.ErrInvalidArgument,
		},
		{
			name: "invalid_launch_state",
			req: &apipb.UpdateExperimentLaunchStateRequest{
				Name:        "experiments/123",
				LaunchState: msgpb.ExperimentStatus_LAUNCH_STATE_UNSPECIFIED,
			},
			wantErr: grpcerr.ErrInvalidArgument,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := server.UpdateExperimentLaunchState(ctx, tc.req)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("UpdateExperimentLaunchState() returned error %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestUpdateExperimentLaunchState_DependencyFailures(t *testing.T) {
	ctx := context.Background()

	goodReq := &apipb.UpdateExperimentLaunchStateRequest{Name: "experiments/123", LaunchState: msgpb.ExperimentStatus_COMPLETED}

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
			name: "xid_get_fails",
			xidClient: &mockXidClient{
				checkUserAuthorizationResp: &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)},
				getXidEntryErr:             fmt.Errorf("XID get failed"),
			},
			storage: &storage.MockStorage{},
			wantErr: grpcerr.ErrInternal,
		},
		{
			name: "xid_get_permission_denied",
			xidClient: &mockXidClient{
				checkUserAuthorizationResp: &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(false)},
			},
			storage: &storage.MockStorage{},
			wantErr: grpcerr.ErrPermissionDenied,
		},
		{
			name: "storage_update_fails",
			xidClient: &mockXidClient{
				checkUserAuthorizationResp: &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)},
				getXidEntryResp:            &xidmsgpb.XidEntry{Name: proto.String("xids/123")},
			},
			storage: &storage.MockStorage{
				UpdateExperimentLaunchStateErr: fmt.Errorf("Storage update failed"),
			},
			wantErr: grpcerr.ErrInternal,
		},
		{
			name: "storage_update_not_found",
			xidClient: &mockXidClient{
				checkUserAuthorizationResp: &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)},
				getXidEntryResp:            &xidmsgpb.XidEntry{Name: proto.String("xids/123")},
			},
			storage: &storage.MockStorage{
				UpdateExperimentLaunchStateErr: grpcerr.ErrNotFound,
			},
			wantErr: grpcerr.ErrNotFound,
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
			_, err := server.UpdateExperimentLaunchState(ctx, goodReq)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("UpdateExperimentLaunchState() returned error %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestUpdateExperimentLaunchState_ActiveState(t *testing.T) {
	ctx := context.Background()
	server, _ := createExperimentStateServer(ctx, t)

	s := server.ExperimentHandler.storage
	testCases := []struct {
		name       string
		wu1State   msgpb.WorkUnitStatus_WorkUnitState
		wu2State   msgpb.WorkUnitStatus_WorkUnitState
		wantActive msgpb.ExperimentStatus_ActiveState
	}{
		{
			name:       "pending_pending",
			wu1State:   msgpb.WorkUnitStatus_INITIALIZING,
			wu2State:   msgpb.WorkUnitStatus_SCHEDULING,
			wantActive: msgpb.ExperimentStatus_ACTIVE,
		},
		{
			name:       "running_pending",
			wu1State:   msgpb.WorkUnitStatus_RUNNING,
			wu2State:   msgpb.WorkUnitStatus_SCHEDULING,
			wantActive: msgpb.ExperimentStatus_ACTIVE,
		},
		{
			name:       "completed_pending",
			wu1State:   msgpb.WorkUnitStatus_COMPLETED,
			wu2State:   msgpb.WorkUnitStatus_INITIALIZING,
			wantActive: msgpb.ExperimentStatus_ACTIVE,
		},
		{
			name:       "completed_running",
			wu1State:   msgpb.WorkUnitStatus_COMPLETED,
			wu2State:   msgpb.WorkUnitStatus_RUNNING,
			wantActive: msgpb.ExperimentStatus_ACTIVE,
		},
		{
			name:       "completed_failed",
			wu1State:   msgpb.WorkUnitStatus_COMPLETED,
			wu2State:   msgpb.WorkUnitStatus_FAILED,
			wantActive: msgpb.ExperimentStatus_INACTIVE,
		},
		{
			name:       "stopped_failed",
			wu1State:   msgpb.WorkUnitStatus_STOPPED,
			wu2State:   msgpb.WorkUnitStatus_FAILED,
			wantActive: msgpb.ExperimentStatus_INACTIVE,
		},
		{
			name:       "running_failed",
			wu1State:   msgpb.WorkUnitStatus_RUNNING,
			wu2State:   msgpb.WorkUnitStatus_FAILED,
			wantActive: msgpb.ExperimentStatus_ACTIVE,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			exp := &msgpb.Experiment{
				Title: "test experiment for is_active",
				Acls: &xidmsgpb.XidAcls{
					Owners: []*xidmsgpb.XidAcls_Role{{Principal: &xidmsgpb.XidAcls_Role_User{User: "test@example.com"}}},
				},
			}
			createdExp := mustCreateExperiment(ctx, t, server, exp)
			xid, err := validator.ValidateExperimentName(createdExp.GetName())
			if err != nil {
				t.Fatalf("ValidateExperimentName() returned an unexpected error: %v", err)
			}

			// create 2 work units
			for i := 1; i <= 2; i++ {
				mustCreateWorkUnit(ctx, t, server, createdExp.GetName(), int64(i), nil)
			}

			// Initially all 2 WUs are pending -> exp should be active.
			gotExp, err := server.GetExperiment(ctx, &apipb.GetExperimentRequest{Name: createdExp.GetName()})
			if err != nil {
				t.Fatalf("GetExperiment() returned an unexpected error: %v", err)
			}
			if gotExp.GetStatus().GetActiveState() != msgpb.ExperimentStatus_ACTIVE {
				t.Errorf("Experiment should be active when work units are pending, got inactive")
			}

			_, err = s.UpdateWorkUnitExecutions(ctx, xid, []*storage.WorkUnitExecutionUpdate{
				{
					WID: 1,
					EID: 1,
					WorkUnitExecution: &msgpb.WorkUnitExecution{
						Status: &msgpb.WorkUnitStatus{State: tc.wu1State, StateUpdateTime: timestamppb.New(time.Now())},
					},
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
				},
				{
					WID: 2,
					EID: 1,
					WorkUnitExecution: &msgpb.WorkUnitExecution{
						Status: &msgpb.WorkUnitStatus{State: tc.wu2State, StateUpdateTime: timestamppb.New(time.Now())},
					},
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
				},
			})
			if err != nil {
				t.Fatalf("UpdateWorkUnitExecutions() returned an unexpected error: %v", err)
			}

			_, err = server.UpdateExperimentLaunchState(ctx, &apipb.UpdateExperimentLaunchStateRequest{
				Name:        createdExp.GetName(),
				LaunchState: msgpb.ExperimentStatus_COMPLETED,
			})
			if err != nil {
				t.Fatalf("UpdateExperimentLaunchState() returned an unexpected error: %v", err)
			}

			gotExp, err = server.GetExperiment(ctx, &apipb.GetExperimentRequest{Name: createdExp.GetName()})
			if err != nil {
				t.Fatalf("GetExperiment() returned an unexpected error: %v", err)
			}
			if gotExp.GetStatus().GetActiveState() != tc.wantActive {
				t.Errorf("IsActive() = %v, want %v for states %v, %v", gotExp.GetStatus().GetActiveState(), tc.wantActive, tc.wu1State, tc.wu2State)
			}
		})
	}
}

func TestUpdateExperimentLaunchState_ActiveStateUpdatesPostLaunching(t *testing.T) {
	ctx := context.Background()
	server, _ := createExperimentStateServer(ctx, t)

	s := server.ExperimentHandler.storage
	testCases := []struct {
		name       string
		wu1State   msgpb.WorkUnitStatus_WorkUnitState
		wu2State   msgpb.WorkUnitStatus_WorkUnitState
		wantActive msgpb.ExperimentStatus_ActiveState
	}{
		{
			name:       "pending_pending",
			wu1State:   msgpb.WorkUnitStatus_INITIALIZING,
			wu2State:   msgpb.WorkUnitStatus_SCHEDULING,
			wantActive: msgpb.ExperimentStatus_ACTIVE,
		},
		{
			name:       "running_pending",
			wu1State:   msgpb.WorkUnitStatus_RUNNING,
			wu2State:   msgpb.WorkUnitStatus_SCHEDULING,
			wantActive: msgpb.ExperimentStatus_ACTIVE,
		},
		{
			name:       "completed_pending",
			wu1State:   msgpb.WorkUnitStatus_COMPLETED,
			wu2State:   msgpb.WorkUnitStatus_INITIALIZING,
			wantActive: msgpb.ExperimentStatus_ACTIVE,
		},
		{
			name:       "completed_running",
			wu1State:   msgpb.WorkUnitStatus_COMPLETED,
			wu2State:   msgpb.WorkUnitStatus_RUNNING,
			wantActive: msgpb.ExperimentStatus_ACTIVE,
		},
		{
			name:       "completed_failed",
			wu1State:   msgpb.WorkUnitStatus_COMPLETED,
			wu2State:   msgpb.WorkUnitStatus_FAILED,
			wantActive: msgpb.ExperimentStatus_INACTIVE,
		},
		{
			name:       "stopped_failed",
			wu1State:   msgpb.WorkUnitStatus_STOPPED,
			wu2State:   msgpb.WorkUnitStatus_FAILED,
			wantActive: msgpb.ExperimentStatus_INACTIVE,
		},
		{
			name:       "running_failed",
			wu1State:   msgpb.WorkUnitStatus_RUNNING,
			wu2State:   msgpb.WorkUnitStatus_FAILED,
			wantActive: msgpb.ExperimentStatus_ACTIVE,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			exp := &msgpb.Experiment{
				Title: "test experiment for is_active",
				Acls: &xidmsgpb.XidAcls{
					Owners: []*xidmsgpb.XidAcls_Role{{Principal: &xidmsgpb.XidAcls_Role_User{User: "test@example.com"}}},
				},
			}
			createdExp := mustCreateExperiment(ctx, t, server, exp)
			xid, err := validator.ValidateExperimentName(createdExp.GetName())
			if err != nil {
				t.Fatalf("ValidateExperimentName() returned an unexpected error: %v", err)
			}

			// create 2 work units
			for i := 1; i <= 2; i++ {
				mustCreateWorkUnit(ctx, t, server, createdExp.GetName(), int64(i), nil)
			}

			// Initially all 2 WUs are pending -> exp should be active.
			gotExp, err := server.GetExperiment(ctx, &apipb.GetExperimentRequest{Name: createdExp.GetName()})
			if err != nil {
				t.Fatalf("GetExperiment() returned an unexpected error: %v", err)
			}
			if gotExp.GetStatus().GetActiveState() != msgpb.ExperimentStatus_ACTIVE {
				t.Errorf("Experiment should be active when work units are pending, got inactive")
			}

			_, err = server.UpdateExperimentLaunchState(ctx, &apipb.UpdateExperimentLaunchStateRequest{
				Name:        createdExp.GetName(),
				LaunchState: msgpb.ExperimentStatus_COMPLETED,
			})
			if err != nil {
				t.Fatalf("UpdateExperimentLaunchState() returned an unexpected error: %v", err)
			}

			_, err = s.UpdateWorkUnitExecutions(ctx, xid, []*storage.WorkUnitExecutionUpdate{
				{
					WID: 1,
					EID: 1,
					WorkUnitExecution: &msgpb.WorkUnitExecution{
						Status: &msgpb.WorkUnitStatus{State: tc.wu1State, StateUpdateTime: timestamppb.New(time.Now())},
					},
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
				},
				{
					WID: 2,
					EID: 1,
					WorkUnitExecution: &msgpb.WorkUnitExecution{
						Status: &msgpb.WorkUnitStatus{State: tc.wu2State, StateUpdateTime: timestamppb.New(time.Now())},
					},
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
				},
			})
			if err != nil {
				t.Fatalf("UpdateWorkUnitExecutions() returned an unexpected error: %v", err)
			}

			gotExp, err = server.GetExperiment(ctx, &apipb.GetExperimentRequest{Name: createdExp.GetName()})
			if err != nil {
				t.Fatalf("GetExperiment() returned an unexpected error: %v", err)
			}
			if gotExp.GetStatus().GetActiveState() != tc.wantActive {
				t.Errorf("IsActive() = %v, want %v for states %v, %v", gotExp.GetStatus().GetActiveState(), tc.wantActive, tc.wu1State, tc.wu2State)
			}
		})
	}
}

func TestUpdateExperimentLaunchState_InvalidTransition(t *testing.T) {
	ctx := context.Background()
	server, _ := createExperimentStateServer(ctx, t)

	exp := &msgpb.Experiment{
		Title: "test experiment for updateexperimentlaunchstate invalid transition",
		Acls: &xidmsgpb.XidAcls{
			Owners: []*xidmsgpb.XidAcls_Role{{Principal: &xidmsgpb.XidAcls_Role_User{User: "test@example.com"}}},
		},
	}
	createdExp := mustCreateExperiment(ctx, t, server, exp)

	// Transition to COMPLETED successfully.
	_, err := server.UpdateExperimentLaunchState(ctx, &apipb.UpdateExperimentLaunchStateRequest{
		Name:        createdExp.GetName(),
		LaunchState: msgpb.ExperimentStatus_COMPLETED,
	})
	if err != nil {
		t.Fatalf("UpdateExperimentLaunchState() to COMPLETED failed unexpectedly: %v", err)
	}

	// Try to transition from COMPLETED to FAILED, which should fail.
	_, err = server.UpdateExperimentLaunchState(ctx, &apipb.UpdateExperimentLaunchStateRequest{
		Name:        createdExp.GetName(),
		LaunchState: msgpb.ExperimentStatus_FAILED,
	})
	if !errors.Is(err, grpcerr.ErrInternal) {
		t.Errorf("UpdateExperimentLaunchState() from COMPLETED to FAILED returned error %v, want %v", err, grpcerr.ErrInternal)
	}
}
