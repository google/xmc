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

func TestDeleteDashboard_Success(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	dash := blankDashboardProto("dashboards/test-delete-1")
	if err := repo.CreateDashboard(ctx, dash, nil); err != nil {
		t.Fatalf("CreateDashboard failed: %v", err)
	}

	if err := repo.DeleteDashboard(ctx, "test-delete-1"); err != nil {
		t.Fatalf("DeleteDashboard failed: %v", err)
	}

	_, err := repo.GetDashboard(ctx, "test-delete-1")
	if err == nil {
		t.Errorf("GetDashboard after DeleteDashboard succeeded, want error")
	}
}

func TestDeleteDashboard_CascadesCharts(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	chartID := "f47ac10b-58cc-4372-a567-0e02b2c3d479"
	chart := blankChartProto(chartID, 100, 1)
	if err := repo.CreateChart(ctx, chart); err != nil {
		t.Fatalf("CreateChart failed: %v", err)
	}

	dash := blankDashboardProto("dashboards/test-delete-cascade-1")
	dash.ChartIds = []string{chartID}
	if err := repo.CreateDashboard(ctx, dash, nil); err != nil {
		t.Fatalf("CreateDashboard failed: %v", err)
	}

	if err := repo.DeleteDashboard(ctx, "test-delete-cascade-1"); err != nil {
		t.Fatalf("DeleteDashboard failed: %v", err)
	}

	// Dashboard should be gone.
	if _, err := repo.GetDashboard(ctx, "test-delete-cascade-1"); err == nil {
		t.Errorf("GetDashboard after DeleteDashboard succeeded, want error")
	}

	// The referenced chart itself should still exist in Charts table.
	if _, err := repo.GetChart(ctx, chartID); err != nil {
		t.Errorf("GetChart after DeleteDashboard failed: %v, chart entity should still exist", err)
	}
}

func TestDeleteDashboard_NotFound(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	err := repo.DeleteDashboard(ctx, "non-existent-delete")
	if err == nil {
		t.Fatalf("DeleteDashboard for non-existent dashboard succeeded, want error")
	}
	if !errors.Is(err, grpcerr.ErrNotFound) {
		t.Errorf("DeleteDashboard error = %v, want grpcerr.ErrNotFound", err)
	}
}

func TestDeleteDashboard_EmptyID(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	if err := repo.DeleteDashboard(ctx, ""); err == nil {
		t.Errorf("DeleteDashboard('') succeeded, want error")
	}
}
