// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package experiment

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	gocmp "github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/testing/protocmp"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	pb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/query_generator/querytools"
	"xmanager_cloud/internal/storage/postgres/schema/experimentschema"
	"xmanager_cloud/internal/validator"
)

// sortByViewDefaultOrder sorts exps in the default order declared by experimentschema's TableView,
// matching querytools.GenerateQuery: OrderByFieldsWithSortDirection when present, otherwise
// OrderedIndicesWithSortDirection.
func sortByViewDefaultOrder(t *testing.T, exps []*pb.Experiment) []*pb.Experiment {
	t.Helper()
	view := experimentschema.ExperimentTables[0]
	order := view.OrderByFieldsWithSortDirection
	if len(order) == 0 {
		order = view.OrderedIndicesWithSortDirection
	}
	sorted := slices.Clone(exps)
	slices.SortFunc(sorted, func(a, b *pb.Experiment) int {
		for _, uoi := range order {
			c := compareExperimentField(t, a, b, string(uoi.TableField))
			if uoi.SortOrder == querytools.Desc {
				c = -c
			}
			if c != 0 {
				return c
			}
		}
		return 0
	})
	return sorted
}

// compareExperimentField compares field on a and b for sortByViewDefaultOrder.
func compareExperimentField(t *testing.T, a, b *pb.Experiment, field string) int {
	t.Helper()
	switch field {
	case "xid":
		aXID, err := validator.ValidateExperimentName(a.GetName())
		if err != nil {
			t.Fatalf("ValidateExperimentName(%q) unexpected error: %v", a.GetName(), err)
		}
		bXID, err := validator.ValidateExperimentName(b.GetName())
		if err != nil {
			t.Fatalf("ValidateExperimentName(%q) unexpected error: %v", b.GetName(), err)
		}
		return cmp.Compare(aXID, bXID)
	case "created":
		return a.GetCreateTime().AsTime().Compare(b.GetCreateTime().AsTime())
	case "updated":
		return a.GetUpdateTime().AsTime().Compare(b.GetUpdateTime().AsTime())
	default:
		t.Fatalf("unsupported default order field %q", field)
		return 0
	}
}

func TestListExperiments(t *testing.T) {
	ctx := t.Context()
	repo, _ := newTestRepository(t)

	var experiments []*pb.Experiment
	for i := 1; i < 6; i++ {
		exp := blankExperimentProto(int64(i))
		exp.Title = fmt.Sprintf("title-%d", i)
		exp.Creator = fmt.Sprintf("creator-%d", i)
		exp.Notes = "test-notes"
		exp.Tags = []string{"tag1", "tag2"}
		exp.Urls = []*pb.Url{{Url: "http://test.com"}}
		experiments = append(experiments, createExperiment(ctx, t, repo, exp))
	}

	gotExperiments, nextPageToken, err := repo.ListExperiments(ctx, 10, "", "", "", 0)
	if err != nil {
		t.Fatalf("ListExperiments() unexpected error: %v", err)
	}
	if nextPageToken != "" {
		t.Errorf("ListExperiments() nextPageToken = %q, want empty", nextPageToken)
	}
	want := sortByViewDefaultOrder(t, experiments)
	if diff := gocmp.Diff(want, gotExperiments, protocmp.Transform()); diff != "" {
		t.Errorf("ListExperiments() mismatch (-want +got):\n%s", diff)
	}
}

func TestListExperiments_Pagination(t *testing.T) {
	ctx := t.Context()
	repo, _ := newTestRepository(t)

	base := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	createExperimentsAt(ctx, t, repo, base, 1, 2, 3, 4, 5)

	tests := []struct {
		pageSize   int32
		wantCounts []int
	}{
		{pageSize: 3, wantCounts: []int{3, 2}},
		{pageSize: 5, wantCounts: []int{5}},
		{pageSize: 1, wantCounts: []int{1, 1, 1, 1, 1}},
	}

	for _, tc := range tests {
		t.Run(fmt.Sprintf("pageSize=%d", tc.pageSize), func(t *testing.T) {
			var gotCounts []int
			var pageToken string
			for i := range tc.wantCounts {
				experiments, nextPageToken, err := repo.ListExperiments(ctx, tc.pageSize, pageToken, "", "xid", 0)
				if err != nil {
					t.Fatalf("ListExperiments() page %d unexpected error: %v", i+1, err)
				}
				gotCounts = append(gotCounts, len(experiments))
				pageToken = nextPageToken
				if i < len(tc.wantCounts)-1 && nextPageToken == "" {
					t.Errorf("ListExperiments() page %d nextPageToken = empty, want non-empty", i+1)
				}
			}
			if pageToken != "" {
				t.Errorf("ListExperiments() last page nextPageToken = %q, want empty", pageToken)
			}
			if diff := gocmp.Diff(tc.wantCounts, gotCounts); diff != "" {
				t.Errorf("ListExperiments() page counts mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// Multi-page pagination with empty orderBy must return the view's declared default order
// consistently across page boundaries.
func TestListExperiments_MultiPageDefaultOrdering(t *testing.T) {
	ctx := t.Context()
	repo, _ := newTestRepository(t)

	// XIDs and create times are given in opposite orders so that XID order and create-time order
	// are distinct.
	base := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	created := createExperimentsAt(ctx, t, repo, base, 70, 20, 50, 10, 60, 30, 40)
	want := sortByViewDefaultOrder(t, created)

	for _, pageSize := range []int32{1, 2, 3, 4} {
		t.Run(fmt.Sprintf("pageSize=%d", pageSize), func(t *testing.T) {
			got, pages := listAllPages(ctx, t, repo, pageSize, "", "")
			if pages < 2 {
				t.Fatalf("listAllPages(pageSize=%d) read %d pages, want >= 2", pageSize, pages)
			}
			if diff := gocmp.Diff(want, got, protocmp.Transform()); diff != "" {
				t.Errorf("listAllPages(pageSize=%d, orderBy=\"\") mismatch (-want +got):\n%s", pageSize, diff)
			}
		})
	}
}

func TestListExperiments_Filter(t *testing.T) {
	ctx := t.Context()
	repo, _ := newTestRepository(t)

	exp1 := blankExperimentProto(1)
	exp1.Creator = "creator-1"
	exp1.Title = "title-1"
	createExperiment(ctx, t, repo, exp1)

	exp2 := blankExperimentProto(2)
	exp2.Creator = "creator-2"
	exp2.Title = "title-2"
	createExperiment(ctx, t, repo, exp2)

	gotExperiments, _, err := repo.ListExperiments(ctx, 10, "", `creator="creator-1"`, "", 0)
	if err != nil {
		t.Fatalf("ListExperiments() unexpected error: %v", err)
	}
	if len(gotExperiments) != 1 {
		t.Fatalf("ListExperiments() returned %d entries, want 1", len(gotExperiments))
	}
	if gotExperiments[0].GetCreator() != "creator-1" {
		t.Errorf("ListExperiments() Creator = %q, want \"creator-1\"", gotExperiments[0].GetCreator())
	}
}

func TestListExperiments_OrderBy(t *testing.T) {
	ctx := t.Context()
	repo, _ := newTestRepository(t)

	base := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	createExperimentsAt(ctx, t, repo, base, 1, 2, 3)

	gotExperiments, _, err := repo.ListExperiments(ctx, 10, "", "", "xid desc", 0)
	if err != nil {
		t.Fatalf("ListExperiments() unexpected error: %v", err)
	}
	if diff := gocmp.Diff([]int64{3, 2, 1}, xidsOf(t, gotExperiments)); diff != "" {
		t.Errorf("ListExperiments(orderBy=\"xid desc\") XIDs mismatch (-want +got):\n%s", diff)
	}
}

func TestListExperiments_Complex(t *testing.T) {
	ctx := t.Context()
	repo, _ := newTestRepository(t)

	base := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
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
	experiments := make([]*pb.Experiment, len(testData))
	for i, d := range testData {
		exp := blankExperimentProto(int64(i + 1))
		exp.Creator = d.creator
		exp.Title = d.title
		exp.CreateTime = timestamppb.New(base.Add(time.Duration(i) * time.Second))
		experiments[i] = createExperiment(ctx, t, repo, exp)
	}

	// List page 1: filter by title=T1, order by created desc, page size 2.
	filter := `title="T1"`
	orderBy := "created desc"
	pageSize := int32(2)

	gotPage1, page1Token, err := repo.ListExperiments(ctx, pageSize, "", filter, orderBy, 0)
	if err != nil {
		t.Fatalf("ListExperiments() page 1 unexpected error: %v", err)
	}
	if page1Token == "" {
		t.Fatalf("ListExperiments() page 1 nextPageToken = empty, want non-empty")
	}

	wantPage1 := []*pb.Experiment{experiments[4], experiments[2]} // B, A
	if diff := gocmp.Diff(wantPage1, gotPage1, protocmp.Transform()); diff != "" {
		t.Errorf("ListExperiments() page 1 mismatch (-want +got):\n%s", diff)
	}

	// List page 2.
	gotPage2, page2Token, err := repo.ListExperiments(ctx, pageSize, page1Token, filter, orderBy, 0)
	if err != nil {
		t.Fatalf("ListExperiments() page 2 unexpected error: %v", err)
	}
	if page2Token != "" {
		t.Errorf("ListExperiments() page 2 nextPageToken = %q, want empty", page2Token)
	}
	wantPage2 := []*pb.Experiment{experiments[0]} // C
	if diff := gocmp.Diff(wantPage2, gotPage2, protocmp.Transform()); diff != "" {
		t.Errorf("ListExperiments() page 2 mismatch (-want +got):\n%s", diff)
	}
}

func TestListExperiments_FilterFields(t *testing.T) {
	ctx := t.Context()
	repo, _ := newTestRepository(t)

	exps := []*pb.Experiment{
		blankExperimentProto(1),
		blankExperimentProto(2),
		blankExperimentProto(3),
	}
	exps[0].Creator = "creator-1"
	exps[0].Title = "alpha baseline"
	exps[0].Tags = []string{"tag1", "vision"}
	exps[0].Notes = "notes-1"
	exps[0].Status.LaunchState = pb.ExperimentStatus_COMPLETED

	exps[1].Creator = "creator-2"
	exps[1].Title = "beta ablation"
	exps[1].Tags = []string{"tag2"}
	exps[1].Notes = "notes-2"
	exps[1].Status.ActiveState = pb.ExperimentStatus_INACTIVE
	exps[1].Status.LaunchState = pb.ExperimentStatus_FAILED

	exps[2].Creator = "creator-1"
	exps[2].Title = "gamma baseline"
	exps[2].Tags = []string{"tag1", "tag3"}
	exps[2].Notes = "notes-3"
	exps[2].Status.LaunchState = pb.ExperimentStatus_LAUNCHING

	for _, exp := range exps {
		createExperiment(ctx, t, repo, exp)
	}

	testCases := []struct {
		name   string
		filter string
		want   []int64
	}{
		{name: "xid", filter: "xid=1", want: []int64{1}},
		{name: "title", filter: `title="beta ablation"`, want: []int64{2}},
		{name: "creator", filter: `creator="creator-1"`, want: []int64{1, 3}},
		{name: "creator-denormalized", filter: `creator=creator-1`, want: []int64{1, 3}},
		{name: "creator-disjunction", filter: `creator="creator-1" OR creator="creator-2"`, want: []int64{1, 2, 3}},
		{name: "tags", filter: `tags:"tag1"`, want: []int64{1, 3}},
		{name: "notes", filter: `notes="notes-3"`, want: []int64{3}},
		{name: "active_state", filter: `active_state="ACTIVE"`, want: []int64{1, 3}},
		{name: "active_state-unquoted", filter: `active_state=INACTIVE`, want: []int64{2}},
		{name: "launch_state", filter: `launch_state="FAILED"`, want: []int64{2}},
		{name: "fulltext-title", filter: `"baseline"`, want: []int64{1, 3}},
		{name: "fulltext-tags", filter: `"vision"`, want: []int64{1}},
		{name: "fulltext-creator", filter: `"creator-2"`, want: []int64{2}},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			gotExperiments, _, err := repo.ListExperiments(ctx, 10, "", tc.filter, "xid", 0)
			if err != nil {
				t.Fatalf("ListExperiments(filter=%q) unexpected error: %v", tc.filter, err)
			}
			if diff := gocmp.Diff(tc.want, xidsOf(t, gotExperiments)); diff != "" {
				t.Errorf("ListExperiments(filter=%q) XIDs mismatch (-want +got):\n%s", tc.filter, diff)
			}
		})
	}
}

func TestListExperiments_OrderByFields(t *testing.T) {
	ctx := t.Context()
	repo, _ := newTestRepository(t)

	base := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	createExperimentsAt(ctx, t, repo, base, 3, 1, 2)

	testCases := []struct {
		name    string
		orderBy string
		want    []int64
	}{
		{name: "xid asc", orderBy: "xid", want: []int64{1, 2, 3}},
		{name: "xid desc", orderBy: "xid desc", want: []int64{3, 2, 1}},
		{name: "created asc", orderBy: "created", want: []int64{3, 1, 2}},
		{name: "created desc", orderBy: "created desc", want: []int64{2, 1, 3}},
		{name: "updated asc", orderBy: "updated", want: []int64{3, 1, 2}},
		{name: "updated desc", orderBy: "updated desc", want: []int64{2, 1, 3}},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			gotExperiments, _, err := repo.ListExperiments(ctx, 10, "", "", tc.orderBy, 0)
			if err != nil {
				t.Fatalf("ListExperiments(orderBy=%q) unexpected error: %v", tc.orderBy, err)
			}
			if diff := gocmp.Diff(tc.want, xidsOf(t, gotExperiments)); diff != "" {
				t.Errorf("ListExperiments(orderBy=%q) XIDs mismatch (-want +got):\n%s", tc.orderBy, diff)
			}

			// Verify that multi-page pagination preserves the same order across page boundaries.
			paged, pages := listAllPages(ctx, t, repo, 2, "", tc.orderBy)
			if pages != 2 {
				t.Errorf("listAllPages(orderBy=%q, pageSize=2) read %d pages, want 2", tc.orderBy, pages)
			}
			if diff := gocmp.Diff(tc.want, xidsOf(t, paged)); diff != "" {
				t.Errorf("paginated ListExperiments(orderBy=%q) XIDs mismatch (-want +got):\n%s", tc.orderBy, diff)
			}
		})
	}
}

func TestListExperiments_PaginationDefaultOrderByAndNullableCompleted(t *testing.T) {
	ctx := t.Context()
	repo, _ := newTestRepository(t)

	base := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

	exp1 := blankExperimentProto(1)
	exp1.CreateTime = timestamppb.New(base)
	exp1.CompleteTime = timestamppb.New(base.Add(10 * time.Second))
	exp1.Status.LaunchState = pb.ExperimentStatus_COMPLETED
	createExperiment(ctx, t, repo, exp1)

	exp2 := blankExperimentProto(2)
	exp2.CreateTime = timestamppb.New(base.Add(time.Second))
	exp2.Status.LaunchState = pb.ExperimentStatus_LAUNCHING
	createExperiment(ctx, t, repo, exp2)

	exp3 := blankExperimentProto(3)
	exp3.CreateTime = timestamppb.New(base.Add(2 * time.Second))
	exp3.CompleteTime = timestamppb.New(base.Add(20 * time.Second))
	exp3.Status.LaunchState = pb.ExperimentStatus_COMPLETED
	createExperiment(ctx, t, repo, exp3)

	tests := []struct {
		orderBy  string
		wantXIDs []int64
	}{
		// Default order follows the view (Xid ASC).
		{orderBy: "", wantXIDs: []int64{1, 2, 3}},
		// PostgreSQL dialect orders nullable ASC with NULLS FIRST.
		{orderBy: "completed", wantXIDs: []int64{2, 1, 3}},
		// PostgreSQL dialect orders nullable DESC with NULLS LAST.
		{orderBy: "completed desc", wantXIDs: []int64{3, 1, 2}},
	}

	for _, tc := range tests {
		t.Run("orderBy="+tc.orderBy, func(t *testing.T) {
			got, pages := listAllPages(ctx, t, repo, 1, "", tc.orderBy)
			if pages != 3 {
				t.Errorf("listAllPages(orderBy=%q, pageSize=1) read %d pages, want 3", tc.orderBy, pages)
			}
			if diff := gocmp.Diff(tc.wantXIDs, xidsOf(t, got)); diff != "" {
				t.Errorf("paginated ListExperiments(orderBy=%q) XIDs mismatch (-want +got):\n%s", tc.orderBy, diff)
			}
		})
	}
}

func TestListExperiments_InvalidInputsReturnInvalidArgument(t *testing.T) {
	ctx := t.Context()
	repo, _ := newTestRepository(t)

	tests := []struct {
		name      string
		pageToken string
		filter    string
		orderBy   string
	}{
		{name: "unparseable_filter", filter: "title ="},
		{name: "unknown_filter_field", filter: "nonexistent = 1"},
		{name: "unknown_order_by_field", orderBy: "nonexistent desc"},
		{name: "malformed_page_token", pageToken: "!!!not-base64!!!"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := repo.ListExperiments(ctx, 10, tc.pageToken, tc.filter, tc.orderBy, 0)
			if !errors.Is(err, grpcerr.ErrInvalidArgument) && status.Code(err) != codes.InvalidArgument {
				t.Errorf("ListExperiments(%s) error = %v (code %v), want InvalidArgument", tc.name, err, status.Code(err))
			}
		})
	}
}

func TestListExperiments_StalenessIgnored(t *testing.T) {
	ctx := t.Context()
	repo, _ := newTestRepository(t)

	createdExp := createBlankExperiment(ctx, t, repo, 42)

	got, _, err := repo.ListExperiments(ctx, 10, "", "", "", 15*time.Second)
	if err != nil {
		t.Fatalf("ListExperiments(staleness=15s) unexpected error: %v", err)
	}
	if diff := gocmp.Diff([]*pb.Experiment{createdExp}, got, protocmp.Transform()); diff != "" {
		t.Errorf("ListExperiments(staleness=15s) mismatch (-want +got):\n%s", diff)
	}
}
