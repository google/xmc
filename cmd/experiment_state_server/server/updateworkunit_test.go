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

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
	apipb "xmanager_cloud/experiment_state_server/proto/api_go_proto"
	msgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/auth"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/storage"
	xidapipb "xmanager_cloud/xid_service/proto/api_go_proto"
	xidgrpcpb "xmanager_cloud/xid_service/proto/api_go_proto"
	xidmsgpb "xmanager_cloud/xid_service/proto/messages_go_proto"

	fieldmaskpb "google.golang.org/protobuf/types/known/fieldmaskpb"
)

func TestUpdateWorkUnit_HappyPath(t *testing.T) {
	ctx := context.Background()
	server, _ := createExperimentStateServer(ctx, t)

	testCases := []struct {
		name       string
		updateWu   *msgpb.WorkUnit
		updateMask *fieldmaskpb.FieldMask
		want       *msgpb.WorkUnit
	}{
		{
			name: "update_priority",
			updateWu: &msgpb.WorkUnit{
				Name:     "experiments/1/workUnits/1",
				Priority: 200,
			},
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{"priority"}},
			want: &msgpb.WorkUnit{
				Name:     "experiments/1/workUnits/1",
				Priority: 200,
			},
		},
		{
			name: "update_urls",
			updateWu: &msgpb.WorkUnit{
				Name: "experiments/1/workUnits/1",
				Urls: []*msgpb.Url{{Url: "http://new.url"}},
			},
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{"urls"}},
			want: &msgpb.WorkUnit{
				Name:     "experiments/1/workUnits/1",
				Urls:     []*msgpb.Url{{Url: "http://new.url"}},
				Priority: msgpb.Priority_LOW,
			},
		},
		{
			name: "update_notes",
			updateWu: &msgpb.WorkUnit{
				Name:  "experiments/1/workUnits/1",
				Notes: "new notes",
			},
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{"notes"}},
			want: &msgpb.WorkUnit{
				Name:     "experiments/1/workUnits/1",
				Notes:    "new notes",
				Priority: msgpb.Priority_LOW,
			},
		},
		{
			name: "update_tags",
			updateWu: &msgpb.WorkUnit{
				Name: "experiments/1/workUnits/1",
				Tags: []string{"tag1", "tag2"},
			},
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{"tags"}},
			want: &msgpb.WorkUnit{
				Name:     "experiments/1/workUnits/1",
				Tags:     []string{"tag1", "tag2"},
				Priority: msgpb.Priority_LOW,
			},
		},
		{
			name: "update_multiple",
			updateWu: &msgpb.WorkUnit{
				Name:     "experiments/1/workUnits/1",
				Priority: 200,
				Notes:    "new notes",
				Tags:     []string{"tag1", "tag2"},
			},
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{"priority", "notes", "tags"}},
			want: &msgpb.WorkUnit{
				Name:     "experiments/1/workUnits/1",
				Priority: 200,
				Notes:    "new notes",
				Tags:     []string{"tag1", "tag2"},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			exp := &msgpb.Experiment{
				Title: "test experiment for updateworkunit " + tc.name,
				Acls: &xidmsgpb.XidAcls{
					Owners: []*xidmsgpb.XidAcls_Role{{Principal: &xidmsgpb.XidAcls_Role_User{User: "test@example.com"}}},
				},
			}
			createdExp := mustCreateExperiment(ctx, t, server, exp)

			workUnit := &msgpb.WorkUnit{
				WorkUnitType: msgpb.WorkUnit_STANDARD,
				Executable:   testExecutable,
			}
			mustCreateWorkUnit(ctx, t, server, createdExp.GetName(), 1, workUnit)

			tc.updateWu.Name = fmt.Sprintf("%s/workUnits/%d", createdExp.GetName(), 1)
			tc.want.Name = tc.updateWu.Name
			req := &apipb.UpdateWorkUnitRequest{
				WorkUnit:   tc.updateWu,
				UpdateMask: tc.updateMask,
			}
			got, err := server.UpdateWorkUnit(ctx, req)
			if err != nil {
				t.Fatalf("UpdateWorkUnit() returned an unexpected error: %v", err)
			}

			// We don't want to compare the whole object, just the fields we updated.
			if diff := cmp.Diff(tc.want, got, protocmp.Transform(), protocmp.IgnoreFields(&msgpb.WorkUnit{}, "create_time", "update_time", "work_unit_type", "executable", "current_work_unit_execution", "urls")); diff != "" {
				t.Errorf("UpdateWorkUnit() returned unexpected response (-want +got):\n%s", diff)
			}
		})
	}
}

func TestUpdateWorkUnit_EmptyMask(t *testing.T) {
	ctx := context.Background()
	server, _ := createExperimentStateServer(ctx, t)

	exp := &msgpb.Experiment{
		Title: "test experiment for updateworkunit empty mask",
		Acls: &xidmsgpb.XidAcls{
			Owners: []*xidmsgpb.XidAcls_Role{{Principal: &xidmsgpb.XidAcls_Role_User{User: "test@example.com"}}},
		},
	}
	createdExp := mustCreateExperiment(ctx, t, server, exp)

	workUnit := &msgpb.WorkUnit{
		WorkUnitType: msgpb.WorkUnit_STANDARD,
		Executable:   testExecutable,
	}
	mustCreateWorkUnit(ctx, t, server, createdExp.GetName(), 1, workUnit)

	wuName := fmt.Sprintf("%s/workUnits/%d", createdExp.GetName(), 1)

	req := &apipb.UpdateWorkUnitRequest{
		WorkUnit:   &msgpb.WorkUnit{Name: wuName},
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{}},
	}
	_, err := server.UpdateWorkUnit(ctx, req)
	if !errors.Is(err, grpcerr.ErrInternal) {
		t.Errorf("UpdateWorkUnit() with empty mask got error %v, want %v", err, grpcerr.ErrInternal)
	}
}

func TestUpdateWorkUnit_BadPaths(t *testing.T) {
	ctx := context.Background()
	server := newTestServer(t)

	testCases := []struct {
		name    string
		req     *apipb.UpdateWorkUnitRequest
		wantErr error
	}{
		{
			name: "missing_work_unit",
			req: &apipb.UpdateWorkUnitRequest{
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"priority"}},
			},
			wantErr: grpcerr.ErrInvalidArgument,
		},
		{
			name: "invalid_name",
			req: &apipb.UpdateWorkUnitRequest{
				WorkUnit: &msgpb.WorkUnit{
					Name: "invalid-name",
				},
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"priority"}},
			},
			wantErr: grpcerr.ErrInvalidArgument,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := server.UpdateWorkUnit(ctx, tc.req)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("UpdateWorkUnit() got error %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestUpdateWorkUnit_DependencyFailures(t *testing.T) {
	ctx := context.Background()

	goodReq := &apipb.UpdateWorkUnitRequest{
		WorkUnit: &msgpb.WorkUnit{
			Name:     "experiments/123/workUnits/456",
			Priority: 200,
		},
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"priority"}},
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
			name: "storage_update_work_unit_fails",
			xidClient: &mockXidClient{
				checkUserAuthorizationResp: &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)},
			},
			storage: &storage.MockStorage{
				UpdateWorkUnitErr: fmt.Errorf("Storage update work unit failed"),
			},
			wantErr: grpcerr.ErrInternal,
		},
		{
			name: "storage_update_work_unit_not_found",
			xidClient: &mockXidClient{
				checkUserAuthorizationResp: &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)},
			},
			storage: &storage.MockStorage{
				UpdateWorkUnitErr: grpcerr.ErrNotFound,
			},
			wantErr: grpcerr.ErrInternal,
		},
		{
			name: "storage_fetch_experiment_fails",
			xidClient: &mockXidClient{
				checkUserAuthorizationResp: &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)},
			},
			storage: &storage.MockStorage{
				UpdateWorkUnitRespOriginal: &msgpb.WorkUnit{Priority: msgpb.Priority_LOW},
				UpdateWorkUnitRespReturned: &msgpb.WorkUnit{Priority: msgpb.Priority_NORMAL},
				FetchExperimentErr:         fmt.Errorf("Storage fetch experiment failed"),
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
			_, err := server.UpdateWorkUnit(ctx, goodReq)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("UpdateWorkUnit() got error %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestUpdateWorkUnit_EnqueuesTaskOnPriorityChange(t *testing.T) {
	ctx := context.Background()
	server, taskQueueClient := createExperimentStateServer(ctx, t)
	createdExp := mustCreateExperiment(ctx, t, server, nil)
	mustCreateWorkUnit(ctx, t, server, createdExp.GetName(), 1, nil)
	wuName := fmt.Sprintf("%s/workUnits/%d", createdExp.GetName(), 1)

	req := &apipb.UpdateWorkUnitRequest{
		WorkUnit: &msgpb.WorkUnit{
			Name:     wuName,
			Priority: 200,
		},
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"priority"}},
	}
	_, err := server.UpdateWorkUnit(ctx, req)
	if err != nil {
		t.Fatalf("UpdateWorkUnit() returned an unexpected error: %v", err)
	}

	if len(taskQueueClient.EnqueuedTasks) != 2 {
		t.Fatalf("UpdateWorkUnit() with priority change should enqueue one task, got %d", len(taskQueueClient.EnqueuedTasks)-1)
	}
	// The first request is from CreateWorkUnit, the second from UpdateWorkUnit.
	task := taskQueueClient.EnqueuedTasks[1]

	action := task.Action
	if action == nil {
		t.Fatalf("Task action is nil")
	}

	updateAction := action.GetUpdate()
	if updateAction == nil {
		t.Fatalf("Task action is not Update: %v", action)
	}

	if task.WorkUnit.GetName() != wuName {
		t.Errorf("Enqueued work unit has wrong name: got %q, want %q", task.WorkUnit.GetName(), wuName)
	}
	if task.WorkUnit.GetPriority() != 200 {
		t.Errorf("Enqueued work unit has wrong priority: got %d, want %d", task.WorkUnit.GetPriority(), 200)
	}
}

func TestUpdateWorkUnit_DoesNotEnqueueOnOtherChanges(t *testing.T) {
	ctx := context.Background()
	server, taskQueueClient := createExperimentStateServer(ctx, t)
	createdExp := mustCreateExperiment(ctx, t, server, nil)
	mustCreateWorkUnit(ctx, t, server, createdExp.GetName(), 1, nil)
	wuName := fmt.Sprintf("%s/workUnits/%d", createdExp.GetName(), 1)
	taskQueueClient.EnqueuedTasks = nil // Clear requests from CreateWorkUnit

	req := &apipb.UpdateWorkUnitRequest{
		WorkUnit: &msgpb.WorkUnit{
			Name:  wuName,
			Notes: "new notes",
		},
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"notes"}},
	}
	_, err := server.UpdateWorkUnit(ctx, req)
	if err != nil {
		t.Fatalf("UpdateWorkUnit() returned an unexpected error: %v", err)
	}

	if len(taskQueueClient.EnqueuedTasks) != 0 {
		t.Errorf("UpdateWorkUnit() with non-priority change should not enqueue tasks, but enqueued %d tasks", len(taskQueueClient.EnqueuedTasks))
	}
}

func TestUpdateWorkUnit_NoExecutionWithoutActuationSucceeds(t *testing.T) {
	ctx := context.Background()
	mockStorage := &storage.MockStorage{
		UpdateWorkUnitRespOriginal: &msgpb.WorkUnit{
			Name:  "experiments/1/workUnits/1",
			Notes: "old notes",
		},
		UpdateWorkUnitRespReturned: &msgpb.WorkUnit{
			Name:  "experiments/1/workUnits/1",
			Notes: "updated notes",
		},
	}
	mockXid := &mockXidClient{
		checkUserAuthorizationResp: &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)},
	}
	server := newTestServer(t, withStorage(mockStorage), withXidClient(mockXid))

	req := &apipb.UpdateWorkUnitRequest{
		WorkUnit: &msgpb.WorkUnit{
			Name:  "experiments/1/workUnits/1",
			Notes: "updated notes",
		},
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"notes"}},
	}
	res, err := server.UpdateWorkUnit(ctx, req)
	if err != nil {
		t.Fatalf("UpdateWorkUnit() without execution returned error: %v", err)
	}
	if res.GetNotes() != "updated notes" {
		t.Errorf("res.GetNotes() = %q, want %q", res.GetNotes(), "updated notes")
	}
}
