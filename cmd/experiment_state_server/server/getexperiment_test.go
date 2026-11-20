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
	"xmanager_cloud/internal/experiment/experimenttest"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/pkg/components/store"
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
	compName := "redpanda"
	observed := int64(2)
	mockStorage := &storage.MockStorage{
		Experiment: &msgpb.Experiment{
			Name: "experiments/123",
			Status: &msgpb.ExperimentStatus{
				ActiveState: msgpb.ExperimentStatus_ACTIVE,
			},
		},
		DriftedComponents: []storage.ComponentDiff{
			{
				ComponentID:        202,
				Name:               &compName,
				LockedGeneration:   1,
				ObservedGeneration: &observed,
			},
		},
	}
	xidClient := &mockXidClient{
		getXidEntryResp: &xidmsgpb.XidEntry{Name: proto.String("xids/123")},
	}
	server := newTestServer(t,
		withStorage(mockStorage),
		withXidClient(xidClient),
	)

	got, err := server.GetExperiment(ctx, req)
	if err != nil {
		t.Fatalf("GetExperiment(%v) returned unexpected error: %v", req, err)
	}
	want := &msgpb.StructuredMessage{
		Severity:       msgpb.StructuredMessage_WARNING,
		Source:         msgpb.StructuredMessage_XMANAGER_BACKEND,
		UserFacingText: "Component drift detected: live component generations diverge from snapshots: redpanda (snapshot generation 1 != live observed generation 2)",
	}
	if diff := cmp.Diff(want, got.GetStatus().GetStructuredStatusMessage(), protocmp.Transform(), protocmp.IgnoreFields(&msgpb.StructuredMessage{}, "create_time")); diff != "" {
		t.Errorf("GetExperiment() StructuredStatusMessage mismatch (-want +got):\n%s", diff)
	}
}

type countingComponentRepository struct {
	storage.ComponentRepository
	calls int
}

func (c *countingComponentRepository) GetActiveComponent(ctx context.Context, name string) (*store.ComponentRecord, error) {
	c.calls++
	return c.ComponentRepository.GetActiveComponent(ctx, name)
}

func TestGetExperiment_StudyControllerAuthz(t *testing.T) {
	ctx := context.Background()
	req := &msgpb.GetExperimentRequest{Name: "experiments/123"}
	xidAcls := &xidmsgpb.XidAcls{
		Owners: []*xidmsgpb.XidAcls_Role{{Principal: &xidmsgpb.XidAcls_Role_User{User: "alice@example.com"}}},
	}
	studyExp := func() *msgpb.Experiment {
		return &msgpb.Experiment{
			Name:           "experiments/123",
			Title:          "hpo study",
			ExperimentType: msgpb.Experiment_STUDY,
			Status:         &msgpb.ExperimentStatus{ActiveState: msgpb.ExperimentStatus_ACTIVE},
		}
	}
	standardExp := func() *msgpb.Experiment {
		return &msgpb.Experiment{
			Name:           "experiments/123",
			Title:          "standard exp",
			ExperimentType: msgpb.Experiment_STANDARD,
			Status:         &msgpb.ExperimentStatus{ActiveState: msgpb.ExperimentStatus_ACTIVE},
		}
	}
	hpoRepo := func(sa string) *storage.MockComponentRepository {
		return &storage.MockComponentRepository{
			Records: experimenttest.HPOCatalog(map[string]string{"studyControllerServiceAccount": sa}),
		}
	}

	testCases := []struct {
		name             string
		caller           string
		components       storage.ComponentRepository
		exp              *msgpb.Experiment
		fetchErr         error
		xidErr           error
		wantErr          error
		wantXidCalls     int
		wantCatalogCalls int
		wantAcls         *xidmsgpb.XidAcls
	}{
		{
			name:             "controller_gets_study_after_xid_denied_with_nil_acls",
			caller:           testStudyControllerSA,
			components:       hpoRepo(testStudyControllerSA),
			exp:              studyExp(),
			xidErr:           grpcerr.ErrPermissionDenied,
			wantXidCalls:     1,
			wantCatalogCalls: 1,
			wantAcls:         nil,
		},
		{
			name:             "controller_denied_standard_experiment",
			caller:           testStudyControllerSA,
			components:       hpoRepo(testStudyControllerSA),
			exp:              standardExp(),
			xidErr:           grpcerr.ErrPermissionDenied,
			wantErr:          grpcerr.ErrPermissionDenied,
			wantXidCalls:     1,
			wantCatalogCalls: 1,
		},
		{
			name:             "controller_study_not_found",
			caller:           testStudyControllerSA,
			components:       hpoRepo(testStudyControllerSA),
			xidErr:           grpcerr.ErrPermissionDenied,
			fetchErr:         grpcerr.ErrNotFound,
			wantErr:          grpcerr.ErrNotFound,
			wantXidCalls:     1,
			wantCatalogCalls: 1,
		},
		{
			name:             "controller_with_empty_sa_fails_closed",
			caller:           testStudyControllerSA,
			components:       hpoRepo(""),
			exp:              studyExp(),
			xidErr:           grpcerr.ErrPermissionDenied,
			wantErr:          grpcerr.ErrPermissionDenied,
			wantXidCalls:     1,
			wantCatalogCalls: 1,
		},
		{
			name:             "controller_with_missing_hpo_component_fails_closed",
			caller:           testStudyControllerSA,
			components:       &storage.MockComponentRepository{},
			exp:              studyExp(),
			xidErr:           grpcerr.ErrPermissionDenied,
			wantErr:          grpcerr.ErrPermissionDenied,
			wantXidCalls:     1,
			wantCatalogCalls: 1,
		},
		{
			name:             "normal_user_gets_study_via_xid_with_zero_catalog_lookups",
			caller:           "alice@example.com",
			components:       hpoRepo(testStudyControllerSA),
			exp:              studyExp(),
			wantXidCalls:     1,
			wantCatalogCalls: 0,
			wantAcls:         xidAcls,
		},
		{
			name:             "normal_user_gets_standard_via_xid_with_zero_catalog_lookups",
			caller:           "alice@example.com",
			components:       hpoRepo(testStudyControllerSA),
			exp:              standardExp(),
			wantXidCalls:     1,
			wantCatalogCalls: 0,
			wantAcls:         xidAcls,
		},
		{
			name:             "component_store_error_returns_internal_on_xid_denied",
			caller:           testStudyControllerSA,
			components:       &storage.MockComponentRepository{GetActiveComponentErr: errors.New("db outage")},
			exp:              studyExp(),
			xidErr:           grpcerr.ErrPermissionDenied,
			wantErr:          grpcerr.ErrInternal,
			wantXidCalls:     1,
			wantCatalogCalls: 1,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			xidClient := &mockXidClient{
				getXidEntryResp: &xidmsgpb.XidEntry{Name: proto.String("xids/123"), Acls: xidAcls},
				getXidEntryErr:  tc.xidErr,
			}
			counting := &countingComponentRepository{ComponentRepository: tc.components}
			mockStorage := &storage.MockStorage{
				Experiment:          tc.exp,
				FetchExperimentErr:  tc.fetchErr,
				ComponentRepository: counting,
			}
			srv := newTestServer(t,
				withStorage(mockStorage),
				withAuthorizer(&mockAuthenticator{email: tc.caller}),
				withXidClient(xidClient),
			)

			got, err := srv.GetExperiment(ctx, req)
			if xidClient.getXidEntryCalls != tc.wantXidCalls {
				t.Errorf("GetXidEntry calls = %d, want %d", xidClient.getXidEntryCalls, tc.wantXidCalls)
			}
			if counting.calls != tc.wantCatalogCalls {
				t.Errorf("catalog lookups = %d, want %d", counting.calls, tc.wantCatalogCalls)
			}
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Errorf("GetExperiment() err = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("GetExperiment() unexpected err = %v", err)
			}
			want := proto.Clone(tc.exp).(*msgpb.Experiment)
			want.Acls = tc.wantAcls
			if diff := cmp.Diff(want, got, protocmp.Transform()); diff != "" {
				t.Errorf("GetExperiment() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
