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

	dpb "github.com/google/xmc/api/protos/dashboard"
	"github.com/google/xmc/internal/grpcerr"
)

func TestUpdateDashboard_Success(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	dash := blankDashboardProto("dashboards/test-update-1")
	if err := repo.CreateDashboard(ctx, dash, nil); err != nil {
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

	fetched, err := repo.GetDashboard(ctx, "test-update-1")
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
	if err := repo.CreateDashboard(ctx, dash, nil); err != nil {
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

	fetched, err := repo.GetDashboard(ctx, "test-update-mask-1")
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

func TestUpdateDashboard_WithChartIDs(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	chartID1 := "f47ac10b-58cc-4372-a567-0e02b2c3d479"
	chartID2 := "c9a646d3-9c61-4cd7-bf10-8e96dc638210"
	chart1 := blankChartProto(chartID1, 100, 1)
	if err := repo.CreateChart(ctx, chart1); err != nil {
		t.Fatalf("CreateChart 1 failed: %v", err)
	}
	chart2 := blankChartProto(chartID2, 100, 1)
	if err := repo.CreateChart(ctx, chart2); err != nil {
		t.Fatalf("CreateChart 2 failed: %v", err)
	}

	dash := blankDashboardProto("dashboards/test-update-charts-1")
	dash.ChartIds = []string{chartID1}
	if err := repo.CreateDashboard(ctx, dash, nil); err != nil {
		t.Fatalf("CreateDashboard failed: %v", err)
	}

	update := blankDashboardProto("dashboards/test-update-charts-1")
	update.ChartIds = []string{chartID2}

	if _, err := repo.UpdateDashboard(ctx, update, nil); err != nil {
		t.Fatalf("UpdateDashboard charts failed: %v", err)
	}

	fetched, err := repo.GetDashboard(ctx, "test-update-charts-1")
	if err != nil {
		t.Fatalf("GetDashboard failed: %v", err)
	}
	if len(fetched.GetChartIds()) != 1 || fetched.GetChartIds()[0] != chartID2 {
		t.Fatalf("fetched ChartIds mismatch: got %v, want [%q]", fetched.GetChartIds(), chartID2)
	}
}

func TestUpdateDashboard_ClearChartIDs(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	chartID1 := "f47ac10b-58cc-4372-a567-0e02b2c3d479"
	chart1 := blankChartProto(chartID1, 100, 1)
	if err := repo.CreateChart(ctx, chart1); err != nil {
		t.Fatalf("CreateChart failed: %v", err)
	}

	dash := blankDashboardProto("dashboards/test-update-clear-1")
	dash.ChartIds = []string{chartID1}
	if err := repo.CreateDashboard(ctx, dash, nil); err != nil {
		t.Fatalf("CreateDashboard failed: %v", err)
	}

	update := blankDashboardProto("dashboards/test-update-clear-1")
	update.ChartIds = []string{}
	mask := &fieldmaskpb.FieldMask{Paths: []string{"chart_ids"}}

	if _, err := repo.UpdateDashboard(ctx, update, mask); err != nil {
		t.Fatalf("UpdateDashboard clearing charts failed: %v", err)
	}

	fetched, err := repo.GetDashboard(ctx, "test-update-clear-1")
	if err != nil {
		t.Fatalf("GetDashboard failed: %v", err)
	}
	if len(fetched.GetChartIds()) != 0 {
		t.Fatalf("Expected 0 chart IDs after clearing, got %v", fetched.GetChartIds())
	}
}

func TestUpdateDashboard_NoOp(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	dash := blankDashboardProto("dashboards/test-update-noop-1")
	if err := repo.CreateDashboard(ctx, dash, nil); err != nil {
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
	if !errors.Is(err, grpcerr.ErrNotFound) {
		t.Errorf("UpdateDashboard error = %v, want grpcerr.ErrNotFound", err)
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

func TestUpdateDashboard_ReorderedChartIDs(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	chartID1 := "c9a646d3-9c61-4cd7-bf10-8e96dc638210"
	chartID2 := "f47ac10b-58cc-4372-a567-0e02b2c3d479"
	chart1 := blankChartProto(chartID1, 100, 1)
	if err := repo.CreateChart(ctx, chart1); err != nil {
		t.Fatalf("CreateChart 1 failed: %v", err)
	}
	chart2 := blankChartProto(chartID2, 100, 1)
	if err := repo.CreateChart(ctx, chart2); err != nil {
		t.Fatalf("CreateChart 2 failed: %v", err)
	}

	dash := blankDashboardProto("dashboards/test-update-reorder-1")
	dash.ChartIds = []string{chartID1, chartID2}
	if err := repo.CreateDashboard(ctx, dash, nil); err != nil {
		t.Fatalf("CreateDashboard failed: %v", err)
	}

	// Update with reordered charts (reverse order)
	update := blankDashboardProto("dashboards/test-update-reorder-1")
	update.ChartIds = []string{chartID2, chartID1}

	got, err := repo.UpdateDashboard(ctx, update, nil)
	if err != nil {
		t.Fatalf("UpdateDashboard with reordered charts failed: %v", err)
	}
	if len(got.GetChartIds()) != 2 || got.GetChartIds()[0] != chartID1 || got.GetChartIds()[1] != chartID2 {
		t.Errorf("got ChartIds %v, want canonically sorted [%s, %s]", got.GetChartIds(), chartID1, chartID2)
	}

	fetched, err := repo.GetDashboard(ctx, "test-update-reorder-1")
	if err != nil {
		t.Fatalf("GetDashboard failed: %v", err)
	}
	if len(fetched.GetChartIds()) != 2 || fetched.GetChartIds()[0] != chartID1 || fetched.GetChartIds()[1] != chartID2 {
		t.Fatalf("fetched ChartIds mismatch: got %v, want sorted charts", fetched.GetChartIds())
	}
}

func TestUpdateDashboard_CamelCaseChartIdsMask(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	chartID1 := "f47ac10b-58cc-4372-a567-0e02b2c3d479"
	chartID2 := "c9a646d3-9c61-4cd7-bf10-8e96dc638210"
	chart1 := blankChartProto(chartID1, 100, 1)
	if err := repo.CreateChart(ctx, chart1); err != nil {
		t.Fatalf("CreateChart 1 failed: %v", err)
	}
	chart2 := blankChartProto(chartID2, 100, 1)
	if err := repo.CreateChart(ctx, chart2); err != nil {
		t.Fatalf("CreateChart 2 failed: %v", err)
	}

	dash := blankDashboardProto("dashboards/test-update-camel-1")
	dash.ChartIds = []string{chartID1}
	if err := repo.CreateDashboard(ctx, dash, nil); err != nil {
		t.Fatalf("CreateDashboard failed: %v", err)
	}

	update := blankDashboardProto("dashboards/test-update-camel-1")
	update.ChartIds = []string{chartID2}
	mask := &fieldmaskpb.FieldMask{Paths: []string{"chartIds"}}

	if _, err := repo.UpdateDashboard(ctx, update, mask); err != nil {
		t.Fatalf("UpdateDashboard with camelCase chartIds mask failed: %v", err)
	}

	fetched, err := repo.GetDashboard(ctx, "test-update-camel-1")
	if err != nil {
		t.Fatalf("GetDashboard failed: %v", err)
	}
	if len(fetched.GetChartIds()) != 1 || fetched.GetChartIds()[0] != chartID2 {
		t.Fatalf("fetched ChartIds mismatch: got %v, want [%q]", fetched.GetChartIds(), chartID2)
	}
}

func TestUpdateDashboard_NonExistentChartID(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	chartID1 := "f47ac10b-58cc-4372-a567-0e02b2c3d479"
	chart1 := blankChartProto(chartID1, 100, 1)
	if err := repo.CreateChart(ctx, chart1); err != nil {
		t.Fatalf("CreateChart failed: %v", err)
	}

	dash := blankDashboardProto("dashboards/test-update-fk-1")
	dash.ChartIds = []string{chartID1}
	if err := repo.CreateDashboard(ctx, dash, nil); err != nil {
		t.Fatalf("CreateDashboard failed: %v", err)
	}

	update := blankDashboardProto("dashboards/test-update-fk-1")
	update.ChartIds = []string{"00000000-0000-0000-0000-000000000000"}
	mask := &fieldmaskpb.FieldMask{Paths: []string{"chart_ids"}}

	_, err := repo.UpdateDashboard(ctx, update, mask)
	if err == nil {
		t.Fatalf("UpdateDashboard with non-existent chart ID expected foreign key error, got nil")
	}
}
