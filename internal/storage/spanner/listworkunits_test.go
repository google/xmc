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
	"fmt"
	"sort"
	"testing"
	"time"

	"golang/cmp/cmp"
	"golang/protobuf/v2/testing/protocmp/protocmp"
	timestamppb "protobuf/timestamp_go_proto"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/storage/spanner/emulator/emulator"
	"xmanager_cloud/validator/validator"
)

func TestListWorkUnits(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storage, err := NewStorage(essClient)
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}

	// Create one experiment and some work units for it.
	xid := int64(1)
	exp := createBlankExperiment(ctx, t, storage, xid)
	var workUnits []*essmsgpb.WorkUnit
	for i := 0; i < 5; i++ {
		wu := createBlankWorkUnit(ctx, t, storage, xid, int64(i+1))
		workUnits = append(workUnits, wu)
	}

	// List all work units
	gotWorkUnits, _, err := storage.ListWorkUnits(ctx, exp.GetName(), 10, "", "", "", 0)
	if err != nil {
		t.Fatalf("ListWorkUnits failed: %v", err)
	}
	if len(gotWorkUnits) != 5 {
		t.Errorf("ListWorkUnits returned %d work units, want 5", len(gotWorkUnits))
	}

	// Sort work units by WID for comparison
	sort.Slice(workUnits, func(i, j int) bool {
		_, iWID, _ := validator.ValidateWorkUnitName(workUnits[i].GetName())
		_, jWID, _ := validator.ValidateWorkUnitName(workUnits[j].GetName())
		return iWID < jWID
	})
	sort.Slice(gotWorkUnits, func(i, j int) bool {
		_, iWID, _ := validator.ValidateWorkUnitName(gotWorkUnits[i].GetName())
		_, jWID, _ := validator.ValidateWorkUnitName(gotWorkUnits[j].GetName())
		return iWID < jWID
	})
	if diff := cmp.Diff(workUnits, gotWorkUnits, protocmp.Transform()); diff != "" {
		t.Errorf("ListWorkUnits() returned diff (-want +got):\n%s", diff)
	}
}

func TestListWorkUnits_Pagination(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storage, err := NewStorage(essClient)
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}

	// Create one experiment and some work units for it.
	xid := int64(7)
	exp := createBlankExperiment(ctx, t, storage, xid)
	for i := 0; i < 5; i++ {
		createBlankWorkUnit(ctx, t, storage, xid, int64(i+1))
	}

	tests := []struct {
		pageSize   int
		wantCounts []int
	}{
		{3, []int{3, 2}},
		{5, []int{5}},
		{1, []int{1, 1, 1, 1, 1}},
	}

	for _, tc := range tests {
		t.Run(fmt.Sprintf("pageSize=%d", tc.pageSize), func(t *testing.T) {
			var gotCounts []int
			var pageToken string
			for i := 0; i < len(tc.wantCounts); i++ {
				workUnits, nextPageToken, err := storage.ListWorkUnits(ctx, exp.GetName(), int32(tc.pageSize), pageToken, "", "created", 0)
				if err != nil {
					t.Fatalf("ListWorkUnits failed: %v", err)
				}
				gotCounts = append(gotCounts, len(workUnits))
				pageToken = nextPageToken
				if i < len(tc.wantCounts)-1 && nextPageToken == "" {
					t.Errorf("ListWorkUnits returned empty nextPageToken on page %d, want non-empty", i+1)
				}
				if i == len(tc.wantCounts)-1 && nextPageToken != "" {
					t.Errorf("ListWorkUnits returned non-empty nextPageToken on last page, want empty")
				}
			}
			if pageToken != "" {
				t.Errorf("ListWorkUnits returned non-empty nextPageToken on last page, want empty")
			}
			if diff := cmp.Diff(tc.wantCounts, gotCounts); diff != "" {
				t.Errorf("ListWorkUnits returned unexpected page counts (-want +got):\n%s", diff)
			}
		})
	}
}

func TestListWorkUnits_Filter(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storage, err := NewStorage(essClient)
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}

	// Create one experiment and some work units for it.
	xid := int64(3)
	exp := createBlankExperiment(ctx, t, storage, xid)
	createBlankWorkUnit(ctx, t, storage, xid, 1)
	createBlankWorkUnitWithState(ctx, t, storage, xid, 2, essmsgpb.WorkUnitStatus_STOPPED)

	tests := []struct {
		name     string
		filter   string
		wantWIDs []int64
	}{
		{
			name:     "state=STOPPED",
			filter:   `state="STOPPED"`,
			wantWIDs: []int64{2},
		},
		{
			name:     "state=STOPPED_normalized",
			filter:   `state=STOPPED`,
			wantWIDs: []int64{2},
		},
	}

	for _, tc := range tests {
		t.Run(tc.filter, func(t *testing.T) {
			gotWorkUnits, _, err := storage.ListWorkUnits(ctx, exp.GetName(), 10, "", tc.filter, "", 0)
			if err != nil {
				t.Fatalf("ListWorkUnits failed: %v", err)
			}
			var gotWIDs []int64
			for _, wu := range gotWorkUnits {
				_, wid, _ := validator.ValidateWorkUnitName(wu.GetName())
				gotWIDs = append(gotWIDs, wid)
			}
			sort.Slice(gotWIDs, func(i, j int) bool { return gotWIDs[i] < gotWIDs[j] })
			if diff := cmp.Diff(tc.wantWIDs, gotWIDs); diff != "" {
				t.Errorf("ListWorkUnits(%s) returned unexpected WIDs (-want +got):\n%s", tc.filter, diff)
			}
		})
	}
}

func TestListWorkUnits_OrderBy(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storage, err := NewStorage(essClient)
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}

	// Create one experiment and some work units for it.
	xid := int64(4)
	exp := createBlankExperiment(ctx, t, storage, xid)
	for i := 0; i < 3; i++ {
		createBlankWorkUnit(ctx, t, storage, xid, int64(i+1))
	}

	tests := []struct {
		orderBy  string
		wantWIDs []int64
	}{
		{"created desc", []int64{3, 2, 1}},
		{"created", []int64{1, 2, 3}},
		{"", []int64{1, 2, 3}},
	}

	for _, tc := range tests {
		t.Run(tc.orderBy, func(t *testing.T) {
			gotWorkUnits, _, err := storage.ListWorkUnits(ctx, exp.GetName(), 10, "", "", tc.orderBy, 0)
			if err != nil {
				t.Fatalf("ListWorkUnits failed: %v", err)
			}
			var gotWIDs []int64
			for _, wu := range gotWorkUnits {
				_, wid, _ := validator.ValidateWorkUnitName(wu.GetName())
				gotWIDs = append(gotWIDs, wid)
			}
			if diff := cmp.Diff(tc.wantWIDs, gotWIDs); diff != "" {
				t.Errorf("ListWorkUnits with orderBy=%q returned unexpected WIDs (-want +got):\n%s", tc.orderBy, diff)
			}
		})
	}
}

func TestListWorkUnits_ParentFiltering(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storage, err := NewStorage(essClient)
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}

	// Create two experiments and one work unit for each.
	xid1 := int64(5)
	exp1 := createBlankExperiment(ctx, t, storage, xid1)
	createBlankWorkUnit(ctx, t, storage, xid1, 1)

	xid2 := int64(6)
	_ = createBlankExperiment(ctx, t, storage, xid2)
	createBlankWorkUnit(ctx, t, storage, xid2, 1)

	// List work units for exp1.
	gotWorkUnits, _, err := storage.ListWorkUnits(ctx, exp1.GetName(), 10, "", "", "", 0)
	if err != nil {
		t.Fatalf("ListWorkUnits failed: %v", err)
	}
	if len(gotWorkUnits) != 1 {
		t.Errorf("ListWorkUnits for %s returned %d work units, want 1", exp1.GetName(), len(gotWorkUnits))
	}
	gotXID, _, err := validator.ValidateWorkUnitName(gotWorkUnits[0].GetName())
	if err != nil {
		t.Fatalf("ValidateWorkUnitName failed: %v", err)
	}
	if gotXID != xid1 {
		t.Errorf("ListWorkUnits for %s returned work unit with xid %d, want %d", exp1.GetName(), gotXID, xid1)
	}
}

func TestListWorkUnits_InvalidParent(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storage, err := NewStorage(essClient)
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}

	_, _, err = storage.ListWorkUnits(ctx, "invalid-parent", 10, "", "", "", 0)
	if err == nil {
		t.Errorf("ListWorkUnits with invalid parent succeeded, want error")
	}
}

func TestExtractWorkUnitCursor(t *testing.T) {
	now := time.Now()
	nowProto := timestamppb.New(now)

	testCases := []struct {
		name    string
		entry   *essmsgpb.WorkUnit
		want    map[string]any
		wantErr bool
	}{
		{
			name: "valid_work_unit_entry",
			entry: &essmsgpb.WorkUnit{
				Name:       "experiments/123/workUnits/1",
				CreateTime: nowProto,
				UpdateTime: nowProto,
			},
			want: map[string]any{
				"xid":          int64(123),
				"work_unit_id": int64(1),
				"created":      now,
				"updated":      now,
			},
			wantErr: false,
		},
		{
			name: "valid_work_unit_entry_no_timestamps",
			entry: &essmsgpb.WorkUnit{
				Name: "experiments/123/workUnits/2",
			},
			want: map[string]any{
				"xid":          int64(123),
				"work_unit_id": int64(2),
			},
			wantErr: false,
		},
		{
			name: "invalid_work_unit_name",
			entry: &essmsgpb.WorkUnit{
				Name: "invalid/789",
			},
			want:    nil,
			wantErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := extractWorkUnitCursor(tc.entry)
			if (err != nil) != tc.wantErr {
				t.Errorf("extractWorkUnitCursor(%v) returned error %v, wantErr=%t", tc.entry, err, tc.wantErr)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("extractWorkUnitCursor(%v) returned diff (-want +got):\n%s", tc.entry, diff)
			}
		})
	}
}
