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

func TestGetChart_Success(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepositoryWithParent(ctx, t, 200, 2)

	chart := blankChartProto("charts/test-get-chart-1", 200, 2)
	if err := repo.CreateChart(ctx, chart); err != nil {
		t.Fatalf("CreateChart failed: %v", err)
	}

	got, err := repo.GetChart(ctx, "charts/test-get-chart-1")
	if err != nil {
		t.Fatalf("GetChart failed: %v", err)
	}

	if got.GetName() != chart.GetName() {
		t.Errorf("GetChart Name mismatch: got %q, want %q", got.GetName(), chart.GetName())
	}
	if got.GetTitle() != chart.GetTitle() {
		t.Errorf("GetChart Title mismatch: got %q, want %q", got.GetTitle(), chart.GetTitle())
	}
	if got.GetXid() != chart.GetXid() {
		t.Errorf("GetChart Xid mismatch: got %d, want %d", got.GetXid(), chart.GetXid())
	}
	if len(got.GetPlots()) != len(chart.GetPlots()) {
		t.Fatalf("GetChart Plots len mismatch: got %d, want %d", len(got.GetPlots()), len(chart.GetPlots()))
	}
	if got.GetPlotLayout() == nil || got.GetPlotLayout().GetColumns() != chart.GetPlotLayout().GetColumns() {
		t.Errorf("GetChart PlotLayout mismatch")
	}
}

func TestGetChart_NotFound(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	_, err := repo.GetChart(ctx, "charts/non-existent")
	if err == nil {
		t.Errorf("GetChart for non-existent chart succeeded, want error")
	}
}
