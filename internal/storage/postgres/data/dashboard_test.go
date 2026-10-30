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

package postgresdata

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/testing/protocmp"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	dpb "xmanager_cloud/api/protos/dashboard"
)

var testDashboardTime = time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)

func TestDashboardProtoAndRowRoundTrip(t *testing.T) {
	c1 := "c9a646d3-9c61-4cd7-bf10-8e96dc638210"
	c2 := "f47ac10b-58cc-4372-a567-0e02b2c3d479"
	dash := &dpb.Dashboard{
		Name:        "dashboards/dash-1",
		Title:       "Main Dashboard",
		Description: "Dashboard description",
		Owner:       "owner@example.com",
		ChartIds:    []string{c1, c2},
		ChartLayout: &dpb.LayoutSpec{
			Type:    dpb.LayoutSpec_GRID,
			Columns: 2,
			Positions: []*dpb.GridPosition{
				{ItemId: c1, X: 0, Y: 0, Width: 2, Height: 1},
			},
		},
		CreateTime: timestamppb.New(testDashboardTime),
		UpdateTime: timestamppb.New(testDashboardTime),
	}

	row, chartRows, err := DashboardProtoToRow(dash, testDashboardTime)
	if err != nil {
		t.Fatalf("DashboardProtoToRow() failed: %v", err)
	}
	if len(chartRows) != 2 {
		t.Fatalf("len(chartRows) = %d, want 2", len(chartRows))
	}
	if len(row.GetInsertArgs()) != 7 || len(row.GetUpdateArgs()) != 5 || len(chartRows[0].GetInsertArgs()) != 5 {
		t.Errorf("unexpected named args lengths: insert=%d update=%d chart=%d", len(row.GetInsertArgs()), len(row.GetUpdateArgs()), len(chartRows[0].GetInsertArgs()))
	}

	got, err := DashboardRowToProto(row)
	if err != nil {
		t.Fatalf("DashboardRowToProto() failed: %v", err)
	}
	if diff := cmp.Diff(dash, got, protocmp.Transform()); diff != "" {
		t.Errorf("Dashboard round-trip mismatch (-want +got):\n%s", diff)
	}
}

func TestChartProtoAndRowRoundTrip(t *testing.T) {
	chart := &dpb.Chart{
		Name:        "experiments/100/workUnits/1/charts/chart-1",
		Title:       "Loss Curve",
		Description: "Training loss",
		Xid:         100,
		Wid:         1,
		Plots: []*dpb.PlotSpec{
			{PlotId: "p1", Title: "Loss", Type: dpb.PlotType_SCATTER},
			{PlotId: "p2", Title: "Accuracy", Type: dpb.PlotType_BAR},
		},
		PlotLayout: &dpb.LayoutSpec{Type: dpb.LayoutSpec_WRAP, Columns: 1},
		CreateTime: timestamppb.New(testDashboardTime),
		UpdateTime: timestamppb.New(testDashboardTime),
	}

	row, plotRows, err := ChartProtoToRow(chart, testDashboardTime)
	if err != nil {
		t.Fatalf("ChartProtoToRow() failed: %v", err)
	}
	if len(plotRows) != 2 {
		t.Fatalf("len(plotRows) = %d, want 2", len(plotRows))
	}
	if len(row.GetInsertArgs()) != 9 || len(row.GetUpdateArgs()) != 6 || len(plotRows[0].GetInsertArgs()) != 5 {
		t.Errorf("unexpected named args lengths: insert=%d update=%d plot=%d", len(row.GetInsertArgs()), len(row.GetUpdateArgs()), len(plotRows[0].GetInsertArgs()))
	}

	row.PlotSpecs = [][]byte{plotRows[0].PlotSpec, plotRows[1].PlotSpec}
	got, err := ChartRowToProto(row)
	if err != nil {
		t.Fatalf("ChartRowToProto() failed: %v", err)
	}
	if diff := cmp.Diff(chart, got, protocmp.Transform()); diff != "" {
		t.Errorf("Chart round-trip mismatch (-want +got):\n%s", diff)
	}
}

func TestDashboardAndChartConversionErrors(t *testing.T) {
	corrupt := []byte{0xff, 0xff, 0xff}
	if _, _, err := DashboardProtoToRow(nil, testDashboardTime); err == nil {
		t.Error("DashboardProtoToRow(nil) = nil, want error")
	}
	if _, _, err := DashboardProtoToRow(&dpb.Dashboard{Name: "invalid"}, testDashboardTime); err == nil {
		t.Error("DashboardProtoToRow(invalid name) = nil, want error")
	}
	if _, _, err := DashboardProtoToRow(&dpb.Dashboard{Name: "dashboards/d1", ChartIds: []string{""}}, testDashboardTime); err == nil {
		t.Error("DashboardProtoToRow(invalid chart_id) = nil, want error")
	}
	if _, err := DashboardRowToProto(DashboardRow{}); err == nil {
		t.Error("DashboardRowToProto(empty ID) = nil, want error")
	}
	if _, err := DashboardRowToProto(DashboardRow{DashboardID: "d1", ChartLayout: corrupt}); err == nil {
		t.Error("DashboardRowToProto(corrupt layout) = nil, want error")
	}
	if _, _, err := ChartProtoToRow(nil, testDashboardTime); err == nil {
		t.Error("ChartProtoToRow(nil) = nil, want error")
	}
	if _, _, err := ChartProtoToRow(&dpb.Chart{Name: "invalid"}, testDashboardTime); err == nil {
		t.Error("ChartProtoToRow(invalid name) = nil, want error")
	}
	if _, err := ChartRowToProto(ChartRow{}); err == nil {
		t.Error("ChartRowToProto(empty ID) = nil, want error")
	}
	if _, err := ChartRowToProto(ChartRow{ChartID: "c1", PlotLayout: corrupt}); err == nil {
		t.Error("ChartRowToProto(corrupt layout) = nil, want error")
	}
	if _, err := ChartRowToProto(ChartRow{ChartID: "c1", PlotSpecs: [][]byte{corrupt}}); err == nil {
		t.Error("ChartRowToProto(corrupt plot spec) = nil, want error")
	}
}
