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
	"testing"
	"xmanager_cloud/internal/storage/spanner"

	"google.golang.org/protobuf/proto"
	"xmanager_cloud/internal/storage/spanner/emulator"
	xidpb "xmanager_cloud/xid_service/proto/messages_go_proto"
)

func TestCreateXidEntry(t *testing.T) {
	ctx := context.Background()
	xidClient, _ := emulator.Setup(ctx, t)
	storage, _ := spanner.NewStorage(xidClient)

	parentEntry := &xidpb.XidEntry{
		Creator: proto.String("test-creator"),
		Origin: &xidpb.OriginatingSystem{
			Endpoint: proto.String("test-endpoint"),
		},
		Acls: &xidpb.XidAcls{
			Owners: []*xidpb.XidAcls_Role{
				{
					Principal: &xidpb.XidAcls_Role_User{
						User: "test-owner",
					},
				},
			},
		},
	}
	createdParentEntry, err := storage.CreateXidEntry(ctx, parentEntry)
	if err != nil {
		t.Fatalf("CreateXidEntry for parent failed: %v", err)
	}

	entry := &xidpb.XidEntry{
		Creator: proto.String("test-creator"),
		Origin: &xidpb.OriginatingSystem{
			Endpoint: proto.String("test-endpoint"),
		},
		Acls: &xidpb.XidAcls{
			Owners: []*xidpb.XidAcls_Role{
				{
					Principal: &xidpb.XidAcls_Role_User{
						User: "test-owner",
					},
				},
			},
		},
		ParentXid: proto.String(createdParentEntry.GetName()),
	}

	createdEntry, err := storage.CreateXidEntry(ctx, entry)
	if err != nil {
		t.Fatalf("CreateXidEntry failed: %v", err)
	}

	if createdEntry.GetName() == "" {
		t.Errorf("CreateXidEntry returned entry with empty name")
	}
	if createdEntry.GetCreateTime() == nil {
		t.Errorf("CreateXidEntry returned entry with nil CreateTime")
	}
	if createdEntry.GetUpdateTime() == nil {
		t.Errorf("CreateXidEntry returned entry with nil UpdateTime")
	}
	if createdEntry.GetCreator() != entry.GetCreator() {
		t.Errorf("CreateXidEntry returned entry with wrong creator: got %q, want %q", createdEntry.GetCreator(), entry.GetCreator())
	}
	if createdEntry.GetParentXid() != entry.GetParentXid() {
		t.Errorf("CreateXidEntry returned entry with wrong parent: got %q, want %q", createdEntry.GetParentXid(), entry.GetParentXid())
	}
}
