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

func TestGetDashboard_Success(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	chart := blankChartProto("test-chart-get-1", 100, 1)
	if err := repo.CreateChart(ctx, chart); err != nil {
		t.Fatalf("CreateChart failed: %v", err)
	}

	dash := blankDashboardProto("dashboards/test-get-1")
	dash.Charts = []*dpb.Chart{chart}
	if err := repo.CreateDashboard(ctx, dash); err != nil {
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
	if len(got.GetCharts()) != len(dash.GetCharts()) {
		t.Fatalf("GetDashboard Charts len mismatch: got %d, want %d", len(got.GetCharts()), len(dash.GetCharts()))
	}
	gotChart := got.GetCharts()[0]
	if gotChart.GetName() != chart.GetName() {
		t.Errorf("GetDashboard Chart Name mismatch: got %q, want %q", gotChart.GetName(), chart.GetName())
	}
	if gotChart.GetTitle() != chart.GetTitle() {
		t.Errorf("GetDashboard Chart Title mismatch: got %q, want %q", gotChart.GetTitle(), chart.GetTitle())
	}
	if gotChart.GetXid() != chart.GetXid() || gotChart.GetWid() != chart.GetWid() {
		t.Errorf("GetDashboard Chart Xid/Wid mismatch: got (%d, %d), want (%d, %d)", gotChart.GetXid(), gotChart.GetWid(), chart.GetXid(), chart.GetWid())
	}
	if got.GetChartLayout() == nil || got.GetChartLayout().GetColumns() != dash.GetChartLayout().GetColumns() {
		t.Errorf("GetDashboard ChartLayout mismatch")
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
