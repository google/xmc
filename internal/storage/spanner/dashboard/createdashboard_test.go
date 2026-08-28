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

func TestCreateDashboard_Success(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	dash := blankDashboardProto("dashboards/test-create-1")
	if err := repo.CreateDashboard(ctx, dash, nil); err != nil {
		t.Fatalf("CreateDashboard failed: %v", err)
	}
}

func TestCreateDashboard_WithChartIDs(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	chartID := "f47ac10b-58cc-4372-a567-0e02b2c3d479"
	chart := blankChartProto(chartID, 100, 1)
	if err := repo.CreateChart(ctx, chart); err != nil {
		t.Fatalf("CreateChart failed: %v", err)
	}

	dash := blankDashboardProto("dashboards/test-create-with-charts")
	dash.ChartIds = []string{chartID}
	if err := repo.CreateDashboard(ctx, dash, nil); err != nil {
		t.Fatalf("CreateDashboard with chart IDs failed: %v", err)
	}
}

func TestCreateDashboard_WithoutLayout(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	dash := blankDashboardProto("dashboards/test-create-no-layout")
	dash.ChartLayout = nil
	if err := repo.CreateDashboard(ctx, dash, nil); err != nil {
		t.Fatalf("CreateDashboard without layout failed: %v", err)
	}
}

func TestCreateDashboard_NonExistentChartID(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	dash := blankDashboardProto("dashboards/test-create-nonexistent-chart")
	dash.ChartIds = []string{"00000000-0000-0000-0000-000000000000"}

	err := repo.CreateDashboard(ctx, dash, nil)
	if err == nil {
		t.Fatalf("CreateDashboard with non-existent chart ID expected foreign key error, got nil")
	}
}

func TestCreateDashboard_WithInlineCharts(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

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
	if len(got.GetChartIds()) != 1 || got.GetChartIds()[0] != chartID {
		t.Errorf("GetDashboard ChartIds mismatch: got %v, want [%q]", got.GetChartIds(), chartID)
	}

	gotChart, err := repo.GetChart(ctx, chartID)
	if err != nil {
		t.Fatalf("GetChart for inline chart failed: %v", err)
	}
	if gotChart.GetTitle() != chart.GetTitle() {
		t.Errorf("GetChart Title mismatch: got %q, want %q", gotChart.GetTitle(), chart.GetTitle())
	}
}

func TestCreateDashboard_InlineChartsRolledBackOnFailure(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	chartID := "1c2d3e4f-5a6b-4c7d-8e9f-0a1b2c3d4e5f"
	chart := blankChartProto(chartID, 100, 1)

	dash := blankDashboardProto("dashboards/test-create-inline-rollback")
	dash.ChartIds = []string{chartID, "00000000-0000-0000-0000-000000000000"}

	if err := repo.CreateDashboard(ctx, dash, []*dpb.Chart{chart}); err == nil {
		t.Fatalf("CreateDashboard referencing a non-existent chart expected foreign key error, got nil")
	}

	if _, err := repo.GetChart(ctx, chartID); err == nil {
		t.Errorf("GetChart found chart %q, want the inline chart to be rolled back with the dashboard", chartID)
	}
}
