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

func TestDashboardRowToProto_Nil(t *testing.T) {
	if _, err := DashboardRowToProto(nil, nil); err == nil {
		t.Errorf("DashboardRowToProto(nil) expected error, got nil")
	}
}

func TestChartRowToProto_Nil(t *testing.T) {
	if _, err := ChartRowToProto(nil, nil); err == nil {
		t.Errorf("ChartRowToProto(nil) expected error, got nil")
	}
}
