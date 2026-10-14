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
	msgpb "xmanager_cloud/api/protos/experiment"
	xidmsgpb "xmanager_cloud/api/protos/xid"
	"xmanager_cloud/internal/auth"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/storage"
)

func TestGetExperiment_HappyPath(t *testing.T) {
	ctx := context.Background()
	server, _ := createExperimentStateServer(ctx, t)

	// Create an experiment first to test GetExperiment
	exp := &msgpb.Experiment{
		Title: "test experiment",
		Acls: &xidmsgpb.XidAcls{
			Owners: []*xidmsgpb.XidAcls_Role{{Principal: &xidmsgpb.XidAcls_Role_User{User: "test@example.com"}}},
		},
	}
	createdExp := mustCreateExperiment(ctx, t, server, exp)

	req := &msgpb.GetExperimentRequest{Name: createdExp.GetName()}
	got, err := server.GetExperiment(ctx, req)
	if err != nil {
		t.Fatalf("GetExperiment() returned an unexpected error: %v", err)
	}

	opts := []cmp.Option{
		protocmp.Transform(),
	}
	if diff := cmp.Diff(createdExp, got, opts...); diff != "" {
		t.Errorf("GetExperiment() mismatch (-want +got):\n%s", diff)
	}
}

func TestGetExperiment_BadPaths(t *testing.T) {
	ctx := context.Background()
	server := newTestServer(t)

	testCases := []struct {
		name    string
		req     *msgpb.GetExperimentRequest
		wantErr error
	}{
		{
			name:    "invalid_name",
			req:     &msgpb.GetExperimentRequest{Name: "invalid-name"},
			wantErr: grpcerr.ErrInvalidArgument,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := server.GetExperiment(ctx, tc.req)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("GetExperiment(%v) returned error %v, want %v", tc.req, err, tc.wantErr)
			}
		})
	}
}

func TestGetExperiment_DependencyFailures(t *testing.T) {
	ctx := context.Background()

	goodReq := &msgpb.GetExperimentRequest{Name: "experiments/123"}

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
			name: "xid_get_fails",
			xidClient: &mockXidClient{
				getXidEntryErr: fmt.Errorf("XID get failed"),
			},
			storage: &storage.MockStorage{},
			wantErr: grpcerr.ErrInternal,
		},
		{
			name: "xid_get_permission_denied",
			xidClient: &mockXidClient{
				getXidEntryErr: grpcerr.ErrPermissionDenied,
			},
			storage: &storage.MockStorage{},
			wantErr: grpcerr.ErrPermissionDenied,
		},
		{
			name: "storage_fetch_fails",
			xidClient: &mockXidClient{
				getXidEntryResp: &xidmsgpb.XidEntry{Name: proto.String("xids/123")},
			},
			storage: &storage.MockStorage{
				FetchExperimentErr: fmt.Errorf("storage fetch failed"),
			},
			wantErr: grpcerr.ErrInternal,
		},
		{
			name: "storage_fetch_not_found",
			xidClient: &mockXidClient{
				getXidEntryResp: &xidmsgpb.XidEntry{Name: proto.String("xids/123")},
			},
			storage: &storage.MockStorage{
				FetchExperimentErr: grpcerr.ErrNotFound,
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
			_, err := server.GetExperiment(ctx, goodReq)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("GetExperiment(%v) returned error %v, want %v", goodReq, err, tc.wantErr)
			}
		})
	}
}

func TestGetExperiment_DriftWarningPropagation(t *testing.T) {
	ctx := context.Background()
	req := &msgpb.GetExperimentRequest{Name: "experiments/123"}
	driftMsg := &msgpb.StructuredMessage{
		Severity:       msgpb.StructuredMessage_WARNING,
		Source:         msgpb.StructuredMessage_XMANAGER_BACKEND,
		UserFacingText: "Component snapshot drift detected",
	}
	mockStorage := &storage.MockStorage{
		Experiment: &msgpb.Experiment{
			Name: "experiments/123",
			Status: &msgpb.ExperimentStatus{
				StructuredStatusMessage: driftMsg,
			},
		},
	}
	xidClient := &mockXidClient{
		getXidEntryResp: &xidmsgpb.XidEntry{Name: proto.String("xids/123")},
	}
	server := newTestServer(t, withStorage(mockStorage), withXidClient(xidClient))

	got, err := server.GetExperiment(ctx, req)
	if err != nil {
		t.Fatalf("GetExperiment(%v) returned unexpected error: %v", req, err)
	}
	if diff := cmp.Diff(driftMsg, got.GetStatus().GetStructuredStatusMessage(), protocmp.Transform()); diff != "" {
		t.Errorf("GetExperiment() StructuredStatusMessage mismatch (-want +got):\n%s", diff)
	}
}
