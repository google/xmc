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

	"golang/cmp/cmp"
	"golang/protobuf/v2/testing/protocmp/protocmp"
	"task/go/status"

	"xmanager_cloud/auth/auth"
	"xmanager_cloud/experiment_state_server/config"
	apipb "xmanager_cloud/experiment_state_server/proto/api_go_proto"
	msgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/storage/storage"
	"xmanager_cloud/validator/validator"
)

func TestListArtifacts_HappyPath(t *testing.T) {
	ctx := t.Context()
	server, _ := createExperimentStateServer(ctx, t)
	exp := mustCreateExperiment(ctx, t, server, nil)

	var artifacts []*msgpb.Artifact
	for i := 0; i < 3; i++ {
		artifacts = append(artifacts, mustCreateArtifact(ctx, t, server, exp.GetName(), nil))
	}
	sort.Slice(artifacts, func(i, j int) bool {
		iAID, _ := validator.ValidateArtifactName(artifacts[i].GetName())
		jAID, _ := validator.ValidateArtifactName(artifacts[j].GetName())
		return iAID < jAID
	})

	// List first page.
	req := &apipb.ListArtifactsRequest{PageSize: 2, OrderBy: "artifact_id"}
	resp1, err := server.ListArtifacts(ctx, req)
	if err != nil {
		t.Fatalf("ListArtifacts() returned an unexpected error: %v", err)
	}
	if len(resp1.GetArtifacts()) != 2 {
		t.Errorf("ListArtifacts() returned %d artifacts, want 2", len(resp1.GetArtifacts()))
	}
	if resp1.GetNextPageToken() == "" {
		t.Errorf("ListArtifacts(%v) returned next_page_token %q, want non-empty", req, resp1.GetNextPageToken())
	}

	// List second page.
	req = &apipb.ListArtifactsRequest{PageSize: 2, PageToken: resp1.GetNextPageToken(), OrderBy: "artifact_id"}
	resp2, err := server.ListArtifacts(ctx, req)
	if err != nil {
		t.Fatalf("ListArtifacts() returned an unexpected error: %v", err)
	}
	if len(resp2.GetArtifacts()) != 1 {
		t.Errorf("ListArtifacts() returned %d artifacts, want 1", len(resp2.GetArtifacts()))
	}
	if resp2.GetNextPageToken() != "" {
		t.Errorf("ListArtifacts(%v) returned next_page_token %q, want empty", req, resp2.GetNextPageToken())
	}

	cmpOpts := []cmp.Option{
		protocmp.Transform(),
	}

	// Check all artifacts are returned in order.
	if diff := cmp.Diff(artifacts[0], resp1.GetArtifacts()[0], cmpOpts...); diff != "" {
		t.Errorf("ListArtifacts() page 1 artifact 1 returned unexpected diff (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(artifacts[1], resp1.GetArtifacts()[1], cmpOpts...); diff != "" {
		t.Errorf("ListArtifacts() page 1 artifact 2 returned unexpected diff (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(artifacts[2], resp2.GetArtifacts()[0], cmpOpts...); diff != "" {
		t.Errorf("ListArtifacts() page 2 artifact 1 returned unexpected diff (-want +got):\n%s", diff)
	}
}

func TestSearchArtifacts_HappyPath(t *testing.T) {
	ctx := context.Background()
	server, _ := createExperimentStateServer(ctx, t)
	exp := mustCreateExperiment(ctx, t, server, nil)

	time.Sleep(15*time.Second + 10*time.Millisecond)

	var artifacts []*msgpb.Artifact
	for i := 0; i < 3; i++ {
		artifacts = append(artifacts, mustCreateArtifact(ctx, t, server, exp.GetName(), nil))
	}

	time.Sleep(15*time.Second + 10*time.Millisecond)

	sort.Slice(artifacts, func(i, j int) bool {
		iAID, _ := validator.ValidateArtifactName(artifacts[i].GetName())
		jAID, _ := validator.ValidateArtifactName(artifacts[j].GetName())
		return iAID < jAID
	})

	// List first page.
	resp1, err := server.SearchArtifacts(ctx, &apipb.ListArtifactsRequest{PageSize: 2, OrderBy: "artifact_id"})
	if err != nil {
		t.Fatalf("SearchArtifacts() returned an unexpected error: %v", err)
	}
	if len(resp1.GetArtifacts()) != 2 {
		t.Errorf("SearchArtifacts() returned %d artifacts, want 2", len(resp1.GetArtifacts()))
	}
	if resp1.GetNextPageToken() == "" {
		t.Errorf("SearchArtifacts() returned next_page_token %q, want non-empty", resp1.GetNextPageToken())
	}

	// List second page.
	resp2, err := server.SearchArtifacts(ctx, &apipb.ListArtifactsRequest{PageSize: 2, PageToken: resp1.GetNextPageToken(), OrderBy: "artifact_id"})
	if err != nil {
		t.Fatalf("SearchArtifacts() returned an unexpected error: %v", err)
	}
	if len(resp2.GetArtifacts()) != 1 {
		t.Errorf("SearchArtifacts() returned %d artifacts, want 1", len(resp2.GetArtifacts()))
	}
	if resp2.GetNextPageToken() != "" {
		t.Errorf("SearchArtifacts() returned next_page_token %q, want empty", resp2.GetNextPageToken())
	}

	cmpOpts := []cmp.Option{
		protocmp.Transform(),
	}

	// Check all artifacts are returned in order.
	if diff := cmp.Diff(artifacts[0], resp1.GetArtifacts()[0], cmpOpts...); diff != "" {
		t.Errorf("SearchArtifacts() page 1 artifact 1 returned unexpected diff (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(artifacts[1], resp1.GetArtifacts()[1], cmpOpts...); diff != "" {
		t.Errorf("SearchArtifacts() page 1 artifact 2 returned unexpected diff (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(artifacts[2], resp2.GetArtifacts()[0], cmpOpts...); diff != "" {
		t.Errorf("SearchArtifacts() page 2 artifact 1 returned unexpected diff (-want +got):\n%s", diff)
	}
}

func TestListArtifacts_BadPaths(t *testing.T) {
	ctx := context.Background()
	server, _ := createExperimentStateServer(ctx, t)

	testCases := []struct {
		name    string
		req     *apipb.ListArtifactsRequest
		wantErr error
	}{
		{
			name:    "invalid_page_size",
			req:     &apipb.ListArtifactsRequest{PageSize: -1},
			wantErr: status.ErrInvalidArgument,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := server.ListArtifacts(ctx, tc.req)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("ListArtifacts() returned error %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestListArtifacts_DependencyFailures(t *testing.T) {
	ctx := context.Background()
	cfg := &config.Config{}

	goodReq := &apipb.ListArtifactsRequest{PageSize: 10}

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
			storage:       &storage.MockStorage{ListArtifactsErr: status.ErrInvalidArgument},
			wantErr:       status.ErrInvalidArgument,
		},
		{
			name:          "storage_fails_grpc_other",
			authenticator: createTestAuthorizer(ctx, t),
			storage:       &storage.MockStorage{ListArtifactsErr: status.ErrPermissionDenied},
			wantErr:       status.ErrInternal,
		},
		{
			name:          "storage_fails_non_grpc",
			authenticator: createTestAuthorizer(ctx, t),
			storage:       &storage.MockStorage{ListArtifactsErr: fmt.Errorf("some other error")},
			wantErr:       status.ErrInternal,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			server := New(tc.storage, cfg, tc.authenticator, &mockXidClient{}, nil, nil)
			_, err := server.ListArtifacts(ctx, goodReq)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("ListArtifacts() returned error %v, want %v", err, tc.wantErr)
			}
		})
	}
}
