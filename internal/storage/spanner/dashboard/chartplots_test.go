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
	"strings"
	"testing"

	dpb "github.com/google/xmc/api/protos/dashboard"
)

func TestHydrateChartPlots_BatchesAcrossCharts(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	withPlots := blankChartProto("hydrate-with-plots", 100, 1)
	withPlots.Plots = []*dpb.PlotSpec{
		{PlotId: "plot-a", Title: "Loss", Type: dpb.PlotType_SCATTER},
		{PlotId: "plot-b", Title: "Accuracy", Type: dpb.PlotType_BAR},
	}
	other := blankChartProto("hydrate-other", 100, 1)
	noPlots := blankChartProto("hydrate-no-plots", 100, 1)
	noPlots.Plots = nil
	for _, c := range []*dpb.Chart{withPlots, other, noPlots} {
		if err := repo.CreateChart(ctx, c); err != nil {
			t.Fatalf("CreateChart %s failed: %v", c.GetName(), err)
		}
	}

	charts := []*dpb.Chart{
		{Name: withPlots.GetName()},
		{Name: other.GetName()},
		{Name: noPlots.GetName()},
	}

	txn := repo.client.ReadOnlyTransaction()
	defer txn.Close()
	if err := repo.hydrateChartPlots(ctx, txn, charts); err != nil {
		t.Fatalf("hydrateChartPlots failed: %v", err)
	}

	if len(charts[0].GetPlots()) != 2 {
		t.Fatalf("hydrateChartPlots plot count mismatch for %q: got %d, want 2", charts[0].GetName(), len(charts[0].GetPlots()))
	}
	gotTitles := []string{charts[0].GetPlots()[0].GetTitle(), charts[0].GetPlots()[1].GetTitle()}
	for _, want := range []string{"Loss", "Accuracy"} {
		if gotTitles[0] != want && gotTitles[1] != want {
			t.Errorf("hydrateChartPlots missing plot %q: got %v", want, gotTitles)
		}
	}
	if len(charts[1].GetPlots()) != 1 || charts[1].GetPlots()[0].GetPlotId() != "plot-1" {
		t.Errorf("hydrateChartPlots plots mismatch for %q: got %+v", charts[1].GetName(), charts[1].GetPlots())
	}
	if len(charts[2].GetPlots()) != 0 {
		t.Errorf("hydrateChartPlots expected no plots for %q, got %d", charts[2].GetName(), len(charts[2].GetPlots()))
	}
}

func TestHydrateChartPlots_NoCharts(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	txn := repo.client.ReadOnlyTransaction()
	defer txn.Close()
	if err := repo.hydrateChartPlots(ctx, txn, nil); err != nil {
		t.Errorf("hydrateChartPlots with no charts failed: %v", err)
	}
}

func TestHydrateChartPlots_InvalidChartName(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	txn := repo.client.ReadOnlyTransaction()
	defer txn.Close()
	charts := []*dpb.Chart{{Name: "not-a-chart-resource-name"}}
	if err := repo.hydrateChartPlots(ctx, txn, charts); err == nil {
		t.Errorf("hydrateChartPlots with invalid chart name succeeded, want error")
	}
}

// TestBuildChartPlotsStatement_BatchesIntoUnnest pins that the per-chart chart_id disjunction is
// collapsed into a single IN UNNEST clause bound to one array parameter, rather than expanding to
// one OR term per chart in the page.
func TestBuildChartPlotsStatement_BatchesIntoUnnest(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	chartIDs := []string{"unnest-chart-a", "unnest-chart-b", "unnest-chart-c"}
	stmt, err := repo.buildChartPlotsStatement(chartIDs)
	if err != nil {
		t.Fatalf("buildChartPlotsStatement failed: %v", err)
	}
	if !strings.Contains(stmt.SQL, "IN UNNEST") {
		t.Errorf("buildChartPlotsStatement SQL = %q, want an IN UNNEST clause", stmt.SQL)
	}
	if strings.Contains(stmt.SQL, " OR ") {
		t.Errorf("buildChartPlotsStatement SQL = %q, want the disjunction collapsed with no OR terms", stmt.SQL)
	}
	params := fmt.Sprint(stmt.Params)
	for _, chartID := range chartIDs {
		if strings.Contains(stmt.SQL, chartID) {
			t.Errorf("buildChartPlotsStatement inlined chart ID %q into SQL %q, want it bound as a parameter", chartID, stmt.SQL)
		}
		if !strings.Contains(params, chartID) {
			t.Errorf("buildChartPlotsStatement params %s missing chart ID %q", params, chartID)
		}
	}
}

// TestBuildChartPlotsStatement_SingleChartID covers the GetChart path, which reaches the batched
// builder through fetchChartPlots with exactly one ID.
func TestBuildChartPlotsStatement_SingleChartID(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	stmt, err := repo.buildChartPlotsStatement([]string{"unnest-single-chart"})
	if err != nil {
		t.Fatalf("buildChartPlotsStatement failed: %v", err)
	}
	if !strings.Contains(fmt.Sprint(stmt.Params), "unnest-single-chart") {
		t.Errorf("buildChartPlotsStatement params %v missing the chart ID", stmt.Params)
	}
}

func TestFetchPlotsByChartIDs_EmptyInput(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	txn := repo.client.ReadOnlyTransaction()
	defer txn.Close()
	plots, err := repo.fetchPlotsByChartIDs(ctx, txn, nil)
	if err != nil {
		t.Fatalf("fetchPlotsByChartIDs with empty input failed: %v", err)
	}
	if len(plots) != 0 {
		t.Errorf("fetchPlotsByChartIDs with empty input returned %d entries, want 0", len(plots))
	}
}
