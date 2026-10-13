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
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/spanner"
	"xmanager_cloud/internal/storage/spanner/emulator"
	"xmanager_cloud/internal/storage/spanner/xid"
	"xmanager_cloud/internal/validator"
	xidpb "xmanager_cloud/xid_service/proto/messages_go_proto"
)

func TestFetchXidEntry(t *testing.T) {
	ctx := context.Background()
	xidClient, _ := emulator.Setup(ctx, t)
	storage, _ := spanner.NewStorage(xidClient)

	parentEntry := &xidpb.XidEntry{
		Creator: proto.String("test-creator"),
		Origin: &xidpb.OriginatingSystem{
			Endpoint: proto.String("test-endpoint"),
		},
		Acls: &xidpb.XidAcls{},
	}
	createdParentEntry, err := storage.CreateXidEntry(ctx, parentEntry)
	if err != nil {
		t.Fatalf("CreateXidEntry failed: %v", err)
	}

	entry := &xidpb.XidEntry{
		Creator: proto.String("test-creator"),
		Origin: &xidpb.OriginatingSystem{
			Endpoint: proto.String("test-endpoint"),
		},
		Acls:      &xidpb.XidAcls{},
		ParentXid: proto.String(createdParentEntry.GetName()),
	}

	createdEntry, err := storage.CreateXidEntry(ctx, entry)
	if err != nil {
		t.Fatalf("CreateXidEntry failed: %v", err)
	}

	xid, err := validator.ValidateXIDName(createdEntry.GetName())
	if err != nil {
		t.Fatalf("Could not parse XID from name %q: %v", createdEntry.GetName(), err)
	}

	gotEntry, err := storage.FetchXidEntry(ctx, xid)
	if err != nil {
		t.Fatalf("FetchXidEntry failed: %v", err)
	}

	if diff := cmp.Diff(createdEntry, gotEntry, protocmp.Transform()); diff != "" {
		t.Errorf("FetchXidEntry(%d) returned diff (-want +got):\n%s", xid, diff)
	}
}

func TestFetchXidEntry_NotFound(t *testing.T) {
	ctx := context.Background()
	xidClient, _ := emulator.Setup(ctx, t)
	storage, _ := spanner.NewStorage(xidClient)

	_, err := storage.FetchXidEntry(ctx, 123456789)
	if !errors.Is(err, grpcerr.ErrNotFound) {
		t.Errorf("FetchXidEntry for non-existent XID returned error %v, want %v", err, grpcerr.ErrNotFound)
	}
}

func TestTxRepository_FetchAndUpdateXidEntry(t *testing.T) {
	ctx := context.Background()
	xidClient, _ := emulator.Setup(ctx, t)
	s, err := spanner.NewStorage(xidClient)
	if err != nil {
		t.Fatalf("spanner.NewStorage() failed: %v", err)
	}

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

	var updatedEntry *xidpb.XidEntry
	err = s.RunInTransaction(ctx, func(ctx context.Context, tx *storage.Transaction) error {
		fetched, err := tx.XIDs.FetchXidEntry(ctx, xidID)
		if err != nil {
			return err
		}
		updatedEntry = proto.Clone(fetched).(*xidpb.XidEntry)
		updatedEntry.Acls = &xidpb.XidAcls{
			Owners: []*xidpb.XidAcls_Role{
				{Principal: &xidpb.XidAcls_Role_User{User: "user2@example.com"}},
			},
		}
		updatedEntry.UpdateTime = timestamppb.New(time.Now().UTC().Truncate(time.Microsecond))
		return tx.XIDs.UpdateXidEntry(ctx, xidID, updatedEntry)
	})
	if err != nil {
		t.Fatalf("RunInTransaction failed: %v", err)
	}

	gotEntry, err := s.FetchXidEntry(ctx, xidID)
	if err != nil {
		t.Fatalf("FetchXidEntry failed: %v", err)
	}
	if diff := cmp.Diff(updatedEntry, gotEntry, protocmp.Transform()); diff != "" {
		t.Errorf("FetchXidEntry returned diff after tx update (-want +got):\n%s", diff)
	}
}

func TestTxRepository_FetchXidEntry_NotFound(t *testing.T) {
	ctx := context.Background()
	xidClient, _ := emulator.Setup(ctx, t)
	s, err := spanner.NewStorage(xidClient)
	if err != nil {
		t.Fatalf("spanner.NewStorage() failed: %v", err)
	}

	err = s.RunInTransaction(ctx, func(ctx context.Context, tx *storage.Transaction) error {
		_, err := tx.XIDs.FetchXidEntry(ctx, 123456789)
		return err
	})
	if !errors.Is(err, grpcerr.ErrNotFound) {
		t.Errorf("FetchXidEntry for non-existent XID returned error %v, want %v", err, grpcerr.ErrNotFound)
	}
}

func TestTxRepository_UpdateXidEntry_ValidationErrors(t *testing.T) {
	ctx := context.Background()
	xidClient, _ := emulator.Setup(ctx, t)
	repo, err := xid.NewRepository(xidClient)
	if err != nil {
		t.Fatalf("xid.NewRepository() failed: %v", err)
	}

	if err := repo.WithTx(nil).UpdateXidEntry(ctx, 1, &xidpb.XidEntry{}); !errors.Is(err, grpcerr.ErrFailedPrecondition) {
		t.Errorf("UpdateXidEntry with nil tx returned error %v, want %v", err, grpcerr.ErrFailedPrecondition)
	}

	s, err := spanner.NewStorage(xidClient)
	if err != nil {
		t.Fatalf("spanner.NewStorage() failed: %v", err)
	}
	err = s.RunInTransaction(ctx, func(ctx context.Context, tx *storage.Transaction) error {
		return tx.XIDs.UpdateXidEntry(ctx, 1, nil)
	})
	if !errors.Is(err, grpcerr.ErrInvalidArgument) {
		t.Errorf("UpdateXidEntry with nil entry returned error %v, want %v", err, grpcerr.ErrInvalidArgument)
	}
}
