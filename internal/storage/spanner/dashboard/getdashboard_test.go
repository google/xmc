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
)

func TestGetDashboard_Success(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	chartID := "f47ac10b-58cc-4372-a567-0e02b2c3d479"
	chart := blankChartProto(chartID, 100, 1)
	if err := repo.CreateChart(ctx, chart); err != nil {
		t.Fatalf("CreateChart failed: %v", err)
	}

	dash := blankDashboardProto("dashboards/test-get-1")
	dash.ChartIds = []string{chartID}
	if err := repo.CreateDashboard(ctx, dash, nil); err != nil {
		t.Fatalf("CreateDashboard failed: %v", err)
	}

	got, err := repo.GetDashboard(ctx, "test-get-1")
	if err != nil {
		t.Fatalf("GetDashboard failed: %v", err)
	}

	if got.GetName() != dash.GetName() {
		t.Errorf("GetDashboard Name mismatch: got %q, want %q", got.GetName(), dash.GetName())
	}
	if got.GetTitle() != dash.GetTitle() {
		t.Errorf("GetDashboard Title mismatch: got %q, want %q", got.GetTitle(), dash.GetTitle())
	}
	if got.GetDescription() != dash.GetDescription() {
		t.Errorf("GetDashboard Description mismatch: got %q, want %q", got.GetDescription(), dash.GetDescription())
	}
	if got.GetOwner() != dash.GetOwner() {
		t.Errorf("GetDashboard Owner mismatch: got %q, want %q", got.GetOwner(), dash.GetOwner())
	}
	if len(got.GetChartIds()) != 1 || got.GetChartIds()[0] != chartID {
		t.Errorf("GetDashboard ChartIds mismatch: got %v, want [%q]", got.GetChartIds(), chartID)
	}
	if got.GetChartLayout() == nil || got.GetChartLayout().GetColumns() != dash.GetChartLayout().GetColumns() {
		t.Errorf("GetDashboard ChartLayout mismatch")
	}
}

func TestGetDashboard_NoCharts(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	dash := blankDashboardProto("dashboards/test-get-no-charts")
	dash.ChartIds = nil
	if err := repo.CreateDashboard(ctx, dash, nil); err != nil {
		t.Fatalf("CreateDashboard failed: %v", err)
	}

	got, err := repo.GetDashboard(ctx, "test-get-no-charts")
	if err != nil {
		t.Fatalf("GetDashboard failed: %v", err)
	}

	if len(got.GetChartIds()) != 0 {
		t.Errorf("Expected 0 chart IDs, got %v", got.GetChartIds())
	}
}

func TestGetDashboard_NotFound(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	_, err := repo.GetDashboard(ctx, "non-existent")
	if err == nil {
		t.Errorf("GetDashboard for non-existent dashboard succeeded, want error")
	}
}

func TestGetDashboard_EmptyID(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	_, err := repo.GetDashboard(ctx, "")
	if err == nil {
		t.Errorf("GetDashboard for empty dashboardId succeeded, want error")
	}
}
