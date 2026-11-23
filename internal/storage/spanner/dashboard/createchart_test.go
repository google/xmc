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
	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/testing/protocmp"
	fieldmaskpb "google.golang.org/protobuf/types/known/fieldmaskpb"

	dpb "xmanager_cloud/api/protos/dashboard"
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

func TestExperimentLevelChart_NullWidRoundTrip(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	e1 := experimentChartProto("study-a", 100)
	if err := repo.CreateChart(ctx, e1); err != nil {
		t.Fatalf("CreateChart %s failed: %v", e1.GetName(), err)
	}

	row, err := repo.client.Single().ReadRow(ctx, "Charts", spanner.Key{"study-a"}, []string{"Wid"})
	if err != nil {
		t.Fatalf("ReadRow failed: %v", err)
	}
	var wid spanner.NullInt64
	if err := row.Columns(&wid); err != nil || wid.Valid {
		t.Fatalf("Charts.Wid for an experiment-level chart = (%v, %v), want NULL", wid, err)
	}
	got, err := repo.GetChart(ctx, "study-a")
	if err != nil {
		t.Fatalf("GetChart failed: %v", err)
	}
	if diff := cmp.Diff(e1, got, protocmp.Transform(), protocmp.IgnoreFields(&dpb.Chart{}, "create_time", "update_time")); diff != "" {
		t.Errorf("GetChart round-trip mismatch (-want +got):\n%s", diff)
	}

	updated, err := repo.UpdateChart(ctx, &dpb.Chart{Name: e1.GetName(), Title: "Renamed"}, &fieldmaskpb.FieldMask{Paths: []string{"title"}})
	if err != nil {
		t.Fatalf("UpdateChart failed: %v", err)
	}
	if updated.GetWid() != 0 || updated.GetName() != e1.GetName() {
		t.Errorf("UpdateChart = (name %q, wid %d), want (%q, 0)", updated.GetName(), updated.GetWid(), e1.GetName())
	}
}
