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
	"sync"
	"testing"

	fieldmaskpb "google.golang.org/protobuf/types/known/fieldmaskpb"

	dpb "xmanager_cloud/api/protos/dashboard"
)

// seedChart creates a chart with the shared fixture contents and returns it.
func seedChart(ctx context.Context, t *testing.T, repo *Repository, chartID string) *dpb.Chart {
	t.Helper()
	chart := blankChartProto(chartID, 100, 1)
	if err := repo.CreateChart(ctx, chart); err != nil {
		t.Fatalf("CreateChart failed: %v", err)
	}
	return chart
}

func TestUpdateChart_Success(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	chart := seedChart(ctx, t, repo, "test-update-chart-1")

	updated, err := repo.UpdateChart(ctx, &dpb.Chart{
		Name:        chart.GetName(),
		Title:       "Updated Chart Title",
		Description: "Updated Description",
		Wid:         5,
		Plots: []*dpb.PlotSpec{
			{PlotId: "plot-2", Title: "Accuracy vs Step", Type: dpb.PlotType_BAR},
		},
		PlotLayout: &dpb.LayoutSpec{Type: dpb.LayoutSpec_WRAP, Columns: 3},
	}, nil)
	if err != nil {
		t.Fatalf("UpdateChart failed: %v", err)
	}

	got, err := repo.GetChart(ctx, "test-update-chart-1")
	if err != nil {
		t.Fatalf("GetChart failed: %v", err)
	}

	if got.GetTitle() != "Updated Chart Title" {
		t.Errorf("GetChart Title mismatch: got %q, want %q", got.GetTitle(), "Updated Chart Title")
	}
	if got.GetDescription() != "Updated Description" {
		t.Errorf("GetChart Description mismatch: got %q, want %q", got.GetDescription(), "Updated Description")
	}
	// Wid is not an updatable field, so the merge keeps the stored value rather than the requested 5.
	if got.GetWid() != 1 {
		t.Errorf("GetChart Wid mismatch: got %d, want 1", got.GetWid())
	}
	if len(got.GetPlots()) != 1 || got.GetPlots()[0].GetPlotId() != "plot-2" {
		t.Errorf("GetChart Plots mismatch: got %+v, want 1 plot with ID plot-2", got.GetPlots())
	}
	if got.GetPlotLayout() == nil || got.GetPlotLayout().GetColumns() != 3 {
		t.Errorf("GetChart PlotLayout mismatch: got %+v", got.GetPlotLayout())
	}
	if updated.GetUpdateTime() == nil || updated.GetUpdateTime().AsTime().IsZero() {
		t.Errorf("UpdateChart result was not stamped with the commit timestamp")
	}
	if got.GetUpdateTime().GetSeconds() != updated.GetUpdateTime().GetSeconds() {
		t.Errorf("GetChart UpdateTime (%v) does not match commit UpdateTime (%v)", got.GetUpdateTime(), updated.GetUpdateTime())
	}
}

func TestUpdateChart_WithoutLayout(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	chart := seedChart(ctx, t, repo, "test-update-chart-no-layout")

	if _, err := repo.UpdateChart(ctx, &dpb.Chart{
		Name:  chart.GetName(),
		Title: "Updated Without Layout",
	}, nil); err != nil {
		t.Fatalf("UpdateChart without layout failed: %v", err)
	}

	got, err := repo.GetChart(ctx, "test-update-chart-no-layout")
	if err != nil {
		t.Fatalf("GetChart failed: %v", err)
	}

	if got.GetTitle() != "Updated Without Layout" {
		t.Errorf("GetChart Title mismatch: got %q, want %q", got.GetTitle(), "Updated Without Layout")
	}
	if got.GetPlotLayout() != nil {
		t.Errorf("GetChart PlotLayout should be nil, got %+v", got.GetPlotLayout())
	}
}

func TestUpdateChart_MaskedUpdateKeepsStoredFields(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	chart := seedChart(ctx, t, repo, "test-update-chart-masked")

	// Only the title is masked. Everything else the request carries must be ignored, and the plot
	// set has to survive even though the write replaces it wholesale.
	if _, err := repo.UpdateChart(ctx, &dpb.Chart{
		Name:        chart.GetName(),
		Title:       "Masked Title",
		Description: "Should Be Ignored",
		Plots:       []*dpb.PlotSpec{{PlotId: "should-be-ignored"}},
	}, &fieldmaskpb.FieldMask{Paths: []string{"title"}}); err != nil {
		t.Fatalf("UpdateChart with mask failed: %v", err)
	}

	got, err := repo.GetChart(ctx, "test-update-chart-masked")
	if err != nil {
		t.Fatalf("GetChart failed: %v", err)
	}

	if got.GetTitle() != "Masked Title" {
		t.Errorf("GetChart Title mismatch: got %q, want %q", got.GetTitle(), "Masked Title")
	}
	if got.GetDescription() != chart.GetDescription() {
		t.Errorf("GetChart Description mismatch: got %q, want the stored %q", got.GetDescription(), chart.GetDescription())
	}
	if len(got.GetPlots()) != 1 || got.GetPlots()[0].GetPlotId() != "plot-1" {
		t.Errorf("GetChart Plots mismatch: got %+v, want the stored plot-1", got.GetPlots())
	}
	if got.GetPlotLayout() == nil || got.GetPlotLayout().GetColumns() != 1 {
		t.Errorf("GetChart PlotLayout mismatch: got %+v, want the stored layout", got.GetPlotLayout())
	}
}

func TestUpdateChart_ConcurrentDisjointUpdatesBothSurvive(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	chart := seedChart(ctx, t, repo, "test-update-chart-concurrent")

	// Both writers replace every mutable column, so whichever commits second has to have merged on
	// top of the first. Merging outside the transaction would let it overwrite the other's field.
	updates := []struct {
		chart *dpb.Chart
		mask  *fieldmaskpb.FieldMask
	}{
		{
			chart: &dpb.Chart{Name: chart.GetName(), Title: "Title From Writer A"},
			mask:  &fieldmaskpb.FieldMask{Paths: []string{"title"}},
		},
		{
			chart: &dpb.Chart{Name: chart.GetName(), Description: "Description From Writer B"},
			mask:  &fieldmaskpb.FieldMask{Paths: []string{"description"}},
		},
	}

	var wg sync.WaitGroup
	errs := make([]error, len(updates))
	for i, update := range updates {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = repo.UpdateChart(ctx, update.chart, update.mask)
		}()
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent UpdateChart %d failed: %v", i, err)
		}
	}

	got, err := repo.GetChart(ctx, "test-update-chart-concurrent")
	if err != nil {
		t.Fatalf("GetChart failed: %v", err)
	}

	if got.GetTitle() != "Title From Writer A" {
		t.Errorf("GetChart Title mismatch: got %q, want %q", got.GetTitle(), "Title From Writer A")
	}
	if got.GetDescription() != "Description From Writer B" {
		t.Errorf("GetChart Description mismatch: got %q, want %q", got.GetDescription(), "Description From Writer B")
	}
}

func TestUpdateChart_UnsupportedMaskPath(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	chart := seedChart(ctx, t, repo, "test-update-chart-bad-mask")

	if _, err := repo.UpdateChart(ctx, &dpb.Chart{Name: chart.GetName(), Title: "Title"},
		&fieldmaskpb.FieldMask{Paths: []string{"owner"}}); err == nil {
		t.Error("UpdateChart with an unsupported mask path succeeded, want error")
	}
}

func TestUpdateChart_NotFound(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	chart := blankChartProto("non-existent-chart", 100, 1)
	if _, err := repo.UpdateChart(ctx, chart, nil); err == nil {
		t.Error("UpdateChart for non-existent chart succeeded, want error")
	}
}

func TestUpdateChart_NilOrEmpty(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	tests := []struct {
		name  string
		chart *dpb.Chart
	}{
		{name: "nil chart", chart: nil},
		{name: "empty name", chart: &dpb.Chart{}},
		{name: "malformed name", chart: &dpb.Chart{Name: "not-a-chart-name"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := repo.UpdateChart(ctx, tc.chart, nil); err == nil {
				t.Error("UpdateChart succeeded, want error")
			}
		})
	}
}
