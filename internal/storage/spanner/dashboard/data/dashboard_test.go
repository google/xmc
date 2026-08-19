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
		Charts: []*dpb.Chart{
			{Name: "charts/c1"},
		},
		ChartLayout: &dpb.LayoutSpec{
			Type:    dpb.LayoutSpec_GRID,
			Columns: 2,
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
		Name:  "charts/test-chart-data-1",
		Title: "Test Chart Title",
		Xid:   100,
		Wid:   1,
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

	if len(mutations) != 3 {
		t.Errorf("Expected 3 mutations (chart + plot spec + artifact chart link), got %d", len(mutations))
	}
}

func TestChartProtoToUpdateMutations_Success(t *testing.T) {
	chart := &dpb.Chart{
		Name:  "charts/test-chart-update-1",
		Title: "Updated Chart Title",
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
		Name:  "charts/test-chart-update-no-layout",
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
		Name:  "charts/test-chart-update-single",
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

	charts := []*dpb.Chart{
		{Name: "charts/c2"},
		{Name: "charts/c1"},
	}

	now := time.Now().UTC()
	row, err := spanner.NewRow(
		[]string{"CreatedAt", "DashboardId", "UpdatedAt", "Description", "Owner", "Title", "ChartLayout", "ExtraIgnoredCol"},
		[]interface{}{now, "d1", now, "Test Desc", "owner@example.com", "Test Title", layoutBytes, "extra_value"},
	)
	if err != nil {
		t.Fatalf("spanner.NewRow failed: %v", err)
	}

	dash, err := DashboardRowToProto(row, charts)
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
	if len(dash.GetCharts()) != 2 || dash.GetCharts()[0].GetName() != "charts/c1" {
		t.Errorf("dash.Charts: got %v, want canonically sorted charts", dash.GetCharts())
	}
	if dash.GetChartLayout() == nil || dash.GetChartLayout().GetColumns() != 3 {
		t.Errorf("dash.ChartLayout: got %v, want columns=3", dash.GetChartLayout())
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
		[]string{"CreatedAt", "ChartId", "UpdatedAt", "Owner", "Title", "Xid", "PlotLayout", "ExtraCol"},
		[]interface{}{now, "charts/c1", now, "owner@example.com", "Test Title", int64(100), layoutBytes, 12345},
	)
	if err != nil {
		t.Fatalf("spanner.NewRow failed: %v", err)
	}

	chart, err := ChartRowToProto(row, plots)
	if err != nil {
		t.Fatalf("ChartRowToProto failed: %v", err)
	}

	if chart.GetName() != "charts/c1" {
		t.Errorf("chart.Name: got %q, want 'charts/c1'", chart.GetName())
	}
	if chart.GetTitle() != "Test Title" {
		t.Errorf("chart.Title: got %q, want 'Test Title'", chart.GetTitle())
	}
	if chart.GetXid() != 100 {
		t.Errorf("chart.Xid: got %d, want 100", chart.GetXid())
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
		Charts:      []*dpb.Chart{{Name: "charts/c1"}},
		ChartLayout: &dpb.LayoutSpec{Type: dpb.LayoutSpec_GRID, Columns: 1},
	}
	updated := &dpb.Dashboard{
		Name:        "dashboards/d1",
		Title:       "New Title",
		Description: "New Desc",
		Owner:       "attacker@example.com",
		Charts:      []*dpb.Chart{{Name: "charts/c2"}},
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
	if len(merged.GetCharts()) != 1 || merged.GetCharts()[0].GetName() != "charts/c2" {
		t.Errorf("Charts: got %v, want charts/c2", merged.GetCharts())
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
		Charts:      []*dpb.Chart{{Name: "charts/c1"}},
	}
	updated := &dpb.Dashboard{
		Name:        "dashboards/d1",
		Title:       "New Title",
		Description: "New Desc",
		Charts:      []*dpb.Chart{{Name: "charts/c2"}},
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
	if len(merged.GetCharts()) != 1 || merged.GetCharts()[0].GetName() != "charts/c1" {
		t.Errorf("Charts: got %v, want charts/c1 (should not have changed)", merged.GetCharts())
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
		Charts:      []*dpb.Chart{{Name: "charts/c1"}, {Name: "charts/c2"}},
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

func TestDashboardProtoToInsertMutations_NilChart(t *testing.T) {
	dash := &dpb.Dashboard{
		Name:   "dashboards/d1",
		Charts: []*dpb.Chart{nil},
	}
	if _, err := DashboardProtoToInsertMutations(dash); err == nil {
		t.Errorf("DashboardProtoToInsertMutations with nil chart succeeded, want error")
	}
}

func TestDashboardProtoToUpdateMutations_NilChart(t *testing.T) {
	dash := &dpb.Dashboard{
		Name:   "dashboards/d1",
		Charts: []*dpb.Chart{nil},
	}
	if _, err := DashboardProtoToUpdateMutations(dash, true); err == nil {
		t.Errorf("DashboardProtoToUpdateMutations with nil chart succeeded, want error")
	}
}

func TestSortCharts(t *testing.T) {
	chart1 := &dpb.Chart{Name: "charts/c1"}
	chart2 := &dpb.Chart{Name: "charts/c2"}
	chart3 := &dpb.Chart{Name: "charts/c3"}

	tests := []struct {
		name     string
		input    []*dpb.Chart
		wantLen  int
		wantName []string
	}{
		{
			name:     "nil slice",
			input:    nil,
			wantLen:  0,
			wantName: nil,
		},
		{
			name:     "empty slice",
			input:    []*dpb.Chart{},
			wantLen:  0,
			wantName: []string{},
		},
		{
			name:     "single element",
			input:    []*dpb.Chart{chart1},
			wantLen:  1,
			wantName: []string{"charts/c1"},
		},
		{
			name:     "already sorted",
			input:    []*dpb.Chart{chart1, chart2, chart3},
			wantLen:  3,
			wantName: []string{"charts/c1", "charts/c2", "charts/c3"},
		},
		{
			name:     "reverse sorted",
			input:    []*dpb.Chart{chart3, chart2, chart1},
			wantLen:  3,
			wantName: []string{"charts/c1", "charts/c2", "charts/c3"},
		},
		{
			name:     "with nil element",
			input:    []*dpb.Chart{chart2, nil, chart1},
			wantLen:  3,
			wantName: []string{"", "charts/c1", "charts/c2"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := SortCharts(tc.input)
			if len(got) != tc.wantLen {
				t.Fatalf("SortCharts() len = %d, want %d", len(got), tc.wantLen)
			}
			for i, want := range tc.wantName {
				if got[i] == nil {
					if want != "" {
						t.Errorf("got[%d] = nil, want %q", i, want)
					}
					continue
				}
				if got[i].GetName() != want {
					t.Errorf("got[%d] Name = %q, want %q", i, got[i].GetName(), want)
				}
			}
		})
	}
}

func TestSortCharts_Immutability(t *testing.T) {
	chart1 := &dpb.Chart{Name: "charts/c1"}
	chart2 := &dpb.Chart{Name: "charts/c2"}
	input := []*dpb.Chart{chart2, chart1}

	got := SortCharts(input)
	if got[0].GetName() != "charts/c1" || got[1].GetName() != "charts/c2" {
		t.Errorf("SortCharts() did not sort properly: got %v", got)
	}
	if input[0].GetName() != "charts/c2" || input[1].GetName() != "charts/c1" {
		t.Errorf("SortCharts() mutated original input slice: got %v", input)
	}
}

func TestMergeDashboards_PreservesChartsOrder(t *testing.T) {
	current := &dpb.Dashboard{
		Name:   "dashboards/d1",
		Title:  "Title",
		Charts: []*dpb.Chart{{Name: "charts/c1"}},
	}
	updated := &dpb.Dashboard{
		Name:   "dashboards/d1",
		Title:  "Title",
		Charts: []*dpb.Chart{{Name: "charts/c3"}, {Name: "charts/c1"}, {Name: "charts/c2"}},
	}

	merged, err := MergeDashboards(current, updated, nil)
	if err != nil {
		t.Fatalf("MergeDashboards failed: %v", err)
	}

	charts := merged.GetCharts()
	if len(charts) != 3 {
		t.Fatalf("merged.Charts len = %d, want 3", len(charts))
	}
	if charts[0].GetName() != "charts/c3" || charts[1].GetName() != "charts/c1" || charts[2].GetName() != "charts/c2" {
		t.Errorf("merged.Charts order not preserved: got %v", charts)
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
