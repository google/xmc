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

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/testing/protocmp"

	dpb "xmanager_cloud/api/protos/dashboard"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/prototest"
)

func TestCreateDashboard_Success(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	dash := blankDashboardProto("dashboards/test-create-1")
	if err := repo.CreateDashboard(ctx, dash, nil); err != nil {
		t.Fatalf("CreateDashboard failed: %v", err)
	}
}

func TestCreateDashboard_WithChartIDs(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	chartID := "f47ac10b-58cc-4372-a567-0e02b2c3d479"
	chart := blankChartProto(chartID, 100, 1)
	if err := repo.CreateChart(ctx, chart); err != nil {
		t.Fatalf("CreateChart failed: %v", err)
	}

	dash := blankDashboardProto("dashboards/test-create-with-charts")
	dash.ChartIds = []string{chartID}
	if err := repo.CreateDashboard(ctx, dash, nil); err != nil {
		t.Fatalf("CreateDashboard with chart IDs failed: %v", err)
	}
}

func TestCreateDashboard_WithoutLayout(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	dash := blankDashboardProto("dashboards/test-create-no-layout")
	dash.ChartLayout = nil
	if err := repo.CreateDashboard(ctx, dash, nil); err != nil {
		t.Fatalf("CreateDashboard without layout failed: %v", err)
	}
}

func TestCreateDashboard_NonExistentChartID(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	dash := blankDashboardProto("dashboards/test-create-nonexistent-chart")
	dash.ChartIds = []string{"00000000-0000-0000-0000-000000000000"}

	err := repo.CreateDashboard(ctx, dash, nil)
	if err == nil {
		t.Fatalf("CreateDashboard with non-existent chart ID expected foreign key error, got nil")
	}
}

func TestCreateDashboard_WithInlineCharts(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	chartID := "9f8a2c5e-1d3b-4f6a-8e7c-2b5d9a1f4c60"
	chart := blankChartProto(chartID, 100, 1)

	dash := blankDashboardProto("dashboards/test-create-inline-charts")
	dash.ChartIds = []string{chartID}
	if err := repo.CreateDashboard(ctx, dash, []*dpb.Chart{chart}); err != nil {
		t.Fatalf("CreateDashboard with inline charts failed: %v", err)
	}

	got, err := repo.GetDashboard(ctx, "test-create-inline-charts")
	if err != nil {
		t.Fatalf("GetDashboard failed: %v", err)
	}
	if len(got.GetChartIds()) != 1 || got.GetChartIds()[0] != chartID {
		t.Errorf("GetDashboard ChartIds mismatch: got %v, want [%q]", got.GetChartIds(), chartID)
	}

	gotChart, err := repo.GetChart(ctx, chartID)
	if err != nil {
		t.Fatalf("GetChart for inline chart failed: %v", err)
	}
	if gotChart.GetTitle() != chart.GetTitle() {
		t.Errorf("GetChart Title mismatch: got %q, want %q", gotChart.GetTitle(), chart.GetTitle())
	}
}

func TestCreateDashboard_InlineChartsRolledBackOnFailure(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	chartID := "1c2d3e4f-5a6b-4c7d-8e9f-0a1b2c3d4e5f"
	chart := blankChartProto(chartID, 100, 1)

	dash := blankDashboardProto("dashboards/test-create-inline-rollback")
	dash.ChartIds = []string{chartID, "00000000-0000-0000-0000-000000000000"}

	if err := repo.CreateDashboard(ctx, dash, []*dpb.Chart{chart}); err == nil {
		t.Fatalf("CreateDashboard referencing a non-existent chart expected foreign key error, got nil")
	}

	if _, err := repo.GetChart(ctx, chartID); err == nil {
		t.Errorf("GetChart found chart %q, want the inline chart to be rolled back with the dashboard", chartID)
	}
}

func TestCreateDashboard_DuplicateInlineChartIDReturnsAlreadyExists(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	const chartID = "2a3b4c5d-6e7f-4a8b-9c0d-1e2f3a4b5c6d"
	if err := repo.CreateChart(ctx, blankChartProto(chartID, 100, 1)); err != nil {
		t.Fatalf("CreateChart(original) failed: %v", err)
	}

	dash := blankDashboardProto("dashboards/test-dup-inline-chart")
	dash.ChartIds = []string{chartID}
	original, err := repo.GetChart(ctx, chartID)
	if err != nil {
		t.Fatalf("GetChart(original) failed: %v", err)
	}
	dup := prototest.Modify(blankChartProto(chartID, 100, 1), func(c *dpb.Chart) { c.Title = "Duplicate Title" })
	err = repo.CreateDashboard(ctx, dash, []*dpb.Chart{dup})
	if !errors.Is(err, grpcerr.ErrAlreadyExists) || status.Code(err) != codes.AlreadyExists {
		t.Fatalf("CreateDashboard(duplicate inline chart) error = %v (code %v), want %v", err, status.Code(err), grpcerr.ErrAlreadyExists)
	}
	if _, err := repo.GetDashboard(ctx, "test-dup-inline-chart"); !errors.Is(err, grpcerr.ErrNotFound) {
		t.Errorf("GetDashboard after rejected CreateDashboard error = %v, want %v", err, grpcerr.ErrNotFound)
	}
	got, err := repo.GetChart(ctx, chartID)
	if err != nil {
		t.Fatalf("GetChart(after rejected CreateDashboard) failed: %v", err)
	}
	if diff := cmp.Diff(original, got, protocmp.Transform()); diff != "" {
		t.Errorf("chart changed after rejected CreateDashboard (-want +got):\n%s", diff)
	}
}
