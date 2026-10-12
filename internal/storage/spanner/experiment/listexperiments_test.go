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
package experiment_test

import (
	"context"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	essmsgpb "github.com/google/xmc/api/protos/experiment"
	"github.com/google/xmc/internal/storage/spanner"
	"github.com/google/xmc/internal/storage/spanner/emulator"
	"github.com/google/xmc/internal/storage/spanner/experiment"
	"github.com/google/xmc/internal/storage/spanner/utils"
	"github.com/google/xmc/internal/validator"
	"google.golang.org/protobuf/testing/protocmp"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
)

func TestListExperiments(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storage, _ := spanner.NewStorage(essClient)

	// Create some experiments.
	var experiments []*essmsgpb.Experiment
	for i := 1; i < 6; i++ {
		exp := spanner.BlankExperimentProto(int64(i))
		exp.Title = fmt.Sprintf("title-%d", i)
		exp.Creator = fmt.Sprintf("creator-%d", i)
		exp.Notes = "test-notes"
		exp.Tags = []string{"tag1", "tag2"}
		exp.Urls = []*essmsgpb.Url{
			{
				Url: "http://test.com",
			},
		}
		createdExp := spanner.CreateAndStoreExperiment(ctx, t, storage, exp)
		experiments = append(experiments, createdExp)
	}

	// List all experiments.
	gotExperiments, _, err := storage.ListExperiments(ctx, 10, "", "", "", 0)
	if err != nil {
		t.Fatalf("ListExperiments failed: %v", err)
	}
	if len(gotExperiments) != 5 {
		t.Errorf("ListExperiments returned %d experiments, want 5", len(gotExperiments))
	}

	// Sort experiments by XID for comparison.
	sort.Slice(experiments, func(i, j int) bool {
		iXID, _ := validator.ValidateExperimentName(experiments[i].GetName())
		jXID, _ := validator.ValidateExperimentName(experiments[j].GetName())
		return iXID < jXID
	})
	sort.Slice(gotExperiments, func(i, j int) bool {
		iXID, _ := validator.ValidateExperimentName(gotExperiments[i].GetName())
		jXID, _ := validator.ValidateExperimentName(gotExperiments[j].GetName())
		return iXID < jXID
	})
	if diff := cmp.Diff(experiments, gotExperiments, protocmp.Transform(),
		protocmp.IgnoreFields(&essmsgpb.Experiment{}, "acls"),
	); diff != "" {
		t.Errorf("ListExperiments() returned diff (-want +got):\n%s", diff)
	}
}

func TestListExperiments_Pagination(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storage, _ := spanner.NewStorage(essClient)

	// Create some experiments.
	for i := 1; i < 6; i++ {
		spanner.CreateBlankExperiment(ctx, t, storage, int64(i))
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
				experiments, nextPageToken, err := storage.ListExperiments(ctx, int32(tc.pageSize), pageToken, "", "xid", 0)
				if err != nil {
					t.Fatalf("ListExperiments failed: %v", err)
				}
				gotCounts = append(gotCounts, len(experiments))
				pageToken = nextPageToken
				if diff := cmp.Diff(tc.wantCounts[i], len(experiments)); diff != "" {
					t.Errorf("ListExperiments returned unexpected page count (-want +got):\n%s", diff)
				}
				if i < len(tc.wantCounts)-1 && nextPageToken == "" {
					t.Errorf("ListExperiments returned empty nextPageToken on page %d, want non-empty", i+1)
				}
			}
			if pageToken != "" {
				t.Errorf("ListExperiments returned non-empty nextPageToken on last page, want empty")
			}
			if diff := cmp.Diff(tc.wantCounts, gotCounts); diff != "" {
				t.Errorf("ListExperiments returned unexpected page counts (-want +got):\n%s", diff)
			}
		})
	}
}

func TestListExperiments_Filter(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storage, _ := spanner.NewStorage(essClient)

	// Create some experiments
	exp1 := spanner.BlankExperimentProto(1)
	exp1.Creator = "creator-1"
	exp1.Title = "title-1"
	spanner.CreateAndStoreExperiment(ctx, t, storage, exp1)
	exp2 := spanner.BlankExperimentProto(2)
	exp2.Creator = "creator-2"
	exp2.Title = "title-2"
	spanner.CreateAndStoreExperiment(ctx, t, storage, exp2)

	// List with filter.
	gotExperiments, _, err := storage.ListExperiments(ctx, 10, "", "creator=\"creator-1\"", "", 0)
	if err != nil {
		t.Fatalf("ListExperiments failed: %v", err)
	}
	if len(gotExperiments) != 1 {
		t.Errorf("ListExperiments with filter returned %d entries, want 1", len(gotExperiments))
	}
	if gotExperiments[0].GetCreator() != "creator-1" {
		t.Errorf("ListExperiments with filter returned entry with creator %q, want creator-1", gotExperiments[0].GetCreator())
	}
}

func TestListExperiments_OrderBy(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storage, _ := spanner.NewStorage(essClient)

	// Create some experiments.
	var xids []int64
	for i := 1; i < 4; i++ {
		exp := spanner.BlankExperimentProto(int64(i))
		created := spanner.CreateAndStoreExperiment(ctx, t, storage, exp)
		xid, _ := validator.ValidateExperimentName(created.GetName())
		xids = append(xids, xid)
	}

	// List with orderBy xid desc.
	gotExperiments, _, err := storage.ListExperiments(ctx, 10, "", "", "xid desc", 0)
	if err != nil {
		t.Fatalf("ListExperiments failed: %v", err)
	}
	if len(gotExperiments) != 3 {
		t.Fatalf("ListExperiments with orderby returned %d experiments, want 3", len(gotExperiments))
	}

	gotXID0, _ := validator.ValidateExperimentName(gotExperiments[0].GetName())
	gotXID1, _ := validator.ValidateExperimentName(gotExperiments[1].GetName())
	gotXID2, _ := validator.ValidateExperimentName(gotExperiments[2].GetName())

	if gotXID0 != xids[2] || gotXID1 != xids[1] || gotXID2 != xids[0] {
		t.Errorf("ListExperiments with orderBy='xid desc' returned experiments in wrong order: got %d, %d, %d, want %d, %d, %d", gotXID0, gotXID1, gotXID2, xids[2], xids[1], xids[0])
	}
}

func TestListExperiments_Complex(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storage, _ := spanner.NewStorage(essClient)

	testData := []struct {
		creator string
		title   string
	}{
		{"C", "T1"},
		{"B", "T2"},
		{"A", "T1"},
		{"C", "T2"},
		{"B", "T1"},
	}
	experiments := make([]*essmsgpb.Experiment, len(testData))
	for i, d := range testData {
		exp := spanner.BlankExperimentProto(int64(i + 1))
		exp.Creator = d.creator
		exp.Title = d.title
		createdExp := spanner.CreateAndStoreExperiment(ctx, t, storage, exp)
		experiments[i] = createdExp
	}

	// List page 1: filter by title=T1, order by created desc, page size 2.
	filter := `title="T1"`
	orderBy := "created desc"
	pageSize := int32(2)

	gotPage1, page1Token, err := storage.ListExperiments(ctx, pageSize, "", filter, orderBy, 0)
	if err != nil {
		t.Fatalf("ListExperiments page 1 failed: %v", err)
	}
	if page1Token == "" {
		t.Errorf("ListExperiments page 1 got empty next page token, want non-empty")
	}

	wantPage1 := []*essmsgpb.Experiment{experiments[4], experiments[2]} // B, A
	cmpOpts := []cmp.Option{
		protocmp.Transform(),
		protocmp.IgnoreFields(&essmsgpb.Experiment{}, "acls"),
	}
	if diff := cmp.Diff(wantPage1, gotPage1, cmpOpts...); diff != "" {
		t.Errorf("ListExperiments page 1 returned diff (-want +got):\n%s", diff)
	}

	// List page 2.
	gotPage2, page2Token, err := storage.ListExperiments(ctx, pageSize, page1Token, filter, orderBy, 0)
	if err != nil {
		t.Fatalf("ListExperiments page 2 failed: %v", err)
	}
	if page2Token != "" {
		t.Errorf("ListExperiments page 2 got next page token %q, want empty", page2Token)
	}
	wantPage2 := []*essmsgpb.Experiment{experiments[0]} // C
	if diff := cmp.Diff(wantPage2, gotPage2, cmpOpts...); diff != "" {
		t.Errorf("ListExperiments page 2 returned diff (-want +got):\n%s", diff)
	}
}

func TestListExperiments_FilterFields(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storage, _ := spanner.NewStorage(essClient)

	// Create some experiments.
	exps := []*essmsgpb.Experiment{
		spanner.BlankExperimentProto(1),
		spanner.BlankExperimentProto(2),
		spanner.BlankExperimentProto(3),
	}
	exps[0].Creator = "creator-1"
	exps[0].Title = "title-1"
	exps[0].Tags = []string{"tag1"}
	exps[0].Notes = "notes-1"
	exps[0].Status.LaunchState = essmsgpb.ExperimentStatus_COMPLETED
	exps[1].Creator = "creator-2"
	exps[1].Title = "title-2"
	exps[1].Tags = []string{"tag2"}
	exps[1].Notes = "notes-2"
	exps[1].Status.ActiveState = essmsgpb.ExperimentStatus_INACTIVE
	exps[1].Status.LaunchState = essmsgpb.ExperimentStatus_FAILED
	exps[2].Creator = "creator-1"
	exps[2].Title = "title-3"
	exps[2].Tags = []string{"tag1", "tag3"}
	exps[2].Notes = "notes-3"
	exps[2].Status.LaunchState = essmsgpb.ExperimentStatus_LAUNCHING

	for _, exp := range exps {
		spanner.CreateAndStoreExperiment(ctx, t, storage, exp)
	}

	testCases := []struct {
		name   string
		filter string
		want   []int64 // xids of expected experiments
	}{
		{"xid", "xid=1", []int64{1}},
		{"title", `title="title-2"`, []int64{2}},
		{"creator", `creator="creator-1"`, []int64{1, 3}},
		{"creator-denormalized", `creator=creator-1`, []int64{1, 3}},
		{"tags", `tags:"tag1"`, []int64{1, 3}},
		{"notes", `notes="notes-3"`, []int64{3}},
		{"active_state", `active_state="ACTIVE"`, []int64{1, 3}},
		{"launch_state", `launch_state="FAILED"`, []int64{2}},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			gotExperiments, _, err := storage.ListExperiments(ctx, 10, "", tc.filter, "xid", 0)
			if err != nil {
				t.Fatalf("ListExperiments failed: %v", err)
			}
			var gotXIDs []int64
			for _, exp := range gotExperiments {
				xid, _ := validator.ValidateExperimentName(exp.GetName())
				gotXIDs = append(gotXIDs, xid)
			}
			if diff := cmp.Diff(tc.want, gotXIDs); diff != "" {
				t.Errorf("ListExperiments with filter %q returned diff (-want +got):\n%s", tc.filter, diff)
			}
		})
	}
}

func TestListExperiments_OrderByFields(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storage, _ := spanner.NewStorage(essClient)

	// Create some experiments.
	xids := []int64{3, 1, 2}
	for _, xid := range xids {
		exp := spanner.BlankExperimentProto(xid)
		spanner.CreateAndStoreExperiment(ctx, t, storage, exp)
	}

	testCases := []struct {
		name    string
		orderBy string
		want    []int64 // xids of expected experiments in order
	}{
		{"xid asc", "xid", []int64{1, 2, 3}},
		{"xid desc", "xid desc", []int64{3, 2, 1}},
		{"created asc", "created", []int64{3, 1, 2}},
		{"created desc", "created desc", []int64{2, 1, 3}},
		{"updated asc", "updated", []int64{3, 1, 2}},
		{"updated desc", "updated desc", []int64{2, 1, 3}},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			gotExperiments, _, err := storage.ListExperiments(ctx, 10, "", "", tc.orderBy, 0)
			if err != nil {
				t.Fatalf("ListExperiments failed: %v", err)
			}
			var gotXIDs []int64
			for _, exp := range gotExperiments {
				xid, _ := validator.ValidateExperimentName(exp.GetName())
				gotXIDs = append(gotXIDs, xid)
			}
			if diff := cmp.Diff(tc.want, gotXIDs); diff != "" {
				t.Errorf("ListExperiments with orderBy %q returned diff (-want +got):\n%s", tc.orderBy, diff)
			}
		})
	}
}

func TestExtractExperimentCursor(t *testing.T) {
	now := time.Now()
	nowProto := timestamppb.New(now)

	testCases := []struct {
		name    string
		entry   *essmsgpb.Experiment
		want    map[string]any
		wantErr bool
	}{
		{
			name: "valid_experiment_entry",
			entry: &essmsgpb.Experiment{
				Name:         "experiments/123",
				CreateTime:   nowProto,
				UpdateTime:   nowProto,
				CompleteTime: nowProto,
			},
			want: map[string]any{
				"xid":              int64(123),
				"bit_reversed_xid": spannerutils.ReverseBits(123),
				"created":          now,
				"updated":          now,
				"completed":        now,
			},
			wantErr: false,
		},
		{
			name: "valid_experiment_entry_no_timestamps",
			entry: &essmsgpb.Experiment{
				Name: "experiments/456",
			},
			want: map[string]any{
				"xid":              int64(456),
				"bit_reversed_xid": spannerutils.ReverseBits(456),
				"completed":        nil,
			},
			wantErr: false,
		},
		{
			name: "invalid_experiment_name",
			entry: &essmsgpb.Experiment{
				Name: "invalid/789",
			},
			want:    nil,
			wantErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := experiment.ExtractExperimentCursor(tc.entry)
			if (err != nil) != tc.wantErr {
				t.Errorf("extractExperimentCursor(%v) returned error %v, wantErr=%t", tc.entry, err, tc.wantErr)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("extractExperimentCursor(%v) returned diff (-want +got):\n%s", tc.entry, diff)
			}
		})
	}
}

func TestListExperiments_PaginationDefaultOrderByAndNullableCompleted(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storage, _ := spanner.NewStorage(essClient)

	exp1 := spanner.BlankExperimentProto(1)
	exp1.Status.LaunchState = essmsgpb.ExperimentStatus_COMPLETED
	spanner.CreateAndStoreExperiment(ctx, t, storage, exp1)

	exp2 := spanner.BlankExperimentProto(2)
	exp2.Status.LaunchState = essmsgpb.ExperimentStatus_LAUNCHING
	spanner.CreateAndStoreExperiment(ctx, t, storage, exp2)

	exp3 := spanner.BlankExperimentProto(3)
	exp3.Status.LaunchState = essmsgpb.ExperimentStatus_COMPLETED
	spanner.CreateAndStoreExperiment(ctx, t, storage, exp3)

	for _, orderBy := range []string{"", "completed", "completed desc"} {
		t.Run("orderBy="+orderBy, func(t *testing.T) {
			var allXIDs []int64
			pageToken := ""
			for page := 0; page < 5; page++ {
				got, nextToken, err := storage.ListExperiments(ctx, 1, pageToken, "", orderBy, 0)
				if err != nil {
					t.Fatalf("ListExperiments(orderBy=%q, page=%d) failed: %v", orderBy, page, err)
				}
				for _, e := range got {
					xid, _ := validator.ValidateExperimentName(e.GetName())
					allXIDs = append(allXIDs, xid)
				}
				pageToken = nextToken
				if pageToken == "" {
					break
				}
			}
			sort.Slice(allXIDs, func(i, j int) bool { return allXIDs[i] < allXIDs[j] })
			if diff := cmp.Diff([]int64{1, 2, 3}, allXIDs); diff != "" {
				t.Errorf("Paginated ListExperiments(orderBy=%q) visited XIDs diff (-want +got):\n%s", orderBy, diff)
			}
		})
	}
}
