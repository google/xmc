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
package xid_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
	fmpb "google.golang.org/protobuf/types/known/fieldmaskpb"
	"xmanager_cloud/cmd/xid_service/xid"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/spanner"
	"xmanager_cloud/internal/storage/spanner/emulator"
	"xmanager_cloud/internal/validator"
	xidpb "xmanager_cloud/xid_service/proto/messages_go_proto"
)

func TestUpdateXidEntry(t *testing.T) {
	ctx := context.Background()
	xidClient, _ := emulator.Setup(ctx, t)
	s, _ := spanner.NewStorage(xidClient)

	// 1. Create an XidEntry.
	entry := &xidpb.XidEntry{
		Creator: proto.String("test-creator"),
		Origin: &xidpb.OriginatingSystem{
			Endpoint: proto.String("test-endpoint"),
		},
		Acls: &xidpb.XidAcls{
			Owners: []*xidpb.XidAcls_Role{
				{Principal: &xidpb.XidAcls_Role_User{User: "user1@example.com"}},
			},
		},
	}
	createdEntry, err := s.CreateXidEntry(ctx, entry)
	if err != nil {
		t.Fatalf("CreateXidEntry failed: %v", err)
	}

	xidID, err := validator.ValidateXIDName(createdEntry.GetName())
	if err != nil {
		t.Fatalf("Could not parse XID from name %q: %v", createdEntry.GetName(), err)
	}

	// 2. Update the XidEntry.
	update := &storage.XidEntryUpdate{
		XID:     xidID,
		Creator: "user1@example.com",
		XidEntry: &xidpb.XidEntry{
			Acls: &xidpb.XidAcls{
				Owners: []*xidpb.XidAcls_Role{
					{Principal: &xidpb.XidAcls_Role_User{User: "user2@example.com"}},
				},
			},
		},
		UpdateMask: &fmpb.FieldMask{Paths: []string{"acls"}},
	}
	updatedEntry, err := xid.UpdateXidEntry(ctx, s, update)
	if err != nil {
		t.Fatalf("UpdateXidEntry failed: %v", err)
	}

	// 3. Verify the update.
	if diff := cmp.Diff(update.XidEntry.Acls, updatedEntry.Acls, protocmp.Transform()); diff != "" {
		t.Errorf("UpdateXidEntry returned diff in Acls (-want +got):\n%s", diff)
	}
	if !updatedEntry.UpdateTime.AsTime().After(createdEntry.UpdateTime.AsTime()) {
		t.Errorf("UpdateXidEntry did not update UpdateTime: got %v, want > %v", updatedEntry.UpdateTime, createdEntry.UpdateTime)
	}
	if updatedEntry.GetCreator() != createdEntry.GetCreator() {
		t.Errorf("UpdateXidEntry unexpectedly updated Creator: got %q, want %q", updatedEntry.GetCreator(), createdEntry.GetCreator())
	}

	// 4. Verify with FetchXidEntry.
	gotEntry, err := s.FetchXidEntry(ctx, xidID)
	if err != nil {
		t.Fatalf("FetchXidEntry failed: %v", err)
	}
	if diff := cmp.Diff(updatedEntry, gotEntry, protocmp.Transform()); diff != "" {
		t.Errorf("FetchXidEntry returned diff after update (-want +got):\n%s", diff)
	}
}

func TestUpdateXidEntry_ImmutableField(t *testing.T) {
	ctx := context.Background()
	xidClient, _ := emulator.Setup(ctx, t)
	s, _ := spanner.NewStorage(xidClient)

	entry := &xidpb.XidEntry{
		Creator: proto.String("test-creator"),
		Origin:  &xidpb.OriginatingSystem{Endpoint: proto.String("test-endpoint")},
		Acls: &xidpb.XidAcls{
			Owners: []*xidpb.XidAcls_Role{
				{Principal: &xidpb.XidAcls_Role_User{User: "user1@example.com"}},
			},
		},
	}
	createdEntry, err := s.CreateXidEntry(ctx, entry)
	if err != nil {
		t.Fatalf("CreateXidEntry failed: %v", err)
	}

	xidID, err := validator.ValidateXIDName(createdEntry.GetName())
	if err != nil {
		t.Fatalf("Could not parse XID from name %q: %v", createdEntry.GetName(), err)
	}

	update := &storage.XidEntryUpdate{
		XID:     xidID,
		Creator: "user1@example.com",
		XidEntry: &xidpb.XidEntry{
			Creator: proto.String("new-creator"),
		},
		UpdateMask: &fmpb.FieldMask{Paths: []string{"creator"}},
	}
	_, err = xid.UpdateXidEntry(ctx, s, update)
	if !errors.Is(err, grpcerr.ErrInvalidArgument) {
		t.Errorf("UpdateXidEntry with immutable field returned error %v, want %v", err, grpcerr.ErrInvalidArgument)
	}
}

func TestUpdateXidEntry_ImmutableParentField(t *testing.T) {
	ctx := context.Background()
	xidClient, _ := emulator.Setup(ctx, t)
	s, _ := spanner.NewStorage(xidClient)

	entry := &xidpb.XidEntry{
		Creator: proto.String("test-creator"),
		Origin:  &xidpb.OriginatingSystem{Endpoint: proto.String("test-endpoint")},
		Acls: &xidpb.XidAcls{
			Owners: []*xidpb.XidAcls_Role{
				{Principal: &xidpb.XidAcls_Role_User{User: "user1@example.com"}},
			},
		},
	}
	createdEntry, err := s.CreateXidEntry(ctx, entry)
	if err != nil {
		t.Fatalf("CreateXidEntry failed: %v", err)
	}

	xidID, err := validator.ValidateXIDName(createdEntry.GetName())
	if err != nil {
		t.Fatalf("Could not parse XID from name %q: %v", createdEntry.GetName(), err)
	}

	update := &storage.XidEntryUpdate{
		XID:     xidID,
		Creator: "user1@example.com",
		XidEntry: &xidpb.XidEntry{
			ParentXid: proto.String("xids/123"),
		},
		UpdateMask: &fmpb.FieldMask{Paths: []string{"parent_xid"}},
	}
	_, err = xid.UpdateXidEntry(ctx, s, update)
	if !errors.Is(err, grpcerr.ErrInvalidArgument) {
		t.Errorf("UpdateXidEntry with immutable field returned error %v, want %v", err, grpcerr.ErrInvalidArgument)
	}
}

func TestUpdateXidEntry_NotFound(t *testing.T) {
	ctx := context.Background()
	xidClient, _ := emulator.Setup(ctx, t)
	s, _ := spanner.NewStorage(xidClient)

	update := &storage.XidEntryUpdate{
		XID:     123456789,
		Creator: "user1@example.com",
		XidEntry: &xidpb.XidEntry{
			Acls: &xidpb.XidAcls{},
		},
		UpdateMask: &fmpb.FieldMask{Paths: []string{"acls"}},
	}
	_, err := xid.UpdateXidEntry(ctx, s, update)
	if !errors.Is(err, grpcerr.ErrNotFound) {
		t.Errorf("UpdateXidEntry for non-existent XID returned error %v, want %v", err, grpcerr.ErrNotFound)
	}
}

func TestUpdateXidEntry_NoOwnerFailsWithoutParent(t *testing.T) {
	ctx := context.Background()
	xidClient, _ := emulator.Setup(ctx, t)
	s, _ := spanner.NewStorage(xidClient)

	// 1. Create entry with owner.
	entry := &xidpb.XidEntry{
		Creator: proto.String("test-creator"),
		Origin:  &xidpb.OriginatingSystem{Endpoint: proto.String("test-endpoint")},
		Acls: &xidpb.XidAcls{
			Owners: []*xidpb.XidAcls_Role{
				{Principal: &xidpb.XidAcls_Role_User{User: "user1@example.com"}},
			},
		},
	}
	createdEntry, err := s.CreateXidEntry(ctx, entry)
	if err != nil {
		t.Fatalf("CreateXidEntry failed: %v", err)
	}

	xidID, err := validator.ValidateXIDName(createdEntry.GetName())
	if err != nil {
		t.Fatalf("Could not parse XID from name %q: %v", createdEntry.GetName(), err)
	}

	// 2. Update entry to have no owners and no parent.
	update := &storage.XidEntryUpdate{
		XID:     xidID,
		Creator: "user1@example.com",
		XidEntry: &xidpb.XidEntry{
			Acls: &xidpb.XidAcls{},
		},
		UpdateMask: &fmpb.FieldMask{Paths: []string{"acls"}},
	}
	_, err = xid.UpdateXidEntry(ctx, s, update)
	if !errors.Is(err, grpcerr.ErrInvalidArgument) {
		t.Errorf("UpdateXidEntry with no owners returned error %v, want %v", err, grpcerr.ErrInvalidArgument)
	}
}

func TestUpdateXidEntry_NoOwnerSucceedsWithParent(t *testing.T) {
	ctx := context.Background()
	xidClient, _ := emulator.Setup(ctx, t)
	s, _ := spanner.NewStorage(xidClient)

	// 1. Create parent with owner.
	parentEntry := &xidpb.XidEntry{
		Creator: proto.String("test-creator"),
		Origin:  &xidpb.OriginatingSystem{Endpoint: proto.String("test-endpoint")},
		Acls: &xidpb.XidAcls{
			Owners: []*xidpb.XidAcls_Role{
				{Principal: &xidpb.XidAcls_Role_User{User: "user1@example.com"}},
			},
		},
	}
	createdParentEntry, err := s.CreateXidEntry(ctx, parentEntry)
	if err != nil {
		t.Fatalf("CreateXidEntry failed for parent: %v", err)
	}

	// 2. Create child entry with owner and parent.
	childEntry := &xidpb.XidEntry{
		Creator:   proto.String("test-creator"),
		Origin:    &xidpb.OriginatingSystem{Endpoint: proto.String("test-endpoint")},
		ParentXid: proto.String(createdParentEntry.GetName()),
		Acls: &xidpb.XidAcls{
			Owners: []*xidpb.XidAcls_Role{
				{Principal: &xidpb.XidAcls_Role_User{User: "user1@example.com"}},
			},
		},
	}
	createdChildEntry, err := s.CreateXidEntry(ctx, childEntry)
	if err != nil {
		t.Fatalf("CreateXidEntry failed for child: %v", err)
	}

	childXID, err := validator.ValidateXIDName(createdChildEntry.GetName())
	if err != nil {
		t.Fatalf("Could not parse XID from name %q: %v", createdChildEntry.GetName(), err)
	}

	// 3. Update child to have no owners. This should succeed because parent has owners.
	updateChild := &storage.XidEntryUpdate{
		XID:     childXID,
		Creator: "user1@example.com",
		XidEntry: &xidpb.XidEntry{
			Acls: &xidpb.XidAcls{},
		},
		UpdateMask: &fmpb.FieldMask{Paths: []string{"acls"}},
	}
	_, err = xid.UpdateXidEntry(ctx, s, updateChild)
	if err != nil {
		t.Errorf("UpdateXidEntry for child with no owners but parent with owners failed: %v", err)
	}
}

func TestUpdateXidEntry_NoUpdateableFields(t *testing.T) {
	ctx := context.Background()
	xidClient, _ := emulator.Setup(ctx, t)
	s, _ := spanner.NewStorage(xidClient)

	entry := &xidpb.XidEntry{
		Creator: proto.String("test-creator"),
		Origin:  &xidpb.OriginatingSystem{Endpoint: proto.String("test-endpoint")},
		Acls: &xidpb.XidAcls{
			Owners: []*xidpb.XidAcls_Role{
				{Principal: &xidpb.XidAcls_Role_User{User: "user1@example.com"}},
			},
		},
	}
	createdEntry, err := s.CreateXidEntry(ctx, entry)
	if err != nil {
		t.Fatalf("CreateXidEntry failed: %v", err)
	}

	xidID, err := validator.ValidateXIDName(createdEntry.GetName())
	if err != nil {
		t.Fatalf("Could not parse XID from name %q: %v", createdEntry.GetName(), err)
	}

	update := &storage.XidEntryUpdate{
		XID:        xidID,
		Creator:    "user1@example.com",
		XidEntry:   &xidpb.XidEntry{},
		UpdateMask: &fmpb.FieldMask{Paths: []string{}},
	}
	_, err = xid.UpdateXidEntry(ctx, s, update)
	if !errors.Is(err, grpcerr.ErrInvalidArgument) {
		t.Errorf("UpdateXidEntry with no updateable fields returned error %v, want %v", err, grpcerr.ErrInvalidArgument)
	}
}

func TestWalkAncestors(t *testing.T) {
	ownerRole := &xidpb.XidAcls_Role{
		Principal: &xidpb.XidAcls_Role_User{User: "owner@example.com"},
	}
	hasOwnersPredicate := func(_ context.Context, entry *xidpb.XidEntry) (bool, error) {
		return len(entry.GetAcls().GetOwners()) > 0, nil
	}

	tests := []struct {
		name      string
		start     *xidpb.XidEntry
		entries   map[int64]*xidpb.XidEntry
		fetchErr  error
		predicate xid.AncestorPredicate
		want      bool
		wantErr   error
	}{
		{
			name:    "nil entry returns ErrInvalidArgument",
			start:   nil,
			wantErr: grpcerr.ErrInvalidArgument,
		},
		{
			name: "self loop returns ErrInternal",
			start: &xidpb.XidEntry{
				Name:      proto.String("xids/1"),
				ParentXid: proto.String("xids/1"),
			},
			predicate: hasOwnersPredicate,
			wantErr:   grpcerr.ErrInternal,
		},
		{
			name: "cycle in ancestors returns ErrInternal",
			start: &xidpb.XidEntry{
				Name:      proto.String("xids/1"),
				ParentXid: proto.String("xids/2"),
			},
			entries: map[int64]*xidpb.XidEntry{
				2: {
					Name:      proto.String("xids/2"),
					ParentXid: proto.String("xids/1"),
				},
			},
			predicate: hasOwnersPredicate,
			wantErr:   grpcerr.ErrInternal,
		},
		{
			name: "invalid parent name returns ErrInvalidArgument",
			start: &xidpb.XidEntry{
				Name:      proto.String("xids/1"),
				ParentXid: proto.String("invalid-parent"),
			},
			predicate: hasOwnersPredicate,
			wantErr:   grpcerr.ErrInvalidArgument,
		},
		{
			name: "non-NotFound fetch error maps to ErrInternal",
			start: &xidpb.XidEntry{
				Name:      proto.String("xids/1"),
				ParentXid: proto.String("xids/2"),
			},
			fetchErr:  errors.New("db failure"),
			predicate: hasOwnersPredicate,
			wantErr:   grpcerr.ErrInternal,
		},
		{
			name: "missing parent preserves ErrNotFound",
			start: &xidpb.XidEntry{
				Name:      proto.String("xids/1"),
				ParentXid: proto.String("xids/2"),
			},
			predicate: hasOwnersPredicate,
			wantErr:   grpcerr.ErrNotFound,
		},
		{
			name: "predicate error is propagated",
			start: &xidpb.XidEntry{
				Name: proto.String("xids/1"),
			},
			predicate: func(context.Context, *xidpb.XidEntry) (bool, error) {
				return false, grpcerr.ErrPermissionDenied
			},
			wantErr: grpcerr.ErrPermissionDenied,
		},
		{
			name: "match on grandparent",
			start: &xidpb.XidEntry{
				Name:      proto.String("xids/1"),
				ParentXid: proto.String("xids/2"),
			},
			entries: map[int64]*xidpb.XidEntry{
				2: {
					Name:      proto.String("xids/2"),
					ParentXid: proto.String("xids/3"),
				},
				3: {
					Name: proto.String("xids/3"),
					Acls: &xidpb.XidAcls{Owners: []*xidpb.XidAcls_Role{ownerRole}},
				},
			},
			predicate: hasOwnersPredicate,
			want:      true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fetcher := func(_ context.Context, id int64) (*xidpb.XidEntry, error) {
				if tc.fetchErr != nil {
					return nil, tc.fetchErr
				}
				entry, ok := tc.entries[id]
				if !ok {
					return nil, grpcerr.ErrNotFound
				}
				return entry, nil
			}

			got, err := xid.WalkAncestors(context.Background(), tc.start, fetcher, tc.predicate)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("WalkAncestors() error = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("WalkAncestors() unexpected error = %v", err)
			}
			if got != tc.want {
				t.Errorf("WalkAncestors() = %v, want %v", got, tc.want)
			}
		})
	}
}
