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
	"xmanager_cloud/validator/validator"
	"xmanager_cloud/xid_service/config"
	apipb "xmanager_cloud/xid_service/proto/api_go_proto"
	msgpb "xmanager_cloud/xid_service/proto/messages_go_proto"
)

func TestCreateXidEntry_HappyPath(t *testing.T) {
	ctx := context.Background()
	server := createXidServer(ctx, t)

	parentReq := &apipb.CreateXidEntryRequest{
		XidEntry: &msgpb.XidEntry{
			Acls: &msgpb.XidAcls{
				Owners: []*msgpb.XidAcls_Role{{Principal: &msgpb.XidAcls_Role_User{User: "test@example.com"}}},
			},
			Origin: &msgpb.OriginatingSystem{
				Endpoint: proto.String("http://testhost"),
			},
		},
	}
	parentXID, err := server.CreateXidEntry(ctx, parentReq)
	if err != nil {
		t.Fatalf("CreateXidEntry() for parent returned an unexpected error: %v", err)
	}

	req := &apipb.CreateXidEntryRequest{
		XidEntry: &msgpb.XidEntry{
			Acls: &msgpb.XidAcls{
				Writers: []*msgpb.XidAcls_Role{{Principal: &msgpb.XidAcls_Role_User{User: "test@example.com"}}},
			},
			Origin: &msgpb.OriginatingSystem{
				Endpoint: proto.String("http://testhost"),
			},
			ParentXid: proto.String(parentXID.GetName()),
		},
	}

	got, err := server.CreateXidEntry(ctx, req)
	if err != nil {
		t.Fatalf("CreateXidEntry() returned an unexpected error: %v", err)
	}

	if _, err := validator.ValidateXIDName(got.GetName()); err != nil {
		t.Errorf("CreateXidEntry() returned an invalid XID name: %v", err)
	}
	if got.GetCreateTime() == nil || got.GetUpdateTime() == nil {
		t.Errorf("CreateXidEntry() returned nil timestamps")
	}

	want := &msgpb.XidEntry{
		Creator: proto.String("test@example.com"),
		Acls: &msgpb.XidAcls{
			Writers: []*msgpb.XidAcls_Role{{Principal: &msgpb.XidAcls_Role_User{User: "test@example.com"}}},
		},
		Origin: &msgpb.OriginatingSystem{
			Endpoint: proto.String("http://testhost"),
		},
		ParentXid: proto.String(parentXID.GetName()),
	}
	opts := []cmp.Option{
		protocmp.Transform(),
		protocmp.IgnoreFields(&msgpb.XidEntry{}, "create_time", "update_time", "name"),
	}
	if diff := cmp.Diff(want, got, opts...); diff != "" {
		t.Errorf("CreateXidEntry() mismatch (-want +got):\n%s", diff)
	}
}

func TestCreateXidEntry_BadPaths(t *testing.T) {
	ctx := context.Background()
	server := createXidServer(ctx, t)

	baseReq := &apipb.CreateXidEntryRequest{
		XidEntry: &msgpb.XidEntry{
			Acls: &msgpb.XidAcls{
				Writers: []*msgpb.XidAcls_Role{{Principal: &msgpb.XidAcls_Role_User{User: "test@example.com"}}},
			},
			Origin: &msgpb.OriginatingSystem{
				Endpoint: proto.String("http://testhost"),
			},
		},
	}

	testCases := []struct {
		name    string
		req     *apipb.CreateXidEntryRequest
		wantErr error
	}{
		{
			name: "missing_xid_entry",
			req: modifyProto(baseReq, func(r *apipb.CreateXidEntryRequest) {
				r.XidEntry = nil
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "missing_acls",
			req: modifyProto(baseReq, func(r *apipb.CreateXidEntryRequest) {
				r.XidEntry.Acls = nil
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "missing_origin",
			req: modifyProto(baseReq, func(r *apipb.CreateXidEntryRequest) {
				r.XidEntry.Origin = nil
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "invalid_parent_xid",
			req: modifyProto(baseReq, func(r *apipb.CreateXidEntryRequest) {
				r.XidEntry.ParentXid = proto.String("xids/invalid")
			}),
			wantErr: status.ErrInvalidArgument,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := server.CreateXidEntry(ctx, tc.req)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("CreateXidEntry() returned error %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestCreateXidEntry_Failures(t *testing.T) {
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
			mockStorage: &storage.MockStorage{CreateErr: errors.New("storage error")},
			wantErr:     status.ErrInternal,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			server := NewXIDServer(config.Config{}, tc.mockAuth, tc.mockStorage)
			_, err := server.CreateXidEntry(ctx, &apipb.CreateXidEntryRequest{
				XidEntry: &msgpb.XidEntry{
					Acls:   &msgpb.XidAcls{Owners: []*msgpb.XidAcls_Role{{Principal: &msgpb.XidAcls_Role_User{User: "test@example.com"}}}},
					Origin: &msgpb.OriginatingSystem{Endpoint: proto.String("http://testhost")},
				},
			})
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("CreateXidEntry() returned error %v, want %v", err, tc.wantErr)
			}
		})
	}
}
