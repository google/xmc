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
package spanner

import (
	"context"
	"errors"
	"testing"

	"golang/cmp/cmp"
	"golang/protobuf/v2/proto/proto"
	"golang/protobuf/v2/testing/protocmp/protocmp"
	fmpb "protobuf/field_mask_go_proto"
	"task/go/status"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/storage/spanner/emulator/emulator"
	"xmanager_cloud/storage/storage"
)

func TestUpdateWorkUnit(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	s, _ := NewStorage(essClient)

	// 1. Create an Experiment and WorkUnit.
	xid := int64(123)
	wid := int64(1)
	createBlankExperiment(ctx, t, s, xid)
	createdWu := createBlankWorkUnit(ctx, t, s, xid, wid)

	// 2. Update the WorkUnit.
	update := &storage.WorkUnitUpdate{
		XID: xid,
		WID: wid,
		WorkUnit: &essmsgpb.WorkUnit{
			Priority: 1,
			Notes:    "new-notes",
			Tags:     []string{"tag3"},
			Urls:     []*essmsgpb.Url{{Url: "http://new.com"}},
		},
		UpdateMask: &fmpb.FieldMask{Paths: []string{"priority", "notes", "tags", "urls"}},
	}
	originalWu, updatedWu, err := s.UpdateWorkUnit(ctx, update)
	if err != nil {
		t.Fatalf("UpdateWorkUnit failed: %v", err)
	}

	// 3. Verify the update.
	if diff := cmp.Diff(createdWu, originalWu, protocmp.Transform()); diff != "" {
		t.Errorf("UpdateWorkUnit returned unexpected original work unit: diff (-want +got):\n%s", diff)
	}
	want := proto.Clone(createdWu).(*essmsgpb.WorkUnit)
	want.Priority = 1
	want.Notes = "new-notes"
	want.Tags = []string{"tag3"}
	want.Urls = []*essmsgpb.Url{{Url: "http://new.com"}}
	if diff := cmp.Diff(want, updatedWu, protocmp.Transform(), protocmp.IgnoreFields(&essmsgpb.WorkUnit{}, "update_time")); diff != "" {
		t.Errorf("UpdateWorkUnit returned unexpected work unit: diff (-want +got):\n%s", diff)
	}
	if !updatedWu.UpdateTime.AsTime().After(createdWu.UpdateTime.AsTime()) {
		t.Errorf("UpdateWorkUnit did not update UpdateTime: got %v, want > %v", updatedWu.UpdateTime, createdWu.UpdateTime)
	}

	// 4. Verify with FetchWorkUnit.
	gotWu, err := s.FetchWorkUnit(ctx, xid, wid)
	if err != nil {
		t.Fatalf("FetchWorkUnit failed: %v", err)
	}
	if diff := cmp.Diff(updatedWu, gotWu, protocmp.Transform()); diff != "" {
		t.Errorf("FetchWorkUnit returned diff after update (-want +got):\n%s", diff)
	}
}

func TestUpdateWorkUnit_ImmutableField(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	s, _ := NewStorage(essClient)

	xid := int64(123)
	wid := int64(1)
	createBlankExperiment(ctx, t, s, xid)
	createBlankWorkUnit(ctx, t, s, xid, wid)

	update := &storage.WorkUnitUpdate{
		XID: xid,
		WID: wid,
		WorkUnit: &essmsgpb.WorkUnit{
			WorkUnitType: essmsgpb.WorkUnit_AUXILIARY,
		},
		UpdateMask: &fmpb.FieldMask{Paths: []string{"work_unit_type"}},
	}
	_, _, err := s.UpdateWorkUnit(ctx, update)
	if !errors.Is(err, status.ErrInvalidArgument) {
		t.Errorf("UpdateWorkUnit with immutable field returned error %v, want %v", err, status.ErrInvalidArgument)
	}
}

func TestUpdateWorkUnit_NotFound(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	s, _ := NewStorage(essClient)

	update := &storage.WorkUnitUpdate{
		XID: 123456789,
		WID: 1,
		WorkUnit: &essmsgpb.WorkUnit{
			Notes: "new-notes",
		},
		UpdateMask: &fmpb.FieldMask{Paths: []string{"notes"}},
	}
	_, _, err := s.UpdateWorkUnit(ctx, update)
	if !errors.Is(err, status.ErrNotFound) {
		t.Errorf("UpdateWorkUnit for non-existent XID returned error %v, want %v", err, status.ErrNotFound)
	}
}

func TestUpdateWorkUnit_EmptyMask(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	s, _ := NewStorage(essClient)

	xid := int64(123)
	wid := int64(1)
	createBlankExperiment(ctx, t, s, xid)
	createBlankWorkUnit(ctx, t, s, xid, wid)

	update := &storage.WorkUnitUpdate{
		XID:        xid,
		WID:        wid,
		WorkUnit:   &essmsgpb.WorkUnit{},
		UpdateMask: &fmpb.FieldMask{Paths: []string{}},
	}
	_, _, err := s.UpdateWorkUnit(ctx, update)
	if !errors.Is(err, status.ErrInvalidArgument) {
		t.Errorf("UpdateWorkUnit with empty mask returned error %v, want %v", err, status.ErrInvalidArgument)
	}
}
