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
)

func TestCreateDashboard_Success(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	dash := blankDashboardProto("dashboards/test-create-1")
	if err := repo.CreateDashboard(ctx, dash); err != nil {
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
	if err := repo.CreateDashboard(ctx, dash); err != nil {
		t.Fatalf("CreateDashboard with chart IDs failed: %v", err)
	}
}

func TestCreateDashboard_WithoutLayout(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	dash := blankDashboardProto("dashboards/test-create-no-layout")
	dash.ChartLayout = nil
	if err := repo.CreateDashboard(ctx, dash); err != nil {
		t.Fatalf("CreateDashboard without layout failed: %v", err)
	}
}

func TestCreateDashboard_NonExistentChartID(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	dash := blankDashboardProto("dashboards/test-create-nonexistent-chart")
	dash.ChartIds = []string{"00000000-0000-0000-0000-000000000000"}

	err := repo.CreateDashboard(ctx, dash)
	if err == nil {
		t.Fatalf("CreateDashboard with non-existent chart ID expected foreign key error, got nil")
	}
}
