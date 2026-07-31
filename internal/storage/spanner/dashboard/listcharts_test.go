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
	"time"

	dpb "xmanager_cloud/dashboard_service/proto/api_go_proto"
)

func TestListCharts_All(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	c1 := blankChartProto("charts/test-list-1", 100, 1)
	c2 := blankChartProto("charts/test-list-2", 100, 1)
	if err := repo.CreateChart(ctx, c1); err != nil {
		t.Fatalf("CreateChart c1 failed: %v", err)
	}
	if err := repo.CreateChart(ctx, c2); err != nil {
		t.Fatalf("CreateChart c2 failed: %v", err)
	}

	charts, token, err := repo.ListCharts(ctx, "", 10, "", "", "", 0)
	if err != nil {
		t.Fatalf("ListCharts failed: %v", err)
	}
	if len(charts) < 2 {
		t.Errorf("ListCharts count mismatch: got %d, want at least 2", len(charts))
	}
	if token != "" {
		t.Errorf("ListCharts next page token should be empty, got %q", token)
	}
}

func TestListCharts_Pagination(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	for _, name := range []string{"charts/p-1", "charts/p-2", "charts/p-3"} {
		c := blankChartProto(name, 100, 1)
		if err := repo.CreateChart(ctx, c); err != nil {
			t.Fatalf("CreateChart %s failed: %v", name, err)
		}
	}

	page1, token1, err := repo.ListCharts(ctx, "", 2, "", "", "chart_id", 0)
	if err != nil {
		t.Fatalf("ListCharts page 1 failed: %v", err)
	}
	if len(page1) != 2 {
		t.Fatalf("ListCharts page 1 len mismatch: got %d, want 2", len(page1))
	}
	if token1 == "" {
		t.Fatalf("ListCharts page 1 token is empty, expected token")
	}

	page2, token2, err := repo.ListCharts(ctx, "", 2, token1, "", "chart_id", 0)
	if err != nil {
		t.Fatalf("ListCharts page 2 failed: %v", err)
	}
	if len(page2) < 1 {
		t.Fatalf("ListCharts page 2 len mismatch: got %d, want >= 1", len(page2))
	}
	if token2 != "" {
		t.Errorf("ListCharts page 2 token should be empty, got %q", token2)
	}
}

func TestListCharts_Filter(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	c1 := blankChartProto("charts/filter-chart-1", 100, 1)
	c1.Title = "Alpha Chart"
	c2 := blankChartProto("charts/filter-chart-2", 100, 1)
	c2.Title = "Beta Chart"
	if err := repo.CreateChart(ctx, c1); err != nil {
		t.Fatalf("CreateChart c1 failed: %v", err)
	}
	if err := repo.CreateChart(ctx, c2); err != nil {
		t.Fatalf("CreateChart c2 failed: %v", err)
	}

	charts, _, err := repo.ListCharts(ctx, "", 10, "", "title = \"Beta Chart\"", "", 0)
	if err != nil {
		t.Fatalf("ListCharts with title filter failed: %v", err)
	}
	if len(charts) != 1 || charts[0].GetName() != "charts/filter-chart-2" {
		t.Errorf("ListCharts filter mismatch: got %+v", charts)
	}

	xidCharts, _, err := repo.ListCharts(ctx, "", 10, "", "xid = 100", "", 0)
	if err != nil {
		t.Fatalf("ListCharts with xid filter failed: %v", err)
	}
	if len(xidCharts) != 2 {
		t.Errorf("ListCharts filter by xid length mismatch: got %d, want 2", len(xidCharts))
	}
}

func TestListCharts_ParentDashboard(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	c1 := blankChartProto("charts/dash-linked-1", 100, 1)
	c2 := blankChartProto("charts/dash-unlinked-2", 100, 1)
	if err := repo.CreateChart(ctx, c1); err != nil {
		t.Fatalf("CreateChart c1 failed: %v", err)
	}
	if err := repo.CreateChart(ctx, c2); err != nil {
		t.Fatalf("CreateChart c2 failed: %v", err)
	}

	dash := blankDashboardProto("dashboards/parent-dash-1")
	dash.Charts = []*dpb.Chart{{Name: "charts/dash-linked-1"}}
	if err := repo.CreateDashboard(ctx, dash); err != nil {
		t.Fatalf("CreateDashboard failed: %v", err)
	}

	charts, _, err := repo.ListCharts(ctx, "dashboards/parent-dash-1", 10, "", "", "", 0)
	if err != nil {
		t.Fatalf("ListCharts with dashboard parent failed: %v", err)
	}
	if len(charts) != 1 || charts[0].GetName() != "charts/dash-linked-1" {
		t.Errorf("ListCharts dashboard parent mismatch: got %+v", charts)
	}
}

func TestListCharts_ParentDashboard_Empty(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	dash := blankDashboardProto("dashboards/empty-dash-1")
	if err := repo.CreateDashboard(ctx, dash); err != nil {
		t.Fatalf("CreateDashboard failed: %v", err)
	}

	charts, token, err := repo.ListCharts(ctx, "dashboards/empty-dash-1", 10, "", "", "", 0)
	if err != nil {
		t.Fatalf("ListCharts empty dashboard failed: %v", err)
	}
	if len(charts) != 0 || token != "" {
		t.Errorf("ListCharts empty dashboard expected 0 charts, got %d", len(charts))
	}
}

func TestListCharts_ParentExperiment(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	c1 := blankChartProto("charts/exp-parent-1", 100, 1)
	if err := repo.CreateChart(ctx, c1); err != nil {
		t.Fatalf("CreateChart c1 failed: %v", err)
	}

	charts, _, err := repo.ListCharts(ctx, "experiments/100", 10, "", "", "", time.Duration(0))
	if err != nil {
		t.Fatalf("ListCharts with experiment parent failed: %v", err)
	}
	if len(charts) == 0 {
		t.Errorf("ListCharts experiment parent returned 0 charts")
	}
}

func TestListCharts_InvalidPageSize(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	if _, _, err := repo.ListCharts(ctx, "", 0, "", "", "", 0); err == nil {
		t.Errorf("ListCharts with pageSize 0 succeeded, want error")
	}
}

func TestListCharts_InvalidPageToken(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	if _, _, err := repo.ListCharts(ctx, "", 10, "invalid-base64-token-!@#$", "", "", 0); err == nil {
		t.Errorf("ListCharts with invalid pageToken succeeded, want error")
	}
}

func TestListCharts_UnsupportedParentFormat(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	if _, _, err := repo.ListCharts(ctx, "invalid/parent/format", 10, "", "", "", 0); err == nil {
		t.Errorf("ListCharts with unsupported parent format succeeded, want error")
	}
}

func TestListCharts_ParentDashboard_WithStaleness(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	c1 := blankChartProto("charts/dash-stale-1", 100, 1)
	if err := repo.CreateChart(ctx, c1); err != nil {
		t.Fatalf("CreateChart c1 failed: %v", err)
	}

	dash := blankDashboardProto("dashboards/parent-dash-stale")
	dash.Charts = []*dpb.Chart{{Name: "charts/dash-stale-1"}}
	if err := repo.CreateDashboard(ctx, dash); err != nil {
		t.Fatalf("CreateDashboard failed: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	charts, _, err := repo.ListCharts(ctx, "dashboards/parent-dash-stale", 10, "", "", "", 10*time.Millisecond)
	if err != nil {
		t.Fatalf("ListCharts with staleness failed: %v", err)
	}
	if len(charts) != 1 || charts[0].GetName() != "charts/dash-stale-1" {
		t.Errorf("ListCharts with staleness mismatch: got %+v", charts)
	}
}

func TestExtractChartCursor_Nil(t *testing.T) {
	if _, err := ExtractChartCursor(nil); err == nil {
		t.Errorf("ExtractChartCursor(nil) expected error, got nil")
	}
}
