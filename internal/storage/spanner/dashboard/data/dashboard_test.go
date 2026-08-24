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

package data

import (
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	"google.golang.org/protobuf/proto"
	fieldmaskpb "google.golang.org/protobuf/types/known/fieldmaskpb"
	dpb "xmanager_cloud/dashboard_service/proto/api_go_proto"
)

func TestDashboardProtoToInsertMutations_Success(t *testing.T) {
	dash := &dpb.Dashboard{
		Name:        "dashboards/test-data-1",
		Title:       "Test Dashboard Title",
		Description: "Test Description",
		Owner:       "user@example.com",
		ChartIds: []string{
			"f47ac10b-58cc-4372-a567-0e02b2c3d479",
		},
		ChartLayout: &dpb.LayoutSpec{
			Type:    dpb.LayoutSpec_GRID,
			Columns: 2,
			Positions: []*dpb.GridPosition{
				{
					ItemId: "f47ac10b-58cc-4372-a567-0e02b2c3d479",
					X:      0,
					Y:      0,
					Width:  6,
					Height: 4,
				},
			},
		},
	}

	mutations, err := DashboardProtoToInsertMutations(dash)
	if err != nil {
		t.Fatalf("DashboardProtoToInsertMutations failed: %v", err)
	}

	if len(mutations) != 2 {
		t.Errorf("Expected 2 mutations (dashboard + chart link), got %d", len(mutations))
	}
}

func TestChartProtoToInsertMutations_Success(t *testing.T) {
	chart := &dpb.Chart{
		Name:        "experiments/100/workUnits/1/charts/test-chart-data-1",
		Title:       "Test Chart Title",
		Description: "Test Chart Description",
		Xid:         100,
		Wid:         1,
		Plots: []*dpb.PlotSpec{
			{
				PlotId: "plot-1",
				Title:  "Loss",
				Type:   dpb.PlotType_SCATTER,
			},
		},
		PlotLayout: &dpb.LayoutSpec{
			Type:    dpb.LayoutSpec_GRID,
			Columns: 1,
		},
	}

	mutations, err := ChartProtoToInsertMutations(chart)
	if err != nil {
		t.Fatalf("ChartProtoToInsertMutations failed: %v", err)
	}

	if len(mutations) != 2 {
		t.Errorf("Expected 2 mutations (chart + plot spec), got %d", len(mutations))
	}
}

func TestChartProtoToInsertMutations_NoPlots(t *testing.T) {
	chart := &dpb.Chart{
		Name:        "experiments/100/workUnits/1/charts/test-chart-no-plots",
		Title:       "Chart Without Plots",
		Description: "No plots description",
	}

	mutations, err := ChartProtoToInsertMutations(chart)
	if err != nil {
		t.Fatalf("ChartProtoToInsertMutations failed: %v", err)
	}

	if len(mutations) != 1 {
		t.Errorf("Expected 1 mutation (chart only), got %d", len(mutations))
	}
}

func TestChartProtoToInsertMutations_InvalidName(t *testing.T) {
	chart := &dpb.Chart{
		Name:  "invalid-chart-name",
		Title: "Invalid Name Chart",
	}

	if _, err := ChartProtoToInsertMutations(chart); err == nil {
		t.Errorf("ChartProtoToInsertMutations with invalid name expected error, got nil")
	}
}

func TestChartProtoToUpdateMutations_Success(t *testing.T) {
	chart := &dpb.Chart{
		Name:        "experiments/100/workUnits/1/charts/test-chart-update-1",
		Title:       "Updated Chart Title",
		Description: "Updated Description",
		Wid:         2,
		Plots: []*dpb.PlotSpec{
			{
				PlotId: "plot-1",
				Title:  "Updated Loss",
				Type:   dpb.PlotType_SCATTER,
			},
		},
		PlotLayout: &dpb.LayoutSpec{
			Type:    dpb.LayoutSpec_GRID,
			Columns: 2,
		},
	}

	mutations, err := ChartProtoToUpdateMutations(chart)
	if err != nil {
		t.Fatalf("ChartProtoToUpdateMutations failed: %v", err)
	}

	// Expect 3 mutations: 1 Update (Charts) + 1 Delete (ChartPlots prefix) + 1 InsertOrUpdate (ChartPlots)
	if len(mutations) != 3 {
		t.Errorf("Expected 3 mutations, got %d", len(mutations))
	}
}

func TestChartProtoToUpdateMutations_WithoutLayout(t *testing.T) {
	chart := &dpb.Chart{
		Name:  "experiments/100/workUnits/1/charts/test-chart-update-no-layout",
		Title: "No Layout Chart",
	}

	mutations, err := ChartProtoToUpdateMutations(chart)
	if err != nil {
		t.Fatalf("ChartProtoToUpdateMutations without layout failed: %v", err)
	}

	if len(mutations) != 2 {
		t.Errorf("Expected 2 mutations (update charts + delete plots prefix), got %d", len(mutations))
	}
}

func TestChartProtoToUpdateMutations_Nil(t *testing.T) {
	if _, err := ChartProtoToUpdateMutations(nil); err == nil {
		t.Errorf("ChartProtoToUpdateMutations(nil) expected error, got nil")
	}
}

func TestChartProtoToUpdateMutations_EmptyName(t *testing.T) {
	chart := &dpb.Chart{Title: "No Name"}
	if _, err := ChartProtoToUpdateMutations(chart); err == nil {
		t.Errorf("ChartProtoToUpdateMutations with empty name expected error, got nil")
	}
}

func TestDashboardProtoToInsertMutations_Nil(t *testing.T) {
	if _, err := DashboardProtoToInsertMutations(nil); err == nil {
		t.Errorf("DashboardProtoToInsertMutations(nil) expected error, got nil")
	}
}

func TestChartProtoToInsertMutations_Nil(t *testing.T) {
	if _, err := ChartProtoToInsertMutations(nil); err == nil {
		t.Errorf("ChartProtoToInsertMutations(nil) expected error, got nil")
	}
}

func TestChartProtoToUpdateMutation_Success(t *testing.T) {
	chart := &dpb.Chart{
		Name:  "experiments/100/workUnits/1/charts/test-chart-update-single",
		Title: "Updated Single Chart Title",
		PlotLayout: &dpb.LayoutSpec{
			Type:    dpb.LayoutSpec_GRID,
			Columns: 2,
		},
	}

	mut, err := ChartProtoToUpdateMutation(chart)
	if err != nil {
		t.Fatalf("ChartProtoToUpdateMutation failed: %v", err)
	}
	if mut == nil {
		t.Fatalf("ChartProtoToUpdateMutation returned nil mutation")
	}
}

func TestChartProtoToUpdateMutation_NilOrEmpty(t *testing.T) {
	if _, err := ChartProtoToUpdateMutation(nil); err == nil {
		t.Errorf("ChartProtoToUpdateMutation(nil) expected error, got nil")
	}
	if _, err := ChartProtoToUpdateMutation(&dpb.Chart{}); err == nil {
		t.Errorf("ChartProtoToUpdateMutation(empty name) expected error, got nil")
	}
}

func TestChartPlotsToDeleteMutation(t *testing.T) {
	mut := ChartPlotsToDeleteMutation("charts/test-chart-delete-plots")
	if mut == nil {
		t.Fatalf("ChartPlotsToDeleteMutation returned nil mutation")
	}
}

func TestPlotSpecsToInsertMutations_Success(t *testing.T) {
	plots := []*dpb.PlotSpec{
		{
			PlotId: "plot-1",
			Title:  "Plot 1",
			Type:   dpb.PlotType_BAR,
		},
		nil, // Ensure nil plot is safely skipped
		{
			PlotId: "plot-2",
			Title:  "Plot 2",
			Type:   dpb.PlotType_SCATTER,
		},
	}

	mutations, err := PlotSpecsToInsertMutations("charts/test-chart-plots", plots)
	if err != nil {
		t.Fatalf("PlotSpecsToInsertMutations failed: %v", err)
	}
	if len(mutations) != 2 {
		t.Errorf("Expected 2 mutations for 2 non-nil plots, got %d", len(mutations))
	}
}

func TestArtifactChartRowToDeleteMutation_Nil(t *testing.T) {
	if _, err := ArtifactChartRowToDeleteMutation(nil); err == nil {
		t.Errorf("ArtifactChartRowToDeleteMutation(nil) expected error, got nil")
	}
}

func TestArtifactChartRowToDeleteMutation_Success(t *testing.T) {
	row, err := spanner.NewRow(
		[]string{"ArtifactChartId", "ArtifactId", "WorkUnitId", "BitReversedXid"},
		[]interface{}{"art-chart-1", int64(10), spanner.NullInt64{Int64: 2, Valid: true}, int64(999)},
	)
	if err != nil {
		t.Fatalf("spanner.NewRow failed: %v", err)
	}

	mut, err := ArtifactChartRowToDeleteMutation(row)
	if err != nil {
		t.Fatalf("ArtifactChartRowToDeleteMutation failed: %v", err)
	}
	if mut == nil {
		t.Fatalf("ArtifactChartRowToDeleteMutation returned nil mutation")
	}
}

func TestDashboardChartRowToDeleteMutation_Nil(t *testing.T) {
	if _, err := DashboardChartRowToDeleteMutation(nil); err == nil {
		t.Errorf("DashboardChartRowToDeleteMutation(nil) expected error, got nil")
	}
}

func TestDashboardChartRowToDeleteMutation_Success(t *testing.T) {
	row, err := spanner.NewRow(
		[]string{"DashboardChartId", "DashboardId"},
		[]interface{}{"dash-chart-1", "dashboards/d1"},
	)
	if err != nil {
		t.Fatalf("spanner.NewRow failed: %v", err)
	}

	mut, err := DashboardChartRowToDeleteMutation(row)
	if err != nil {
		t.Fatalf("DashboardChartRowToDeleteMutation failed: %v", err)
	}
	if mut == nil {
		t.Fatalf("DashboardChartRowToDeleteMutation returned nil mutation")
	}
}

func TestDashboardRowToProto_Nil(t *testing.T) {
	if _, err := DashboardRowToProto(nil, nil); err == nil {
		t.Errorf("DashboardRowToProto(nil) expected error, got nil")
	}
}

func TestDashboardRowToProto_Success(t *testing.T) {
	layout := &dpb.LayoutSpec{
		Type:    dpb.LayoutSpec_GRID,
		Columns: 3,
	}
	layoutBytes, err := proto.Marshal(layout)
	if err != nil {
		t.Fatalf("proto.Marshal(layout) failed: %v", err)
	}

	chartIDs := []string{
		"f47ac10b-58cc-4372-a567-0e02b2c3d479",
		"c9a646d3-9c61-4cd7-bf10-8e96dc638210",
	}

	now := time.Now().UTC()
	row, err := spanner.NewRow(
		[]string{"CreatedAt", "DashboardId", "UpdatedAt", "Description", "Owner", "Title", "ChartLayout", "ExtraIgnoredCol"},
		[]interface{}{now, "d1", now, "Test Desc", "owner@example.com", "Test Title", layoutBytes, "extra_value"},
	)
	if err != nil {
		t.Fatalf("spanner.NewRow failed: %v", err)
	}

	dash, err := DashboardRowToProto(row, chartIDs)
	if err != nil {
		t.Fatalf("DashboardRowToProto failed: %v", err)
	}

	if dash.GetName() != "dashboards/d1" {
		t.Errorf("dash.Name: got %q, want 'dashboards/d1'", dash.GetName())
	}
	if dash.GetTitle() != "Test Title" {
		t.Errorf("dash.Title: got %q, want 'Test Title'", dash.GetTitle())
	}
	if dash.GetDescription() != "Test Desc" {
		t.Errorf("dash.Description: got %q, want 'Test Desc'", dash.GetDescription())
	}
	if dash.GetOwner() != "owner@example.com" {
		t.Errorf("dash.Owner: got %q, want 'owner@example.com'", dash.GetOwner())
	}
	if len(dash.GetChartIds()) != 2 || dash.GetChartIds()[0] != "c9a646d3-9c61-4cd7-bf10-8e96dc638210" {
		t.Errorf("dash.ChartIds: got %v, want canonically sorted chart IDs", dash.GetChartIds())
	}
	if dash.GetChartLayout() == nil || dash.GetChartLayout().GetColumns() != 3 {
		t.Errorf("dash.ChartLayout: got %v, want columns=3", dash.GetChartLayout())
	}
}

func TestDashboardRowToProto_QueryGenAliases(t *testing.T) {
	layout := &dpb.LayoutSpec{
		Type:    dpb.LayoutSpec_GRID,
		Columns: 3,
	}
	layoutBytes, err := proto.Marshal(layout)
	if err != nil {
		t.Fatalf("proto.Marshal(layout) failed: %v", err)
	}

	chartIDs := []string{
		"f47ac10b-58cc-4372-a567-0e02b2c3d479",
	}

	now := time.Now().UTC()
	row, err := spanner.NewRow(
		[]string{"created", "dashboard_id", "updated", "description", "owner", "title", "chart_layout"},
		[]interface{}{now, "d1", now, "Test Desc", "owner@example.com", "Test Title", layoutBytes},
	)
	if err != nil {
		t.Fatalf("spanner.NewRow failed: %v", err)
	}

	dash, err := DashboardRowToProto(row, chartIDs)
	if err != nil {
		t.Fatalf("DashboardRowToProto failed with query generator aliases: %v", err)
	}

	if dash.GetName() != "dashboards/d1" {
		t.Errorf("dash.Name: got %q, want 'dashboards/d1'", dash.GetName())
	}
	if dash.GetTitle() != "Test Title" {
		t.Errorf("dash.Title: got %q, want 'Test Title'", dash.GetTitle())
	}
	if dash.GetDescription() != "Test Desc" {
		t.Errorf("dash.Description: got %q, want 'Test Desc'", dash.GetDescription())
	}
	if dash.GetOwner() != "owner@example.com" {
		t.Errorf("dash.Owner: got %q, want 'owner@example.com'", dash.GetOwner())
	}
	if dash.GetChartLayout() == nil || dash.GetChartLayout().GetColumns() != 3 {
		t.Errorf("dash.ChartLayout: got %v, want columns=3", dash.GetChartLayout())
	}
	if dash.GetCreateTime() == nil || dash.GetCreateTime().AsTime().Unix() != now.Unix() {
		t.Errorf("dash.CreateTime mismatch: got %v, want %v", dash.GetCreateTime(), now)
	}
	if dash.GetUpdateTime() == nil || dash.GetUpdateTime().AsTime().Unix() != now.Unix() {
		t.Errorf("dash.UpdateTime mismatch: got %v, want %v", dash.GetUpdateTime(), now)
	}
}

func TestDashboardRowToProto_MissingRequiredColumn(t *testing.T) {
	row, err := spanner.NewRow(
		[]string{"Title", "Description"},
		[]interface{}{"Test Title", "Test Desc"},
	)
	if err != nil {
		t.Fatalf("spanner.NewRow failed: %v", err)
	}

	if _, err := DashboardRowToProto(row, nil); err == nil {
		t.Errorf("DashboardRowToProto with missing DashboardId expected error, got nil")
	}
}

func TestChartRowToProto_Nil(t *testing.T) {
	if _, err := ChartRowToProto(nil, nil); err == nil {
		t.Errorf("ChartRowToProto(nil) expected error, got nil")
	}
}

func TestChartRowToProto_Success(t *testing.T) {
	layout := &dpb.LayoutSpec{
		Type:    dpb.LayoutSpec_GRID,
		Columns: 2,
	}
	layoutBytes, err := proto.Marshal(layout)
	if err != nil {
		t.Fatalf("proto.Marshal(layout) failed: %v", err)
	}

	plots := []*dpb.PlotSpec{
		{PlotId: "plot-1", Title: "Loss"},
	}

	now := time.Now().UTC()
	row, err := spanner.NewRow(
		[]string{"CreatedAt", "ChartId", "UpdatedAt", "Owner", "Title", "Description", "Xid", "Wid", "PlotLayout", "ExtraCol"},
		[]interface{}{now, "c1", now, "owner@example.com", "Test Title", "Test Description", int64(100), int64(1), layoutBytes, 12345},
	)
	if err != nil {
		t.Fatalf("spanner.NewRow failed: %v", err)
	}

	chart, err := ChartRowToProto(row, plots)
	if err != nil {
		t.Fatalf("ChartRowToProto failed: %v", err)
	}

	if chart.GetName() != "experiments/100/workUnits/1/charts/c1" {
		t.Errorf("chart.Name: got %q, want 'experiments/100/workUnits/1/charts/c1'", chart.GetName())
	}
	if chart.GetTitle() != "Test Title" {
		t.Errorf("chart.Title: got %q, want 'Test Title'", chart.GetTitle())
	}
	if chart.GetDescription() != "Test Description" {
		t.Errorf("chart.Description: got %q, want 'Test Description'", chart.GetDescription())
	}
	if chart.GetXid() != 100 {
		t.Errorf("chart.Xid: got %d, want 100", chart.GetXid())
	}
	if chart.GetWid() != 1 {
		t.Errorf("chart.Wid: got %d, want 1", chart.GetWid())
	}
	if chart.GetCreateTime() == nil || chart.GetCreateTime().AsTime().Unix() != now.Unix() {
		t.Errorf("chart.CreateTime mismatch: got %v, want %v", chart.GetCreateTime(), now)
	}
	if chart.GetUpdateTime() == nil || chart.GetUpdateTime().AsTime().Unix() != now.Unix() {
		t.Errorf("chart.UpdateTime mismatch: got %v, want %v", chart.GetUpdateTime(), now)
	}
	if len(chart.GetPlots()) != 1 || chart.GetPlots()[0].GetPlotId() != "plot-1" {
		t.Errorf("chart.Plots: got %v, want 1 plot with ID 'plot-1'", chart.GetPlots())
	}
	if chart.GetPlotLayout() == nil || chart.GetPlotLayout().GetColumns() != 2 {
		t.Errorf("chart.PlotLayout: got %v, want columns=2", chart.GetPlotLayout())
	}
}

func TestChartRowToProto_QueryGenAliases(t *testing.T) {
	layout := &dpb.LayoutSpec{
		Type:    dpb.LayoutSpec_GRID,
		Columns: 2,
	}
	layoutBytes, err := proto.Marshal(layout)
	if err != nil {
		t.Fatalf("proto.Marshal(layout) failed: %v", err)
	}

	plots := []*dpb.PlotSpec{
		{PlotId: "plot-1", Title: "Loss"},
	}

	now := time.Now().UTC()
	row, err := spanner.NewRow(
		[]string{"created", "chart_id", "updated", "owner", "title", "description", "xid", "wid", "plot_layout"},
		[]interface{}{now, "c1", now, "owner@example.com", "Test Title", "Test Description", int64(100), int64(1), layoutBytes},
	)
	if err != nil {
		t.Fatalf("spanner.NewRow failed: %v", err)
	}

	chart, err := ChartRowToProto(row, plots)
	if err != nil {
		t.Fatalf("ChartRowToProto failed with query generator aliases: %v", err)
	}

	if chart.GetName() != "experiments/100/workUnits/1/charts/c1" {
		t.Errorf("chart.Name: got %q, want 'experiments/100/workUnits/1/charts/c1'", chart.GetName())
	}
	if chart.GetTitle() != "Test Title" {
		t.Errorf("chart.Title: got %q, want 'Test Title'", chart.GetTitle())
	}
	if chart.GetDescription() != "Test Description" {
		t.Errorf("chart.Description: got %q, want 'Test Description'", chart.GetDescription())
	}
	if chart.GetXid() != 100 {
		t.Errorf("chart.Xid: got %d, want 100", chart.GetXid())
	}
	if chart.GetWid() != 1 {
		t.Errorf("chart.Wid: got %d, want 1", chart.GetWid())
	}
	if chart.GetCreateTime() == nil || chart.GetCreateTime().AsTime().Unix() != now.Unix() {
		t.Errorf("chart.CreateTime mismatch: got %v, want %v", chart.GetCreateTime(), now)
	}
	if chart.GetUpdateTime() == nil || chart.GetUpdateTime().AsTime().Unix() != now.Unix() {
		t.Errorf("chart.UpdateTime mismatch: got %v, want %v", chart.GetUpdateTime(), now)
	}
	if len(chart.GetPlots()) != 1 || chart.GetPlots()[0].GetPlotId() != "plot-1" {
		t.Errorf("chart.Plots: got %v, want 1 plot with ID 'plot-1'", chart.GetPlots())
	}
	if chart.GetPlotLayout() == nil || chart.GetPlotLayout().GetColumns() != 2 {
		t.Errorf("chart.PlotLayout: got %v, want columns=2", chart.GetPlotLayout())
	}
}

func TestChartRowToProto_MissingRequiredColumn(t *testing.T) {
	row, err := spanner.NewRow(
		[]string{"Title", "Owner"},
		[]interface{}{"Test Title", "owner@example.com"},
	)
	if err != nil {
		t.Fatalf("spanner.NewRow failed: %v", err)
	}

	if _, err := ChartRowToProto(row, nil); err == nil {
		t.Errorf("ChartRowToProto with missing ChartId expected error, got nil")
	}
}

func TestChartRowToProto_TypeMismatch(t *testing.T) {
	row, err := spanner.NewRow(
		[]string{"ChartId", "Xid"},
		[]interface{}{"charts/c1", "not-an-int64"},
	)
	if err != nil {
		t.Fatalf("spanner.NewRow failed: %v", err)
	}

	if _, err := ChartRowToProto(row, nil); err == nil {
		t.Errorf("ChartRowToProto with invalid Xid type expected error, got nil")
	}
}

func TestMergeDashboards_AllFieldsNoMask(t *testing.T) {
	current := &dpb.Dashboard{
		Name:        "dashboards/d1",
		Title:       "Old Title",
		Description: "Old Desc",
		Owner:       "owner@example.com",
		ChartIds:    []string{"f47ac10b-58cc-4372-a567-0e02b2c3d479"},
		ChartLayout: &dpb.LayoutSpec{Type: dpb.LayoutSpec_GRID, Columns: 1},
	}
	updated := &dpb.Dashboard{
		Name:        "dashboards/d1",
		Title:       "New Title",
		Description: "New Desc",
		Owner:       "attacker@example.com",
		ChartIds:    []string{"c9a646d3-9c61-4cd7-bf10-8e96dc638210"},
		ChartLayout: &dpb.LayoutSpec{Type: dpb.LayoutSpec_SIDE_BY_SIDE, Columns: 2},
	}

	merged, err := MergeDashboards(current, updated, nil)
	if err != nil {
		t.Fatalf("MergeDashboards failed: %v", err)
	}

	if merged.GetTitle() != "New Title" {
		t.Errorf("Title: got %q, want 'New Title'", merged.GetTitle())
	}
	if merged.GetDescription() != "New Desc" {
		t.Errorf("Description: got %q, want 'New Desc'", merged.GetDescription())
	}
	if merged.GetOwner() != "owner@example.com" {
		t.Errorf("Owner: got %q, want 'owner@example.com' (owner must not be overwritten)", merged.GetOwner())
	}
	if len(merged.GetChartIds()) != 1 || merged.GetChartIds()[0] != "c9a646d3-9c61-4cd7-bf10-8e96dc638210" {
		t.Errorf("ChartIds: got %v, want c9a646d3-9c61-4cd7-bf10-8e96dc638210", merged.GetChartIds())
	}
	if merged.GetChartLayout().GetType() != dpb.LayoutSpec_SIDE_BY_SIDE {
		t.Errorf("ChartLayout: got %v, want SIDE_BY_SIDE", merged.GetChartLayout().GetType())
	}
}

func TestMergeDashboards_WithFieldMask(t *testing.T) {
	current := &dpb.Dashboard{
		Name:        "dashboards/d1",
		Title:       "Old Title",
		Description: "Old Desc",
		ChartIds:    []string{"f47ac10b-58cc-4372-a567-0e02b2c3d479"},
	}
	updated := &dpb.Dashboard{
		Name:        "dashboards/d1",
		Title:       "New Title",
		Description: "New Desc",
		ChartIds:    []string{"c9a646d3-9c61-4cd7-bf10-8e96dc638210"},
	}

	mask := &fieldmaskpb.FieldMask{Paths: []string{"title"}}
	merged, err := MergeDashboards(current, updated, mask)
	if err != nil {
		t.Fatalf("MergeDashboards failed: %v", err)
	}

	if merged.GetTitle() != "New Title" {
		t.Errorf("Title: got %q, want 'New Title'", merged.GetTitle())
	}
	if merged.GetDescription() != "Old Desc" {
		t.Errorf("Description: got %q, want 'Old Desc' (should not have changed)", merged.GetDescription())
	}
	if len(merged.GetChartIds()) != 1 || merged.GetChartIds()[0] != "f47ac10b-58cc-4372-a567-0e02b2c3d479" {
		t.Errorf("ChartIds: got %v, want f47ac10b-58cc-4372-a567-0e02b2c3d479 (should not have changed)", merged.GetChartIds())
	}
}

func TestMergeDashboards_InvalidPath(t *testing.T) {
	current := &dpb.Dashboard{Name: "dashboards/d1"}
	updated := &dpb.Dashboard{Name: "dashboards/d1"}
	mask := &fieldmaskpb.FieldMask{Paths: []string{"invalid_field"}}

	_, err := MergeDashboards(current, updated, mask)
	if err == nil {
		t.Errorf("MergeDashboards with invalid field mask path succeeded, want error")
	}
}

func TestMergeDashboards_NilInputs(t *testing.T) {
	if _, err := MergeDashboards(nil, &dpb.Dashboard{}, nil); err == nil {
		t.Errorf("MergeDashboards(nil, updated, nil) succeeded, want error")
	}
	if _, err := MergeDashboards(&dpb.Dashboard{}, nil, nil); err == nil {
		t.Errorf("MergeDashboards(current, nil, nil) succeeded, want error")
	}
}

func TestDashboardProtoToUpdateMutations_Success(t *testing.T) {
	dash := &dpb.Dashboard{
		Name:        "dashboards/d1",
		Title:       "Updated Title",
		Description: "Updated Desc",
		ChartIds:    []string{"f47ac10b-58cc-4372-a567-0e02b2c3d479", "c9a646d3-9c61-4cd7-bf10-8e96dc638210"},
	}

	// Without charts update
	mutsNoCharts, err := DashboardProtoToUpdateMutations(dash, false)
	if err != nil {
		t.Fatalf("DashboardProtoToUpdateMutations failed: %v", err)
	}
	if len(mutsNoCharts) != 1 {
		t.Errorf("mutsNoCharts: got %d mutations, want 1", len(mutsNoCharts))
	}

	// With charts update
	mutsWithCharts, err := DashboardProtoToUpdateMutations(dash, true)
	if err != nil {
		t.Fatalf("DashboardProtoToUpdateMutations with charts failed: %v", err)
	}
	// 1 dashboard update + 1 delete child prefix + 2 chart inserts = 4
	if len(mutsWithCharts) != 4 {
		t.Errorf("mutsWithCharts: got %d mutations, want 4", len(mutsWithCharts))
	}
}

func TestDashboardProtoToUpdateMutations_InvalidInput(t *testing.T) {
	if _, err := DashboardProtoToUpdateMutations(nil, false); err == nil {
		t.Errorf("DashboardProtoToUpdateMutations(nil) succeeded, want error")
	}
	if _, err := DashboardProtoToUpdateMutations(&dpb.Dashboard{}, false); err == nil {
		t.Errorf("DashboardProtoToUpdateMutations(empty name) succeeded, want error")
	}
}

func TestDashboardProtoToInsertMutations_InvalidChartID(t *testing.T) {
	dash := &dpb.Dashboard{
		Name:     "dashboards/d1",
		ChartIds: []string{"invalid chart id with spaces"},
	}
	if _, err := DashboardProtoToInsertMutations(dash); err == nil {
		t.Errorf("DashboardProtoToInsertMutations with invalid chart ID succeeded, want error")
	}
}

func TestDashboardProtoToUpdateMutations_InvalidChartID(t *testing.T) {
	dash := &dpb.Dashboard{
		Name:     "dashboards/d1",
		ChartIds: []string{"invalid chart id with spaces"},
	}
	if _, err := DashboardProtoToUpdateMutations(dash, true); err == nil {
		t.Errorf("DashboardProtoToUpdateMutations with invalid chart ID succeeded, want error")
	}
}

func TestSortChartIDs(t *testing.T) {
	c1 := "f47ac10b-58cc-4372-a567-0e02b2c3d479"
	c2 := "c9a646d3-9c61-4cd7-bf10-8e96dc638210"
	c3 := "d8a646d3-9c61-4cd7-bf10-8e96dc638211"

	tests := []struct {
		name     string
		input    []string
		wantLen  int
		wantList []string
	}{
		{
			name:     "nil slice",
			input:    nil,
			wantLen:  0,
			wantList: nil,
		},
		{
			name:     "empty slice",
			input:    []string{},
			wantLen:  0,
			wantList: nil,
		},
		{
			name:     "single element",
			input:    []string{c1},
			wantLen:  1,
			wantList: []string{c1},
		},
		{
			name:     "already sorted",
			input:    []string{c2, c3, c1},
			wantLen:  3,
			wantList: []string{c2, c3, c1},
		},
		{
			name:     "reverse sorted",
			input:    []string{c1, c3, c2},
			wantLen:  3,
			wantList: []string{c2, c3, c1},
		},
		{
			name:     "with duplicates",
			input:    []string{c1, c2, c1},
			wantLen:  2,
			wantList: []string{c2, c1},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := SortChartIDs(tc.input)
			if len(got) != tc.wantLen {
				t.Fatalf("SortChartIDs() len = %d, want %d", len(got), tc.wantLen)
			}
			for i, want := range tc.wantList {
				if got[i] != want {
					t.Errorf("got[%d] = %q, want %q", i, got[i], want)
				}
			}
		})
	}
}

func TestSortChartIDs_Immutability(t *testing.T) {
	c1 := "f47ac10b-58cc-4372-a567-0e02b2c3d479"
	c2 := "c9a646d3-9c61-4cd7-bf10-8e96dc638210"
	input := []string{c1, c2}

	got := SortChartIDs(input)
	if got[0] != c2 || got[1] != c1 {
		t.Errorf("SortChartIDs() did not sort properly: got %v", got)
	}
	if input[0] != c1 || input[1] != c2 {
		t.Errorf("SortChartIDs() mutated original input slice: got %v", input)
	}
}

func TestChartIDsEqual(t *testing.T) {
	tests := []struct {
		name string
		a    []string
		b    []string
		want bool
	}{
		{name: "both nil", a: nil, b: nil, want: true},
		{name: "one nil one empty", a: nil, b: []string{}, want: true},
		{name: "both empty", a: []string{}, b: []string{}, want: true},
		{name: "equal single", a: []string{"c1"}, b: []string{"c1"}, want: true},
		{name: "equal multiple", a: []string{"c1", "c2"}, b: []string{"c1", "c2"}, want: true},
		{name: "different lengths", a: []string{"c1"}, b: []string{"c1", "c2"}, want: false},
		{name: "different elements", a: []string{"c1"}, b: []string{"c2"}, want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ChartIDsEqual(tc.a, tc.b); got != tc.want {
				t.Errorf("ChartIDsEqual(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

func TestMergeDashboards_PreservesChartIDsOrder(t *testing.T) {
	current := &dpb.Dashboard{
		Name:     "dashboards/d1",
		Title:    "Title",
		ChartIds: []string{"f47ac10b-58cc-4372-a567-0e02b2c3d479"},
	}
	updated := &dpb.Dashboard{
		Name:     "dashboards/d1",
		Title:    "Title",
		ChartIds: []string{"d8a646d3-9c61-4cd7-bf10-8e96dc638211", "f47ac10b-58cc-4372-a567-0e02b2c3d479", "c9a646d3-9c61-4cd7-bf10-8e96dc638210"},
	}

	merged, err := MergeDashboards(current, updated, nil)
	if err != nil {
		t.Fatalf("MergeDashboards failed: %v", err)
	}

	chartIDs := merged.GetChartIds()
	if len(chartIDs) != 3 {
		t.Fatalf("merged.ChartIds len = %d, want 3", len(chartIDs))
	}
	if chartIDs[0] != "d8a646d3-9c61-4cd7-bf10-8e96dc638211" || chartIDs[1] != "f47ac10b-58cc-4372-a567-0e02b2c3d479" || chartIDs[2] != "c9a646d3-9c61-4cd7-bf10-8e96dc638210" {
		t.Errorf("merged.ChartIds order not preserved: got %v", chartIDs)
	}
}

func TestDashboardProtoToInsertMutations_InvalidDashboardName(t *testing.T) {
	dash := &dpb.Dashboard{
		Name: "invalid-name-without-prefix",
	}
	if _, err := DashboardProtoToInsertMutations(dash); err == nil {
		t.Errorf("DashboardProtoToInsertMutations with invalid name succeeded, want error")
	}
}

func TestDashboardProtoToUpdateMutations_InvalidDashboardName(t *testing.T) {
	dash := &dpb.Dashboard{
		Name: "invalid-name-without-prefix",
	}
	if _, err := DashboardProtoToUpdateMutations(dash, false); err == nil {
		t.Errorf("DashboardProtoToUpdateMutations with invalid name succeeded, want error")
	}
}

func TestDashboardProtoToInsertMutations_EmptyChartIDs(t *testing.T) {
	dash := &dpb.Dashboard{
		Name:     "dashboards/test-empty-charts",
		Title:    "Dashboard without charts",
		ChartIds: []string{},
	}
	mutations, err := DashboardProtoToInsertMutations(dash)
	if err != nil {
		t.Fatalf("DashboardProtoToInsertMutations failed: %v", err)
	}
	if len(mutations) != 1 {
		t.Errorf("Expected 1 mutation (dashboard only), got %d", len(mutations))
	}
}

func TestDashboardProtoToInsertMutations_LayoutPositionMatching(t *testing.T) {
	uuid1 := "c9a646d3-9c61-4cd7-bf10-8e96dc638210"
	uuid2 := "f47ac10b-58cc-4372-a567-0e02b2c3d479"

	dash := &dpb.Dashboard{
		Name:  "dashboards/test-layout-matching",
		Title: "Dashboard with Layout Matching",
		ChartIds: []string{
			uuid1,
			uuid2,
		},
		ChartLayout: &dpb.LayoutSpec{
			Type:    dpb.LayoutSpec_GRID,
			Columns: 2,
			Positions: []*dpb.GridPosition{
				{
					ItemId: uuid1,
					X:      0,
					Y:      0,
					Width:  6,
					Height: 4,
				},
				{
					ItemId: uuid2,
					X:      6,
					Y:      0,
					Width:  6,
					Height: 4,
				},
			},
		},
	}

	mutations, err := DashboardProtoToInsertMutations(dash)
	if err != nil {
		t.Fatalf("DashboardProtoToInsertMutations failed: %v", err)
	}

	// 1 Dashboard mutation + 2 DashboardCharts mutations = 3
	if len(mutations) != 3 {
		t.Fatalf("Expected 3 mutations, got %d", len(mutations))
	}
}
