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
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/testing/protocmp"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	dpb "xmanager_cloud/api/protos/dashboard"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/validator"
)

var ignoreChartTimestamps = protocmp.IgnoreFields(&dpb.Chart{}, "create_time", "update_time")

func TestCreateChart_Success(t *testing.T) {
	ctx := context.Background()
	repo, _ := setupDashboardRepository(ctx, t)

	chart := blankChartProto("chart-create-1", 100, 1)
	if err := repo.CreateChart(ctx, chart); err != nil {
		t.Fatalf("CreateChart() failed: %v", err)
	}

	got, err := repo.GetChart(ctx, "chart-create-1")
	if err != nil {
		t.Fatalf("GetChart() failed: %v", err)
	}
	if diff := cmp.Diff(chart, got, protocmp.Transform(), ignoreChartTimestamps); diff != "" {
		t.Errorf("GetChart() mismatch (-want +got):\n%s", diff)
	}
	if got.GetCreateTime() == nil || !got.GetCreateTime().IsValid() {
		t.Errorf("GetChart().CreateTime = %v, want valid timestamp", got.GetCreateTime())
	}
	if got.GetUpdateTime() == nil || !got.GetUpdateTime().IsValid() {
		t.Errorf("GetChart().UpdateTime = %v, want valid timestamp", got.GetUpdateTime())
	}
}

func TestCreateChart_WithoutLayout(t *testing.T) {
	ctx := context.Background()
	repo, _ := setupDashboardRepository(ctx, t)

	chart := blankChartProto("chart-no-layout", 100, 1)
	chart.PlotLayout = nil
	if err := repo.CreateChart(ctx, chart); err != nil {
		t.Fatalf("CreateChart() failed: %v", err)
	}

	got, err := repo.GetChart(ctx, "chart-no-layout")
	if err != nil {
		t.Fatalf("GetChart() failed: %v", err)
	}
	if got.GetPlotLayout() != nil {
		t.Errorf("GetChart().PlotLayout = %v, want nil", got.GetPlotLayout())
	}
}

func TestCreateChart_RootChartName(t *testing.T) {
	ctx := context.Background()
	repo, _ := setupDashboardRepository(ctx, t)

	chart := &dpb.Chart{
		Name:        validator.ConstructRootChartName("chart-root-1"),
		Title:       "Root Chart",
		Description: "Chart without experiment/work-unit",
		Plots: []*dpb.PlotSpec{
			{PlotId: "plot-1", Title: "Root Plot", Type: dpb.PlotType_SUMMARY_BOX},
		},
	}
	if err := repo.CreateChart(ctx, chart); err != nil {
		t.Fatalf("CreateChart() failed: %v", err)
	}

	got, err := repo.GetChart(ctx, "chart-root-1")
	if err != nil {
		t.Fatalf("GetChart() failed: %v", err)
	}
	if got.GetName() != "charts/chart-root-1" {
		t.Errorf("GetChart().Name = %q, want %q", got.GetName(), "charts/chart-root-1")
	}
	if got.GetXid() != 0 || got.GetWid() != 0 {
		t.Errorf("GetChart() Xid=%d Wid=%d, want 0, 0", got.GetXid(), got.GetWid())
	}
}

func TestCreateChart_DerivesXidAndWidFromResourceName(t *testing.T) {
	ctx := context.Background()
	repo, _ := setupDashboardRepository(ctx, t)

	chart := &dpb.Chart{
		Name:        "experiments/777/workUnits/9/charts/chart-derived",
		Title:       "Derived Xid/Wid Chart",
		Description: "Proto xid and wid are left zero",
	}
	if err := repo.CreateChart(ctx, chart); err != nil {
		t.Fatalf("CreateChart() failed: %v", err)
	}

	got, err := repo.GetChart(ctx, "chart-derived")
	if err != nil {
		t.Fatalf("GetChart() failed: %v", err)
	}
	if got.GetXid() != 777 || got.GetWid() != 9 {
		t.Errorf("GetChart() Xid=%d Wid=%d, want 777, 9", got.GetXid(), got.GetWid())
	}
	if got.GetName() != "experiments/777/workUnits/9/charts/chart-derived" {
		t.Errorf("GetChart().Name = %q, want %q", got.GetName(), "experiments/777/workUnits/9/charts/chart-derived")
	}
}

func TestCreateChart_DuplicatePlotIDInSameChart(t *testing.T) {
	ctx := context.Background()
	repo, _ := setupDashboardRepository(ctx, t)

	chart := blankChartProto("chart-dup-plots", 100, 1)
	chart.Plots = []*dpb.PlotSpec{
		{PlotId: "plot-dup", Title: "First Plot", Type: dpb.PlotType_SUMMARY_BOX},
		{PlotId: "plot-dup", Title: "Overwritten Plot", Type: dpb.PlotType_SCATTER},
	}
	if err := repo.CreateChart(ctx, chart); err != nil {
		t.Fatalf("CreateChart() failed: %v", err)
	}

	got, err := repo.GetChart(ctx, "chart-dup-plots")
	if err != nil {
		t.Fatalf("GetChart() failed: %v", err)
	}
	if len(got.GetPlots()) != 1 {
		t.Fatalf("GetChart() plots len = %d, want 1", len(got.GetPlots()))
	}
	if got.GetPlots()[0].GetTitle() != "Overwritten Plot" {
		t.Errorf("GetChart() plot title = %q, want %q", got.GetPlots()[0].GetTitle(), "Overwritten Plot")
	}
}

func TestCreateChart_AlreadyExists(t *testing.T) {
	ctx := context.Background()
	repo, _ := setupDashboardRepository(ctx, t)

	seedChart(ctx, t, repo, "chart-dup-id")

	err := repo.CreateChart(ctx, blankChartProto("chart-dup-id", 100, 1))
	assertErrorIs(t, "CreateChart(duplicate)", err, grpcerr.ErrAlreadyExists)
}

func TestCreateChart_DoesNotInsertArtifactCharts(t *testing.T) {
	ctx := context.Background()
	repo, pool := setupDashboardRepository(ctx, t)

	seedChart(ctx, t, repo, "chart-no-artifact")

	if got := countRows(ctx, t, pool, `SELECT count(*) FROM ArtifactCharts WHERE ChartId = $1`, "chart-no-artifact"); got != 0 {
		t.Errorf("ArtifactCharts row count = %d, want 0", got)
	}
}

func TestCreateChart_InvalidInputs(t *testing.T) {
	ctx := context.Background()
	repo, _ := setupDashboardRepository(ctx, t)

	tests := []struct {
		name  string
		chart *dpb.Chart
	}{
		{
			name:  "nil chart",
			chart: nil,
		},
		{
			name:  "empty chart name",
			chart: &dpb.Chart{Name: ""},
		},
		{
			name:  "malformed chart name",
			chart: &dpb.Chart{Name: "invalid/chart/name"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := repo.CreateChart(ctx, tc.chart)
			assertErrorIs(t, "CreateChart()", err, grpcerr.ErrInvalidArgument)
		})
	}
}

func TestGetChart_WithoutPlots(t *testing.T) {
	ctx := context.Background()
	repo, _ := setupDashboardRepository(ctx, t)

	chart := blankChartProto("chart-no-plots", 100, 1)
	chart.Plots = nil
	if err := repo.CreateChart(ctx, chart); err != nil {
		t.Fatalf("CreateChart() failed: %v", err)
	}

	got, err := repo.GetChart(ctx, "chart-no-plots")
	if err != nil {
		t.Fatalf("GetChart() failed: %v", err)
	}
	if len(got.GetPlots()) != 0 {
		t.Errorf("GetChart().Plots = %v, want empty", got.GetPlots())
	}
}

func TestGetChart_NotFound(t *testing.T) {
	ctx := context.Background()
	repo, _ := setupDashboardRepository(ctx, t)

	_, err := repo.GetChart(ctx, "nonexistent-chart")
	assertErrorIs(t, "GetChart(nonexistent)", err, grpcerr.ErrNotFound)
}

func TestGetChart_EmptyID(t *testing.T) {
	ctx := context.Background()
	repo, _ := setupDashboardRepository(ctx, t)

	_, err := repo.GetChart(ctx, "")
	assertErrorIs(t, "GetChart(empty)", err, grpcerr.ErrInvalidArgument)
}

func TestListCharts_EmptyStore(t *testing.T) {
	ctx := context.Background()
	repo, _ := setupDashboardRepository(ctx, t)

	got, nextPageToken, err := repo.ListCharts(ctx, "", 10, "", "", "", 0)
	if err != nil {
		t.Fatalf("ListCharts() failed: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("ListCharts() returned %d charts, want 0", len(got))
	}
	if nextPageToken != "" {
		t.Errorf("ListCharts() nextPageToken = %q, want empty", nextPageToken)
	}
}

func TestListCharts_MultiPageDefaultOrdering(t *testing.T) {
	ctx := context.Background()
	repo, _ := setupDashboardRepository(ctx, t)

	const total = 7
	const pageSize = 3
	// Seed in reverse order to verify default ordering is chart_id ASC, not insertion order.
	for i := total; i >= 1; i-- {
		chartID := fmt.Sprintf("chart-page-%02d", i)
		seedChart(ctx, t, repo, chartID)
	}

	var all []*dpb.Chart
	pageToken := ""
	pages := 0
	for {
		page, nextToken, err := repo.ListCharts(ctx, "", pageSize, pageToken, "", "", 0)
		if err != nil {
			t.Fatalf("ListCharts(page %d) failed: %v", pages, err)
		}
		if len(page) == 0 {
			t.Fatalf("ListCharts(page %d) returned empty page before pagination finished", pages)
		}
		if len(page) > int(pageSize) {
			t.Fatalf("ListCharts(page %d) returned %d items, want <= %d", pages, len(page), pageSize)
		}
		all = append(all, page...)
		pages++
		if nextToken == "" {
			break
		}
		pageToken = nextToken
	}

	if pages != 3 {
		t.Errorf("ListCharts() took %d pages, want 3", pages)
	}
	if len(all) != total {
		t.Fatalf("ListCharts() total items = %d, want %d", len(all), total)
	}
	for i, c := range all {
		wantName := validator.ConstructWorkUnitChartName(100, 1, fmt.Sprintf("chart-page-%02d", i+1))
		if c.GetName() != wantName {
			t.Errorf("all[%d].Name = %q, want %q", i, c.GetName(), wantName)
		}
	}
}

func TestListCharts_FilterAndOrderBy(t *testing.T) {
	ctx := context.Background()
	repo, _ := setupDashboardRepository(ctx, t)

	charts := []*dpb.Chart{
		{
			Name:        validator.ConstructWorkUnitChartName(100, 1, "chart-1"),
			Title:       "Alpha Chart",
			Description: "Desc A",
			Xid:         100,
			Wid:         1,
		},
		{
			Name:        validator.ConstructWorkUnitChartName(100, 2, "chart-2"),
			Title:       "Beta Chart",
			Description: "Desc B",
			Xid:         100,
			Wid:         2,
		},
		{
			Name:        validator.ConstructWorkUnitChartName(200, 1, "chart-3"),
			Title:       "Gamma Chart",
			Description: "Desc C",
			Xid:         200,
			Wid:         1,
		},
	}
	for _, c := range charts {
		if err := repo.CreateChart(ctx, c); err != nil {
			t.Fatalf("CreateChart(%s) failed: %v", c.GetName(), err)
		}
	}

	t.Run("filter by title", func(t *testing.T) {
		got, _, err := repo.ListCharts(ctx, "", 10, "", `title = "Beta Chart"`, "", 0)
		if err != nil {
			t.Fatalf("ListCharts(filter title) failed: %v", err)
		}
		if len(got) != 1 || got[0].GetTitle() != "Beta Chart" {
			t.Errorf("ListCharts(filter title) = %v, want [Beta Chart]", got)
		}
	})

	t.Run("filter by xid", func(t *testing.T) {
		got, _, err := repo.ListCharts(ctx, "", 10, "", "xid = 100", "title desc", 0)
		if err != nil {
			t.Fatalf("ListCharts(filter xid) failed: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("ListCharts(filter xid) len = %d, want 2", len(got))
		}
		if got[0].GetTitle() != "Beta Chart" || got[1].GetTitle() != "Alpha Chart" {
			t.Errorf("ListCharts(order title desc) titles = [%q, %q], want [Beta Chart, Alpha Chart]", got[0].GetTitle(), got[1].GetTitle())
		}
	})
}

func TestListCharts_ByWorkUnitParent(t *testing.T) {
	ctx := context.Background()
	repo, _ := setupDashboardRepository(ctx, t)

	c1 := blankChartProto("c-wu-1", 100, 1)
	c2 := blankChartProto("c-wu-2", 100, 1)
	cOtherWid := blankChartProto("c-wu-other-wid", 100, 2)
	cOtherXid := blankChartProto("c-wu-other-xid", 200, 1)
	for _, c := range []*dpb.Chart{c1, c2, cOtherWid, cOtherXid} {
		if err := repo.CreateChart(ctx, c); err != nil {
			t.Fatalf("CreateChart(%s) failed: %v", c.GetName(), err)
		}
	}

	got, _, err := repo.ListCharts(ctx, "experiments/100/workUnits/1", 10, "", "", "", 0)
	if err != nil {
		t.Fatalf("ListCharts(workUnit parent) failed: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListCharts(workUnit parent) len = %d, want 2", len(got))
	}
	if got[0].GetName() != c1.GetName() || got[1].GetName() != c2.GetName() {
		t.Errorf("ListCharts(workUnit parent) = [%s, %s], want [%s, %s]", got[0].GetName(), got[1].GetName(), c1.GetName(), c2.GetName())
	}
}

func TestListCharts_ByDashboardParent(t *testing.T) {
	ctx := context.Background()
	repo, _ := setupDashboardRepository(ctx, t)

	seedChart(ctx, t, repo, "c-dash-1")
	seedChart(ctx, t, repo, "c-dash-2")
	seedChart(ctx, t, repo, "c-dash-3")
	seedChart(ctx, t, repo, "c-unlinked")

	seedDashboard(ctx, t, repo, "dash-parent", "c-dash-1", "c-dash-2", "c-dash-3")
	seedDashboard(ctx, t, repo, "dash-empty")

	t.Run("full resource name parent", func(t *testing.T) {
		got, _, err := repo.ListCharts(ctx, "dashboards/dash-parent", 10, "", "", "", 0)
		if err != nil {
			t.Fatalf("ListCharts(dashboards/dash-parent) failed: %v", err)
		}
		if len(got) != 3 {
			t.Fatalf("ListCharts(dashboards/dash-parent) len = %d, want 3", len(got))
		}
	})

	t.Run("bare dashboard ID parent", func(t *testing.T) {
		got, _, err := repo.ListCharts(ctx, "dash-parent", 10, "", "", "", 0)
		if err != nil {
			t.Fatalf("ListCharts(dash-parent) failed: %v", err)
		}
		if len(got) != 3 {
			t.Fatalf("ListCharts(dash-parent) len = %d, want 3", len(got))
		}
	})

	t.Run("empty dashboard parent", func(t *testing.T) {
		got, _, err := repo.ListCharts(ctx, "dashboards/dash-empty", 10, "", "", "", 0)
		if err != nil {
			t.Fatalf("ListCharts(dashboards/dash-empty) failed: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("ListCharts(dashboards/dash-empty) len = %d, want 0", len(got))
		}
	})

	t.Run("multi-page pagination under dashboard parent", func(t *testing.T) {
		page1, token1, err := repo.ListCharts(ctx, "dashboards/dash-parent", 2, "", "", "", 0)
		if err != nil {
			t.Fatalf("ListCharts(page 1) failed: %v", err)
		}
		if len(page1) != 2 || token1 == "" {
			t.Fatalf("ListCharts(page 1) len=%d token=%q, want len=2 non-empty token", len(page1), token1)
		}
		page2, token2, err := repo.ListCharts(ctx, "dashboards/dash-parent", 2, token1, "", "", 0)
		if err != nil {
			t.Fatalf("ListCharts(page 2) failed: %v", err)
		}
		if len(page2) != 1 || token2 != "" {
			t.Fatalf("ListCharts(page 2) len=%d token=%q, want len=1 empty token", len(page2), token2)
		}
	})

	t.Run("order by title desc under dashboard parent", func(t *testing.T) {
		got, _, err := repo.ListCharts(ctx, "dashboards/dash-parent", 10, "", "", "title desc", 0)
		if err != nil {
			t.Fatalf("ListCharts(order by title desc) failed: %v", err)
		}
		if len(got) != 3 {
			t.Fatalf("ListCharts(order by title desc) len = %d, want 3", len(got))
		}
	})
}

func TestListCharts_HydratesPlotsAcrossPages(t *testing.T) {
	ctx := context.Background()
	repo, _ := setupDashboardRepository(ctx, t)

	c0 := blankChartProto("c-plots-0", 100, 1)
	c0.Plots = nil
	c1 := blankChartProto("c-plots-1", 100, 1)
	c1.Plots = []*dpb.PlotSpec{
		{PlotId: "p1", Title: "Plot 1", Type: dpb.PlotType_SUMMARY_BOX},
	}
	c2 := blankChartProto("c-plots-2", 100, 1)
	c2.Plots = []*dpb.PlotSpec{
		{PlotId: "p1", Title: "Plot 1", Type: dpb.PlotType_SUMMARY_BOX},
		{PlotId: "p2", Title: "Plot 2", Type: dpb.PlotType_BAR},
	}
	for _, c := range []*dpb.Chart{c0, c1, c2} {
		if err := repo.CreateChart(ctx, c); err != nil {
			t.Fatalf("CreateChart(%s) failed: %v", c.GetName(), err)
		}
	}

	page1, token, err := repo.ListCharts(ctx, "", 2, "", "", "", 0)
	if err != nil {
		t.Fatalf("ListCharts(page 1) failed: %v", err)
	}
	if len(page1) != 2 {
		t.Fatalf("page1 len = %d, want 2", len(page1))
	}
	if len(page1[0].GetPlots()) != 0 {
		t.Errorf("page1[0] plots len = %d, want 0", len(page1[0].GetPlots()))
	}
	if len(page1[1].GetPlots()) != 1 {
		t.Errorf("page1[1] plots len = %d, want 1", len(page1[1].GetPlots()))
	}

	page2, _, err := repo.ListCharts(ctx, "", 2, token, "", "", 0)
	if err != nil {
		t.Fatalf("ListCharts(page 2) failed: %v", err)
	}
	if len(page2) != 1 || len(page2[0].GetPlots()) != 2 {
		t.Errorf("page2 plots = %v, want 1 chart with 2 plots", page2)
	}
}

func TestListCharts_StalenessAccepted(t *testing.T) {
	ctx := context.Background()
	repo, _ := setupDashboardRepository(ctx, t)

	seedChart(ctx, t, repo, "c-stale")

	got, _, err := repo.ListCharts(ctx, "", 10, "", "", "", 5*time.Second)
	if err != nil {
		t.Fatalf("ListCharts(staleness) failed: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("ListCharts(staleness) len = %d, want 1", len(got))
	}
}

func TestListCharts_InvalidInputs(t *testing.T) {
	ctx := context.Background()
	repo, _ := setupDashboardRepository(ctx, t)

	tests := []struct {
		name      string
		parent    string
		pageSize  int32
		pageToken string
		filter    string
		orderBy   string
		wantCode  codes.Code
	}{
		{
			name:     "zero page size",
			pageSize: 0,
		},
		{
			name:     "negative page size",
			pageSize: -5,
		},
		{
			name:     "experiment-only parent rejected",
			parent:   "experiments/100",
			pageSize: 10,
			wantCode: codes.InvalidArgument,
		},
		{
			name:     "unsupported parent format",
			parent:   "projects/foo/bars/baz",
			pageSize: 10,
		},
		{
			name:      "invalid page token",
			pageSize:  10,
			pageToken: "not-a-valid-page-token",
			wantCode:  codes.InvalidArgument,
		},
		{
			name:     "invalid filter field",
			pageSize: 10,
			filter:   `nonexistent_field = "foo"`,
			wantCode: codes.InvalidArgument,
		},
		{
			name:     "invalid orderBy field",
			pageSize: 10,
			orderBy:  "nonexistent_field asc",
			wantCode: codes.InvalidArgument,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := repo.ListCharts(ctx, tc.parent, tc.pageSize, tc.pageToken, tc.filter, tc.orderBy, 0)
			if err == nil {
				t.Fatalf("ListCharts() succeeded, want error")
			}
			if tc.wantCode != codes.OK {
				assertCode(t, "ListCharts()", err, tc.wantCode)
			}
		})
	}
}

func TestUpdateChart_FullReplacement(t *testing.T) {
	ctx := context.Background()
	repo, _ := setupDashboardRepository(ctx, t)

	seedChart(ctx, t, repo, "chart-update-full")

	replacement := &dpb.Chart{
		Name:        validator.ConstructWorkUnitChartName(100, 1, "chart-update-full"),
		Title:       "Updated Chart Title",
		Description: "Updated Chart Description",
		Xid:         999, // Immutable; stored Xid=100 must be preserved.
		Wid:         999, // Immutable; stored Wid=1 must be preserved.
		Plots: []*dpb.PlotSpec{
			{PlotId: "plot-2", Title: "Accuracy", Type: dpb.PlotType_SUMMARY_BOX},
			{PlotId: "plot-3", Title: "Throughput", Type: dpb.PlotType_BAR},
		},
		PlotLayout: &dpb.LayoutSpec{
			Type:    dpb.LayoutSpec_STACKED,
			Columns: 1,
		},
	}

	updated, err := repo.UpdateChart(ctx, replacement, nil)
	if err != nil {
		t.Fatalf("UpdateChart() failed: %v", err)
	}
	if updated.GetTitle() != "Updated Chart Title" {
		t.Errorf("UpdateChart().Title = %q, want %q", updated.GetTitle(), "Updated Chart Title")
	}
	if updated.GetXid() != 100 || updated.GetWid() != 1 {
		t.Errorf("UpdateChart() Xid=%d Wid=%d, want 100, 1", updated.GetXid(), updated.GetWid())
	}
	if len(updated.GetPlots()) != 2 {
		t.Fatalf("UpdateChart() plots len = %d, want 2", len(updated.GetPlots()))
	}
	if updated.GetUpdateTime() == nil || !updated.GetUpdateTime().IsValid() {
		t.Errorf("UpdateChart().UpdateTime = %v, want valid timestamp", updated.GetUpdateTime())
	}

	got, err := repo.GetChart(ctx, "chart-update-full")
	if err != nil {
		t.Fatalf("GetChart() failed: %v", err)
	}
	if diff := cmp.Diff(updated, got, protocmp.Transform()); diff != "" {
		t.Errorf("GetChart() after UpdateChart mismatch (-want +got):\n%s", diff)
	}
}

func TestUpdateChart_WithFieldMaskPreservesPlotsAndOtherFields(t *testing.T) {
	ctx := context.Background()
	repo, _ := setupDashboardRepository(ctx, t)

	orig := seedChart(ctx, t, repo, "chart-update-mask")

	patch := &dpb.Chart{
		Name:        orig.GetName(),
		Title:       "Masked Title Only",
		Description: "Should Not Be Written",
		Plots:       nil,
	}
	mask := &fieldmaskpb.FieldMask{Paths: []string{"title"}}

	updated, err := repo.UpdateChart(ctx, patch, mask)
	if err != nil {
		t.Fatalf("UpdateChart(mask) failed: %v", err)
	}
	if updated.GetTitle() != "Masked Title Only" {
		t.Errorf("updated.Title = %q, want %q", updated.GetTitle(), "Masked Title Only")
	}
	if updated.GetDescription() != orig.GetDescription() {
		t.Errorf("updated.Description = %q, want %q", updated.GetDescription(), orig.GetDescription())
	}
	if len(updated.GetPlots()) != 1 || updated.GetPlots()[0].GetPlotId() != "plot-1" {
		t.Errorf("updated.Plots = %v, want original plot-1 preserved", updated.GetPlots())
	}
}

func TestUpdateChart_UnsupportedMaskPath(t *testing.T) {
	ctx := context.Background()
	repo, _ := setupDashboardRepository(ctx, t)

	orig := seedChart(ctx, t, repo, "chart-bad-mask")
	mask := &fieldmaskpb.FieldMask{Paths: []string{"owner"}}

	_, err := repo.UpdateChart(ctx, orig, mask)
	assertErrorIs(t, "UpdateChart(unsupported mask)", err, grpcerr.ErrInvalidArgument)
}

func TestUpdateChart_NotFound(t *testing.T) {
	ctx := context.Background()
	repo, _ := setupDashboardRepository(ctx, t)

	chart := blankChartProto("chart-missing", 100, 1)
	_, err := repo.UpdateChart(ctx, chart, nil)
	assertErrorIs(t, "UpdateChart(nonexistent)", err, grpcerr.ErrNotFound)
}

func TestUpdateChart_InvalidInputs(t *testing.T) {
	ctx := context.Background()
	repo, _ := setupDashboardRepository(ctx, t)

	tests := []struct {
		name  string
		chart *dpb.Chart
	}{
		{
			name:  "nil chart",
			chart: nil,
		},
		{
			name:  "empty chart name",
			chart: &dpb.Chart{Name: ""},
		},
		{
			name:  "malformed chart name",
			chart: &dpb.Chart{Name: "bad/resource/name"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := repo.UpdateChart(ctx, tc.chart, nil)
			assertErrorIs(t, "UpdateChart()", err, grpcerr.ErrInvalidArgument)
		})
	}
}

func TestUpdateChart_ConcurrentDisjointUpdatesBothSurvive(t *testing.T) {
	ctx := context.Background()
	repo, _ := setupDashboardRepository(ctx, t)

	orig := seedChart(ctx, t, repo, "chart-concurrent-mask")

	type updateOp struct {
		chart *dpb.Chart
		mask  *fieldmaskpb.FieldMask
	}
	ops := []updateOp{
		{
			chart: &dpb.Chart{
				Name:  orig.GetName(),
				Title: "Concurrent Title",
			},
			mask: &fieldmaskpb.FieldMask{Paths: []string{"title"}},
		},
		{
			chart: &dpb.Chart{
				Name:        orig.GetName(),
				Description: "Concurrent Description",
			},
			mask: &fieldmaskpb.FieldMask{Paths: []string{"description"}},
		},
		{
			chart: &dpb.Chart{
				Name: orig.GetName(),
				Plots: []*dpb.PlotSpec{
					{PlotId: "plot-concurrent-1", Title: "Concurrent Plot 1", Type: dpb.PlotType_SUMMARY_BOX},
					{PlotId: "plot-concurrent-2", Title: "Concurrent Plot 2", Type: dpb.PlotType_BAR},
				},
			},
			mask: &fieldmaskpb.FieldMask{Paths: []string{"plots"}},
		},
		{
			chart: &dpb.Chart{
				Name: orig.GetName(),
				PlotLayout: &dpb.LayoutSpec{
					Type:    dpb.LayoutSpec_GRID,
					Columns: 4,
				},
			},
			mask: &fieldmaskpb.FieldMask{Paths: []string{"plot_layout"}},
		},
	}

	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make([]error, len(ops))
	for i, op := range ops {
		wg.Add(1)
		go func(idx int, op updateOp) {
			defer wg.Done()
			<-start
			_, errs[idx] = repo.UpdateChart(ctx, op.chart, op.mask)
		}(i, op)
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent UpdateChart[%d] failed: %v", i, err)
		}
	}

	got, err := repo.GetChart(ctx, "chart-concurrent-mask")
	if err != nil {
		t.Fatalf("GetChart() failed: %v", err)
	}
	if got.GetTitle() != "Concurrent Title" {
		t.Errorf("GetChart().Title = %q, want %q", got.GetTitle(), "Concurrent Title")
	}
	if got.GetDescription() != "Concurrent Description" {
		t.Errorf("GetChart().Description = %q, want %q", got.GetDescription(), "Concurrent Description")
	}
	if len(got.GetPlots()) != 2 {
		t.Errorf("GetChart().Plots len = %d, want 2", len(got.GetPlots()))
	}
	if got.GetPlotLayout().GetColumns() != 4 {
		t.Errorf("GetChart().PlotLayout.Columns = %d, want 4", got.GetPlotLayout().GetColumns())
	}
}

func TestUpdateChart_LocksRowAgainstLostUpdates(t *testing.T) {
	ctx := context.Background()
	repo, pool := setupDashboardRepository(ctx, t)

	orig := seedChart(ctx, t, repo, "chart-lock-test")

	// Hold a FOR NO KEY UPDATE lock on the target Charts row inside an explicit transaction and
	// mutate Description before UpdateChart can read it.
	tx := beginLockingTx(ctx, t, pool, `SELECT ChartId FROM Charts WHERE ChartId = $1 FOR NO KEY UPDATE`, "chart-lock-test")
	if _, err := tx.Exec(ctx, `UPDATE Charts SET Description = 'Committed Under Lock' WHERE ChartId = $1`, "chart-lock-test"); err != nil {
		t.Fatalf("tx.Exec(UPDATE Charts) failed: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		_, err := repo.UpdateChart(
			ctx,
			&dpb.Chart{
				Name:  orig.GetName(),
				Title: "Updated After Lock Release",
			},
			&fieldmaskpb.FieldMask{Paths: []string{"title"}},
		)
		errCh <- err
	}()

	// Verify UpdateChart actually blocks on the row lock rather than reading stale state.
	waitForLockWaiter(ctx, t, pool)

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("tx.Commit() failed: %v", err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("UpdateChart() after lock release failed: %v", err)
	}

	got, err := repo.GetChart(ctx, "chart-lock-test")
	if err != nil {
		t.Fatalf("GetChart() failed: %v", err)
	}
	if got.GetTitle() != "Updated After Lock Release" {
		t.Errorf("GetChart().Title = %q, want %q", got.GetTitle(), "Updated After Lock Release")
	}
	if got.GetDescription() != "Committed Under Lock" {
		t.Errorf("GetChart().Description = %q, want %q (lost update!)", got.GetDescription(), "Committed Under Lock")
	}
}

func TestDeleteChart_SuccessAndCascadesPlots(t *testing.T) {
	ctx := context.Background()
	repo, pool := setupDashboardRepository(ctx, t)

	seedChart(ctx, t, repo, "chart-del-1")
	if got := countRows(ctx, t, pool, `SELECT count(*) FROM ChartPlots WHERE ChartId = $1`, "chart-del-1"); got != 1 {
		t.Fatalf("ChartPlots count before delete = %d, want 1", got)
	}

	if err := repo.DeleteChart(ctx, "chart-del-1"); err != nil {
		t.Fatalf("DeleteChart() failed: %v", err)
	}

	_, err := repo.GetChart(ctx, "chart-del-1")
	assertErrorIs(t, "GetChart(deleted)", err, grpcerr.ErrNotFound)

	if got := countRows(ctx, t, pool, `SELECT count(*) FROM ChartPlots WHERE ChartId = $1`, "chart-del-1"); got != 0 {
		t.Errorf("ChartPlots count after delete = %d, want 0", got)
	}
}

func TestDeleteChart_RemovesDashboardChartLinks(t *testing.T) {
	ctx := context.Background()
	repo, _ := setupDashboardRepository(ctx, t)

	seedChart(ctx, t, repo, "chart-linked-dash")
	seedDashboard(ctx, t, repo, "dash-with-linked-chart", "chart-linked-dash")

	if err := repo.DeleteChart(ctx, "chart-linked-dash"); err != nil {
		t.Fatalf("DeleteChart() failed: %v", err)
	}

	dash, err := repo.GetDashboard(ctx, "dash-with-linked-chart")
	if err != nil {
		t.Fatalf("GetDashboard() after DeleteChart failed: %v", err)
	}
	if len(dash.GetChartIds()) != 0 {
		t.Errorf("GetDashboard().ChartIds = %v, want empty", dash.GetChartIds())
	}
}

func TestDeleteChart_RemovesArtifactChartLinks(t *testing.T) {
	ctx := context.Background()
	repo, pool := setupDashboardRepository(ctx, t)

	seedChart(ctx, t, repo, "chart-linked-artifact")
	seedArtifact(ctx, t, pool, 100, 1, 501)
	seedArtifactChart(ctx, t, pool, 100, 1, 501, "ac-1", "chart-linked-artifact")

	if err := repo.DeleteChart(ctx, "chart-linked-artifact"); err != nil {
		t.Fatalf("DeleteChart() with ArtifactCharts link failed: %v", err)
	}

	if got := countRows(ctx, t, pool, `SELECT count(*) FROM ArtifactCharts WHERE ChartId = $1`, "chart-linked-artifact"); got != 0 {
		t.Errorf("ArtifactCharts count after DeleteChart = %d, want 0", got)
	}
}

func TestDeleteChart_NotFound(t *testing.T) {
	ctx := context.Background()
	repo, _ := setupDashboardRepository(ctx, t)

	err := repo.DeleteChart(ctx, "nonexistent-chart")
	assertErrorIs(t, "DeleteChart(nonexistent)", err, grpcerr.ErrNotFound)
}

func TestDeleteChart_EmptyID(t *testing.T) {
	ctx := context.Background()
	repo, _ := setupDashboardRepository(ctx, t)

	err := repo.DeleteChart(ctx, "")
	assertErrorIs(t, "DeleteChart(empty)", err, grpcerr.ErrInvalidArgument)
}
