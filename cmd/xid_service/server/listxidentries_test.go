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
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
	"xmanager_cloud/auth/auth"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/storage/storage"
	apipb "xmanager_cloud/xid_service/proto/api_go_proto"
	msgpb "xmanager_cloud/xid_service/proto/messages_go_proto"
)

// The tables can be up to 15 seconds stale! So sometimes.... the tables don't exist. :(
// So we wait 15 seconds for the tables to be online and the data to be available.
// Obviously, this isn't optimal, but that's the function we wrote. So. It's accurate.
// I've added an epsilon to avoid flakiness based on system time fluctuations.
const (
	epsilon             = 10 * time.Millisecond
	expectedSearchDelay = 15*time.Second + epsilon
)

func createListTestData(ctx context.Context, t *testing.T, server *XIDServer) []*msgpb.XidEntry {
	t.Helper()
	endpoints := []string{"E1", "E2", "E1", "E2", "E1"}
	entries := make([]*msgpb.XidEntry, len(endpoints))
	for i, endpoint := range endpoints {
		req := &apipb.CreateXidEntryRequest{
			XidEntry: &msgpb.XidEntry{
				Origin: &msgpb.OriginatingSystem{
					Endpoint: proto.String(endpoint),
				},
				Acls: &msgpb.XidAcls{
					Owners:  []*msgpb.XidAcls_Role{{Principal: &msgpb.XidAcls_Role_User{User: "test@example.com"}}},
					Writers: []*msgpb.XidAcls_Role{{Principal: &msgpb.XidAcls_Role_User{User: "test@example.com"}}},
				},
			},
		}
		createdEntry, err := server.CreateXidEntry(ctx, req)
		if err != nil {
			t.Fatalf("CreateXidEntry failed: %v", err)
		}
		entries[i] = createdEntry
		// Ensure creation times are distinct for ordering.
		time.Sleep(10 * time.Millisecond)
	}
	return entries
}

func TestListXidEntries_HappyPath(t *testing.T) {
	ctx := context.Background()
	server := createXidServer(ctx, t)
	entries := createListTestData(ctx, t, server)

	// Test case: filter by endpoint=E1, order by created desc, page size 2
	req1 := &apipb.ListXidEntriesRequest{
		PageSize: proto.Int32(2),
		Filter:   proto.String(`endpoint="E1"`),
		OrderBy:  proto.String("created desc"),
	}
	resp1, err := server.ListXidEntries(ctx, req1)
	if err != nil {
		t.Fatalf("ListXidEntries page 1 failed: %v", err)
	}
	if resp1.GetNextPageToken() == "" {
		t.Errorf("ListXidEntries page 1 got empty next page token, want non-empty")
	}

	// The entries with E1 are entries[0], entries[2], entries[4].
	// They are created in order 0, 2, 4.
	// Order by created desc: 4, 2, 0.
	wantPage1 := []*msgpb.XidEntry{entries[4], entries[2]}
	if diff := cmp.Diff(wantPage1, resp1.GetXidEntries(), protocmp.Transform()); diff != "" {
		t.Errorf("ListXidEntries page 1 returned diff (-want +got):\n%s", diff)
	}

	// Test case: second page
	req2 := &apipb.ListXidEntriesRequest{
		PageSize:  proto.Int32(2),
		PageToken: proto.String(resp1.GetNextPageToken()),
		Filter:    proto.String(`endpoint="E1"`),
		OrderBy:   proto.String("created desc"),
	}
	resp2, err := server.ListXidEntries(ctx, req2)
	if err != nil {
		t.Fatalf("ListXidEntries page 2 failed: %v", err)
	}
	if resp2.GetNextPageToken() != "" {
		t.Errorf("ListXidEntries page 2 got next page token %q, want empty", resp2.GetNextPageToken())
	}
	wantPage2 := []*msgpb.XidEntry{entries[0]}
	if diff := cmp.Diff(wantPage2, resp2.GetXidEntries(), protocmp.Transform()); diff != "" {
		t.Errorf("ListXidEntries page 2 returned diff (-want +got):\n%s", diff)
	}
}

func TestListXidEntries_BadPaths(t *testing.T) {
	ctx := context.Background()
	server := createXidServer(ctx, t)

	testCases := []struct {
		name    string
		req     *apipb.ListXidEntriesRequest
		wantErr error
	}{
		{
			name:    "negative_page_size",
			req:     &apipb.ListXidEntriesRequest{PageSize: proto.Int32(-1)},
			wantErr: grpcerr.ErrInvalidArgument,
		},
		{
			name:    "invalid_filter",
			req:     &apipb.ListXidEntriesRequest{Filter: proto.String("invalid-filter syntax")},
			wantErr: grpcerr.ErrInvalidArgument,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := server.ListXidEntries(ctx, tc.req)
			if status.Code(err) != status.Code(tc.wantErr) {
				t.Errorf("ListXidEntries(%v) returned error %v, want code %v", tc.req, err, status.Code(tc.wantErr))
			}
		})
	}
}

func TestSearchXidEntries_HappyPath(t *testing.T) {
	ctx := context.Background()
	server := createXidServer(ctx, t)
	entries := createListTestData(ctx, t, server)

	time.Sleep(expectedSearchDelay)

	// Test case: filter by endpoint=E2, order by created asc, page size 1
	req1 := &apipb.ListXidEntriesRequest{
		PageSize: proto.Int32(1),
		Filter:   proto.String(`endpoint="E2"`),
		OrderBy:  proto.String("created asc"),
	}
	resp1, err := server.SearchXidEntries(ctx, req1)
	if err != nil {
		t.Fatalf("SearchXidEntries page 1 failed: %v", err)
	}
	if resp1.GetNextPageToken() == "" {
		t.Errorf("SearchXidEntries page 1 got empty next page token, want non-empty")
	}

	// The entries with E2 are entries[1], entries[3].
	// They are created in order 1, 3.
	// Order by created asc: 1, 3.
	wantPage1 := []*msgpb.XidEntry{entries[1]}
	if diff := cmp.Diff(wantPage1, resp1.GetXidEntries(), protocmp.Transform()); diff != "" {
		t.Errorf("SearchXidEntries page 1 returned diff (-want +got):\n%s", diff)
	}

	// Test case: second page
	req2 := &apipb.ListXidEntriesRequest{
		PageSize:  proto.Int32(1),
		PageToken: proto.String(resp1.GetNextPageToken()),
		Filter:    proto.String(`endpoint="E2"`),
		OrderBy:   proto.String("created asc"),
	}
	resp2, err := server.SearchXidEntries(ctx, req2)
	if err != nil {
		t.Fatalf("SearchXidEntries page 2 failed: %v", err)
	}
	if resp2.GetNextPageToken() != "" {
		t.Errorf("SearchXidEntries page 2 got next page token %q, want empty", resp2.GetNextPageToken())
	}
	wantPage2 := []*msgpb.XidEntry{entries[3]}
	if diff := cmp.Diff(wantPage2, resp2.GetXidEntries(), protocmp.Transform()); diff != "" {
		t.Errorf("SearchXidEntries page 2 returned diff (-want +got):\n%s", diff)
	}
}

func TestSearchXidEntries_BadPaths(t *testing.T) {
	ctx := context.Background()
	server := createXidServer(ctx, t)

	testCases := []struct {
		name    string
		req     *apipb.ListXidEntriesRequest
		wantErr error
	}{
		{
			name:    "negative_page_size",
			req:     &apipb.ListXidEntriesRequest{PageSize: proto.Int32(-1)},
			wantErr: grpcerr.ErrInvalidArgument,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := server.SearchXidEntries(ctx, tc.req)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("SearchXidEntries(%v) returned error %v, want %v", tc.req, err, tc.wantErr)
			}
		})
	}
}

func TestListXidEntries_Failures(t *testing.T) {
	testCases := []struct {
		name        string
		mockAuth    auth.AuthorizerInterface
		mockStorage storage.Storage
		wantErr     error
	}{
		{
			name:        "AuthFailure",
			mockAuth:    &auth.MockAuth{AuthnErr: grpcerr.ErrPermissionDenied},
			mockStorage: &storage.MockStorage{},
			wantErr:     grpcerr.ErrPermissionDenied,
		},
		{
			name:        "StorageGrpcFailure",
			mockAuth:    &auth.MockAuth{},
			mockStorage: &storage.MockStorage{ListErr: grpcerr.ErrAborted},
			wantErr:     grpcerr.ErrAborted,
		},
		{
			name:        "StorageNonGrpcFailure",
			mockAuth:    &auth.MockAuth{},
			mockStorage: &storage.MockStorage{ListErr: errors.New("non-grpc error")},
			wantErr:     grpcerr.ErrInternal,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			server := newTestServer(t, withAuthorizer(tc.mockAuth), withStorage(tc.mockStorage))
			_, err := server.ListXidEntries(ctx, &apipb.ListXidEntriesRequest{})
			if status.Code(err) != status.Code(tc.wantErr) {
				t.Errorf("ListXidEntries() returned error %v, want code %v", err, status.Code(tc.wantErr))
			}
		})
	}
}

func TestSearchXidEntries_AuthFailure(t *testing.T) {
	ctx := context.Background()
	server := newTestServer(t, withAuthorizer(&auth.MockAuth{AuthnErr: grpcerr.ErrPermissionDenied}), withStorage(&storage.MockStorage{}))
	_, err := server.SearchXidEntries(ctx, &apipb.ListXidEntriesRequest{})
	if !errors.Is(err, grpcerr.ErrPermissionDenied) {
		t.Errorf("SearchXidEntries() returned error %v, want %v", err, grpcerr.ErrPermissionDenied)
	}
}
