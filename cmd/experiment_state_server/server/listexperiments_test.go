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
	"sort"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/testing/protocmp"
	apipb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/auth"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/validator"
)

func TestListExperiments_HappyPath(t *testing.T) {
	ctx := context.Background()
	server, _ := createExperimentStateServer(ctx, t)

	var exps []*apipb.Experiment
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

	var exps []*apipb.Experiment
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

	// Verify multi-page pagination with default order_by ("") and no filter ("") (b/564895895).
	defaultPage1, err := server.ListExperiments(ctx, &apipb.ListExperimentsRequest{PageSize: 2})
	if err != nil {
		t.Fatalf("ListExperiments() page 1 with default order_by failed: %v", err)
	}
	if len(defaultPage1.GetExperiments()) != 2 || defaultPage1.GetNextPageToken() == "" {
		t.Fatalf("ListExperiments() page 1 with default order_by returned %d experiments and token %q, want 2 and non-empty token", len(defaultPage1.GetExperiments()), defaultPage1.GetNextPageToken())
	}
	defaultPage2, err := server.ListExperiments(ctx, &apipb.ListExperimentsRequest{PageSize: 2, PageToken: defaultPage1.GetNextPageToken()})
	if err != nil {
		t.Fatalf("ListExperiments() page 2 with default order_by failed: %v", err)
	}
	if len(defaultPage2.GetExperiments()) != 1 || defaultPage2.GetNextPageToken() != "" {
		t.Fatalf("ListExperiments() page 2 with default order_by returned %d experiments and token %q, want 1 and empty token", len(defaultPage2.GetExperiments()), defaultPage2.GetNextPageToken())
	}
}

func TestListExperiments_BadPaths(t *testing.T) {
	ctx := context.Background()
	server := newTestServer(t)

	testCases := []struct {
		name    string
		req     *apipb.ListExperimentsRequest
		wantErr error
	}{
		{
			name:    "invalid_page_size",
			req:     &apipb.ListExperimentsRequest{PageSize: -1},
			wantErr: grpcerr.ErrInvalidArgument,
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

	goodReq := &apipb.ListExperimentsRequest{PageSize: 10}

	testCases := []struct {
		name          string
		authenticator auth.AuthorizerInterface
		storage       storage.Storage
		wantErr       error
	}{
		{
			name:          "auth_fails",
			authenticator: &mockAuthenticator{err: grpcerr.ErrUnauthenticated},
			storage:       &storage.MockStorage{},
			wantErr:       grpcerr.ErrUnauthenticated,
		},
		{
			name:          "storage_fails_grpc_invalid_arg",
			authenticator: createTestAuthorizer(ctx, t),
			storage:       &storage.MockStorage{ListExperimentsErr: grpcerr.ErrInvalidArgument},
			wantErr:       grpcerr.ErrInvalidArgument,
		},
		{
			name:          "storage_fails_grpc_other",
			authenticator: createTestAuthorizer(ctx, t),
			storage:       &storage.MockStorage{ListExperimentsErr: grpcerr.ErrPermissionDenied},
			wantErr:       grpcerr.ErrInternal,
		},
		{
			name:          "storage_fails_non_grpc",
			authenticator: createTestAuthorizer(ctx, t),
			storage:       &storage.MockStorage{ListExperimentsErr: fmt.Errorf("some other error")},
			wantErr:       grpcerr.ErrInternal,
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
			server := newTestServer(t, opts...)
			_, err := server.ListExperiments(ctx, goodReq)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("ListExperiments() returned error %v, want %v", err, tc.wantErr)
			}
		})
	}
}
