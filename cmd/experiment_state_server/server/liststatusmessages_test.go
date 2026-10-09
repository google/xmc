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
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
	apipb "xmanager_cloud/experiment_state_server/proto/api_go_proto"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/storage/storage"
	xidapipb "xmanager_cloud/xid_service/proto/api_go_proto"
)

func TestListStatusMessages(t *testing.T) {
	ctx := t.Context()

	wantMessages := mockStatusMessages()

	mockStorage := &storage.MockStorage{
		ListStatusMessagesResp: wantMessages,
	}

	mockXid := &mockXidClient{
		checkUserAuthorizationResp: &xidapipb.CheckUserAuthorizationResponse{
			Authorized: proto.Bool(true),
		},
	}

	server := newTestServer(t,
		withStorage(mockStorage),
		withXidClient(mockXid),
	)

	req := &apipb.ListStatusMessagesRequest{
		Parent: "experiments/123",
	}

	resp, err := server.ListStatusMessages(ctx, req)
	if err != nil {
		t.Fatalf("ListStatusMessages(%v) returned an unexpected error: %v", req, err)
	}

	want := &apipb.ListStatusMessagesResponse{
		StatusMessages: mockStatusMessages(),
	}
	if diff := cmp.Diff(want, resp, protocmp.Transform()); diff != "" {
		t.Errorf("ListStatusMessages(%v) returned unexpected response diff (-want +got):\n%s", req, diff)
	}
}

func TestListStatusMessages_InvalidRequest(t *testing.T) {
	ctx := t.Context()
	server, _ := createExperimentStateServer(ctx, t)

	req := &apipb.ListStatusMessagesRequest{
		Parent:   "experiments/123",
		PageSize: -1, // Invalid page size
	}

	_, err := server.ListStatusMessages(ctx, req)
	if !errors.Is(err, grpcerr.ErrInvalidArgument) {
		t.Errorf("ListStatusMessages(%v) returned error %v, want %v", req, err, grpcerr.ErrInvalidArgument)
	}
}

func TestListStatusMessages_Unauthenticated(t *testing.T) {
	ctx := t.Context()
	server := newTestServer(t, withAuthorizer(&mockAuthenticator{err: grpcerr.ErrUnauthenticated}))

	req := &apipb.ListStatusMessagesRequest{
		Parent: "experiments/123",
	}

	_, err := server.ListStatusMessages(ctx, req)
	if err == nil {
		t.Fatalf("ListStatusMessages(%v) expected an error but got nil", req)
	}
}

func TestListStatusMessages_PermissionDenied(t *testing.T) {
	ctx := t.Context()

	// Mock XidClient to deny access.
	mockXid := &mockXidClient{
		checkUserAuthorizationResp: &xidapipb.CheckUserAuthorizationResponse{
			Authorized: proto.Bool(false),
		},
	}
	server := newTestServer(t, withXidClient(mockXid))

	req := &apipb.ListStatusMessagesRequest{
		Parent: "experiments/123",
	}

	_, err := server.ListStatusMessages(ctx, req)
	if !errors.Is(err, grpcerr.ErrPermissionDenied) {
		t.Errorf("ListStatusMessages(%v) returned error %v, want %v", req, err, grpcerr.ErrPermissionDenied)
	}
}

func TestSearchStatusMessages(t *testing.T) {
	ctx := t.Context()

	wantMessages := mockStatusMessages()

	mockStorage := &storage.MockStorage{
		ListStatusMessagesResp: wantMessages,
	}

	mockXid := &mockXidClient{
		checkUserAuthorizationResp: &xidapipb.CheckUserAuthorizationResponse{
			Authorized: proto.Bool(true),
		},
	}

	server := newTestServer(t,
		withStorage(mockStorage),
		withXidClient(mockXid),
	)

	req := &apipb.ListStatusMessagesRequest{
		Parent: "experiments/123",
	}

	resp, err := server.SearchStatusMessages(ctx, req)
	if err != nil {
		t.Fatalf("SearchStatusMessages(%v) returned an unexpected error: %v", req, err)
	}

	want := &apipb.ListStatusMessagesResponse{
		StatusMessages: mockStatusMessages(),
	}
	if diff := cmp.Diff(want, resp, protocmp.Transform()); diff != "" {
		t.Errorf("SearchStatusMessages(%v) returned unexpected response diff (-want +got):\n%s", req, diff)
	}
}

func mockStatusMessages() []*essmsgpb.StatusMessage {
	return []*essmsgpb.StatusMessage{
		{
			Name: "experiments/123/statusMessages/1",
			Content: &essmsgpb.StructuredMessage{
				UserFacingText: "Dummy status message 1",
				Severity:       essmsgpb.StructuredMessage_INFO,
				Source:         essmsgpb.StructuredMessage_XMANAGER_BACKEND,
			},
		},
		{
			Name: "experiments/123/statusMessages/2",
			Content: &essmsgpb.StructuredMessage{
				UserFacingText: "Dummy status message 2",
				Severity:       essmsgpb.StructuredMessage_WARNING,
				Source:         essmsgpb.StructuredMessage_APPLICATION,
			},
		},
	}
}
