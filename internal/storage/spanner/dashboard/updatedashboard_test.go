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
	"testing"

	fieldmaskpb "google.golang.org/protobuf/types/known/fieldmaskpb"

	"task/go/status"
	dpb "xmanager_cloud/dashboard_service/proto/api_go_proto"
)

func TestUpdateDashboard_Success(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	dash := blankDashboardProto("dashboards/test-update-1")
	if err := repo.CreateDashboard(ctx, dash); err != nil {
		t.Fatalf("CreateDashboard failed: %v", err)
	}

	update := blankDashboardProto("dashboards/test-update-1")
	update.Title = "Updated Dashboard Title"
	update.Description = "Updated Dashboard Description"
	update.ChartLayout = &dpb.LayoutSpec{Type: dpb.LayoutSpec_SIDE_BY_SIDE, Columns: 3}

	got, err := repo.UpdateDashboard(ctx, update, nil)
	if err != nil {
		t.Fatalf("UpdateDashboard failed: %v", err)
	}
	if got.GetTitle() != "Updated Dashboard Title" {
		t.Errorf("got Title %q, want 'Updated Dashboard Title'", got.GetTitle())
	}

	fetched, err := repo.GetDashboard(ctx, "dashboards/test-update-1")
	if err != nil {
		t.Fatalf("GetDashboard failed: %v", err)
	}
	if fetched.GetTitle() != "Updated Dashboard Title" {
		t.Errorf("fetched Title %q, want 'Updated Dashboard Title'", fetched.GetTitle())
	}
	if fetched.GetDescription() != "Updated Dashboard Description" {
		t.Errorf("fetched Description %q, want 'Updated Dashboard Description'", fetched.GetDescription())
	}
	if fetched.GetChartLayout() == nil || fetched.GetChartLayout().GetColumns() != 3 {
		t.Errorf("fetched ChartLayout columns mismatch: got %v", fetched.GetChartLayout())
	}
}

func TestUpdateDashboard_WithFieldMask(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	dash := blankDashboardProto("dashboards/test-update-mask-1")
	dash.Title = "Initial Title"
	dash.Description = "Initial Description"
	if err := repo.CreateDashboard(ctx, dash); err != nil {
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

	fetched, err := repo.GetDashboard(ctx, "dashboards/test-update-mask-1")
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

func TestUpdateDashboard_WithCharts(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	chart1 := blankChartProto("charts/test-chart-u1", 100, 1)
	if err := repo.CreateChart(ctx, chart1); err != nil {
		t.Fatalf("CreateChart 1 failed: %v", err)
	}
	chart2 := blankChartProto("charts/test-chart-u2", 100, 1)
	if err := repo.CreateChart(ctx, chart2); err != nil {
		t.Fatalf("CreateChart 2 failed: %v", err)
	}

	dash := blankDashboardProto("dashboards/test-update-charts-1")
	dash.Charts = []*dpb.Chart{{Name: "charts/test-chart-u1"}}
	if err := repo.CreateDashboard(ctx, dash); err != nil {
		t.Fatalf("CreateDashboard failed: %v", err)
	}

	update := blankDashboardProto("dashboards/test-update-charts-1")
	update.Charts = []*dpb.Chart{{Name: "charts/test-chart-u2"}}

	if _, err := repo.UpdateDashboard(ctx, update, nil); err != nil {
		t.Fatalf("UpdateDashboard charts failed: %v", err)
	}

	fetched, err := repo.GetDashboard(ctx, "dashboards/test-update-charts-1")
	if err != nil {
		t.Fatalf("GetDashboard failed: %v", err)
	}
	if len(fetched.GetCharts()) != 1 || fetched.GetCharts()[0].GetName() != "charts/test-chart-u2" {
		t.Fatalf("fetched Charts mismatch: got %v, want charts/test-chart-u2", fetched.GetCharts())
	}
}

func TestUpdateDashboard_NoOp(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	dash := blankDashboardProto("dashboards/test-update-noop-1")
	if err := repo.CreateDashboard(ctx, dash); err != nil {
		t.Fatalf("CreateDashboard failed: %v", err)
	}

	update := blankDashboardProto("dashboards/test-update-noop-1")
	if _, err := repo.UpdateDashboard(ctx, update, nil); err != nil {
		t.Fatalf("UpdateDashboard no-op failed: %v", err)
	}
}

func TestUpdateDashboard_NotFound(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	update := blankDashboardProto("dashboards/non-existent-update")
	_, err := repo.UpdateDashboard(ctx, update, nil)
	if err == nil {
		t.Fatalf("UpdateDashboard for non-existent dashboard succeeded, want error")
	}
	if !errors.Is(err, status.ErrNotFound) {
		t.Errorf("UpdateDashboard error = %v, want status.ErrNotFound", err)
	}
}

func TestUpdateDashboard_NilOrInvalid(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	if _, err := repo.UpdateDashboard(ctx, nil, nil); err == nil {
		t.Errorf("UpdateDashboard(nil) succeeded, want error")
	}
	if _, err := repo.UpdateDashboard(ctx, &dpb.Dashboard{}, nil); err == nil {
		t.Errorf("UpdateDashboard(empty name) succeeded, want error")
	}
}

func TestChartsEqual_NilElements(t *testing.T) {
	chartA := &dpb.Chart{Name: "charts/c1"}
	chartB := &dpb.Chart{Name: "charts/c2"}

	tests := []struct {
		name string
		a    []*dpb.Chart
		b    []*dpb.Chart
		want bool
	}{
		{
			name: "both nil slices",
			a:    nil,
			b:    nil,
			want: true,
		},
		{
			name: "both empty slices",
			a:    []*dpb.Chart{},
			b:    []*dpb.Chart{},
			want: true,
		},
		{
			name: "different lengths",
			a:    []*dpb.Chart{chartA},
			b:    []*dpb.Chart{chartA, chartB},
			want: false,
		},
		{
			name: "both have nil element at same position",
			a:    []*dpb.Chart{nil},
			b:    []*dpb.Chart{nil},
			want: true,
		},
		{
			name: "one has nil element, other has non-nil",
			a:    []*dpb.Chart{nil},
			b:    []*dpb.Chart{chartA},
			want: false,
		},
		{
			name: "one has non-nil element, other has nil",
			a:    []*dpb.Chart{chartA},
			b:    []*dpb.Chart{nil},
			want: false,
		},
		{
			name: "equal chart names",
			a:    []*dpb.Chart{chartA},
			b:    []*dpb.Chart{{Name: "charts/c1"}},
			want: true,
		},
		{
			name: "different chart names",
			a:    []*dpb.Chart{chartA},
			b:    []*dpb.Chart{chartB},
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := chartsEqual(tc.a, tc.b); got != tc.want {
				t.Errorf("chartsEqual() = %v, want %v", got, tc.want)
			}
		})
	}
}
