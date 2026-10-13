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
	"fmt"
	"slices"
	"testing"

	dpb "xmanager_cloud/api/protos/dashboard"
	storagedata "xmanager_cloud/internal/storage/data"
)

func TestListDashboards_Empty(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

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
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	for i := 1; i <= 3; i++ {
		dash := blankDashboardProto(fmt.Sprintf("dashboards/test-list-basic-%d", i))
		dash.Title = fmt.Sprintf("Dashboard Title %d", i)
		if err := repo.CreateDashboard(ctx, dash, nil); err != nil {
			t.Fatalf("CreateDashboard %d failed: %v", i, err)
		}
	}

	dashboards, _, err := repo.ListDashboards(ctx, 10, "", "", "", 0)
	if err != nil {
		t.Fatalf("ListDashboards failed: %v", err)
	}
	if len(dashboards) != 3 {
		t.Fatalf("got %d dashboards, want 3", len(dashboards))
	}
}

func TestListDashboards_Pagination(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	for i := 1; i <= 5; i++ {
		dash := blankDashboardProto(fmt.Sprintf("dashboards/test-list-page-%d", i))
		dash.Title = fmt.Sprintf("Page Dashboard %d", i)
		if err := repo.CreateDashboard(ctx, dash, nil); err != nil {
			t.Fatalf("CreateDashboard %d failed: %v", i, err)
		}
	}

	// Page 1: 2 items
	page1, token1, err := repo.ListDashboards(ctx, 2, "", "", "dashboard_id asc", 0)
	if err != nil {
		t.Fatalf("ListDashboards page 1 failed: %v", err)
	}
	if len(page1) != 2 {
		t.Fatalf("page 1: got %d items, want 2", len(page1))
	}
	if token1 == "" {
		t.Fatalf("page 1: expected non-empty token")
	}

	// Page 2: 2 items
	page2, token2, err := repo.ListDashboards(ctx, 2, token1, "", "dashboard_id asc", 0)
	if err != nil {
		t.Fatalf("ListDashboards page 2 failed: %v", err)
	}
	if len(page2) != 2 {
		t.Fatalf("page 2: got %d items, want 2", len(page2))
	}
	if token2 == "" {
		t.Fatalf("page 2: expected non-empty token")
	}

	// Page 3: 1 item
	page3, token3, err := repo.ListDashboards(ctx, 2, token2, "", "dashboard_id asc", 0)
	if err != nil {
		t.Fatalf("ListDashboards page 3 failed: %v", err)
	}
	if len(page3) != 1 {
		t.Fatalf("page 3: got %d items, want 1", len(page3))
	}
	if token3 != "" {
		t.Errorf("page 3: expected empty token, got %q", token3)
	}
}

func TestListDashboards_Filter(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	dash1 := blankDashboardProto("dashboards/filter-alpha")
	dash1.Title = "Alpha Target"
	if err := repo.CreateDashboard(ctx, dash1, nil); err != nil {
		t.Fatalf("CreateDashboard dash1 failed: %v", err)
	}

	dash2 := blankDashboardProto("dashboards/filter-beta")
	dash2.Title = "Beta Target"
	if err := repo.CreateDashboard(ctx, dash2, nil); err != nil {
		t.Fatalf("CreateDashboard dash2 failed: %v", err)
	}

	filter := `title = "Alpha Target"`
	dashboards, _, err := repo.ListDashboards(ctx, 10, "", filter, "", 0)
	if err != nil {
		t.Fatalf("ListDashboards with filter failed: %v", err)
	}
	if len(dashboards) != 1 {
		t.Fatalf("got %d dashboards, want 1", len(dashboards))
	}
	if dashboards[0].GetTitle() != "Alpha Target" {
		t.Errorf("got Title %q, want 'Alpha Target'", dashboards[0].GetTitle())
	}
}

func TestListDashboards_OrderBy(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	titles := []string{"Zebra", "Apple", "Mango"}
	for i, title := range titles {
		dash := blankDashboardProto(fmt.Sprintf("dashboards/test-order-%d", i))
		dash.Title = title
		if err := repo.CreateDashboard(ctx, dash, nil); err != nil {
			t.Fatalf("CreateDashboard failed: %v", err)
		}
	}

	dashboards, _, err := repo.ListDashboards(ctx, 10, "", "", "title asc", 0)
	if err != nil {
		t.Fatalf("ListDashboards with orderBy failed: %v", err)
	}
	if len(dashboards) != 3 {
		t.Fatalf("got %d dashboards, want 3", len(dashboards))
	}
	if dashboards[0].GetTitle() != "Apple" || dashboards[1].GetTitle() != "Mango" || dashboards[2].GetTitle() != "Zebra" {
		t.Errorf("unexpected order: %v, %v, %v", dashboards[0].GetTitle(), dashboards[1].GetTitle(), dashboards[2].GetTitle())
	}
}

func TestListDashboards_InvalidFilter(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	_, _, err := repo.ListDashboards(ctx, 10, "", "invalid = [filter", "", 0)
	if err == nil {
		t.Errorf("ListDashboards with invalid filter succeeded, want error")
	}
}

func TestListDashboards_WithStaleness(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	dash := blankDashboardProto("dashboards/test-list-staleness-1")
	dash.Title = "Staleness Dashboard"
	if err := repo.CreateDashboard(ctx, dash, nil); err != nil {
		t.Fatalf("CreateDashboard failed: %v", err)
	}

	dashboards, _, err := repo.ListDashboards(ctx, 10, "", "", "", 10)
	if err != nil {
		t.Fatalf("ListDashboards with staleness failed: %v", err)
	}
	if len(dashboards) != 1 {
		t.Fatalf("got %d dashboards, want 1", len(dashboards))
	}
}

func TestListDashboards_HydratesChartIDs(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

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
	// order GetDashboard does, not in whatever order the chart links came back from Spanner.
	wantChartIDs := storagedata.SortChartIDs(chartIDs)
	if !slices.Equal(dashboards[0].GetChartIds(), wantChartIDs) {
		t.Errorf("ChartIds for %q = %v, want %v", dashboards[0].GetName(), dashboards[0].GetChartIds(), wantChartIDs)
	}
	if len(dashboards[1].GetChartIds()) != 0 {
		t.Errorf("ChartIds for %q = %v, want none", dashboards[1].GetName(), dashboards[1].GetChartIds())
	}
}

func TestListDashboards_HydratesOnlyThePageReturned(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	for i := 1; i <= 3; i++ {
		chartID := fmt.Sprintf("00000000-0000-0000-0000-00000000000%d", i)
		dash := blankDashboardProto(fmt.Sprintf("dashboards/test-list-hydrate-page-%d", i))
		dash.ChartIds = []string{chartID}
		if err := repo.CreateDashboard(ctx, dash, []*dpb.Chart{blankChartProto(chartID, 100, 1)}); err != nil {
			t.Fatalf("CreateDashboard %d failed: %v", i, err)
		}
	}

	page, token, err := repo.ListDashboards(ctx, 2, "", "", "dashboard_id asc", 0)
	if err != nil {
		t.Fatalf("ListDashboards failed: %v", err)
	}
	if len(page) != 2 || token == "" {
		t.Fatalf("got %d dashboards and token %q, want 2 and a non-empty token", len(page), token)
	}
	for i, dash := range page {
		want := fmt.Sprintf("00000000-0000-0000-0000-00000000000%d", i+1)
		if len(dash.GetChartIds()) != 1 || dash.GetChartIds()[0] != want {
			t.Errorf("ChartIds for %q = %v, want [%q]", dash.GetName(), dash.GetChartIds(), want)
		}
	}
}
