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
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/testing/protocmp"
	fmpb "google.golang.org/protobuf/types/known/fieldmaskpb"
	dpb "xmanager_cloud/api/protos/dashboard"
	"xmanager_cloud/internal/grpcerr"
	storagedata "xmanager_cloud/internal/storage/data"
	postgrestest "xmanager_cloud/internal/storage/postgres/testing"
	"xmanager_cloud/internal/validator"
)

const (
	chartID1 = "c9a646d3-9c61-4cd7-bf10-8e96dc638210"
	chartID2 = "f47ac10b-58cc-4372-a567-0e02b2c3d479"
	chartID3 = "00000000-0000-0000-0000-000000000003"
)

func setupDashboardRepository(ctx context.Context, t *testing.T) (*Repository, *pgxpool.Pool) {
	t.Helper()
	_, essPool := postgrestest.Setup(ctx, t)
	return NewRepository(essPool), essPool
}

func blankDashboardProto(name string) *dpb.Dashboard {
	return &dpb.Dashboard{
		Name:        name,
		Title:       "Test Dashboard Title",
		Description: "Test Dashboard Description",
		Owner:       "user@example.com",
		ChartIds:    []string{},
		ChartLayout: &dpb.LayoutSpec{Type: dpb.LayoutSpec_GRID, Columns: 2},
	}
}

func blankChartProto(chartID string, xid, wid int64) *dpb.Chart {
	return &dpb.Chart{
		Name:        validator.ConstructWorkUnitChartName(xid, wid, chartID),
		Title:       "Test Chart Title",
		Description: "Test Chart Description",
		Xid:         xid,
		Wid:         wid,
		Plots: []*dpb.PlotSpec{
			{PlotId: "plot-1", Title: "Loss vs Epochs", Type: dpb.PlotType_SCATTER},
		},
		PlotLayout: &dpb.LayoutSpec{Type: dpb.LayoutSpec_GRID, Columns: 1},
	}
}

func seedArtifactWithChart(ctx context.Context, t *testing.T, pool *pgxpool.Pool, xid, wid, artifactID int64, chartID string) {
	t.Helper()
	now := time.Now().UTC()
	if _, err := pool.Exec(ctx, `INSERT INTO Experiments (Xid, Title, Creator, Priority, Urls, Tags, Notes, Resources, Status, ActiveState, LaunchState, CreateTime, UpdateTime)
		VALUES ($1, 'Exp', 'user', 1, '{}', '{}', '', '{}', '\x', 1, 1, $2, $2) ON CONFLICT (Xid) DO NOTHING`, xid, now); err != nil {
		t.Fatalf("insert Experiment failed: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO Artifacts (Xid, WorkUnitId, ArtifactId, Url, Icon, DisplayName, Priority, Tags, Notes, LifecyclePhase, DataType, MimeType, CreateTime, UpdateTime)
		VALUES ($1, $2, $3, 'http://test', 'icon', 'Art', 1, '{}', '', 0, 0, 'text/plain', $4, $4)`, xid, wid, artifactID, now); err != nil {
		t.Fatalf("insert Artifact failed: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO ArtifactCharts (Xid, WorkUnitId, ArtifactId, ArtifactChartId, ChartId, CreatedAt)
		VALUES ($1, $2, $3, 'ac-1', $4, $5)`, xid, wid, artifactID, chartID, now); err != nil {
		t.Fatalf("insert ArtifactChart failed: %v", err)
	}
}

func TestDashboardCRUD_AndChildOrdering(t *testing.T) {
	ctx := context.Background()
	repo, pool := setupDashboardRepository(ctx, t)

	// Insert inline charts and reverse-ordered ChartIds to verify canonical ordering via ARRAY(SELECT ... ORDER BY DashboardChartId ASC).
	c1 := blankChartProto(chartID1, 100, 1)
	c2 := blankChartProto(chartID2, 100, 1)
	dash := blankDashboardProto("dashboards/dash-crud")
	dash.ChartIds = []string{chartID2, chartID1}
	if err := repo.CreateDashboard(ctx, dash, []*dpb.Chart{c1, c2}); err != nil {
		t.Fatalf("CreateDashboard failed: %v", err)
	}

	got, err := repo.GetDashboard(ctx, "dash-crud")
	if err != nil {
		t.Fatalf("GetDashboard failed: %v", err)
	}
	wantChartIDs := []string{chartID1, chartID2}
	if !slices.Equal(got.GetChartIds(), wantChartIDs) {
		t.Errorf("GetDashboard ChartIds = %v, want %v", got.GetChartIds(), wantChartIDs)
	}

	// Masked update: only title changes; description and chart_ids remain intact.
	updated, err := repo.UpdateDashboard(ctx, &dpb.Dashboard{
		Name:        "dashboards/dash-crud",
		Title:       "Masked Title",
		Description: "Ignored Description",
	}, &fmpb.FieldMask{Paths: []string{"title"}})
	if err != nil {
		t.Fatalf("UpdateDashboard masked failed: %v", err)
	}
	if updated.GetTitle() != "Masked Title" || updated.GetDescription() != dash.GetDescription() {
		t.Errorf("UpdateDashboard masked = (%q, %q), want ('Masked Title', %q)", updated.GetTitle(), updated.GetDescription(), dash.GetDescription())
	}

	// CamelCase chartIds mask + clearing ChartLayout via full update.
	if _, err := repo.UpdateDashboard(ctx, &dpb.Dashboard{
		Name:     "dashboards/dash-crud",
		ChartIds: []string{chartID2},
	}, &fmpb.FieldMask{Paths: []string{"chartIds"}}); err != nil {
		t.Fatalf("UpdateDashboard camelCase chartIds failed: %v", err)
	}

	// Monotonic UpdateTime when previous UpdatedAt is in the future.
	future := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
	if _, err := pool.Exec(ctx, `UPDATE Dashboards SET UpdatedAt = $1 WHERE DashboardId = 'dash-crud'`, future); err != nil {
		t.Fatalf("set future UpdatedAt failed: %v", err)
	}
	monotonicGot, err := repo.UpdateDashboard(ctx, &dpb.Dashboard{
		Name:  "dashboards/dash-crud",
		Title: "Monotonic Title",
	}, &fmpb.FieldMask{Paths: []string{"title"}})
	if err != nil {
		t.Fatalf("UpdateDashboard monotonic failed: %v", err)
	}
	wantMinTime := future.Add(time.Microsecond)
	if !monotonicGot.GetUpdateTime().AsTime().Equal(wantMinTime) {
		t.Errorf("monotonic UpdateTime = %v, want %v", monotonicGot.GetUpdateTime().AsTime(), wantMinTime)
	}

	// No-op update preserves UpdateTime.
	noopGot, err := repo.UpdateDashboard(ctx, monotonicGot, nil)
	if err != nil {
		t.Fatalf("UpdateDashboard no-op failed: %v", err)
	}
	if !noopGot.GetUpdateTime().AsTime().Equal(wantMinTime) {
		t.Errorf("no-op UpdateTime = %v, want %v", noopGot.GetUpdateTime().AsTime(), wantMinTime)
	}

	// Clear chart_ids via mask.
	if _, err := repo.UpdateDashboard(ctx, &dpb.Dashboard{
		Name: "dashboards/dash-crud",
	}, &fmpb.FieldMask{Paths: []string{"chart_ids"}}); err != nil {
		t.Fatalf("UpdateDashboard clear chart_ids failed: %v", err)
	}
	cleared, err := repo.GetDashboard(ctx, "dash-crud")
	if err != nil || len(cleared.GetChartIds()) != 0 {
		t.Fatalf("GetDashboard after clear = (%v, %v), want 0 charts", cleared, err)
	}

	// DeleteDashboard cascades DashboardCharts and returns NotFound afterward.
	if err := repo.DeleteDashboard(ctx, "dash-crud"); err != nil {
		t.Fatalf("DeleteDashboard failed: %v", err)
	}
	if _, err := repo.GetDashboard(ctx, "dash-crud"); !errors.Is(err, grpcerr.ErrNotFound) {
		t.Errorf("GetDashboard after delete error = %v, want ErrNotFound", err)
	}
	if err := repo.DeleteDashboard(ctx, "dash-crud"); !errors.Is(err, grpcerr.ErrNotFound) {
		t.Errorf("second DeleteDashboard error = %v, want ErrNotFound", err)
	}
}

func TestCreateDashboard_InlineChartsRolledBackOnFailure(t *testing.T) {
	ctx := context.Background()
	repo, _ := setupDashboardRepository(ctx, t)

	inlineChart := blankChartProto(chartID1, 100, 1)
	dash := blankDashboardProto("dashboards/rollback-dash")
	dash.ChartIds = []string{chartID1, "00000000-0000-0000-0000-000000000000"}

	if err := repo.CreateDashboard(ctx, dash, []*dpb.Chart{inlineChart}); err == nil {
		t.Fatal("CreateDashboard referencing non-existent chart succeeded, want FK error")
	}
	if _, err := repo.GetChart(ctx, chartID1); !errors.Is(err, grpcerr.ErrNotFound) {
		t.Errorf("GetChart for rolled-back inline chart error = %v, want ErrNotFound", err)
	}
}

func TestChartCRUD_ConcurrentUpdate_Monotonic_AndDeleteFKCleanup(t *testing.T) {
	ctx := context.Background()
	repo, pool := setupDashboardRepository(ctx, t)

	// Create chart with out-of-order PlotIds ("plot-b", "plot-a") to verify ORDER BY PlotId ASC.
	chart := blankChartProto(chartID1, 100, 1)
	chart.Plots = []*dpb.PlotSpec{
		{PlotId: "plot-b", Title: "Accuracy", Type: dpb.PlotType_BAR},
		{PlotId: "plot-a", Title: "Loss", Type: dpb.PlotType_SCATTER},
	}
	if err := repo.CreateChart(ctx, chart); err != nil {
		t.Fatalf("CreateChart failed: %v", err)
	}

	got, err := repo.GetChart(ctx, chartID1)
	if err != nil {
		t.Fatalf("GetChart failed: %v", err)
	}
	if len(got.GetPlots()) != 2 || got.GetPlots()[0].GetPlotId() != "plot-a" || got.GetPlots()[1].GetPlotId() != "plot-b" {
		t.Errorf("GetChart Plots = %+v, want [plot-a, plot-b]", got.GetPlots())
	}

	// Concurrent disjoint masked updates both survive under FOR NO KEY UPDATE.
	var wg sync.WaitGroup
	errs := make([]error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, errs[0] = repo.UpdateChart(ctx, &dpb.Chart{Name: chart.GetName(), Title: "Writer A Title"}, &fmpb.FieldMask{Paths: []string{"title"}})
	}()
	go func() {
		defer wg.Done()
		_, errs[1] = repo.UpdateChart(ctx, &dpb.Chart{Name: chart.GetName(), Description: "Writer B Desc"}, &fmpb.FieldMask{Paths: []string{"description"}})
	}()
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent UpdateChart[%d] failed: %v", i, err)
		}
	}

	afterConcurrent, err := repo.GetChart(ctx, chartID1)
	if err != nil {
		t.Fatalf("GetChart after concurrent updates failed: %v", err)
	}
	if afterConcurrent.GetTitle() != "Writer A Title" || afterConcurrent.GetDescription() != "Writer B Desc" {
		t.Errorf("concurrent result = (%q, %q), want ('Writer A Title', 'Writer B Desc')", afterConcurrent.GetTitle(), afterConcurrent.GetDescription())
	}
	if len(afterConcurrent.GetPlots()) != 2 {
		t.Errorf("concurrent update lost plots: got %d, want 2", len(afterConcurrent.GetPlots()))
	}

	// Monotonic UpdateTime when previous UpdatedAt is in the future + immutable Wid check.
	future := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
	if _, err := pool.Exec(ctx, `UPDATE Charts SET UpdatedAt = $1 WHERE ChartId = $2`, future, chartID1); err != nil {
		t.Fatalf("set future UpdatedAt on Chart failed: %v", err)
	}
	updated, err := repo.UpdateChart(ctx, &dpb.Chart{
		Name:        chart.GetName(),
		Title:       "Full Update Title",
		Description: "Full Update Desc",
		Wid:         99,
		Plots:       []*dpb.PlotSpec{{PlotId: "plot-c", Title: "Latency", Type: dpb.PlotType_HISTOGRAM}},
		PlotLayout:  nil,
	}, nil)
	if err != nil {
		t.Fatalf("UpdateChart full failed: %v", err)
	}
	if !updated.GetUpdateTime().AsTime().Equal(future.Add(time.Microsecond)) {
		t.Errorf("UpdateChart monotonic UpdateTime = %v, want %v", updated.GetUpdateTime().AsTime(), future.Add(time.Microsecond))
	}
	if updated.GetWid() != 1 || updated.GetPlotLayout() != nil {
		t.Errorf("UpdateChart Wid=%d PlotLayout=%v, want Wid=1 PlotLayout=nil", updated.GetWid(), updated.GetPlotLayout())
	}

	// Link chart to a Dashboard (DashboardCharts) and an Artifact (ArtifactCharts), then DeleteChart.
	dash := blankDashboardProto("dashboards/linked-dash")
	dash.ChartIds = []string{chartID1}
	if err := repo.CreateDashboard(ctx, dash, nil); err != nil {
		t.Fatalf("CreateDashboard linked-dash failed: %v", err)
	}
	seedArtifactWithChart(ctx, t, pool, 100, 1, 10, chartID1)

	if err := repo.DeleteChart(ctx, chartID1); err != nil {
		t.Fatalf("DeleteChart with references failed: %v", err)
	}
	if _, err := repo.GetChart(ctx, chartID1); !errors.Is(err, grpcerr.ErrNotFound) {
		t.Errorf("GetChart after DeleteChart error = %v, want ErrNotFound", err)
	}
	gotDash, err := repo.GetDashboard(ctx, "linked-dash")
	if err != nil || len(gotDash.GetChartIds()) != 0 {
		t.Errorf("GetDashboard after DeleteChart = (%v, %v), want 0 ChartIds", gotDash, err)
	}
	var acCount int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM ArtifactCharts WHERE ChartId = $1`, chartID1).Scan(&acCount); err != nil || acCount != 0 {
		t.Errorf("ArtifactCharts count after DeleteChart = (%d, %v), want 0", acCount, err)
	}
}

func TestListDashboards_PaginationAndOrderBy(t *testing.T) {
	ctx := context.Background()
	repo, _ := setupDashboardRepository(ctx, t)

	c1 := blankChartProto(chartID1, 100, 1)
	c2 := blankChartProto(chartID2, 100, 1)
	if err := repo.CreateChart(ctx, c1); err != nil {
		t.Fatalf("CreateChart c1 failed: %v", err)
	}
	if err := repo.CreateChart(ctx, c2); err != nil {
		t.Fatalf("CreateChart c2 failed: %v", err)
	}

	specs := []struct {
		name, title, owner string
		charts             []string
	}{
		{"dashboards/a-oldest", "Zebra", "owner-c@example.com", []string{chartID2, chartID1}},
		{"dashboards/b-middle", "Apple", "owner-a@example.com", nil},
		{"dashboards/c-newest", "Mango", "owner-b@example.com", []string{chartID1}},
	}
	for _, s := range specs {
		d := blankDashboardProto(s.name)
		d.Title = s.title
		d.Owner = s.owner
		d.ChartIds = s.charts
		if err := repo.CreateDashboard(ctx, d, nil); err != nil {
			t.Fatalf("CreateDashboard %s failed: %v", s.name, err)
		}
	}

	// Default ordering: newest first (created DESC, dashboard_id ASC) + ChartIds hydration.
	all, _, err := repo.ListDashboards(ctx, 10, "", "", "", 0)
	if err != nil || len(all) != 3 {
		t.Fatalf("ListDashboards default = (%d, %v), want 3", len(all), err)
	}
	if all[0].GetName() != "dashboards/c-newest" || all[2].GetName() != "dashboards/a-oldest" {
		t.Errorf("default order = [%s, %s, %s], want c-newest first and a-oldest last", all[0].GetName(), all[1].GetName(), all[2].GetName())
	}
	if !slices.Equal(all[2].GetChartIds(), storagedata.SortChartIDs([]string{chartID2, chartID1})) {
		t.Errorf("hydrated ChartIds = %v, want sorted", all[2].GetChartIds())
	}

	// Filter test.
	filtered, _, err := repo.ListDashboards(ctx, 10, "", `title = "Apple"`, "", 0)
	if err != nil || len(filtered) != 1 || filtered[0].GetName() != "dashboards/b-middle" {
		t.Errorf("ListDashboards filter = (%+v, %v), want only b-middle", filtered, err)
	}

	// Multi-page pagination across the orderings supported by ExtractDashboardCursor.
	orderTests := []struct {
		orderBy   string
		wantNames []string
	}{
		{"", []string{"dashboards/c-newest", "dashboards/b-middle", "dashboards/a-oldest"}},
		{"created asc", []string{"dashboards/a-oldest", "dashboards/b-middle", "dashboards/c-newest"}},
		{"updated desc", []string{"dashboards/c-newest", "dashboards/b-middle", "dashboards/a-oldest"}},
		{"dashboard_id asc", []string{"dashboards/a-oldest", "dashboards/b-middle", "dashboards/c-newest"}},
	}
	for _, tc := range orderTests {
		t.Run("order_"+tc.orderBy, func(t *testing.T) {
			var gotNames []string
			var token string
			for pageNum := 0; pageNum < 2; pageNum++ {
				page, next, err := repo.ListDashboards(ctx, 2, token, "", tc.orderBy, 0)
				if err != nil {
					t.Fatalf("ListDashboards(%q) page %d failed: %v", tc.orderBy, pageNum, err)
				}
				for _, d := range page {
					gotNames = append(gotNames, d.GetName())
				}
				token = next
			}
			if token != "" {
				t.Errorf("expected empty token after 2 pages, got %q", token)
			}
			if !slices.Equal(gotNames, tc.wantNames) {
				t.Errorf("ListDashboards(%q) = %v, want %v", tc.orderBy, gotNames, tc.wantNames)
			}
		})
	}
}

func TestListCharts_ParentsFiltersAndPagination(t *testing.T) {
	ctx := context.Background()
	repo, _ := setupDashboardRepository(ctx, t)

	c1 := blankChartProto(chartID1, 100, 1)
	c1.Title = "Beta Chart"
	c2 := blankChartProto(chartID2, 100, 2)
	c2.Title = "Alpha Chart"
	c2.Plots = []*dpb.PlotSpec{
		{PlotId: "p-2", Title: "P2", Type: dpb.PlotType_BAR},
		{PlotId: "p-1", Title: "P1", Type: dpb.PlotType_SCATTER},
	}
	c3 := blankChartProto(chartID3, 200, 3)
	c3.Title = "Gamma Chart"
	c3.Plots = nil
	for _, c := range []*dpb.Chart{c1, c2, c3} {
		if err := repo.CreateChart(ctx, c); err != nil {
			t.Fatalf("CreateChart %s failed: %v", c.GetName(), err)
		}
	}

	dash := blankDashboardProto("dashboards/parent-dash")
	dash.ChartIds = []string{chartID1, chartID2, chartID3}
	if err := repo.CreateDashboard(ctx, dash, nil); err != nil {
		t.Fatalf("CreateDashboard parent-dash failed: %v", err)
	}
	emptyDash := blankDashboardProto("dashboards/empty-dash")
	if err := repo.CreateDashboard(ctx, emptyDash, nil); err != nil {
		t.Fatalf("CreateDashboard empty-dash failed: %v", err)
	}

	// WorkUnit parent.
	wuCharts, _, err := repo.ListCharts(ctx, "experiments/200/workUnits/3", 10, "", "", "", 0)
	if err != nil || len(wuCharts) != 1 || wuCharts[0].GetName() != c3.GetName() {
		t.Errorf("ListCharts workUnit parent = (%+v, %v), want [%s]", wuCharts, err, c3.GetName())
	}

	// Empty dashboard parent.
	emptyCharts, tok, err := repo.ListCharts(ctx, "dashboards/empty-dash", 10, "", "", "", 0)
	if err != nil || len(emptyCharts) != 0 || tok != "" {
		t.Errorf("ListCharts empty dashboard = (%+v, %q, %v), want empty", emptyCharts, tok, err)
	}

	// Dashboard parent + filter + plot hydration order.
	dashFiltered, _, err := repo.ListCharts(ctx, "dashboards/parent-dash", 10, "", `title = "Alpha Chart"`, "", 0)
	if err != nil || len(dashFiltered) != 1 {
		t.Fatalf("ListCharts dashboard parent + filter = (%+v, %v), want 1 chart", dashFiltered, err)
	}
	if diff := cmp.Diff([]*dpb.PlotSpec{c2.GetPlots()[1], c2.GetPlots()[0]}, dashFiltered[0].GetPlots(), protocmp.Transform()); diff != "" {
		t.Errorf("hydrated plots diff (-want +got):\n%s", diff)
	}

	// Multi-page pagination across the orderings supported by ExtractChartCursor (both root and dashboard parent).
	for _, parent := range []string{"", "dashboards/parent-dash"} {
		for _, orderBy := range []string{"", "chart_id asc"} {
			t.Run(fmt.Sprintf("parent_%s_order_%s", parent, orderBy), func(t *testing.T) {
				p1, tok1, err := repo.ListCharts(ctx, parent, 2, "", "", orderBy, 0)
				if err != nil || len(p1) != 2 || tok1 == "" {
					t.Fatalf("page 1 (%q, %q) = (len=%d, tok=%q, err=%v), want 2 items and token", parent, orderBy, len(p1), tok1, err)
				}
				p2, tok2, err := repo.ListCharts(ctx, parent, 2, tok1, "", orderBy, 0)
				if err != nil || len(p2) != 1 || tok2 != "" {
					t.Fatalf("page 2 (%q, %q) = (len=%d, tok=%q, err=%v), want 1 item and empty token", parent, orderBy, len(p2), tok2, err)
				}
			})
		}
	}
}

func TestRepository_ValidationAndNotFoundErrors(t *testing.T) {
	ctx := context.Background()
	repo, _ := setupDashboardRepository(ctx, t)

	if err := repo.CreateDashboard(ctx, nil, nil); !errors.Is(err, grpcerr.ErrInvalidArgument) {
		t.Errorf("CreateDashboard(nil) = %v, want ErrInvalidArgument", err)
	}
	if err := repo.CreateDashboard(ctx, blankDashboardProto("dashboards/d1"), []*dpb.Chart{{Name: "bad"}}); !errors.Is(err, grpcerr.ErrInvalidArgument) {
		t.Errorf("CreateDashboard(bad inline chart) = %v, want ErrInvalidArgument", err)
	}
	if _, err := repo.GetDashboard(ctx, ""); !errors.Is(err, grpcerr.ErrInvalidArgument) {
		t.Errorf("GetDashboard('') = %v, want ErrInvalidArgument", err)
	}
	if _, err := repo.GetDashboard(ctx, "missing"); !errors.Is(err, grpcerr.ErrNotFound) {
		t.Errorf("GetDashboard('missing') = %v, want ErrNotFound", err)
	}
	if _, err := repo.UpdateDashboard(ctx, nil, nil); !errors.Is(err, grpcerr.ErrInvalidArgument) {
		t.Errorf("UpdateDashboard(nil) = %v, want ErrInvalidArgument", err)
	}
	if _, err := repo.UpdateDashboard(ctx, blankDashboardProto("dashboards/missing"), nil); !errors.Is(err, grpcerr.ErrNotFound) {
		t.Errorf("UpdateDashboard(missing) = %v, want ErrNotFound", err)
	}
	if err := repo.DeleteDashboard(ctx, ""); !errors.Is(err, grpcerr.ErrInvalidArgument) {
		t.Errorf("DeleteDashboard('') = %v, want ErrInvalidArgument", err)
	}
	if _, _, err := repo.ListDashboards(ctx, 0, "", "", "", 0); !errors.Is(err, grpcerr.ErrInvalidArgument) {
		t.Errorf("ListDashboards(pageSize=0) = %v, want ErrInvalidArgument", err)
	}
	if _, _, err := repo.ListDashboards(ctx, 10, "", "a = b = c", "", 0); !errors.Is(err, grpcerr.ErrInvalidArgument) {
		t.Errorf("ListDashboards(unparseable filter) = %v, want ErrInvalidArgument", err)
	}
	if _, _, err := repo.ListDashboards(ctx, 10, "", "invalid = [filter", "", 0); err == nil {
		t.Error("ListDashboards(unsupported filter field) = nil, want error")
	}

	if err := repo.CreateChart(ctx, nil); !errors.Is(err, grpcerr.ErrInvalidArgument) {
		t.Errorf("CreateChart(nil) = %v, want ErrInvalidArgument", err)
	}
	if _, err := repo.GetChart(ctx, ""); !errors.Is(err, grpcerr.ErrInvalidArgument) {
		t.Errorf("GetChart('') = %v, want ErrInvalidArgument", err)
	}
	if _, err := repo.GetChart(ctx, "missing"); !errors.Is(err, grpcerr.ErrNotFound) {
		t.Errorf("GetChart('missing') = %v, want ErrNotFound", err)
	}
	if _, err := repo.UpdateChart(ctx, nil, nil); !errors.Is(err, grpcerr.ErrInvalidArgument) {
		t.Errorf("UpdateChart(nil) = %v, want ErrInvalidArgument", err)
	}
	if _, err := repo.UpdateChart(ctx, blankChartProto("missing", 100, 1), nil); !errors.Is(err, grpcerr.ErrNotFound) {
		t.Errorf("UpdateChart(missing) = %v, want ErrNotFound", err)
	}
	if err := repo.DeleteChart(ctx, ""); !errors.Is(err, grpcerr.ErrInvalidArgument) {
		t.Errorf("DeleteChart('') = %v, want ErrInvalidArgument", err)
	}
	if err := repo.DeleteChart(ctx, "missing"); !errors.Is(err, grpcerr.ErrNotFound) {
		t.Errorf("DeleteChart('missing') = %v, want ErrNotFound", err)
	}
	if _, _, err := repo.ListCharts(ctx, "", 0, "", "", "", 0); !errors.Is(err, grpcerr.ErrInvalidArgument) {
		t.Errorf("ListCharts(pageSize=0) = %v, want ErrInvalidArgument", err)
	}
	if _, _, err := repo.ListCharts(ctx, "experiments/100", 10, "", "", "", 0); err == nil || !strings.Contains(err.Error(), "work unit parent name") {
		t.Errorf("ListCharts(experiment-only parent) = %v, want work unit parent error", err)
	}
	if _, _, err := repo.ListCharts(ctx, "bad/parent/format", 10, "", "", "", 0); err == nil {
		t.Error("ListCharts(bad parent) = nil, want error")
	}
	if _, _, err := repo.ListCharts(ctx, "", 10, "invalid-token-!@#", "", "", 0); !errors.Is(err, grpcerr.ErrInvalidArgument) {
		t.Errorf("ListCharts(bad pageToken) = %v, want ErrInvalidArgument", err)
	}
}
