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

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
	msgpb "xmanager_cloud/api/protos/xid"
	"xmanager_cloud/internal/auth"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/prototest"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/validator"
)

func TestCreateXidEntry_HappyPath(t *testing.T) {
	ctx := context.Background()
	server := createXidServer(ctx, t)

	parentReq := &msgpb.CreateXidEntryRequest{
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

	req := &msgpb.CreateXidEntryRequest{
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

func TestCreateXidEntry_NormalizesACLPrincipals(t *testing.T) {
	ctx := context.Background()
	server := createXidServer(ctx, t)

	req := &msgpb.CreateXidEntryRequest{
		XidEntry: &msgpb.XidEntry{
			Origin: &msgpb.OriginatingSystem{Endpoint: proto.String("http://testhost")},
			Acls: &msgpb.XidAcls{
				Owners:  []*msgpb.XidAcls_Role{{Principal: &msgpb.XidAcls_Role_User{User: " Test@Example.com "}}},
				Writers: []*msgpb.XidAcls_Role{{Principal: &msgpb.XidAcls_Role_Group{Group: "Team@Example.com"}}},
				Readers: []*msgpb.XidAcls_Role{
					{Principal: &msgpb.XidAcls_Role_ServiceAccount{ServiceAccount: "SA@Project.iam.gserviceaccount.com"}},
					{Principal: &msgpb.XidAcls_Role_Domain{Domain: "Example.COM"}},
				},
			},
		},
	}
	want := &msgpb.XidAcls{
		Owners:  []*msgpb.XidAcls_Role{{Principal: &msgpb.XidAcls_Role_User{User: "test@example.com"}}},
		Writers: []*msgpb.XidAcls_Role{{Principal: &msgpb.XidAcls_Role_Group{Group: "team@example.com"}}},
		Readers: []*msgpb.XidAcls_Role{
			{Principal: &msgpb.XidAcls_Role_ServiceAccount{ServiceAccount: "sa@project.iam.gserviceaccount.com"}},
			{Principal: &msgpb.XidAcls_Role_Domain{Domain: "example.com"}},
		},
	}
	originalReq := proto.Clone(req)

	created, err := server.CreateXidEntry(ctx, req)
	if err != nil {
		t.Fatalf("CreateXidEntry() returned an unexpected error: %v", err)
	}
	if diff := cmp.Diff(originalReq, proto.Message(req), protocmp.Transform()); diff != "" {
		t.Errorf("CreateXidEntry() modified its request (-before +after):\n%s", diff)
	}
	if diff := cmp.Diff(want, created.GetAcls(), protocmp.Transform()); diff != "" {
		t.Errorf("CreateXidEntry() returned unnormalized ACLs (-want +got):\n%s", diff)
	}
	stored, err := server.GetXidEntry(ctx, &msgpb.GetXidEntryRequest{Name: created.Name})
	if err != nil {
		t.Fatalf("GetXidEntry() returned an unexpected error: %v", err)
	}
	if diff := cmp.Diff(want, stored.GetAcls(), protocmp.Transform()); diff != "" {
		t.Errorf("GetXidEntry() returned unnormalized ACLs (-want +got):\n%s", diff)
	}
}

func TestCreateXidEntry_BadPaths(t *testing.T) {
	ctx := context.Background()
	server := createXidServer(ctx, t)

	baseReq := &msgpb.CreateXidEntryRequest{
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
		req     *msgpb.CreateXidEntryRequest
		wantErr error
	}{
		{
			name: "missing_xid_entry",
			req: prototest.Modify(baseReq, func(r *msgpb.CreateXidEntryRequest) {
				r.XidEntry = nil
			}),
			wantErr: grpcerr.ErrInvalidArgument,
		},
		{
			name: "missing_acls",
			req: prototest.Modify(baseReq, func(r *msgpb.CreateXidEntryRequest) {
				r.XidEntry.Acls = nil
			}),
			wantErr: grpcerr.ErrInvalidArgument,
		},
		{
			name: "missing_origin",
			req: prototest.Modify(baseReq, func(r *msgpb.CreateXidEntryRequest) {
				r.XidEntry.Origin = nil
			}),
			wantErr: grpcerr.ErrInvalidArgument,
		},
		{
			name: "invalid_parent_xid",
			req: prototest.Modify(baseReq, func(r *msgpb.CreateXidEntryRequest) {
				r.XidEntry.ParentXid = proto.String("xids/invalid")
			}),
			wantErr: grpcerr.ErrInvalidArgument,
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
			mockAuth:    &auth.MockAuth{AuthnErr: grpcerr.ErrPermissionDenied},
			mockStorage: &storage.MockStorage{},
			wantErr:     grpcerr.ErrPermissionDenied,
		},
		{
			name:        "StorageFailure",
			mockAuth:    &auth.MockAuth{},
			mockStorage: &storage.MockStorage{CreateErr: errors.New("storage error")},
			wantErr:     grpcerr.ErrInternal,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			server := newTestServer(t, withAuthorizer(tc.mockAuth), withStorage(tc.mockStorage))
			_, err := server.CreateXidEntry(ctx, &msgpb.CreateXidEntryRequest{
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
