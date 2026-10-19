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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
	"xmanager_cloud/cmd/experiment_state_server/config"
	"xmanager_cloud/internal/auth"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/prototest"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/validator"

	msgpb "xmanager_cloud/api/protos/experiment"
	xidmsgpb "xmanager_cloud/api/protos/xid"
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
	req := &msgpb.CreateExperimentRequest{
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
	req := &msgpb.CreateExperimentRequest{
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

	baseReq := &msgpb.CreateExperimentRequest{
		Experiment: &msgpb.Experiment{
			Title: "test experiment",
			Acls: &xidmsgpb.XidAcls{
				Owners: []*xidmsgpb.XidAcls_Role{{Principal: &xidmsgpb.XidAcls_Role_User{User: "test@example.com"}}},
			},
		},
	}

	testCases := []struct {
		name    string
		req     *msgpb.CreateExperimentRequest
		wantErr error
	}{
		{
			name:    "missing_experiment",
			req:     prototest.Modify(baseReq, func(r *msgpb.CreateExperimentRequest) { r.Experiment = nil }),
			wantErr: grpcerr.ErrInvalidArgument,
		},
		{
			name: "creator_not_in_writers",
			req: prototest.Modify(baseReq, func(r *msgpb.CreateExperimentRequest) {
				r.Experiment.Acls = &xidmsgpb.XidAcls{
					Owners:  []*xidmsgpb.XidAcls_Role{{Principal: &xidmsgpb.XidAcls_Role_User{User: "not-me@example.com"}}},
					Readers: []*xidmsgpb.XidAcls_Role{{Principal: &xidmsgpb.XidAcls_Role_User{User: "test@example.com"}}},
				}
			}),
			wantErr: grpcerr.ErrPermissionDenied,
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
	goodReq := &msgpb.CreateExperimentRequest{
		Experiment: goodExp,
	}

	testCases := []struct {
		name          string
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
			name: "xid_creation_fails",
			xidClient: &mockXidClient{
				createXidEntryErr: fmt.Errorf("XID creation failed"),
			},
			storage: &storage.MockStorage{},
			wantErr: grpcerr.ErrInternal,
		},
		{
			name: "storage_creation_fails",
			xidClient: &mockXidClient{
				createXidEntryResp: &xidmsgpb.XidEntry{Name: proto.String("xids/123")},
			},
			storage: &storage.MockStorage{
				InsertExperimentErr: fmt.Errorf("Storage creation failed"),
			},
			wantErr: grpcerr.ErrInternal,
		},
		{
			name: "invalid_xid_name",
			xidClient: &mockXidClient{
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
			_, err := server.CreateExperiment(ctx, goodReq)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("CreateExperiment() returned error %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func userRole(user string) *xidmsgpb.XidAcls_Role {
	return &xidmsgpb.XidAcls_Role{Principal: &xidmsgpb.XidAcls_Role_User{User: user}}
}

func TestCreateExperiment_CreatorAuthorization(t *testing.T) {
	ctx := context.Background()
	creatorOwner := &xidmsgpb.XidAcls{Owners: []*xidmsgpb.XidAcls_Role{userRole("test@example.com")}}
	baseReq := &msgpb.CreateExperimentRequest{
		Experiment: &msgpb.Experiment{Title: "test experiment", Acls: creatorOwner},
	}
	withACLs := func(acls *xidmsgpb.XidAcls) *msgpb.CreateExperimentRequest {
		return prototest.Modify(baseReq, func(r *msgpb.CreateExperimentRequest) { r.Experiment.Acls = acls })
	}
	creatorWriter := &xidmsgpb.XidAcls{
		Owners:  []*xidmsgpb.XidAcls_Role{userRole("other@example.com")},
		Writers: []*xidmsgpb.XidAcls_Role{userRole("test@example.com")},
	}
	creatorReader := &xidmsgpb.XidAcls{
		Owners:  []*xidmsgpb.XidAcls_Role{userRole("other@example.com")},
		Readers: []*xidmsgpb.XidAcls_Role{userRole("test@example.com")},
	}
	creatorAbsent := &xidmsgpb.XidAcls{Owners: []*xidmsgpb.XidAcls_Role{userRole("other@example.com")}}
	domainWriter := &xidmsgpb.XidAcls{
		Owners:  []*xidmsgpb.XidAcls_Role{userRole("other@example.com")},
		Writers: []*xidmsgpb.XidAcls_Role{{Principal: &xidmsgpb.XidAcls_Role_Domain{Domain: "example.com"}}},
	}
	allAuthenticatedOwner := &xidmsgpb.XidAcls{
		Owners: []*xidmsgpb.XidAcls_Role{{Principal: &xidmsgpb.XidAcls_Role_AllAuthenticatedUsers{AllAuthenticatedUsers: true}}},
	}
	groupWriter := &xidmsgpb.XidAcls{
		Owners:  []*xidmsgpb.XidAcls_Role{userRole("other@example.com")},
		Writers: []*xidmsgpb.XidAcls_Role{{Principal: &xidmsgpb.XidAcls_Role_Group{Group: "team@example.com"}}},
	}

	testCases := []struct {
		name string
		req  *msgpb.CreateExperimentRequest
		// authorizer overrides the default test authorizer, which evaluates ACLs with the real
		// internal/auth logic but cannot resolve Cloud Identity group memberships.
		authorizer         *mockAuthenticator
		createXidEntryErr  error
		wantCode           codes.Code
		wantCreateXidCalls int
		wantXidACLs        *xidmsgpb.XidAcls
	}{
		{name: "creator_owner", req: baseReq, wantCode: codes.OK, wantCreateXidCalls: 1, wantXidACLs: creatorOwner},
		{name: "creator_writer", req: withACLs(creatorWriter), wantCode: codes.OK, wantCreateXidCalls: 1, wantXidACLs: creatorWriter},
		{name: "creator_domain_writer", req: withACLs(domainWriter), wantCode: codes.OK, wantCreateXidCalls: 1, wantXidACLs: domainWriter},
		{name: "all_authenticated_users_owner", req: withACLs(allAuthenticatedOwner), wantCode: codes.OK, wantCreateXidCalls: 1, wantXidACLs: allAuthenticatedOwner},
		{name: "creator_group_writer", req: withACLs(groupWriter), authorizer: &mockAuthenticator{}, wantCode: codes.OK, wantCreateXidCalls: 1, wantXidACLs: groupWriter},
		{name: "nil_acls_default_to_creator_owner", req: withACLs(nil), wantCode: codes.OK, wantCreateXidCalls: 1, wantXidACLs: creatorOwner},
		{name: "empty_acls_default_to_creator_owner", req: withACLs(&xidmsgpb.XidAcls{}), wantCode: codes.OK, wantCreateXidCalls: 1, wantXidACLs: creatorOwner},
		{name: "creator_reader_only_denied", req: withACLs(creatorReader), wantCode: codes.PermissionDenied},
		{name: "creator_absent_denied", req: withACLs(creatorAbsent), wantCode: codes.PermissionDenied},
		{name: "authorizer_denies", req: baseReq, authorizer: &mockAuthenticator{denyAuthorization: true}, wantCode: codes.PermissionDenied},
		{name: "authorizer_error", req: baseReq, authorizer: &mockAuthenticator{isUserAuthorizedErr: errors.New("invalid email")}, wantCode: codes.PermissionDenied},
		{
			// The XID failure is still wrapped without a status sentinel, as before this check moved.
			name:               "xid_creation_fails_after_authorization",
			req:                baseReq,
			createXidEntryErr:  errors.New("XID creation failed"),
			wantCode:           codes.Unknown,
			wantCreateXidCalls: 1,
			wantXidACLs:        creatorOwner,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			xidClient := &mockXidClient{
				createXidEntryResp: &xidmsgpb.XidEntry{Name: proto.String("xids/123")},
				createXidEntryErr:  tc.createXidEntryErr,
			}
			opts := []testServerOpt{
				withConfig(&config.Config{DefaultExecutionEnvironment: "gke"}),
				withXidClient(xidClient),
			}
			if tc.authorizer != nil {
				opts = append(opts, withAuthorizer(tc.authorizer))
			}
			server := newTestServer(t, opts...)

			_, err := server.CreateExperiment(ctx, tc.req)

			if got := status.Code(err); got != tc.wantCode {
				t.Errorf("CreateExperiment() returned code %v (error %v), want %v", got, err, tc.wantCode)
			}
			if xidClient.createXidEntryCalls != tc.wantCreateXidCalls {
				t.Errorf("CreateExperiment() made %d CreateXidEntry calls, want %d", xidClient.createXidEntryCalls, tc.wantCreateXidCalls)
			}
			if diff := cmp.Diff(tc.wantXidACLs, xidClient.gotCreateXidEntryReq.GetXidEntry().GetAcls(), protocmp.Transform()); diff != "" {
				t.Errorf("CreateExperiment() sent unexpected ACLs to CreateXidEntry (-want +got):\n%s", diff)
			}
			if tc.authorizer != nil && tc.authorizer.gotIsUserAuthorizedRole != auth.Writer {
				t.Errorf("CreateExperiment() checked role %v, want %v", tc.authorizer.gotIsUserAuthorizedRole, auth.Writer)
			}
		})
	}
}

func TestCreateExperiment_DefaultACLs(t *testing.T) {
	ctx := context.Background()
	server, _ := createExperimentStateServer(ctx, t)
	wantACLs := &xidmsgpb.XidAcls{Owners: []*xidmsgpb.XidAcls_Role{userRole("test@example.com")}}
	baseReq := &msgpb.CreateExperimentRequest{Experiment: &msgpb.Experiment{Title: "test experiment"}}

	testCases := []struct {
		name string
		acls *xidmsgpb.XidAcls
	}{
		{name: "nil_acls", acls: nil},
		{name: "empty_acls", acls: &xidmsgpb.XidAcls{}},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := prototest.Modify(baseReq, func(r *msgpb.CreateExperimentRequest) { r.Experiment.Acls = tc.acls })

			got, err := server.CreateExperiment(ctx, req)
			if err != nil {
				t.Fatalf("CreateExperiment() returned an unexpected error: %v", err)
			}

			if diff := cmp.Diff(wantACLs, got.GetAcls(), protocmp.Transform()); diff != "" {
				t.Errorf("CreateExperiment() returned unexpected ACLs (-want +got):\n%s", diff)
			}
		})
	}
}
