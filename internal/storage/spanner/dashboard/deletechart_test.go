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

	"xmanager_cloud/internal/grpcerr"
)

func TestDeleteChart_Success(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	chart := blankChartProto("test-delete-chart-1", 100, 1)
	if err := repo.CreateChart(ctx, chart); err != nil {
		t.Fatalf("CreateChart failed: %v", err)
	}

	if err := repo.DeleteChart(ctx, "test-delete-chart-1"); err != nil {
		t.Fatalf("DeleteChart failed: %v", err)
	}

	if _, err := repo.GetChart(ctx, "test-delete-chart-1"); err == nil {
		t.Errorf("GetChart after DeleteChart succeeded, want error")
	}
}

func TestDeleteChart_WithDashboardLink(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	chartID := "f47ac10b-58cc-4372-a567-0e02b2c3d479"
	chart := blankChartProto(chartID, 100, 1)
	if err := repo.CreateChart(ctx, chart); err != nil {
		t.Fatalf("CreateChart failed: %v", err)
	}

	dash := blankDashboardProto("dashboards/test-delete-parent-dash")
	dash.ChartIds = []string{chartID}
	if err := repo.CreateDashboard(ctx, dash, nil); err != nil {
		t.Fatalf("CreateDashboard failed: %v", err)
	}

	if err := repo.DeleteChart(ctx, chartID); err != nil {
		t.Fatalf("DeleteChart failed: %v", err)
	}

	if _, err := repo.GetChart(ctx, chartID); err == nil {
		t.Errorf("GetChart after DeleteChart succeeded, want error")
	}
}

func TestDeleteChart_NotFound(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	if err := repo.DeleteChart(ctx, "non-existent"); !errors.Is(err, grpcerr.ErrNotFound) {
		t.Errorf("DeleteChart for non-existent chart error = %v, want grpcerr.ErrNotFound", err)
	}
}

func TestDeleteChart_EmptyID(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	if err := repo.DeleteChart(ctx, ""); err == nil {
		t.Errorf("DeleteChart with empty chartId succeeded, want error")
	}
}
