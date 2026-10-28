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

	"google.golang.org/protobuf/proto"
	msgpb "xmanager_cloud/api/protos/xid"
	"xmanager_cloud/internal/auth"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/storage"
)

func TestCheckUserAuthorization_HappyPath(t *testing.T) {
	ctx := context.Background()
	server := createXidServer(ctx, t)

	// In mock auth, the user is "test@example.com".
	// Create an entry where user is only a reader.
	readerEntryReq := &msgpb.CreateXidEntryRequest{
		XidEntry: &msgpb.XidEntry{
			Origin: &msgpb.OriginatingSystem{Endpoint: proto.String("http://testhost")},
			Acls: &msgpb.XidAcls{
				Readers: []*msgpb.XidAcls_Role{{Principal: &msgpb.XidAcls_Role_User{User: "test@example.com"}}},
				Writers: []*msgpb.XidAcls_Role{{Principal: &msgpb.XidAcls_Role_User{User: "other@example.com"}}},
				Owners:  []*msgpb.XidAcls_Role{{Principal: &msgpb.XidAcls_Role_User{User: "other@example.com"}}},
			},
		},
	}
	readerEntry, err := server.CreateXidEntry(ctx, readerEntryReq)
	if err != nil {
		t.Fatalf("CreateXidEntry() returned an unexpected error: %v", err)
	}

	// Create an entry where user is an owner.
	ownerEntryReq := &msgpb.CreateXidEntryRequest{
		XidEntry: &msgpb.XidEntry{
			Origin: &msgpb.OriginatingSystem{Endpoint: proto.String("http://testhost")},
			Acls: &msgpb.XidAcls{
				Owners: []*msgpb.XidAcls_Role{{Principal: &msgpb.XidAcls_Role_User{User: "test@example.com"}}},
			},
		},
	}
	ownerEntry, err := server.CreateXidEntry(ctx, ownerEntryReq)
	if err != nil {
		t.Fatalf("CreateXidEntry() returned an unexpected error: %v", err)
	}

	// Create an entry with no parent where user is an owner.
	noParentOwnerEntryReq := &msgpb.CreateXidEntryRequest{
		XidEntry: &msgpb.XidEntry{
			Origin: &msgpb.OriginatingSystem{Endpoint: proto.String("http://testhost")},
			Acls: &msgpb.XidAcls{
				Owners: []*msgpb.XidAcls_Role{{Principal: &msgpb.XidAcls_Role_User{User: "test@example.com"}}},
			},
		},
	}
	noParentOwnerEntry, err := server.CreateXidEntry(ctx, noParentOwnerEntryReq)
	if err != nil {
		t.Fatalf("CreateXidEntry() returned an unexpected error: %v", err)
	}

	// Create a parent entry where user is an owner.
	parentOwnerEntryReq := &msgpb.CreateXidEntryRequest{
		XidEntry: &msgpb.XidEntry{
			Origin: &msgpb.OriginatingSystem{Endpoint: proto.String("http://testhost")},
			Acls: &msgpb.XidAcls{
				Owners: []*msgpb.XidAcls_Role{{Principal: &msgpb.XidAcls_Role_User{User: "test@example.com"}}},
			},
		},
	}
	parentOwnerEntry, err := server.CreateXidEntry(ctx, parentOwnerEntryReq)
	if err != nil {
		t.Fatalf("CreateXidEntry() returned an unexpected error: %v", err)
	}

	// Create a child entry where "other@example.com" is owner, but "test@example.com" has no
	// permissions, and set parent_xid to parentOwnerEntry's name.
	childEntryReq := &msgpb.CreateXidEntryRequest{
		XidEntry: &msgpb.XidEntry{
			Origin:    &msgpb.OriginatingSystem{Endpoint: proto.String("http://testhost")},
			ParentXid: proto.String(parentOwnerEntry.GetName()),
			Acls: &msgpb.XidAcls{
				Owners: []*msgpb.XidAcls_Role{{Principal: &msgpb.XidAcls_Role_User{User: "other@example.com"}}},
			},
		},
	}
	childEntry, err := server.CreateXidEntry(ctx, childEntryReq)
	if err != nil {
		t.Fatalf("CreateXidEntry() returned an unexpected error: %v", err)
	}

	// Create a parent entry where "other@example.com" is owner.
	otherOwnerParentEntryReq := &msgpb.CreateXidEntryRequest{
		XidEntry: &msgpb.XidEntry{
			Origin: &msgpb.OriginatingSystem{Endpoint: proto.String("http://testhost")},
			Acls: &msgpb.XidAcls{
				Owners: []*msgpb.XidAcls_Role{{Principal: &msgpb.XidAcls_Role_User{User: "other@example.com"}}},
			},
		},
	}
	otherOwnerParentEntry, err := server.CreateXidEntry(ctx, otherOwnerParentEntryReq)
	if err != nil {
		t.Fatalf("CreateXidEntry() returned an unexpected error: %v", err)
	}

	// Create a child entry where "other@example.com" is owner, and set parent_xid to
	// otherOwnerParentEntry's name.
	otherOwnerChildEntryReq := &msgpb.CreateXidEntryRequest{
		XidEntry: &msgpb.XidEntry{
			Origin:    &msgpb.OriginatingSystem{Endpoint: proto.String("http://testhost")},
			ParentXid: proto.String(otherOwnerParentEntry.GetName()),
			Acls: &msgpb.XidAcls{
				Owners: []*msgpb.XidAcls_Role{{Principal: &msgpb.XidAcls_Role_User{User: "other@example.com"}}},
			},
		},
	}
	otherOwnerChildEntry, err := server.CreateXidEntry(ctx, otherOwnerChildEntryReq)
	if err != nil {
		t.Fatalf("CreateXidEntry() returned an unexpected error: %v", err)
	}

	// Seed a legacy entry, stored before principals were normalized on write, whose owner ACL
	// spells the user's email with different casing. It bypasses CreateXidEntry to keep read-path
	// coverage of unnormalized stored ACLs.
	mixedCaseOwnerEntry, err := server.storage.CreateXidEntry(ctx, &msgpb.XidEntry{
		Creator: proto.String("Test@Example.com"),
		Origin:  &msgpb.OriginatingSystem{Endpoint: proto.String("http://testhost")},
		Acls: &msgpb.XidAcls{
			Owners: []*msgpb.XidAcls_Role{{Principal: &msgpb.XidAcls_Role_User{User: "Test@Example.com"}}},
		},
	})
	if err != nil {
		t.Fatalf("storage.CreateXidEntry() returned an unexpected error: %v", err)
	}

	testCases := []struct {
		name           string
		req            *msgpb.CheckUserAuthorizationRequest
		wantAuthorized bool
	}{
		{
			name: "reader_can_read_reader_xid",
			req: &msgpb.CheckUserAuthorizationRequest{
				Name:     proto.String(readerEntry.GetName()),
				RoleType: msgpb.XidAcls_READER.Enum(),
			},
			wantAuthorized: true,
		},
		{
			name: "reader_cannot_write_reader_xid",
			req: &msgpb.CheckUserAuthorizationRequest{
				Name:     proto.String(readerEntry.GetName()),
				RoleType: msgpb.XidAcls_WRITER.Enum(),
			},
			wantAuthorized: false,
		},
		{
			name: "reader_cannot_own_reader_xid",
			req: &msgpb.CheckUserAuthorizationRequest{
				Name:     proto.String(readerEntry.GetName()),
				RoleType: msgpb.XidAcls_OWNER.Enum(),
			},
			wantAuthorized: false,
		},
		{
			name: "owner_can_read_owner_xid",
			req: &msgpb.CheckUserAuthorizationRequest{
				Name:     proto.String(ownerEntry.GetName()),
				RoleType: msgpb.XidAcls_READER.Enum(),
			},
			wantAuthorized: true,
		},
		{
			name: "owner_can_write_owner_xid",
			req: &msgpb.CheckUserAuthorizationRequest{
				Name:     proto.String(ownerEntry.GetName()),
				RoleType: msgpb.XidAcls_WRITER.Enum(),
			},
			wantAuthorized: true,
		},
		{
			name: "owner_can_own_owner_xid",
			req: &msgpb.CheckUserAuthorizationRequest{
				Name:     proto.String(ownerEntry.GetName()),
				RoleType: msgpb.XidAcls_OWNER.Enum(),
			},
			wantAuthorized: true,
		},
		{
			name: "user_can_read_child_xid_if_owner_of_parent_xid",
			req: &msgpb.CheckUserAuthorizationRequest{
				Name:     proto.String(childEntry.GetName()),
				RoleType: msgpb.XidAcls_READER.Enum(),
			},
			wantAuthorized: true,
		},
		{
			name: "user_can_write_child_xid_if_owner_of_parent_xid",
			req: &msgpb.CheckUserAuthorizationRequest{
				Name:     proto.String(childEntry.GetName()),
				RoleType: msgpb.XidAcls_WRITER.Enum(),
			},
			wantAuthorized: true,
		},
		{
			name: "user_can_own_child_xid_if_owner_of_parent_xid",
			req: &msgpb.CheckUserAuthorizationRequest{
				Name:     proto.String(childEntry.GetName()),
				RoleType: msgpb.XidAcls_OWNER.Enum(),
			},
			wantAuthorized: true,
		},
		{
			name: "user_cannot_read_child_xid_if_not_authorized_on_parent_xid",
			req: &msgpb.CheckUserAuthorizationRequest{
				Name:     proto.String(otherOwnerChildEntry.GetName()),
				RoleType: msgpb.XidAcls_READER.Enum(),
			},
			wantAuthorized: false,
		},
		{
			name: "no_parent_owner_can_read",
			req: &msgpb.CheckUserAuthorizationRequest{
				Name:     proto.String(noParentOwnerEntry.GetName()),
				RoleType: msgpb.XidAcls_READER.Enum(),
			},
			wantAuthorized: true,
		},
		{
			name: "mixed_case_owner_acl_matches_lowercase_user",
			req: &msgpb.CheckUserAuthorizationRequest{
				Name:     proto.String(mixedCaseOwnerEntry.GetName()),
				RoleType: msgpb.XidAcls_OWNER.Enum(),
			},
			wantAuthorized: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := server.CheckUserAuthorization(ctx, tc.req)
			if err != nil {
				t.Fatalf("CheckUserAuthorization(%v) returned unexpected error: %v", tc.req, err)
			}
			if resp.GetAuthorized() != tc.wantAuthorized {
				t.Errorf("CheckUserAuthorization(%v) returned authorized=%t, want %t", tc.req, resp.GetAuthorized(), tc.wantAuthorized)
			}
		})
	}
}

func TestCheckUserAuthorization_BadPaths(t *testing.T) {
	ctx := context.Background()
	server := createXidServer(ctx, t)

	// In mock auth, the user is "test@example.com".
	// Create an entry where user is only a reader.
	readerEntryReq := &msgpb.CreateXidEntryRequest{
		XidEntry: &msgpb.XidEntry{
			Origin: &msgpb.OriginatingSystem{Endpoint: proto.String("http://testhost")},
			Acls: &msgpb.XidAcls{
				Readers: []*msgpb.XidAcls_Role{{Principal: &msgpb.XidAcls_Role_User{User: "test@example.com"}}},
				Writers: []*msgpb.XidAcls_Role{{Principal: &msgpb.XidAcls_Role_User{User: "other@example.com"}}},
				Owners:  []*msgpb.XidAcls_Role{{Principal: &msgpb.XidAcls_Role_User{User: "other@example.com"}}},
			},
		},
	}
	readerEntry, err := server.CreateXidEntry(ctx, readerEntryReq)
	if err != nil {
		t.Fatalf("CreateXidEntry() returned an unexpected error: %v", err)
	}

	testCases := []struct {
		name    string
		req     *msgpb.CheckUserAuthorizationRequest
		wantErr error
	}{
		{
			name: "invalid_name",
			req: &msgpb.CheckUserAuthorizationRequest{
				Name:     proto.String("xids/invalid"),
				RoleType: msgpb.XidAcls_READER.Enum(),
			},
			wantErr: grpcerr.ErrInvalidArgument,
		},
		{
			name: "not_found",
			req: &msgpb.CheckUserAuthorizationRequest{
				Name:     proto.String("xids/999999"),
				RoleType: msgpb.XidAcls_READER.Enum(),
			},
			wantErr: grpcerr.ErrNotFound,
		},
		{
			name: "unspecified_role",
			req: &msgpb.CheckUserAuthorizationRequest{
				Name:     proto.String(readerEntry.GetName()),
				RoleType: msgpb.XidAcls_ROLE_TYPE_UNSPECIFIED.Enum(),
			},
			wantErr: grpcerr.ErrInvalidArgument,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := server.CheckUserAuthorization(ctx, tc.req)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("CheckUserAuthorization(%v) returned error %v, want %v", tc.req, err, tc.wantErr)
			}
		})
	}
}

func TestCheckUserAuthorization_Failures(t *testing.T) {
	testCases := []struct {
		name        string
		mockAuth    auth.AuthorizerInterface
		mockStorage storage.Storage
		wantErr     error
	}{
		{
			name:        "AuthFailure",
			mockAuth:    &auth.MockAuth{AuthnErr: grpcerr.ErrUnauthenticated},
			mockStorage: &storage.MockStorage{},
			wantErr:     grpcerr.ErrUnauthenticated,
		},
		{
			name:        "StorageFailure",
			mockAuth:    &auth.MockAuth{},
			mockStorage: &storage.MockStorage{FetchErr: errors.New("storage error")},
			wantErr:     grpcerr.ErrInternal,
		},
		{
			name:        "AuthorizationError",
			mockAuth:    &auth.MockAuth{AuthzErr: errors.New("authz error")},
			mockStorage: &storage.MockStorage{},
			wantErr:     grpcerr.ErrPermissionDenied,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			server := newTestServer(t, withAuthorizer(tc.mockAuth), withStorage(tc.mockStorage))
			_, err := server.CheckUserAuthorization(ctx, &msgpb.CheckUserAuthorizationRequest{
				Name:     proto.String("xids/1"),
				RoleType: msgpb.XidAcls_READER.Enum(),
			})
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("CheckUserAuthorization() returned error %v, want %v", err, tc.wantErr)
			}
		})
	}
}
