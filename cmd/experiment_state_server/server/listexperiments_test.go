// Copyright 2025 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
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
	"sort"
	"testing"
	"time"

	"golang/cmp/cmp"
	"golang/protobuf/v2/testing/protocmp/protocmp"
	"xmanager_cloud/auth/auth"
	"xmanager_cloud/experiment_state_server/config"
	apipb "xmanager_cloud/experiment_state_server/proto/api_go_proto"
	msgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/storage/storage"
	"xmanager_cloud/validator/validator"
	"task/go/status"
)

func TestListExperiments_HappyPath(t *testing.T) {
	ctx := context.Background()
	server, _ := createExperimentStateServer(ctx, t)

	var exps []*msgpb.Experiment
	for i := 0; i < 3; i++ {
		exps = append(exps, mustCreateExperiment(ctx, t, server, nil))
	}
	sort.Slice(exps, func(i, j int) bool {
		iXID, _ := validator.ValidateExperimentName(exps[i].GetName())
		jXID, _ := validator.ValidateExperimentName(exps[j].GetName())
		return iXID < jXID
	})

	// List first page.
	resp1, err := server.ListExperiments(ctx, &apipb.ListExperimentsRequest{PageSize: 2, OrderBy: "xid"})
	if err != nil {
		t.Fatalf("ListExperiments() returned an unexpected error: %v", err)
	}
	if len(resp1.GetExperiments()) != 2 {
		t.Errorf("ListExperiments() returned %d experiments, want 2", len(resp1.GetExperiments()))
	}
	if resp1.GetNextPageToken() == "" {
		t.Errorf("ListExperiments() returned empty next_page_token, want non-empty")
	}

	// List second page.
	resp2, err := server.ListExperiments(ctx, &apipb.ListExperimentsRequest{PageSize: 2, PageToken: resp1.GetNextPageToken(), OrderBy: "xid"})
	if err != nil {
		t.Fatalf("ListExperiments() returned an unexpected error: %v", err)
	}
	if len(resp2.GetExperiments()) != 1 {
		t.Errorf("ListExperiments() returned %d experiments, want 1", len(resp2.GetExperiments()))
	}
	if resp2.GetNextPageToken() != "" {
		t.Errorf("ListExperiments() returned non-empty next_page_token, want empty")
	}

	cmpOpts := []cmp.Option{
		protocmp.Transform(),
	}

	// Check all experiments are returned in order.
	if diff := cmp.Diff(exps[0], resp1.GetExperiments()[0], cmpOpts...); diff != "" {
		t.Errorf("ListExperiments() page 1 experiment 1 returned unexpected diff (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(exps[1], resp1.GetExperiments()[1], cmpOpts...); diff != "" {
		t.Errorf("ListExperiments() page 1 experiment 2 returned unexpected diff (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(exps[2], resp2.GetExperiments()[0], cmpOpts...); diff != "" {
		t.Errorf("ListExperiments() page 2 experiment 1 returned unexpected diff (-want +got):\n%s", diff)
	}
}

func TestSearchExperiments_HappyPath(t *testing.T) {
	ctx := context.Background()
	server, _ := createExperimentStateServer(ctx, t)

	time.Sleep(experimentSearchStaleness + 10*time.Millisecond)

	var exps []*msgpb.Experiment
	for i := 0; i < 3; i++ {
		exps = append(exps, mustCreateExperiment(ctx, t, server, nil))
	}

	time.Sleep(experimentSearchStaleness + 10*time.Millisecond)

	sort.Slice(exps, func(i, j int) bool {
		iXID, _ := validator.ValidateExperimentName(exps[i].GetName())
		jXID, _ := validator.ValidateExperimentName(exps[j].GetName())
		return iXID < jXID
	})

	// List first page.
	resp1, err := server.SearchExperiments(ctx, &apipb.ListExperimentsRequest{PageSize: 2, OrderBy: "xid"})
	if err != nil {
		t.Fatalf("SearchExperiments() returned an unexpected error: %v", err)
	}
	if len(resp1.GetExperiments()) != 2 {
		t.Errorf("SearchExperiments() returned %d experiments, want 2", len(resp1.GetExperiments()))
	}
	if resp1.GetNextPageToken() == "" {
		t.Errorf("SearchExperiments() returned empty next_page_token, want non-empty")
	}

	// List second page.
	resp2, err := server.SearchExperiments(ctx, &apipb.ListExperimentsRequest{PageSize: 2, PageToken: resp1.GetNextPageToken(), OrderBy: "xid"})
	if err != nil {
		t.Fatalf("SearchExperiments() returned an unexpected error: %v", err)
	}
	if len(resp2.GetExperiments()) != 1 {
		t.Errorf("SearchExperiments() returned %d experiments, want 1", len(resp2.GetExperiments()))
	}
	if resp2.GetNextPageToken() != "" {
		t.Errorf("SearchExperiments() returned non-empty next_page_token, want empty")
	}

	cmpOpts := []cmp.Option{
		protocmp.Transform(),
	}

	// Check all experiments are returned in order.
	if diff := cmp.Diff(exps[0], resp1.GetExperiments()[0], cmpOpts...); diff != "" {
		t.Errorf("SearchExperiments() page 1 experiment 1 returned unexpected diff (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(exps[1], resp1.GetExperiments()[1], cmpOpts...); diff != "" {
		t.Errorf("SearchExperiments() page 1 experiment 2 returned unexpected diff (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(exps[2], resp2.GetExperiments()[0], cmpOpts...); diff != "" {
		t.Errorf("SearchExperiments() page 2 experiment 1 returned unexpected diff (-want +got):\n%s", diff)
	}
}

func TestListExperiments_BadPaths(t *testing.T) {
	ctx := context.Background()
	server, _ := createExperimentStateServer(ctx, t)

	testCases := []struct {
		name    string
		req     *apipb.ListExperimentsRequest
		wantErr error
	}{
		{
			name:    "invalid_page_size",
			req:     &apipb.ListExperimentsRequest{PageSize: -1},
			wantErr: status.ErrInvalidArgument,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := server.ListExperiments(ctx, tc.req)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("ListExperiments(%v) returned error %v, want %v", tc.req, err, tc.wantErr)
			}
		})
	}
}

func TestListExperiments_DependencyFailures(t *testing.T) {
	ctx := context.Background()
	cfg := &config.Config{}

	goodReq := &apipb.ListExperimentsRequest{PageSize: 10}

	testCases := []struct {
		name          string
		authenticator auth.AuthorizerInterface
		storage       storage.Storage
		wantErr       error
	}{
		{
			name:          "auth_fails",
			authenticator: &mockAuthenticator{err: status.ErrUnauthenticated},
			storage:       &storage.MockStorage{},
			wantErr:       status.ErrUnauthenticated,
		},
		{
			name:          "storage_fails_grpc_invalid_arg",
			authenticator: createTestAuthorizer(ctx, t),
			storage:       &storage.MockStorage{ListExperimentsErr: status.ErrInvalidArgument},
			wantErr:       status.ErrInvalidArgument,
		},
		{
			name:          "storage_fails_grpc_other",
			authenticator: createTestAuthorizer(ctx, t),
			storage:       &storage.MockStorage{ListExperimentsErr: status.ErrPermissionDenied},
			wantErr:       status.ErrInternal,
		},
		{
			name:          "storage_fails_non_grpc",
			authenticator: createTestAuthorizer(ctx, t),
			storage:       &storage.MockStorage{ListExperimentsErr: fmt.Errorf("some other error")},
			wantErr:       status.ErrInternal,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			server := New(tc.storage, cfg, tc.authenticator, &mockXidClient{}, nil)
			_, err := server.ListExperiments(ctx, goodReq)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("ListExperiments() returned error %v, want %v", err, tc.wantErr)
			}
		})
	}
}
func TestListExperiments_DefaultToCursorPagination(t *testing.T) {
	ctx := context.Background()
	mockStorage := &storage.MockStorage{}

	testCases := []struct {
		name                      string
		defaultToCursorPagination bool
	}{
		{
			name:                      "DefaultToCursorTrue",
			defaultToCursorPagination: true,
		},
		{
			name:                      "DefaultToCursorFalse",
			defaultToCursorPagination: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{DefaultToCursorPagination: tc.defaultToCursorPagination}
			server := New(mockStorage, cfg, createTestAuthorizer(ctx, t), &mockXidClient{}, nil)
			_, err := server.ListExperiments(ctx, &apipb.ListExperimentsRequest{PageSize: 10})
			if err != nil {
				t.Fatalf("ListExperiments() returned an unexpected error: %v", err)
			}
			if mockStorage.GotDefaultToCursorPaginationExperiments != tc.defaultToCursorPagination {
				t.Errorf("ListExperiments() passed defaultToCursorPagination to storage: got %v, want %v", mockStorage.GotDefaultToCursorPaginationExperiments, tc.defaultToCursorPagination)
			}
		})
	}
}
