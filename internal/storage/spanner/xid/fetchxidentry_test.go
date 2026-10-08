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
	"xmanager_cloud/internal/storage/spanner"

	"github.com/google/go-cmp/cmp"
	"golang/protobuf/v2/proto/proto"
	"google.golang.org/protobuf/testing/protocmp"
	"task/go/status"
	"xmanager_cloud/storage/spanner/emulator/emulator"
	"xmanager_cloud/validator/validator"
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
	if !errors.Is(err, status.ErrNotFound) {
		t.Errorf("FetchXidEntry for non-existent XID returned error %v, want %v", err, status.ErrNotFound)
	}
}
