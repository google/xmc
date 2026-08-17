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

	"golang/cmp/cmp"
	"golang/protobuf/v2/proto/proto"
	"golang/protobuf/v2/testing/protocmp/protocmp"
	"task/go/status"
	"xmanager_cloud/auth/auth"
	"xmanager_cloud/storage/storage"
	"xmanager_cloud/xid_service/config"
	apipb "xmanager_cloud/xid_service/proto/api_go_proto"
	msgpb "xmanager_cloud/xid_service/proto/messages_go_proto"
)

func TestGetXidEntry_HappyPath(t *testing.T) {
	ctx := context.Background()
	server := createXidServer(ctx, t)

	parentReq := &apipb.CreateXidEntryRequest{
		XidEntry: &msgpb.XidEntry{
			Acls: &msgpb.XidAcls{
				Owners: []*msgpb.XidAcls_Role{{Principal: &msgpb.XidAcls_Role_User{User: "test@example.com"}}},
			},
			Origin: &msgpb.OriginatingSystem{Endpoint: proto.String("http://testhost")},
		},
	}
	parentXID, err := server.CreateXidEntry(ctx, parentReq)
	if err != nil {
		t.Fatalf("CreateXidEntry() for parent returned an unexpected error: %v", err)
	}

	createXidEntryRequest := &apipb.CreateXidEntryRequest{
		XidEntry: &msgpb.XidEntry{
			Creator: proto.String("test@example.com"),
			Acls: &msgpb.XidAcls{
				Writers: []*msgpb.XidAcls_Role{{Principal: &msgpb.XidAcls_Role_User{User: "test@example.com"}}},
			},
			Origin:    &msgpb.OriginatingSystem{Endpoint: proto.String("http://testhost")},
			ParentXid: proto.String(parentXID.GetName()),
		},
	}

	createXidEntry, err := server.CreateXidEntry(ctx, createXidEntryRequest)
	if err != nil {
		t.Fatalf("CreateXidEntry() returned an unexpected error: %v", err)
	}

	gotXidEntry, err := server.GetXidEntry(ctx, &apipb.GetXidEntryRequest{Name: createXidEntry.Name})
	if err != nil {
		t.Fatalf("GetXidEntry() returned an unexpected error: %v", err)
	}

	// Assert successful response.
	opts := []cmp.Option{protocmp.Transform(), protocmp.IgnoreFields(&msgpb.XidEntry{}, "create_time", "update_time")}
	if diff := cmp.Diff(createXidEntry, gotXidEntry, opts...); diff != "" {
		t.Errorf("GetXid() mismatch (-want +got):\n%s", diff)
	}
}

func TestGetXidEntry_BadPaths(t *testing.T) {
	ctx := context.Background()
	server := createXidServer(ctx, t)

	testCases := []struct {
		name    string
		req     *apipb.GetXidEntryRequest
		wantErr error
	}{
		{
			name:    "invalid_name",
			req:     &apipb.GetXidEntryRequest{Name: proto.String("xids/invalid")},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name:    "non_numeric_name",
			req:     &apipb.GetXidEntryRequest{Name: proto.String("xids/123abc")},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name:    "not_found",
			req:     &apipb.GetXidEntryRequest{Name: proto.String("xids/999999999")},
			wantErr: status.ErrNotFound,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := server.GetXidEntry(ctx, tc.req)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("GetXidEntry(%v) returned error %v, want %v", tc.req, err, tc.wantErr)
			}
		})
	}
}

func TestGetXidEntry_Failures(t *testing.T) {
	testCases := []struct {
		name        string
		mockAuth    auth.AuthorizerInterface
		mockStorage storage.Storage
		wantErr     error
	}{
		{
			name:        "AuthFailure",
			mockAuth:    &auth.MockAuth{AuthnErr: status.ErrPermissionDenied},
			mockStorage: &storage.MockStorage{},
			wantErr:     status.ErrPermissionDenied,
		},
		{
			name:        "StorageFailure",
			mockAuth:    &auth.MockAuth{},
			mockStorage: &storage.MockStorage{FetchErr: errors.New("storage error")},
			wantErr:     status.ErrInternal,
		},
		{
			name:        "PermissionDenied",
			mockAuth:    &auth.MockAuth{IsAuthed: false},
			mockStorage: &storage.MockStorage{},
			wantErr:     status.ErrPermissionDenied,
		},
		{
			name:        "AuthorizationError",
			mockAuth:    &auth.MockAuth{IsAuthed: false, AuthzErr: errors.New("authz error")},
			mockStorage: &storage.MockStorage{},
			wantErr:     status.ErrPermissionDenied,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			server := NewXIDServer(config.Config{}, tc.mockAuth, tc.mockStorage)
			_, err := server.GetXidEntry(ctx, &apipb.GetXidEntryRequest{Name: proto.String("xids/1")})
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("GetXidEntry() returned error %v, want %v", err, tc.wantErr)
			}
		})
	}
}
