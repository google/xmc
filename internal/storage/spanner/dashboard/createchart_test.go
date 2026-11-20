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

	"cloud.google.com/go/spanner"
	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/testing/protocmp"

	dpb "xmanager_cloud/api/protos/dashboard"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/prototest"
	"xmanager_cloud/internal/validator"
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

func assertDuplicateChartRejected(ctx context.Context, t *testing.T, repo *Repository, chartID string, xid, wid int64) {
	t.Helper()
	base := blankChartProto(chartID, 100, 1)
	if err := repo.CreateChart(ctx, base); err != nil {
		t.Fatalf("CreateChart(original) failed: %v", err)
	}
	want, err := repo.GetChart(ctx, chartID)
	if err != nil {
		t.Fatalf("GetChart(original) failed: %v", err)
	}
	dup := prototest.Modify(base, func(c *dpb.Chart) {
		c.Name = validator.ConstructWorkUnitChartName(xid, wid, chartID)
		c.Xid, c.Wid, c.Title = xid, wid, "Duplicate Title"
		c.Plots = []*dpb.PlotSpec{
			{PlotId: "plot-1", Title: "Replaced", Type: dpb.PlotType_BAR},
			{PlotId: "plot-2", Title: "Extra", Type: dpb.PlotType_SCATTER},
		}
	})
	if err := repo.CreateChart(ctx, dup); !errors.Is(err, grpcerr.ErrAlreadyExists) || status.Code(err) != codes.AlreadyExists {
		t.Fatalf("CreateChart(duplicate) error = %v (code %v), want %v", err, status.Code(err), grpcerr.ErrAlreadyExists)
	}
	got, err := repo.GetChart(ctx, chartID)
	if err != nil {
		t.Fatalf("GetChart(after duplicate) failed: %v", err)
	}
	if diff := cmp.Diff(want, got, protocmp.Transform()); diff != "" {
		t.Errorf("chart changed after rejected duplicate CreateChart (-want +got):\n%s", diff)
	}
}

func TestCreateChart_DuplicateIDReturnsAlreadyExists(t *testing.T) {
	tests := []struct {
		name     string
		xid, wid int64
	}{
		{name: "same work unit", xid: 100, wid: 1},
		{name: "different experiment", xid: 200, wid: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			repo := setupDashboardRepository(ctx, t)
			assertDuplicateChartRejected(ctx, t, repo, "test-create-chart-dup", tt.xid, tt.wid)
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
