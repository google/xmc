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

	fmpb "protobuf/field_mask_go_proto"
	"xmanager_cloud/auth/auth"
	"xmanager_cloud/storage/storage"
	"xmanager_cloud/xid_service/config"
	apipb "xmanager_cloud/xid_service/proto/api_go_proto"
	msgpb "xmanager_cloud/xid_service/proto/messages_go_proto"
)

func TestUpdateXidEntry_HappyPath(t *testing.T) {
	ctx := context.Background()
	server := createXidServer(ctx, t)
	// Create an XID entry to update.
	createReq := &apipb.CreateXidEntryRequest{
		XidEntry: &msgpb.XidEntry{
			Origin: &msgpb.OriginatingSystem{Endpoint: proto.String("http://testhost")},
			Acls: &msgpb.XidAcls{
				Owners: []*msgpb.XidAcls_Role{{Principal: &msgpb.XidAcls_Role_User{User: "test@example.com"}}},
			},
		},
	}
	createdEntry, err := server.CreateXidEntry(ctx, createReq)
	if err != nil {
		t.Fatalf("CreateXidEntry() returned an unexpected error: %v", err)
	}

	// Update ACLs.
	updateReq := &apipb.UpdateXidEntryRequest{
		XidEntry: &msgpb.XidEntry{
			Name: createdEntry.Name,
			Acls: &msgpb.XidAcls{
				Owners:  []*msgpb.XidAcls_Role{{Principal: &msgpb.XidAcls_Role_User{User: "owner@example.com"}}},
				Readers: []*msgpb.XidAcls_Role{{Principal: &msgpb.XidAcls_Role_User{User: "test@example.com"}}},
			},
		},
		UpdateMask: &fmpb.FieldMask{Paths: []string{"acls"}},
	}
	updatedEntry, err := server.UpdateXidEntry(ctx, updateReq)
	if err != nil {
		t.Fatalf("UpdateXidEntry() returned an unexpected error updating ACLs: %v", err)
	}

	// Check if ACLs are updated and update time is newer.
	if diff := cmp.Diff(updateReq.XidEntry.Acls, updatedEntry.GetAcls(), protocmp.Transform()); diff != "" {
		t.Errorf("UpdateXidEntry() returned diff in Acls (-want +got):\n%s", diff)
	}
	if !updatedEntry.GetUpdateTime().AsTime().After(createdEntry.GetUpdateTime().AsTime()) {
		t.Errorf("UpdateXidEntry() did not update UpdateTime: got %v, want > %v", updatedEntry.GetUpdateTime(), createdEntry.GetUpdateTime())
	}

	// Verify with GetXidEntry.
	gotEntry, err := server.GetXidEntry(ctx, &apipb.GetXidEntryRequest{Name: createdEntry.Name})
	if err != nil {
		t.Fatalf("GetXidEntry() returned an unexpected error: %v", err)
	}
	if diff := cmp.Diff(updatedEntry, gotEntry, protocmp.Transform()); diff != "" {
		t.Errorf("GetXidEntry() returned diff after update (-want +got):\n%s", diff)
	}
}

func TestUpdateXidEntry_BadPaths(t *testing.T) {
	ctx := context.Background()
	server := createXidServer(ctx, t)

	createReq := &apipb.CreateXidEntryRequest{
		XidEntry: &msgpb.XidEntry{
			Origin: &msgpb.OriginatingSystem{Endpoint: proto.String("http://testhost")},
			Acls: &msgpb.XidAcls{
				Owners: []*msgpb.XidAcls_Role{{Principal: &msgpb.XidAcls_Role_User{User: "test@example.com"}}},
			},
		},
	}
	createdEntry, err := server.CreateXidEntry(ctx, createReq)
	if err != nil {
		t.Fatalf("CreateXidEntry() returned an unexpected error: %v", err)
	}

	baseReq := &apipb.UpdateXidEntryRequest{
		XidEntry: &msgpb.XidEntry{
			Name: createdEntry.Name,
			Acls: &msgpb.XidAcls{},
		},
		UpdateMask: &fmpb.FieldMask{Paths: []string{"acls"}},
	}

	testCases := []struct {
		name    string
		req     *apipb.UpdateXidEntryRequest
		wantErr error
	}{
		{
			name: "invalid_name",
			req: proto.Modify(baseReq, func(r *apipb.UpdateXidEntryRequest) {
				r.XidEntry.Name = proto.String("xids/invalid")
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "not_found",
			req: proto.Modify(baseReq, func(r *apipb.UpdateXidEntryRequest) {
				r.XidEntry.Name = proto.String("xids/999999")
			}),
			wantErr: status.ErrNotFound,
		},
		{
			name: "immutable_field",
			req: proto.Modify(baseReq, func(r *apipb.UpdateXidEntryRequest) {
				r.XidEntry.Creator = proto.String("new-creator")
				r.UpdateMask.Paths = []string{"creator"}
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "no_acls",
			req: proto.Modify(baseReq, func(r *apipb.UpdateXidEntryRequest) {
				r.XidEntry.Acls = nil
			}),
			wantErr: status.ErrInvalidArgument,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := server.UpdateXidEntry(ctx, tc.req)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("UpdateXidEntry(%v) returned error %v, want %v", tc.req, err, tc.wantErr)
			}
		})
	}
}

func TestUpdateXidEntry_Failures(t *testing.T) {
	testCases := []struct {
		name        string
		mockAuth    auth.AuthorizerInterface
		mockStorage storage.Storage
		wantErr     error
	}{
		{
			name:        "AuthFailure",
			mockAuth:    &auth.MockAuth{AuthnErr: status.ErrUnauthenticated},
			mockStorage: &storage.MockStorage{},
			wantErr:     status.ErrUnauthenticated,
		},
		{
			name:        "FetchFailure",
			mockAuth:    &auth.MockAuth{},
			mockStorage: &storage.MockStorage{FetchErr: errors.New("fetch error")},
			wantErr:     status.ErrInternal,
		},
		{
			name:        "StorageFailure",
			mockAuth:    &auth.MockAuth{IsAuthed: true},
			mockStorage: &storage.MockStorage{UpdateErr: errors.New("storage error")},
			wantErr:     status.ErrInternal,
		},
		{
			name:        "PermissionDenied",
			mockAuth:    &auth.MockAuth{IsAuthed: false},
			mockStorage: &storage.MockStorage{},
			wantErr:     status.ErrPermissionDenied,
		},
		{
			name:        "PermissionDeniedDuringStorage",
			mockAuth:    &auth.MockAuth{IsAuthed: true},
			mockStorage: &storage.MockStorage{UpdateErr: status.ErrPermissionDenied},
			wantErr:     status.ErrInternal,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			server := NewXIDServer(config.Config{}, tc.mockAuth, tc.mockStorage)
			_, err := server.UpdateXidEntry(ctx, &apipb.UpdateXidEntryRequest{
				XidEntry: &msgpb.XidEntry{Name: proto.String("xids/1")},
			})
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("UpdateXidEntry() returned error %v, want %v", err, tc.wantErr)
			}
		})
	}
}
