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
package workunit_test

import (
	"context"
	"fmt"
	"sort"
	"testing"
	"time"
	"xmanager_cloud/internal/storage/spanner"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/testing/protocmp"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/storage/spanner/emulator"
	"xmanager_cloud/internal/storage/spanner/utils"
	"xmanager_cloud/internal/storage/spanner/workunit"
	"xmanager_cloud/internal/validator"
)

func TestListWorkUnits(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storage, err := spanner.NewStorage(essClient)
	if err != nil {
		t.Fatalf("spanner.NewStorage failed: %v", err)
	}

	// Create one experiment and some work units for it.
	xid := int64(1)
	exp := spanner.CreateBlankExperiment(ctx, t, storage, xid)
	var workUnits []*essmsgpb.WorkUnit
	for i := 0; i < 5; i++ {
		wu := spanner.CreateBlankWorkUnit(ctx, t, storage, xid, int64(i+1))
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
	storage, err := spanner.NewStorage(essClient)
	if err != nil {
		t.Fatalf("spanner.NewStorage failed: %v", err)
	}

	// Create one experiment and some work units for it.
	xid := int64(7)
	exp := spanner.CreateBlankExperiment(ctx, t, storage, xid)
	for i := 0; i < 5; i++ {
		spanner.CreateBlankWorkUnit(ctx, t, storage, xid, int64(i+1))
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
	storage, err := spanner.NewStorage(essClient)
	if err != nil {
		t.Fatalf("spanner.NewStorage failed: %v", err)
	}

	// Create one experiment and some work units for it.
	xid := int64(3)
	exp := spanner.CreateBlankExperiment(ctx, t, storage, xid)
	spanner.CreateBlankWorkUnit(ctx, t, storage, xid, 1)
	spanner.CreateBlankWorkUnitWithState(ctx, t, storage, xid, 2, essmsgpb.WorkUnitStatus_STOPPED)

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
	storage, err := spanner.NewStorage(essClient)
	if err != nil {
		t.Fatalf("spanner.NewStorage failed: %v", err)
	}

	// Create one experiment and some work units for it.
	xid := int64(4)
	exp := spanner.CreateBlankExperiment(ctx, t, storage, xid)
	for i := 0; i < 3; i++ {
		spanner.CreateBlankWorkUnit(ctx, t, storage, xid, int64(i+1))
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
	storage, err := spanner.NewStorage(essClient)
	if err != nil {
		t.Fatalf("spanner.NewStorage failed: %v", err)
	}

	// Create two experiments and one work unit for each.
	xid1 := int64(5)
	exp1 := spanner.CreateBlankExperiment(ctx, t, storage, xid1)
	spanner.CreateBlankWorkUnit(ctx, t, storage, xid1, 1)

	xid2 := int64(6)
	_ = spanner.CreateBlankExperiment(ctx, t, storage, xid2)
	spanner.CreateBlankWorkUnit(ctx, t, storage, xid2, 1)

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
	storage, err := spanner.NewStorage(essClient)
	if err != nil {
		t.Fatalf("spanner.NewStorage failed: %v", err)
	}

	_, _, err = storage.ListWorkUnits(ctx, "invalid-parent", 10, "", "", "", 0)
	if err == nil {
		t.Errorf("ListWorkUnits with invalid parent succeeded, want error")
	}
}

// orderingFixtureBaseTime is the reference time for the ordering fixture's completion times.
var orderingFixtureBaseTime = time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)

// orderingFixtureWorkUnit describes one work unit of the tie-heavy ordering fixture.
type orderingFixtureWorkUnit struct {
	wid          int64
	state        essmsgpb.WorkUnitStatus_WorkUnitState
	completeTime *time.Time
}

func timeAt(offset time.Duration) *time.Time {
	t := orderingFixtureBaseTime.Add(offset)
	return &t
}

// orderingFixture has ties on state (2/4 FAILED, 1/5 COMPLETED), on completion time (2/4) and on
// NULL completion time (3/5), so page boundaries fall between tied rows.
var orderingFixture = []orderingFixtureWorkUnit{
	{wid: 1, state: essmsgpb.WorkUnitStatus_COMPLETED, completeTime: timeAt(2 * time.Hour)},
	{wid: 2, state: essmsgpb.WorkUnitStatus_FAILED, completeTime: timeAt(time.Hour)},
	{wid: 3, state: essmsgpb.WorkUnitStatus_RUNNING},
	{wid: 4, state: essmsgpb.WorkUnitStatus_FAILED, completeTime: timeAt(time.Hour)},
	{wid: 5, state: essmsgpb.WorkUnitStatus_COMPLETED},
}

func createWorkUnits(ctx context.Context, t *testing.T, storage *spanner.Storage, xid int64, fixture []orderingFixtureWorkUnit) {
	t.Helper()
	for _, f := range fixture {
		wu := spanner.BlankWorkUnitProto(xid, f.wid)
		wu.CurrentWorkUnitExecution.Status.State = f.state
		if f.completeTime != nil {
			wu.CurrentWorkUnitExecution.CompleteTime = timestamppb.New(*f.completeTime)
		}
		spanner.CreateAndStoreWorkUnit(ctx, t, storage, wu)
	}
}

func workUnitIDs(t *testing.T, workUnits []*essmsgpb.WorkUnit) []int64 {
	t.Helper()
	var wids []int64
	for _, wu := range workUnits {
		_, wid, err := validator.ValidateWorkUnitName(wu.GetName())
		if err != nil {
			t.Fatalf("ValidateWorkUnitName(%q) failed: %v", wu.GetName(), err)
		}
		wids = append(wids, wid)
	}
	return wids
}

// listAllWorkUnitIDs walks every page and returns the WIDs in the order they were returned.
func listAllWorkUnitIDs(ctx context.Context, t *testing.T, storage *spanner.Storage, parent string, pageSize int32, filter, orderBy string) []int64 {
	t.Helper()
	var wids []int64
	pageToken := ""
	for page := 0; page < 20; page++ {
		workUnits, nextPageToken, err := storage.ListWorkUnits(ctx, parent, pageSize, pageToken, filter, orderBy, 0)
		if err != nil {
			t.Fatalf("ListWorkUnits(filter=%q, orderBy=%q, page=%d) failed: %v", filter, orderBy, page, err)
		}
		wids = append(wids, workUnitIDs(t, workUnits)...)
		if nextPageToken == "" {
			return wids
		}
		pageToken = nextPageToken
	}
	t.Fatalf("ListWorkUnits(filter=%q, orderBy=%q) did not finish paginating", filter, orderBy)
	return nil
}

// orderByFieldTestCases are shared by the single-page and paginated ordering tests. The state
// order follows the enum numbers (RUNNING=4 < COMPLETED=10 < FAILED=11); ties are broken by
// work_unit_id ascending. Spanner sorts NULLs first in ASC and last in DESC.
var orderByFieldTestCases = []struct {
	orderBy  string
	wantWIDs []int64
}{
	{"work_unit_id desc", []int64{5, 4, 3, 2, 1}},
	{"state", []int64{3, 1, 5, 2, 4}},
	{"state desc", []int64{2, 4, 1, 5, 3}},
	{"execution_completed", []int64{3, 5, 2, 4, 1}},
	{"execution_completed desc", []int64{1, 2, 4, 3, 5}},
}

func TestListWorkUnits_OrderByField(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storage, err := spanner.NewStorage(essClient)
	if err != nil {
		t.Fatalf("spanner.NewStorage failed: %v", err)
	}
	xid := int64(8)
	exp := spanner.CreateBlankExperiment(ctx, t, storage, xid)
	createWorkUnits(ctx, t, storage, xid, orderingFixture)

	for _, tc := range orderByFieldTestCases {
		t.Run(tc.orderBy, func(t *testing.T) {
			gotWorkUnits, _, err := storage.ListWorkUnits(ctx, exp.GetName(), 10, "", "", tc.orderBy, 0)
			if err != nil {
				t.Fatalf("ListWorkUnits(orderBy=%q) failed: %v", tc.orderBy, err)
			}
			if diff := cmp.Diff(tc.wantWIDs, workUnitIDs(t, gotWorkUnits)); diff != "" {
				t.Errorf("ListWorkUnits(orderBy=%q) returned unexpected WIDs (-want +got):\n%s", tc.orderBy, diff)
			}
		})
	}
}

func TestListWorkUnits_PaginationByOrderField(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storage, err := spanner.NewStorage(essClient)
	if err != nil {
		t.Fatalf("spanner.NewStorage failed: %v", err)
	}
	xid := int64(9)
	exp := spanner.CreateBlankExperiment(ctx, t, storage, xid)
	createWorkUnits(ctx, t, storage, xid, orderingFixture)

	type testCase struct {
		name     string
		filter   string
		orderBy  string
		wantWIDs []int64
	}
	var tests []testCase
	for _, tc := range orderByFieldTestCases {
		tests = append(tests, testCase{name: tc.orderBy, orderBy: tc.orderBy, wantWIDs: tc.wantWIDs})
	}
	tests = append(tests,
		// state is equality-filtered, so it is dropped from the cursor predicate and pagination
		// relies on the work_unit_id tie-breaker.
		testCase{name: "equality_filter_on_sort_field", filter: `state="FAILED"`, orderBy: "state", wantWIDs: []int64{2, 4}},
		testCase{name: "or_filter_with_nullable_sort", filter: `state = "FAILED" OR state = "COMPLETED"`, orderBy: "execution_completed desc", wantWIDs: []int64{1, 2, 4, 5}},
		// state is OR-filtered (not equality-pruned) and is also the sort field, so it stays in the
		// cursor predicate.
		testCase{name: "or_filter_on_sort_field", filter: `state = "FAILED" OR state = "COMPLETED"`, orderBy: "state", wantWIDs: []int64{1, 5, 2, 4}},
		testCase{name: "or_filter_on_sort_field_desc", filter: `state = "FAILED" OR state = "COMPLETED"`, orderBy: "state desc", wantWIDs: []int64{2, 4, 1, 5}},
	)

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := listAllWorkUnitIDs(ctx, t, storage, exp.GetName(), 1, tc.filter, tc.orderBy)
			if diff := cmp.Diff(tc.wantWIDs, got); diff != "" {
				t.Errorf("paginated ListWorkUnits(filter=%q, orderBy=%q) returned unexpected WIDs (-want +got):\n%s", tc.filter, tc.orderBy, diff)
			}
		})
	}
}

func TestListWorkUnits_FilterByAnyState(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storage, err := spanner.NewStorage(essClient)
	if err != nil {
		t.Fatalf("spanner.NewStorage failed: %v", err)
	}
	xid := int64(10)
	exp := spanner.CreateBlankExperiment(ctx, t, storage, xid)
	createWorkUnits(ctx, t, storage, xid, []orderingFixtureWorkUnit{
		{wid: 1, state: essmsgpb.WorkUnitStatus_FAILED},
		{wid: 2, state: essmsgpb.WorkUnitStatus_STOPPED},
		{wid: 3, state: essmsgpb.WorkUnitStatus_RUNNING},
		{wid: 4, state: essmsgpb.WorkUnitStatus_PARTIALLY_FAILED},
	})

	tests := []struct {
		filter   string
		wantWIDs []int64
	}{
		{`state = "FAILED" OR state = "STOPPED"`, []int64{1, 2}},
		{`state = "PARTIALLY_FAILED"`, []int64{4}},
	}
	for _, tc := range tests {
		t.Run(tc.filter, func(t *testing.T) {
			got := listAllWorkUnitIDs(ctx, t, storage, exp.GetName(), 10, tc.filter, "")
			if diff := cmp.Diff(tc.wantWIDs, got); diff != "" {
				t.Errorf("ListWorkUnits(filter=%q) returned unexpected WIDs (-want +got):\n%s", tc.filter, diff)
			}
		})
	}
}

func TestListWorkUnits_FilterIsolation(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storage, err := spanner.NewStorage(essClient)
	if err != nil {
		t.Fatalf("spanner.NewStorage failed: %v", err)
	}
	fixture := []orderingFixtureWorkUnit{
		{wid: 1, state: essmsgpb.WorkUnitStatus_FAILED},
		{wid: 2, state: essmsgpb.WorkUnitStatus_STOPPED},
		{wid: 3, state: essmsgpb.WorkUnitStatus_RUNNING},
	}
	targetXID, otherXID := int64(11), int64(12)
	target := spanner.CreateBlankExperiment(ctx, t, storage, targetXID)
	spanner.CreateBlankExperiment(ctx, t, storage, otherXID)
	createWorkUnits(ctx, t, storage, targetXID, fixture)
	createWorkUnits(ctx, t, storage, otherXID, fixture)

	filter := `state = "FAILED" OR state = "STOPPED"`
	var gotXIDs []int64
	pageToken := ""
	for page := 0; page < 10; page++ {
		workUnits, nextPageToken, err := storage.ListWorkUnits(ctx, target.GetName(), 1, pageToken, filter, "", 0)
		if err != nil {
			t.Fatalf("ListWorkUnits(filter=%q, page=%d) failed: %v", filter, page, err)
		}
		for _, wu := range workUnits {
			xid, _, err := validator.ValidateWorkUnitName(wu.GetName())
			if err != nil {
				t.Fatalf("ValidateWorkUnitName(%q) failed: %v", wu.GetName(), err)
			}
			gotXIDs = append(gotXIDs, xid)
		}
		if nextPageToken == "" {
			break
		}
		pageToken = nextPageToken
	}
	if diff := cmp.Diff([]int64{targetXID, targetXID}, gotXIDs); diff != "" {
		t.Errorf("ListWorkUnits(filter=%q) returned work units from unexpected experiments (-want +got):\n%s", filter, diff)
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
				"xid":                 int64(123),
				"bit_reversed_xid":    spannerutils.ReverseBits(123),
				"work_unit_id":        int64(1),
				"created":             now,
				"updated":             now,
				"state":               "WORK_UNIT_STATE_UNSPECIFIED",
				"execution_completed": nil,
			},
			wantErr: false,
		},
		{
			name: "valid_work_unit_entry_no_timestamps",
			entry: &essmsgpb.WorkUnit{
				Name: "experiments/123/workUnits/2",
			},
			want: map[string]any{
				"xid":                 int64(123),
				"bit_reversed_xid":    spannerutils.ReverseBits(123),
				"work_unit_id":        int64(2),
				"state":               "WORK_UNIT_STATE_UNSPECIFIED",
				"execution_completed": nil,
			},
			wantErr: false,
		},
		{
			name: "completed_execution",
			entry: &essmsgpb.WorkUnit{
				Name: "experiments/123/workUnits/3",
				CurrentWorkUnitExecution: &essmsgpb.WorkUnitExecution{
					Status:       &essmsgpb.WorkUnitStatus{State: essmsgpb.WorkUnitStatus_FAILED},
					CompleteTime: nowProto,
				},
			},
			want: map[string]any{
				"xid":                 int64(123),
				"bit_reversed_xid":    spannerutils.ReverseBits(123),
				"work_unit_id":        int64(3),
				"state":               "FAILED",
				"execution_completed": now,
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
			got, err := workunit.ExtractWorkUnitCursor(tc.entry)
			if (err != nil) != tc.wantErr {
				t.Errorf("extractWorkUnitCursor(%v) returned error %v, wantErr=%t", tc.entry, err, tc.wantErr)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("extractWorkUnitCursor(%v) returned diff (-want +got):\n%s", tc.entry, diff)
			}
		})
	}
}
