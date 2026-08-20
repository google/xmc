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
	"testing"

	dpb "xmanager_cloud/dashboard_service/proto/api_go_proto"
)

func TestUpdateChart_Success(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	chart := blankChartProto("test-update-chart-1", 100, 1)
	if err := repo.CreateChart(ctx, chart); err != nil {
		t.Fatalf("CreateChart failed: %v", err)
	}

	updated := &dpb.Chart{
		Name:        chart.GetName(),
		Title:       "Updated Chart Title",
		Description: "Updated Description",
		Wid:         5,
		Plots: []*dpb.PlotSpec{
			{
				PlotId: "plot-2",
				Title:  "Accuracy vs Step",
				Type:   dpb.PlotType_BAR,
			},
		},
		PlotLayout: &dpb.LayoutSpec{
			Type:    dpb.LayoutSpec_WRAP,
			Columns: 3,
		},
	}

	if err := repo.UpdateChart(ctx, updated); err != nil {
		t.Fatalf("UpdateChart failed: %v", err)
	}

	got, err := repo.GetChart(ctx, "test-update-chart-1")
	if err != nil {
		t.Fatalf("GetChart failed: %v", err)
	}

	if got.GetTitle() != updated.GetTitle() {
		t.Errorf("GetChart Title mismatch: got %q, want %q", got.GetTitle(), updated.GetTitle())
	}
	if got.GetDescription() != updated.GetDescription() {
		t.Errorf("GetChart Description mismatch: got %q, want %q", got.GetDescription(), updated.GetDescription())
	}
	if got.GetWid() != updated.GetWid() {
		t.Errorf("GetChart Wid mismatch: got %d, want %d", got.GetWid(), updated.GetWid())
	}
	if len(got.GetPlots()) != 1 || got.GetPlots()[0].GetPlotId() != "plot-2" {
		t.Errorf("GetChart Plots mismatch: got %+v, want 1 plot with ID plot-2", got.GetPlots())
	}
	if got.GetPlotLayout() == nil || got.GetPlotLayout().GetColumns() != 3 {
		t.Errorf("GetChart PlotLayout mismatch: got %+v", got.GetPlotLayout())
	}
}

func TestUpdateChart_WithoutLayout(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	chart := blankChartProto("test-update-chart-no-layout", 100, 1)
	if err := repo.CreateChart(ctx, chart); err != nil {
		t.Fatalf("CreateChart failed: %v", err)
	}

	updated := &dpb.Chart{
		Name:  chart.GetName(),
		Title: "Updated Without Layout",
	}

	if err := repo.UpdateChart(ctx, updated); err != nil {
		t.Fatalf("UpdateChart without layout failed: %v", err)
	}

	got, err := repo.GetChart(ctx, "test-update-chart-no-layout")
	if err != nil {
		t.Fatalf("GetChart failed: %v", err)
	}

	if got.GetTitle() != updated.GetTitle() {
		t.Errorf("GetChart Title mismatch: got %q, want %q", got.GetTitle(), updated.GetTitle())
	}
	if got.GetPlotLayout() != nil {
		t.Errorf("GetChart PlotLayout should be nil, got %+v", got.GetPlotLayout())
	}
}

func TestUpdateChart_NotFound(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	chart := blankChartProto("non-existent-chart", 100, 1)
	if err := repo.UpdateChart(ctx, chart); err == nil {
		t.Errorf("UpdateChart for non-existent chart succeeded, want error")
	}
}

func TestUpdateChart_NilOrEmpty(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	if err := repo.UpdateChart(ctx, nil); err == nil {
		t.Errorf("UpdateChart(nil) succeeded, want error")
	}

	if err := repo.UpdateChart(ctx, &dpb.Chart{}); err == nil {
		t.Errorf("UpdateChart(empty name) succeeded, want error")
	}
}
