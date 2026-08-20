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
