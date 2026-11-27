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

	"cloud.google.com/go/spanner"

	dpb "xmanager_cloud/api/protos/dashboard"
	storagetest "xmanager_cloud/internal/storage/testing"
)

func TestCreateChart_Success(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	chart := blankChartProto("test-create-chart-1", 100, 1)
	if err := repo.CreateChart(ctx, chart); err != nil {
		t.Fatalf("CreateChart failed: %v", err)
	}

	got, err := repo.GetChart(ctx, "test-create-chart-1")
	if err != nil {
		t.Fatalf("GetChart failed: %v", err)
	}
	if got.GetTitle() != chart.GetTitle() {
		t.Errorf("GetChart Title mismatch: got %q, want %q", got.GetTitle(), chart.GetTitle())
	}
}

func TestCreateChart_WithoutLayout(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	chart := blankChartProto("test-create-chart-no-layout", 100, 1)
	chart.PlotLayout = nil
	if err := repo.CreateChart(ctx, chart); err != nil {
		t.Fatalf("CreateChart without layout failed: %v", err)
	}

	got, err := repo.GetChart(ctx, "test-create-chart-no-layout")
	if err != nil {
		t.Fatalf("GetChart failed: %v", err)
	}
	if got.GetPlotLayout() != nil {
		t.Errorf("GetChart PlotLayout expected nil, got %v", got.GetPlotLayout())
	}
}

func TestCreateChart_DuplicateIDReturnsAlreadyExists(t *testing.T) {
	const chartID = "test-create-chart-dup"
	workUnitChart := blankChartProto(chartID, 100, 1)
	tests := []struct {
		name     string
		base     *dpb.Chart
		xid, wid int64
	}{
		{name: "same work unit", base: workUnitChart, xid: 100, wid: 1},
		{name: "different experiment", base: workUnitChart, xid: 200, wid: 2},
		{name: "experiment-level duplicate of work-unit chart", base: workUnitChart, xid: 100},
		{name: "experiment-level duplicate of experiment-level chart", base: storagetest.ExperimentChartProto(chartID, 100), xid: 100},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			repo := setupDashboardRepository(ctx, t)
			storagetest.AssertDuplicateChartRejected(ctx, t, repo, tt.base, tt.xid, tt.wid)
		})
	}
}

func TestCreateChart_NoArtifactChartsCreated(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	chart := blankChartProto("test-create-chart-no-art", 100, 1)
	if err := repo.CreateChart(ctx, chart); err != nil {
		t.Fatalf("CreateChart failed: %v", err)
	}

	stmt := spanner.Statement{
		SQL: "SELECT COUNT(1) FROM ArtifactCharts WHERE ChartId = @chartId",
		Params: map[string]interface{}{
			"chartId": "test-create-chart-no-art",
		},
	}
	iter := repo.client.Single().Query(ctx, stmt)
	defer iter.Stop()

	row, err := iter.Next()
	if err != nil {
		t.Fatalf("Failed to query ArtifactCharts: %v", err)
	}
	var count int64
	if err := row.Column(0, &count); err != nil {
		t.Fatalf("Failed to read count: %v", err)
	}
	if count != 0 {
		t.Errorf("Expected 0 ArtifactCharts rows for new chart, got %d", count)
	}
}

func TestExperimentLevelChart_NullWidRoundTrip(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	e1 := storagetest.ExperimentChartProto("study-a", 100)
	if err := repo.CreateChart(ctx, e1); err != nil {
		t.Fatalf("CreateChart %s failed: %v", e1.GetName(), err)
	}

	assertWidNull := func() {
		row, err := repo.client.Single().ReadRow(ctx, "Charts", spanner.Key{"study-a"}, []string{"Wid"})
		if err != nil {
			t.Fatalf("ReadRow failed: %v", err)
		}
		var wid spanner.NullInt64
		if err := row.Columns(&wid); err != nil || wid.Valid {
			t.Fatalf("Charts.Wid for experiment-level chart = (%v, %v), want NULL", wid, err)
		}
	}
	assertWidNull()
	storagetest.VerifyExperimentLevelChartRoundTrip(ctx, t, repo, e1)
	assertWidNull()
}
