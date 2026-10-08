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

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
	"task/go/status"
	"xmanager_cloud/auth/auth"
	"xmanager_cloud/internal/prototest"
	"xmanager_cloud/storage/storage"
	"xmanager_cloud/validator/validator"

	apipb "xmanager_cloud/experiment_state_server/proto/api_go_proto"
	msgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	xidgrpcpb "xmanager_cloud/xid_service/proto/api_go_grpc_proto"
	xidapipb "xmanager_cloud/xid_service/proto/api_go_proto"
	xidmsgpb "xmanager_cloud/xid_service/proto/messages_go_proto"
)

func TestCreateExperiment_HappyPath(t *testing.T) {
	ctx := context.Background()
	server, _ := createExperimentStateServer(ctx, t)

	now := time.Now()
	exp := &msgpb.Experiment{
		Title: "test experiment",
		Acls: &xidmsgpb.XidAcls{
			Owners: []*xidmsgpb.XidAcls_Role{{Principal: &xidmsgpb.XidAcls_Role_User{User: "test@example.com"}}},
		},
		Tags:  []string{"tag1", "tag2"},
		Notes: "test notes",
		Urls: []*msgpb.Url{
			{
				Url:         "http://www.google.com",
				DisplayName: "google",
				Icon:        "icon1",
				Priority:    1,
			},
		},
	}
	req := &apipb.CreateExperimentRequest{
		Experiment: exp,
	}

	got, err := server.CreateExperiment(ctx, req)
	if err != nil {
		t.Fatalf("CreateExperiment() returned an unexpected error: %v", err)
	}

	exp.Creator = "test@example.com"
	wantStatus := &msgpb.ExperimentStatus{
		ActiveState:             msgpb.ExperimentStatus_ACTIVE,
		LaunchState:             msgpb.ExperimentStatus_LAUNCHING,
		ChildStateCounts:        []*msgpb.ExperimentStatus_StateCount{},
		StructuredStatusMessage: &msgpb.StructuredMessage{},
	}
	exp.Status = wantStatus

	if got.GetName() == "" {
		t.Errorf("CreateExperiment() returned an empty experiment name")
	}
	if got.GetCreateTime() == nil || got.GetUpdateTime() == nil {
		t.Errorf("CreateExperiment() returned nil timestamps")
	}
	opts := []cmp.Option{
		protocmp.Transform(),
		protocmp.IgnoreFields(&msgpb.Experiment{}, "create_time", "update_time", "status", "name"),
		protocmp.IgnoreFields(&msgpb.ExperimentStatus{}, "launch_id"),
	}
	if diff := cmp.Diff(exp, got, opts...); diff != "" {
		t.Errorf("CreateExperiment() mismatch (-want +got):\n%s", diff)
	}
	if got.GetCreateTime().AsTime().Before(now) {
		t.Errorf("CreateExperiment() returned a create time %v that is before the test start time %v", got.GetCreateTime(), now)
	}
	if got.GetUpdateTime().AsTime().Before(now) {
		t.Errorf("CreateExperiment() returned an update time %v that is before the test start time %v", got.GetUpdateTime(), now)
	}
	xid, err := validator.ValidateExperimentName(got.GetName())
	if err != nil {
		t.Errorf("CreateExperiment() returned an invalid experiment name %s: %v", got.GetName(), err)
	}

	// XID should be a positive integer, and something weird has happened if we are out of the range
	// [0, 100). Technically, it should return either 1 or 2, but that's a database implementation
	// detail, so we'll just check that it's in a reasonable range.
	if xid <= 0 || xid >= 100 {
		t.Errorf("CreateExperiment() returned an invalid XID %d", xid)
	}
}

func TestCreateExperiment_FillNilValues(t *testing.T) {
	ctx := context.Background()
	server, _ := createExperimentStateServer(ctx, t)

	exp := &msgpb.Experiment{
		Title: "test experiment",
		Acls: &xidmsgpb.XidAcls{
			Owners: []*xidmsgpb.XidAcls_Role{{Principal: &xidmsgpb.XidAcls_Role_User{User: "test@example.com"}}},
		},
	}
	req := &apipb.CreateExperimentRequest{
		Experiment: exp,
	}

	got, err := server.CreateExperiment(ctx, req)
	if err != nil {
		t.Fatalf("CreateExperiment() returned an unexpected error: %v", err)
	}

	if got.GetTags() == nil {
		t.Errorf("CreateExperiment() returned nil tags, want empty slice")
	}
	if got.GetUrls() == nil {
		t.Errorf("CreateExperiment() returned nil urls, want empty slice")
	}
	if got.GetNotes() != "" {
		t.Errorf("CreateExperiment() returned notes %q, want empty string", got.GetNotes())
	}
}

func TestCreateExperiment_BadPaths(t *testing.T) {
	ctx := context.Background()
	server, _ := createExperimentStateServer(ctx, t)

	baseReq := &apipb.CreateExperimentRequest{
		Experiment: &msgpb.Experiment{
			Title: "test experiment",
			Acls: &xidmsgpb.XidAcls{
				Owners: []*xidmsgpb.XidAcls_Role{{Principal: &xidmsgpb.XidAcls_Role_User{User: "test@example.com"}}},
			},
		},
	}

	testCases := []struct {
		name    string
		req     *apipb.CreateExperimentRequest
		wantErr error
	}{
		{
			name:    "missing_experiment",
			req:     prototest.Modify(baseReq, func(r *apipb.CreateExperimentRequest) { r.Experiment = nil }),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "missing_acls",
			req: prototest.Modify(baseReq, func(r *apipb.CreateExperimentRequest) {
				r.Experiment.Acls = nil
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "creator_not_in_writers",
			req: prototest.Modify(baseReq, func(r *apipb.CreateExperimentRequest) {
				r.Experiment.Acls = &xidmsgpb.XidAcls{
					Owners:  []*xidmsgpb.XidAcls_Role{{Principal: &xidmsgpb.XidAcls_Role_User{User: "not-me@example.com"}}},
					Readers: []*xidmsgpb.XidAcls_Role{{Principal: &xidmsgpb.XidAcls_Role_User{User: "test@example.com"}}},
				}
			}),
			wantErr: status.ErrPermissionDenied,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := server.CreateExperiment(ctx, tc.req)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("CreateExperiment() returned error %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestCreateExperiment_DependencyFailures(t *testing.T) {
	ctx := context.Background()

	goodExp := &msgpb.Experiment{
		Title: "test experiment",
		Acls: &xidmsgpb.XidAcls{
			Owners: []*xidmsgpb.XidAcls_Role{{Principal: &xidmsgpb.XidAcls_Role_User{User: "test@example.com"}}},
		},
	}
	goodReq := &apipb.CreateExperimentRequest{
		Experiment: goodExp,
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
			name: "xid_creation_fails",
			xidClient: &mockXidClient{
				createXidEntryErr: fmt.Errorf("XID creation failed"),
			},
			storage: &storage.MockStorage{},
			wantErr: status.ErrInternal,
		},
		{
			name: "storage_creation_fails",
			xidClient: &mockXidClient{
				createXidEntryResp:         &xidmsgpb.XidEntry{Name: proto.String("xids/123")},
				checkUserAuthorizationResp: &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)},
			},
			storage: &storage.MockStorage{
				CreateExperimentErr: fmt.Errorf("Storage creation failed"),
			},
			wantErr: status.ErrInternal,
		},
		{
			name: "invalid_xid_name",
			xidClient: &mockXidClient{
				createXidEntryResp: &xidmsgpb.XidEntry{Name: proto.String("invalid-xid-name")},
			},
			storage: &storage.MockStorage{},
			wantErr: status.ErrInternal,
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
			_, err := server.CreateExperiment(ctx, goodReq)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("CreateExperiment() returned error %v, want %v", err, tc.wantErr)
			}
		})
	}
}
