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

package dashboard

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/testing/protocmp"
	fieldmaskpb "google.golang.org/protobuf/types/known/fieldmaskpb"
	dpb "xmanager_cloud/api/protos/dashboard"
	"xmanager_cloud/internal/grpcerr"
	storagedata "xmanager_cloud/internal/storage/data"
)

// clockTolerance bounds how far a database-set timestamp may be from the test's clock.
const clockTolerance = time.Minute

// ignoreDashboardTimestamps ignores the database-assigned timestamps when comparing dashboards.
var ignoreDashboardTimestamps = protocmp.IgnoreFields(&dpb.Dashboard{}, "create_time", "update_time")

func TestCreateDashboard_Success(t *testing.T) {
	ctx := t.Context()
	repo, _ := setupDashboardRepository(ctx, t)

	dash := blankDashboardProto("dashboards/test-create-1")
	if err := repo.CreateDashboard(ctx, dash, nil); err != nil {
		t.Fatalf("CreateDashboard failed: %v", err)
	}

	got, err := repo.GetDashboard(ctx, "test-create-1")
	if err != nil {
		t.Fatalf("GetDashboard failed: %v", err)
	}
	want := blankDashboardProto("dashboards/test-create-1")
	want.ChartIds = nil
	if diff := cmp.Diff(want, got, protocmp.Transform(), ignoreDashboardTimestamps); diff != "" {
		t.Errorf("GetDashboard mismatch (-want +got):\n%s", diff)
	}
	for name, ts := range map[string]time.Time{"create_time": got.GetCreateTime().AsTime(), "update_time": got.GetUpdateTime().AsTime()} {
		if time.Since(ts).Abs() > clockTolerance {
			t.Errorf("GetDashboard %s = %v, want a database timestamp within %v of now", name, ts, clockTolerance)
		}
	}
}

func TestCreateDashboard_WithChartIDs(t *testing.T) {
	ctx := t.Context()
	repo, _ := setupDashboardRepository(ctx, t)

	chartID := "f47ac10b-58cc-4372-a567-0e02b2c3d479"
	seedChart(ctx, t, repo, chartID)

	dash := blankDashboardProto("dashboards/test-create-with-charts")
	dash.ChartIds = []string{chartID}
	if err := repo.CreateDashboard(ctx, dash, nil); err != nil {
		t.Fatalf("CreateDashboard with chart IDs failed: %v", err)
	}

	got, err := repo.GetDashboard(ctx, "test-create-with-charts")
	if err != nil {
		t.Fatalf("GetDashboard failed: %v", err)
	}
	if !slices.Equal(got.GetChartIds(), []string{chartID}) {
		t.Errorf("GetDashboard ChartIds = %v, want [%q]", got.GetChartIds(), chartID)
	}
}

func TestCreateDashboard_WithoutLayout(t *testing.T) {
	ctx := t.Context()
	repo, _ := setupDashboardRepository(ctx, t)

	dash := blankDashboardProto("dashboards/test-create-no-layout")
	dash.ChartLayout = nil
	if err := repo.CreateDashboard(ctx, dash, nil); err != nil {
		t.Fatalf("CreateDashboard without layout failed: %v", err)
	}

	got, err := repo.GetDashboard(ctx, "test-create-no-layout")
	if err != nil {
		t.Fatalf("GetDashboard failed: %v", err)
	}
	if got.GetChartLayout() != nil {
		t.Errorf("GetDashboard ChartLayout = %v, want nil", got.GetChartLayout())
	}
}

func TestCreateDashboard_NonExistentChartID(t *testing.T) {
	ctx := t.Context()
	repo, _ := setupDashboardRepository(ctx, t)

	dash := blankDashboardProto("dashboards/test-create-nonexistent-chart")
	dash.ChartIds = []string{"00000000-0000-0000-0000-000000000000"}

	err := repo.CreateDashboard(ctx, dash, nil)
	assertErrorIs(t, "CreateDashboard with non-existent chart ID", err, grpcerr.ErrFailedPrecondition)

	// The foreign key failure rolls the dashboard row back with the link.
	if _, err := repo.GetDashboard(ctx, "test-create-nonexistent-chart"); !errors.Is(err, grpcerr.ErrNotFound) {
		t.Errorf("GetDashboard after failed create error = %v, want %v", err, grpcerr.ErrNotFound)
	}
}

func TestCreateDashboard_AlreadyExists(t *testing.T) {
	ctx := t.Context()
	repo, _ := setupDashboardRepository(ctx, t)

	dash := blankDashboardProto("dashboards/test-create-twice")
	if err := repo.CreateDashboard(ctx, dash, nil); err != nil {
		t.Fatalf("CreateDashboard failed: %v", err)
	}

	duplicate := blankDashboardProto("dashboards/test-create-twice")
	duplicate.Title = "Second Title"
	err := repo.CreateDashboard(ctx, duplicate, nil)
	assertErrorIs(t, "CreateDashboard of an existing dashboard", err, grpcerr.ErrAlreadyExists)

	got, err := repo.GetDashboard(ctx, "test-create-twice")
	if err != nil {
		t.Fatalf("GetDashboard failed: %v", err)
	}
	if got.GetTitle() != dash.GetTitle() {
		t.Errorf("GetDashboard Title = %q, want the original %q", got.GetTitle(), dash.GetTitle())
	}
}

func TestCreateDashboard_WithInlineCharts(t *testing.T) {
	ctx := t.Context()
	repo, _ := setupDashboardRepository(ctx, t)

	chartID := "9f8a2c5e-1d3b-4f6a-8e7c-2b5d9a1f4c60"
	chart := blankChartProto(chartID, 100, 1)

	dash := blankDashboardProto("dashboards/test-create-inline-charts")
	dash.ChartIds = []string{chartID}
	if err := repo.CreateDashboard(ctx, dash, []*dpb.Chart{chart}); err != nil {
		t.Fatalf("CreateDashboard with inline charts failed: %v", err)
	}

	got, err := repo.GetDashboard(ctx, "test-create-inline-charts")
	if err != nil {
		t.Fatalf("GetDashboard failed: %v", err)
	}
	if !slices.Equal(got.GetChartIds(), []string{chartID}) {
		t.Errorf("GetDashboard ChartIds = %v, want [%q]", got.GetChartIds(), chartID)
	}

	gotChart, err := repo.GetChart(ctx, chartID)
	if err != nil {
		t.Fatalf("GetChart for inline chart failed: %v", err)
	}
	if diff := cmp.Diff(chart, gotChart, protocmp.Transform(), ignoreChartTimestamps); diff != "" {
		t.Errorf("GetChart for inline chart mismatch (-want +got):\n%s", diff)
	}
	// Rows written in one transaction share its timestamp.
	if !gotChart.GetCreateTime().AsTime().Equal(got.GetCreateTime().AsTime()) {
		t.Errorf("inline chart create_time = %v, want the dashboard's %v", gotChart.GetCreateTime().AsTime(), got.GetCreateTime().AsTime())
	}
}

func TestCreateDashboard_InlineChartsRolledBackOnFailure(t *testing.T) {
	ctx := t.Context()
	repo, _ := setupDashboardRepository(ctx, t)

	chartID := "1c2d3e4f-5a6b-4c7d-8e9f-0a1b2c3d4e5f"
	chart := blankChartProto(chartID, 100, 1)

	dash := blankDashboardProto("dashboards/test-create-inline-rollback")
	dash.ChartIds = []string{chartID, "00000000-0000-0000-0000-000000000000"}

	err := repo.CreateDashboard(ctx, dash, []*dpb.Chart{chart})
	assertErrorIs(t, "CreateDashboard referencing a non-existent chart", err, grpcerr.ErrFailedPrecondition)

	if _, err := repo.GetChart(ctx, chartID); !errors.Is(err, grpcerr.ErrNotFound) {
		t.Errorf("GetChart error = %v, want %v: the inline chart must be rolled back with the dashboard", err, grpcerr.ErrNotFound)
	}
}

func TestCreateDashboard_InvalidInput(t *testing.T) {
	ctx := t.Context()
	repo, _ := setupDashboardRepository(ctx, t)

	tests := []struct {
		name      string
		dashboard *dpb.Dashboard
		charts    []*dpb.Chart
	}{
		{name: "nil dashboard", dashboard: nil},
		{name: "malformed name", dashboard: blankDashboardProto("not-a-dashboard-name")},
		{name: "invalid chart id", dashboard: func() *dpb.Dashboard {
			dash := blankDashboardProto("dashboards/test-create-invalid-chart-id")
			dash.ChartIds = []string{"has/slash"}
			return dash
		}()},
		{name: "nil inline chart", dashboard: blankDashboardProto("dashboards/test-create-nil-chart"), charts: []*dpb.Chart{nil}},
		{name: "malformed inline chart name", dashboard: blankDashboardProto("dashboards/test-create-bad-chart"), charts: []*dpb.Chart{{Name: "bad"}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := repo.CreateDashboard(ctx, tc.dashboard, tc.charts)
			assertErrorIs(t, "CreateDashboard", err, grpcerr.ErrInvalidArgument)
		})
	}
}

func TestGetDashboard_Success(t *testing.T) {
	ctx := t.Context()
	repo, _ := setupDashboardRepository(ctx, t)

	chartID := "f47ac10b-58cc-4372-a567-0e02b2c3d479"
	seedChart(ctx, t, repo, chartID)
	dash := seedDashboard(ctx, t, repo, "test-get-1", chartID)

	got, err := repo.GetDashboard(ctx, "test-get-1")
	if err != nil {
		t.Fatalf("GetDashboard failed: %v", err)
	}
	if diff := cmp.Diff(dash, got, protocmp.Transform(), ignoreDashboardTimestamps); diff != "" {
		t.Errorf("GetDashboard mismatch (-want +got):\n%s", diff)
	}
}

func TestGetDashboard_NoCharts(t *testing.T) {
	ctx := t.Context()
	repo, _ := setupDashboardRepository(ctx, t)

	seedDashboard(ctx, t, repo, "test-get-no-charts")

	got, err := repo.GetDashboard(ctx, "test-get-no-charts")
	if err != nil {
		t.Fatalf("GetDashboard failed: %v", err)
	}
	if len(got.GetChartIds()) != 0 {
		t.Errorf("GetDashboard ChartIds = %v, want none", got.GetChartIds())
	}
}

func TestGetDashboard_SortsChartIDs(t *testing.T) {
	ctx := t.Context()
	repo, _ := setupDashboardRepository(ctx, t)

	chartIDs := []string{"f47ac10b-58cc-4372-a567-0e02b2c3d479", "c9a646d3-9c61-4cd7-bf10-8e96dc638210", "00000000-0000-0000-0000-000000000001"}
	for _, chartID := range chartIDs {
		seedChart(ctx, t, repo, chartID)
	}
	seedDashboard(ctx, t, repo, "test-get-sorted", chartIDs...)

	got, err := repo.GetDashboard(ctx, "test-get-sorted")
	if err != nil {
		t.Fatalf("GetDashboard failed: %v", err)
	}
	if want := storagedata.SortChartIDs(chartIDs); !slices.Equal(got.GetChartIds(), want) {
		t.Errorf("GetDashboard ChartIds = %v, want canonically sorted %v", got.GetChartIds(), want)
	}
}

func TestGetDashboard_NotFound(t *testing.T) {
	ctx := t.Context()
	repo, _ := setupDashboardRepository(ctx, t)

	_, err := repo.GetDashboard(ctx, "non-existent")
	assertErrorIs(t, "GetDashboard of a missing dashboard", err, grpcerr.ErrNotFound)
}

func TestGetDashboard_EmptyID(t *testing.T) {
	ctx := t.Context()
	repo, _ := setupDashboardRepository(ctx, t)

	_, err := repo.GetDashboard(ctx, "")
	assertErrorIs(t, "GetDashboard with empty ID", err, grpcerr.ErrInvalidArgument)
}

func TestListDashboards_Empty(t *testing.T) {
	ctx := t.Context()
	repo, _ := setupDashboardRepository(ctx, t)

	dashboards, nextPageToken, err := repo.ListDashboards(ctx, 10, "", "", "", 0)
	if err != nil {
		t.Fatalf("ListDashboards on empty store failed: %v", err)
	}
	if len(dashboards) != 0 {
		t.Errorf("got %d dashboards, want 0", len(dashboards))
	}
	if nextPageToken != "" {
		t.Errorf("got nextPageToken %q, want empty", nextPageToken)
	}
}

func TestListDashboards_Basic(t *testing.T) {
	ctx := t.Context()
	repo, _ := setupDashboardRepository(ctx, t)

	// Insert with ascending lexicographical prefixes ("a-", "b-", "c-") in sequential commits
	// so default ordering (created DESC, dashboard_id ASC) returns newest first ("c-", "b-", "a-"),
	// rather than primary-key alphabetical order.
	names := []string{"dashboards/a-oldest", "dashboards/b-middle", "dashboards/c-newest"}
	for i, name := range names {
		dash := blankDashboardProto(name)
		dash.Title = fmt.Sprintf("Dashboard Title %d", i+1)
		if err := repo.CreateDashboard(ctx, dash, nil); err != nil {
			t.Fatalf("CreateDashboard %s failed: %v", name, err)
		}
	}

	dashboards, _, err := repo.ListDashboards(ctx, 10, "", "", "", 0)
	if err != nil {
		t.Fatalf("ListDashboards failed: %v", err)
	}
	wantOrder := []string{"dashboards/c-newest", "dashboards/b-middle", "dashboards/a-oldest"}
	if diff := cmp.Diff(wantOrder, dashboardNames(dashboards)); diff != "" {
		t.Errorf("ListDashboards order mismatch (-want +got):\n%s", diff)
	}
}

func TestListDashboards_Pagination(t *testing.T) {
	ctx := t.Context()
	repo, _ := setupDashboardRepository(ctx, t)

	for i := 1; i <= 5; i++ {
		dash := blankDashboardProto(fmt.Sprintf("dashboards/test-list-page-%d", i))
		dash.Title = fmt.Sprintf("Page Dashboard %d", i)
		if err := repo.CreateDashboard(ctx, dash, nil); err != nil {
			t.Fatalf("CreateDashboard %d failed: %v", i, err)
		}
	}

	testCases := []struct {
		name      string
		orderBy   string
		wantNames []string
	}{
		{
			name:      "default_newest_first",
			orderBy:   "",
			wantNames: []string{"dashboards/test-list-page-5", "dashboards/test-list-page-4", "dashboards/test-list-page-3", "dashboards/test-list-page-2", "dashboards/test-list-page-1"},
		},
		{
			name:      "dashboard_id_asc",
			orderBy:   "dashboard_id asc",
			wantNames: []string{"dashboards/test-list-page-1", "dashboards/test-list-page-2", "dashboards/test-list-page-3", "dashboards/test-list-page-4", "dashboards/test-list-page-5"},
		},
		{
			name:      "created_asc",
			orderBy:   "created",
			wantNames: []string{"dashboards/test-list-page-1", "dashboards/test-list-page-2", "dashboards/test-list-page-3", "dashboards/test-list-page-4", "dashboards/test-list-page-5"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			gotNames := collectPaginatedDashboardNames(ctx, t, repo, tc.orderBy, 2, []int{2, 2, 1})
			if diff := cmp.Diff(tc.wantNames, gotNames); diff != "" {
				t.Errorf("paginated names mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// Default ordering has to hold across page boundaries once the collection is larger than a page:
// a cursor that compared the wrong columns would repeat or skip dashboards between pages.
func TestListDashboards_MultiPageDefaultOrder(t *testing.T) {
	const (
		total    = 7
		pageSize = 3
	)
	ctx := t.Context()
	repo, _ := setupDashboardRepository(ctx, t)

	var wantNames []string
	for i := 1; i <= total; i++ {
		// IDs sort opposite to creation order, so that a primary-key ordering would be caught.
		name := fmt.Sprintf("dashboards/multi-page-%02d", total-i)
		if err := repo.CreateDashboard(ctx, blankDashboardProto(name), nil); err != nil {
			t.Fatalf("CreateDashboard %s failed: %v", name, err)
		}
		wantNames = append([]string{name}, wantNames...)
	}

	gotNames := collectPaginatedDashboardNames(ctx, t, repo, "", pageSize, []int{3, 3, 1})
	if diff := cmp.Diff(wantNames, gotNames); diff != "" {
		t.Errorf("newest-first names across pages mismatch (-want +got):\n%s", diff)
	}
}

func collectPaginatedDashboardNames(ctx context.Context, t *testing.T, repo *Repository, orderBy string, pageSize int32, wantPageLengths []int) []string {
	t.Helper()
	var gotNames []string
	var pageToken string
	for i, wantLen := range wantPageLengths {
		page, nextToken, err := repo.ListDashboards(ctx, pageSize, pageToken, "", orderBy, 0)
		if err != nil {
			t.Fatalf("ListDashboards page %d failed: %v", i+1, err)
		}
		if len(page) != wantLen {
			t.Fatalf("page %d: got %d items, want %d", i+1, len(page), wantLen)
		}
		isLastPage := i == len(wantPageLengths)-1
		if !isLastPage && nextToken == "" {
			t.Fatalf("page %d: expected non-empty token", i+1)
		}
		if isLastPage && nextToken != "" {
			t.Errorf("page %d: expected empty token, got %q", i+1, nextToken)
		}
		gotNames = append(gotNames, dashboardNames(page)...)
		pageToken = nextToken
	}
	return gotNames
}

func dashboardNames(dashboards []*dpb.Dashboard) []string {
	names := make([]string, 0, len(dashboards))
	for _, dash := range dashboards {
		names = append(names, dash.GetName())
	}
	return names
}

func TestListDashboards_Filter(t *testing.T) {
	ctx := t.Context()
	repo, _ := setupDashboardRepository(ctx, t)

	dash1 := blankDashboardProto("dashboards/filter-alpha")
	dash1.Title = "Alpha Target"
	if err := repo.CreateDashboard(ctx, dash1, nil); err != nil {
		t.Fatalf("CreateDashboard dash1 failed: %v", err)
	}
	dash2 := blankDashboardProto("dashboards/filter-beta")
	dash2.Title = "Beta Target"
	dash2.Owner = "other@example.com"
	if err := repo.CreateDashboard(ctx, dash2, nil); err != nil {
		t.Fatalf("CreateDashboard dash2 failed: %v", err)
	}

	tests := []struct {
		name      string
		filter    string
		wantNames []string
	}{
		{name: "title", filter: `title = "Alpha Target"`, wantNames: []string{"dashboards/filter-alpha"}},
		{name: "owner", filter: `owner = "other@example.com"`, wantNames: []string{"dashboards/filter-beta"}},
		{name: "dashboard_id", filter: `dashboard_id = "filter-alpha"`, wantNames: []string{"dashboards/filter-alpha"}},
		{name: "no match", filter: `title = "Gamma"`, wantNames: nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dashboards, _, err := repo.ListDashboards(ctx, 10, "", tc.filter, "", 0)
			if err != nil {
				t.Fatalf("ListDashboards(%q) failed: %v", tc.filter, err)
			}
			if diff := cmp.Diff(tc.wantNames, dashboardNames(dashboards), cmp.Comparer(func(a, b []string) bool { return slices.Equal(a, b) })); diff != "" {
				t.Errorf("ListDashboards(%q) mismatch (-want +got):\n%s", tc.filter, diff)
			}
		})
	}
}

func TestListDashboards_OrderBy(t *testing.T) {
	ctx := t.Context()
	repo, _ := setupDashboardRepository(ctx, t)

	titles := []string{"Zebra", "Apple", "Mango"}
	for i, title := range titles {
		dash := blankDashboardProto(fmt.Sprintf("dashboards/test-order-%d", i))
		dash.Title = title
		if err := repo.CreateDashboard(ctx, dash, nil); err != nil {
			t.Fatalf("CreateDashboard failed: %v", err)
		}
	}

	tests := []struct {
		orderBy    string
		wantTitles []string
	}{
		{orderBy: "title asc", wantTitles: []string{"Apple", "Mango", "Zebra"}},
		{orderBy: "title desc", wantTitles: []string{"Zebra", "Mango", "Apple"}},
	}
	for _, tc := range tests {
		t.Run(tc.orderBy, func(t *testing.T) {
			dashboards, _, err := repo.ListDashboards(ctx, 10, "", "", tc.orderBy, 0)
			if err != nil {
				t.Fatalf("ListDashboards with orderBy failed: %v", err)
			}
			var gotTitles []string
			for _, dash := range dashboards {
				gotTitles = append(gotTitles, dash.GetTitle())
			}
			if diff := cmp.Diff(tc.wantTitles, gotTitles); diff != "" {
				t.Errorf("ListDashboards(%q) order mismatch (-want +got):\n%s", tc.orderBy, diff)
			}
		})
	}
}

func TestListDashboards_InvalidArguments(t *testing.T) {
	ctx := t.Context()
	repo, _ := setupDashboardRepository(ctx, t)

	tests := []struct {
		name      string
		filter    string
		orderBy   string
		pageToken string
	}{
		{name: "invalid filter", filter: "invalid = [filter"},
		{name: "unknown order by field", orderBy: "no_such_field"},
		{name: "invalid page token", pageToken: "invalid-base64-token-!@#$"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := repo.ListDashboards(ctx, 10, tc.pageToken, tc.filter, tc.orderBy, 0)
			assertCode(t, "ListDashboards", err, codes.InvalidArgument)
		})
	}
}

func TestListDashboards_InvalidPageSize(t *testing.T) {
	ctx := t.Context()
	repo, _ := setupDashboardRepository(ctx, t)

	if _, _, err := repo.ListDashboards(ctx, 0, "", "", "", 0); err == nil {
		t.Errorf("ListDashboards with pageSize 0 succeeded, want error")
	}
}

func TestListDashboards_WithStaleness(t *testing.T) {
	ctx := t.Context()
	repo, _ := setupDashboardRepository(ctx, t)

	dash := blankDashboardProto("dashboards/test-list-staleness-1")
	dash.Title = "Staleness Dashboard"
	if err := repo.CreateDashboard(ctx, dash, nil); err != nil {
		t.Fatalf("CreateDashboard failed: %v", err)
	}

	// Staleness is ignored on PostgreSQL, so even a just-written dashboard is listed.
	dashboards, _, err := repo.ListDashboards(ctx, 10, "", "", "", 10*time.Second)
	if err != nil {
		t.Fatalf("ListDashboards with staleness failed: %v", err)
	}
	if len(dashboards) != 1 {
		t.Fatalf("got %d dashboards, want 1", len(dashboards))
	}
}

func TestListDashboards_HydratesChartIDs(t *testing.T) {
	ctx := t.Context()
	repo, _ := setupDashboardRepository(ctx, t)

	chartIDs := []string{
		"f47ac10b-58cc-4372-a567-0e02b2c3d479",
		"c9a646d3-9c61-4cd7-bf10-8e96dc638210",
	}
	charts := make([]*dpb.Chart, 0, len(chartIDs))
	for _, chartID := range chartIDs {
		charts = append(charts, blankChartProto(chartID, 100, 1))
	}

	withCharts := blankDashboardProto("dashboards/test-list-hydrate-charts")
	withCharts.ChartIds = chartIDs
	if err := repo.CreateDashboard(ctx, withCharts, charts); err != nil {
		t.Fatalf("CreateDashboard with charts failed: %v", err)
	}
	withoutCharts := blankDashboardProto("dashboards/test-list-hydrate-empty")
	if err := repo.CreateDashboard(ctx, withoutCharts, nil); err != nil {
		t.Fatalf("CreateDashboard without charts failed: %v", err)
	}

	dashboards, _, err := repo.ListDashboards(ctx, 10, "", "", "dashboard_id asc", 0)
	if err != nil {
		t.Fatalf("ListDashboards failed: %v", err)
	}
	if len(dashboards) != 2 {
		t.Fatalf("got %d dashboards, want 2", len(dashboards))
	}

	// The IDs are created out of order on purpose: a listing must serve them in the same canonical
	// order GetDashboard does.
	wantChartIDs := storagedata.SortChartIDs(chartIDs)
	if !slices.Equal(dashboards[0].GetChartIds(), wantChartIDs) {
		t.Errorf("ChartIds for %q = %v, want %v", dashboards[0].GetName(), dashboards[0].GetChartIds(), wantChartIDs)
	}
	if len(dashboards[1].GetChartIds()) != 0 {
		t.Errorf("ChartIds for %q = %v, want none", dashboards[1].GetName(), dashboards[1].GetChartIds())
	}
}

func TestListDashboards_HydratesEveryPage(t *testing.T) {
	ctx := t.Context()
	repo, _ := setupDashboardRepository(ctx, t)

	for i := 1; i <= 3; i++ {
		chartID := fmt.Sprintf("00000000-0000-0000-0000-00000000000%d", i)
		dash := blankDashboardProto(fmt.Sprintf("dashboards/test-list-hydrate-page-%d", i))
		dash.ChartIds = []string{chartID}
		if err := repo.CreateDashboard(ctx, dash, []*dpb.Chart{blankChartProto(chartID, 100, 1)}); err != nil {
			t.Fatalf("CreateDashboard %d failed: %v", i, err)
		}
	}

	var pageToken string
	seen := 0
	for page := 1; ; page++ {
		dashboards, nextToken, err := repo.ListDashboards(ctx, 2, pageToken, "", "dashboard_id asc", 0)
		if err != nil {
			t.Fatalf("ListDashboards page %d failed: %v", page, err)
		}
		for _, dash := range dashboards {
			seen++
			want := fmt.Sprintf("00000000-0000-0000-0000-00000000000%d", seen)
			if !slices.Equal(dash.GetChartIds(), []string{want}) {
				t.Errorf("ChartIds for %q = %v, want [%q]", dash.GetName(), dash.GetChartIds(), want)
			}
		}
		if nextToken == "" {
			break
		}
		pageToken = nextToken
	}
	if seen != 3 {
		t.Errorf("listed %d dashboards across pages, want 3", seen)
	}
}

func TestUpdateDashboard_Success(t *testing.T) {
	ctx := t.Context()
	repo, _ := setupDashboardRepository(ctx, t)

	created := seedDashboard(ctx, t, repo, "test-update-1")
	before, err := repo.GetDashboard(ctx, "test-update-1")
	if err != nil {
		t.Fatalf("GetDashboard failed: %v", err)
	}

	update := blankDashboardProto("dashboards/test-update-1")
	update.Title = "Updated Dashboard Title"
	update.Description = "Updated Dashboard Description"
	update.ChartLayout = &dpb.LayoutSpec{Type: dpb.LayoutSpec_SIDE_BY_SIDE, Columns: 3}
	// Owner is not updatable and must keep its stored value.
	update.Owner = "intruder@example.com"

	got, err := repo.UpdateDashboard(ctx, update, nil)
	if err != nil {
		t.Fatalf("UpdateDashboard failed: %v", err)
	}

	want := blankDashboardProto("dashboards/test-update-1")
	want.Title = "Updated Dashboard Title"
	want.Description = "Updated Dashboard Description"
	want.ChartLayout = &dpb.LayoutSpec{Type: dpb.LayoutSpec_SIDE_BY_SIDE, Columns: 3}
	want.Owner = created.GetOwner()
	want.ChartIds = nil
	if diff := cmp.Diff(want, got, protocmp.Transform(), ignoreDashboardTimestamps); diff != "" {
		t.Errorf("UpdateDashboard result mismatch (-want +got):\n%s", diff)
	}

	fetched, err := repo.GetDashboard(ctx, "test-update-1")
	if err != nil {
		t.Fatalf("GetDashboard failed: %v", err)
	}
	if diff := cmp.Diff(got, fetched, protocmp.Transform()); diff != "" {
		t.Errorf("GetDashboard after update differs from UpdateDashboard result (-update +get):\n%s", diff)
	}
	if !fetched.GetUpdateTime().AsTime().After(before.GetUpdateTime().AsTime()) {
		t.Errorf("update_time after update = %v, want after %v", fetched.GetUpdateTime().AsTime(), before.GetUpdateTime().AsTime())
	}
	if !fetched.GetCreateTime().AsTime().Equal(before.GetCreateTime().AsTime()) {
		t.Errorf("create_time after update = %v, want unchanged %v", fetched.GetCreateTime().AsTime(), before.GetCreateTime().AsTime())
	}
}

func TestUpdateDashboard_WithFieldMask(t *testing.T) {
	ctx := t.Context()
	repo, _ := setupDashboardRepository(ctx, t)

	dash := blankDashboardProto("dashboards/test-update-mask-1")
	dash.Title = "Initial Title"
	dash.Description = "Initial Description"
	if err := repo.CreateDashboard(ctx, dash, nil); err != nil {
		t.Fatalf("CreateDashboard failed: %v", err)
	}

	update := blankDashboardProto("dashboards/test-update-mask-1")
	update.Title = "New Title"
	update.Description = "New Description Attempt"

	mask := &fieldmaskpb.FieldMask{Paths: []string{"title"}}
	got, err := repo.UpdateDashboard(ctx, update, mask)
	if err != nil {
		t.Fatalf("UpdateDashboard with mask failed: %v", err)
	}
	if got.GetTitle() != "New Title" {
		t.Errorf("got Title %q, want 'New Title'", got.GetTitle())
	}

	fetched, err := repo.GetDashboard(ctx, "test-update-mask-1")
	if err != nil {
		t.Fatalf("GetDashboard failed: %v", err)
	}
	if fetched.GetTitle() != "New Title" {
		t.Errorf("fetched Title %q, want 'New Title'", fetched.GetTitle())
	}
	if fetched.GetDescription() != "Initial Description" {
		t.Errorf("fetched Description %q, want 'Initial Description' (should remain unchanged)", fetched.GetDescription())
	}
}

func TestUpdateDashboard_WithChartIDs(t *testing.T) {
	ctx := t.Context()
	repo, pool := setupDashboardRepository(ctx, t)

	chartID1 := "f47ac10b-58cc-4372-a567-0e02b2c3d479"
	chartID2 := "c9a646d3-9c61-4cd7-bf10-8e96dc638210"
	seedChart(ctx, t, repo, chartID1)
	seedChart(ctx, t, repo, chartID2)
	seedDashboard(ctx, t, repo, "test-update-charts-1", chartID1)

	update := blankDashboardProto("dashboards/test-update-charts-1")
	update.ChartIds = []string{chartID2}
	if _, err := repo.UpdateDashboard(ctx, update, nil); err != nil {
		t.Fatalf("UpdateDashboard charts failed: %v", err)
	}

	fetched, err := repo.GetDashboard(ctx, "test-update-charts-1")
	if err != nil {
		t.Fatalf("GetDashboard failed: %v", err)
	}
	if !slices.Equal(fetched.GetChartIds(), []string{chartID2}) {
		t.Fatalf("fetched ChartIds = %v, want [%q]", fetched.GetChartIds(), chartID2)
	}
	// The old link is deleted rather than left behind.
	if got := countRows(ctx, t, pool, "SELECT count(*) FROM DashboardCharts WHERE DashboardId = $1", "test-update-charts-1"); got != 1 {
		t.Errorf("DashboardCharts rows = %d, want 1", got)
	}
}

func TestUpdateDashboard_ClearChartIDs(t *testing.T) {
	ctx := t.Context()
	repo, _ := setupDashboardRepository(ctx, t)

	chartID1 := "f47ac10b-58cc-4372-a567-0e02b2c3d479"
	seedChart(ctx, t, repo, chartID1)
	seedDashboard(ctx, t, repo, "test-update-clear-1", chartID1)

	update := blankDashboardProto("dashboards/test-update-clear-1")
	update.ChartIds = []string{}
	mask := &fieldmaskpb.FieldMask{Paths: []string{"chart_ids"}}
	if _, err := repo.UpdateDashboard(ctx, update, mask); err != nil {
		t.Fatalf("UpdateDashboard clearing charts failed: %v", err)
	}

	fetched, err := repo.GetDashboard(ctx, "test-update-clear-1")
	if err != nil {
		t.Fatalf("GetDashboard failed: %v", err)
	}
	if len(fetched.GetChartIds()) != 0 {
		t.Fatalf("Expected 0 chart IDs after clearing, got %v", fetched.GetChartIds())
	}
}

func TestUpdateDashboard_NoOp(t *testing.T) {
	ctx := t.Context()
	repo, _ := setupDashboardRepository(ctx, t)

	seedDashboard(ctx, t, repo, "test-update-noop-1")
	before, err := repo.GetDashboard(ctx, "test-update-noop-1")
	if err != nil {
		t.Fatalf("GetDashboard failed: %v", err)
	}

	got, err := repo.UpdateDashboard(ctx, blankDashboardProto("dashboards/test-update-noop-1"), nil)
	if err != nil {
		t.Fatalf("UpdateDashboard no-op failed: %v", err)
	}
	if diff := cmp.Diff(before, got, protocmp.Transform()); diff != "" {
		t.Errorf("UpdateDashboard no-op result mismatch (-stored +got):\n%s", diff)
	}

	// Nothing was written, so the update time did not move.
	after, err := repo.GetDashboard(ctx, "test-update-noop-1")
	if err != nil {
		t.Fatalf("GetDashboard failed: %v", err)
	}
	if diff := cmp.Diff(before, after, protocmp.Transform()); diff != "" {
		t.Errorf("GetDashboard after no-op update mismatch (-before +after):\n%s", diff)
	}
}

func TestUpdateDashboard_NotFound(t *testing.T) {
	ctx := t.Context()
	repo, _ := setupDashboardRepository(ctx, t)

	_, err := repo.UpdateDashboard(ctx, blankDashboardProto("dashboards/non-existent-update"), nil)
	assertErrorIs(t, "UpdateDashboard of a missing dashboard", err, grpcerr.ErrNotFound)
}

func TestUpdateDashboard_NilOrInvalid(t *testing.T) {
	ctx := t.Context()
	repo, _ := setupDashboardRepository(ctx, t)

	seedDashboard(ctx, t, repo, "test-update-invalid")

	tests := []struct {
		name      string
		dashboard *dpb.Dashboard
		mask      *fieldmaskpb.FieldMask
	}{
		{name: "nil dashboard", dashboard: nil},
		{name: "empty name", dashboard: &dpb.Dashboard{}},
		{name: "malformed name", dashboard: &dpb.Dashboard{Name: "not-a-dashboard-name"}},
		{name: "unsupported mask path", dashboard: blankDashboardProto("dashboards/test-update-invalid"), mask: &fieldmaskpb.FieldMask{Paths: []string{"owner"}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := repo.UpdateDashboard(ctx, tc.dashboard, tc.mask)
			assertErrorIs(t, "UpdateDashboard", err, grpcerr.ErrInvalidArgument)
		})
	}
}

func TestUpdateDashboard_ReorderedChartIDs(t *testing.T) {
	ctx := t.Context()
	repo, _ := setupDashboardRepository(ctx, t)

	chartID1 := "c9a646d3-9c61-4cd7-bf10-8e96dc638210"
	chartID2 := "f47ac10b-58cc-4372-a567-0e02b2c3d479"
	seedChart(ctx, t, repo, chartID1)
	seedChart(ctx, t, repo, chartID2)
	seedDashboard(ctx, t, repo, "test-update-reorder-1", chartID1, chartID2)

	// Update with reordered charts (reverse order).
	update := blankDashboardProto("dashboards/test-update-reorder-1")
	update.ChartIds = []string{chartID2, chartID1}

	got, err := repo.UpdateDashboard(ctx, update, nil)
	if err != nil {
		t.Fatalf("UpdateDashboard with reordered charts failed: %v", err)
	}
	if want := []string{chartID1, chartID2}; !slices.Equal(got.GetChartIds(), want) {
		t.Errorf("got ChartIds %v, want canonically sorted %v", got.GetChartIds(), want)
	}

	fetched, err := repo.GetDashboard(ctx, "test-update-reorder-1")
	if err != nil {
		t.Fatalf("GetDashboard failed: %v", err)
	}
	if want := []string{chartID1, chartID2}; !slices.Equal(fetched.GetChartIds(), want) {
		t.Fatalf("fetched ChartIds = %v, want sorted %v", fetched.GetChartIds(), want)
	}
}

func TestUpdateDashboard_CamelCaseChartIdsMask(t *testing.T) {
	ctx := t.Context()
	repo, _ := setupDashboardRepository(ctx, t)

	chartID1 := "f47ac10b-58cc-4372-a567-0e02b2c3d479"
	chartID2 := "c9a646d3-9c61-4cd7-bf10-8e96dc638210"
	seedChart(ctx, t, repo, chartID1)
	seedChart(ctx, t, repo, chartID2)
	seedDashboard(ctx, t, repo, "test-update-camel-1", chartID1)

	update := blankDashboardProto("dashboards/test-update-camel-1")
	update.ChartIds = []string{chartID2}
	mask := &fieldmaskpb.FieldMask{Paths: []string{"chartIds"}}
	if _, err := repo.UpdateDashboard(ctx, update, mask); err != nil {
		t.Fatalf("UpdateDashboard with camelCase chartIds mask failed: %v", err)
	}

	fetched, err := repo.GetDashboard(ctx, "test-update-camel-1")
	if err != nil {
		t.Fatalf("GetDashboard failed: %v", err)
	}
	if !slices.Equal(fetched.GetChartIds(), []string{chartID2}) {
		t.Fatalf("fetched ChartIds = %v, want [%q]", fetched.GetChartIds(), chartID2)
	}
}

func TestUpdateDashboard_NonExistentChartID(t *testing.T) {
	ctx := t.Context()
	repo, _ := setupDashboardRepository(ctx, t)

	chartID1 := "f47ac10b-58cc-4372-a567-0e02b2c3d479"
	seedChart(ctx, t, repo, chartID1)
	seedDashboard(ctx, t, repo, "test-update-fk-1", chartID1)

	update := blankDashboardProto("dashboards/test-update-fk-1")
	update.Title = "Should Not Persist"
	update.ChartIds = []string{"00000000-0000-0000-0000-000000000000"}

	_, err := repo.UpdateDashboard(ctx, update, &fieldmaskpb.FieldMask{Paths: []string{"title", "chart_ids"}})
	assertErrorIs(t, "UpdateDashboard with non-existent chart ID", err, grpcerr.ErrFailedPrecondition)

	// The whole update is rolled back, including the columns written before the failing link.
	fetched, err := repo.GetDashboard(ctx, "test-update-fk-1")
	if err != nil {
		t.Fatalf("GetDashboard failed: %v", err)
	}
	if fetched.GetTitle() == "Should Not Persist" {
		t.Errorf("Title was persisted by a failed update")
	}
	if !slices.Equal(fetched.GetChartIds(), []string{chartID1}) {
		t.Errorf("fetched ChartIds = %v, want the original [%q]", fetched.GetChartIds(), chartID1)
	}
}

// Concurrent updates of disjoint fields must all survive: every writer merges on top of the state
// the previous one committed, because the read inside the transaction locks the row.
func TestUpdateDashboard_ConcurrentDisjointUpdatesAllSurvive(t *testing.T) {
	ctx := t.Context()
	repo, _ := setupDashboardRepository(ctx, t)

	chartID := "f47ac10b-58cc-4372-a567-0e02b2c3d479"
	seedChart(ctx, t, repo, chartID)
	dash := seedDashboard(ctx, t, repo, "test-update-concurrent")

	updates := []struct {
		dashboard *dpb.Dashboard
		mask      *fieldmaskpb.FieldMask
	}{
		{dashboard: &dpb.Dashboard{Name: dash.GetName(), Title: "Title From Writer A"}, mask: &fieldmaskpb.FieldMask{Paths: []string{"title"}}},
		{dashboard: &dpb.Dashboard{Name: dash.GetName(), Description: "Description From Writer B"}, mask: &fieldmaskpb.FieldMask{Paths: []string{"description"}}},
		{dashboard: &dpb.Dashboard{Name: dash.GetName(), ChartLayout: &dpb.LayoutSpec{Type: dpb.LayoutSpec_STACKED, Columns: 7}}, mask: &fieldmaskpb.FieldMask{Paths: []string{"chart_layout"}}},
		{dashboard: &dpb.Dashboard{Name: dash.GetName(), ChartIds: []string{chartID}}, mask: &fieldmaskpb.FieldMask{Paths: []string{"chart_ids"}}},
	}

	errs := make([]error, len(updates))
	var wg sync.WaitGroup
	for i, update := range updates {
		wg.Go(func() {
			_, errs[i] = repo.UpdateDashboard(ctx, update.dashboard, update.mask)
		})
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		t.Fatalf("concurrent UpdateDashboard failed: %v", err)
	}

	got, err := repo.GetDashboard(ctx, "test-update-concurrent")
	if err != nil {
		t.Fatalf("GetDashboard failed: %v", err)
	}
	want := blankDashboardProto(dash.GetName())
	want.Title = "Title From Writer A"
	want.Description = "Description From Writer B"
	want.ChartLayout = &dpb.LayoutSpec{Type: dpb.LayoutSpec_STACKED, Columns: 7}
	want.ChartIds = []string{chartID}
	if diff := cmp.Diff(want, got, protocmp.Transform(), ignoreDashboardTimestamps); diff != "" {
		t.Errorf("GetDashboard after concurrent updates mismatch (-want +got); a missing field is a lost update:\n%s", diff)
	}
}

// A writer that reads while another transaction holds the row must see that transaction's commit,
// not the state from before it: the locking read waits for the lock instead of merging on top of a
// stale snapshot and overwriting the concurrent update.
func TestUpdateDashboard_LocksRowAgainstLostUpdates(t *testing.T) {
	ctx := t.Context()
	repo, pool := setupDashboardRepository(ctx, t)

	dash := seedDashboard(ctx, t, repo, "test-update-lock")
	blocker := beginLockingTx(ctx, t, pool, "SELECT 1 FROM Dashboards WHERE DashboardId = $1 FOR NO KEY UPDATE", "test-update-lock")

	errCh := make(chan error, 1)
	go func() {
		_, err := repo.UpdateDashboard(ctx, &dpb.Dashboard{Name: dash.GetName(), Title: "Title From Writer"}, &fieldmaskpb.FieldMask{Paths: []string{"title"}})
		errCh <- err
	}()

	// Once the writer is blocked on the row, change the description underneath it and let it go.
	waitForLockWaiter(ctx, t, pool)
	if _, err := blocker.Exec(ctx, "UPDATE Dashboards SET Description = $2 WHERE DashboardId = $1", "test-update-lock", "Description From Blocker"); err != nil {
		t.Fatalf("UPDATE in blocking transaction failed: %v", err)
	}
	if err := blocker.Commit(ctx); err != nil {
		t.Fatalf("Commit of blocking transaction failed: %v", err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("UpdateDashboard failed: %v", err)
	}

	got, err := repo.GetDashboard(ctx, "test-update-lock")
	if err != nil {
		t.Fatalf("GetDashboard failed: %v", err)
	}
	if got.GetTitle() != "Title From Writer" {
		t.Errorf("Title = %q, want %q", got.GetTitle(), "Title From Writer")
	}
	if got.GetDescription() != "Description From Blocker" {
		t.Errorf("Description = %q, want %q; the writer overwrote the concurrent update", got.GetDescription(), "Description From Blocker")
	}
}

func TestDeleteDashboard_Success(t *testing.T) {
	ctx := t.Context()
	repo, _ := setupDashboardRepository(ctx, t)

	seedDashboard(ctx, t, repo, "test-delete-1")
	if err := repo.DeleteDashboard(ctx, "test-delete-1"); err != nil {
		t.Fatalf("DeleteDashboard failed: %v", err)
	}

	_, err := repo.GetDashboard(ctx, "test-delete-1")
	assertErrorIs(t, "GetDashboard after DeleteDashboard", err, grpcerr.ErrNotFound)
}

func TestDeleteDashboard_CascadesLinksAndKeepsCharts(t *testing.T) {
	ctx := t.Context()
	repo, pool := setupDashboardRepository(ctx, t)

	chartID := "f47ac10b-58cc-4372-a567-0e02b2c3d479"
	seedChart(ctx, t, repo, chartID)
	seedDashboard(ctx, t, repo, "test-delete-cascade-1", chartID)

	if err := repo.DeleteDashboard(ctx, "test-delete-cascade-1"); err != nil {
		t.Fatalf("DeleteDashboard failed: %v", err)
	}

	if _, err := repo.GetDashboard(ctx, "test-delete-cascade-1"); !errors.Is(err, grpcerr.ErrNotFound) {
		t.Errorf("GetDashboard after DeleteDashboard error = %v, want %v", err, grpcerr.ErrNotFound)
	}
	if got := countRows(ctx, t, pool, "SELECT count(*) FROM DashboardCharts WHERE DashboardId = $1", "test-delete-cascade-1"); got != 0 {
		t.Errorf("DashboardCharts rows after delete = %d, want 0", got)
	}
	// The referenced chart itself should still exist in the Charts table.
	if _, err := repo.GetChart(ctx, chartID); err != nil {
		t.Errorf("GetChart after DeleteDashboard failed: %v, chart entity should still exist", err)
	}
}

func TestDeleteDashboard_NotFound(t *testing.T) {
	ctx := t.Context()
	repo, _ := setupDashboardRepository(ctx, t)

	err := repo.DeleteDashboard(ctx, "non-existent-delete")
	assertErrorIs(t, "DeleteDashboard of a missing dashboard", err, grpcerr.ErrNotFound)
}

func TestDeleteDashboard_EmptyID(t *testing.T) {
	ctx := t.Context()
	repo, _ := setupDashboardRepository(ctx, t)

	err := repo.DeleteDashboard(ctx, "")
	assertErrorIs(t, "DeleteDashboard with empty ID", err, grpcerr.ErrInvalidArgument)
}
