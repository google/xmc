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

package storagetest

import (
	"context"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/testing/protocmp"
	fieldmaskpb "google.golang.org/protobuf/types/known/fieldmaskpb"
	dpb "xmanager_cloud/api/protos/dashboard"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/validator"
)

// ExperimentChartProto returns an experiment-level chart fixture, which has no wid.
func ExperimentChartProto(chartID string, xid int64) *dpb.Chart {
	return &dpb.Chart{
		Name:  validator.ConstructExperimentChartName(xid, chartID),
		Title: "Study " + chartID,
		Xid:   xid,
		Plots: []*dpb.PlotSpec{{PlotId: "study", Title: "Study", Type: dpb.PlotType_PARALLEL_COORDINATES}},
	}
}

// CollectChartPageProtos reads all ListCharts pages using pageSize and returns every chart proto.
func CollectChartPageProtos(ctx context.Context, t *testing.T, repo storage.DashboardStore, parent string, pageSize int32, filter, orderBy string) []*dpb.Chart {
	t.Helper()
	if pageSize <= 0 {
		t.Fatalf("CollectChartPageProtos pageSize = %d, want > 0", pageSize)
	}
	var charts []*dpb.Chart
	seenTokens := make(map[string]bool)
	for tok := ""; ; {
		page, next, err := repo.ListCharts(ctx, parent, pageSize, tok, filter, orderBy, 0)
		if err != nil {
			t.Fatalf("ListCharts(%q, %q, %q) failed: %v", parent, filter, orderBy, err)
		}
		if len(page) > int(pageSize) {
			t.Fatalf("ListCharts(%q) page len = %d, want <= %d", orderBy, len(page), pageSize)
		}
		if next != "" && len(page) != int(pageSize) {
			t.Fatalf("ListCharts(%q) non-terminal page len = %d, want %d", orderBy, len(page), pageSize)
		}
		charts = append(charts, page...)
		if next == "" {
			return charts
		}
		if seenTokens[next] {
			t.Fatalf("ListCharts(%q) repeated page token %q", orderBy, next)
		}
		seenTokens[next] = true
		tok = next
	}
}

// CollectChartPages reads all ListCharts pages using pageSize and returns their names.
func CollectChartPages(ctx context.Context, t *testing.T, repo storage.DashboardStore, parent string, pageSize int32, filter, orderBy string) []string {
	t.Helper()
	pages := CollectChartPageProtos(ctx, t, repo, parent, pageSize, filter, orderBy)
	names := make([]string, len(pages))
	for i, c := range pages {
		names[i] = c.GetName()
	}
	return names
}

// VerifyExperimentLevelChartRoundTrip asserts GetChart round-trip and UpdateChart Wid=0 preservation.
func VerifyExperimentLevelChartRoundTrip(ctx context.Context, t *testing.T, repo storage.DashboardStore, created *dpb.Chart) {
	t.Helper()
	_, _, chartID, err := validator.ValidateChartName(created.GetName())
	if err != nil {
		t.Fatalf("ValidateChartName(%q) failed: %v", created.GetName(), err)
	}
	got, err := repo.GetChart(ctx, chartID)
	if err != nil {
		t.Fatalf("GetChart failed: %v", err)
	}
	if diff := gocmp.Diff(created, got, protocmp.Transform(), protocmp.IgnoreFields(&dpb.Chart{}, "create_time", "update_time")); diff != "" {
		t.Errorf("GetChart round-trip mismatch (-want +got):\n%s", diff)
	}
	updated, err := repo.UpdateChart(ctx, &dpb.Chart{Name: created.GetName(), Title: "Renamed"}, &fieldmaskpb.FieldMask{Paths: []string{"title"}})
	if err != nil || updated.GetWid() != 0 || updated.GetName() != created.GetName() {
		t.Fatalf("UpdateChart = (%q, wid=%d, err=%v), want (%q, 0, nil)", updated.GetName(), updated.GetWid(), err, created.GetName())
	}
	afterUpdate, err := repo.GetChart(ctx, chartID)
	if err != nil || afterUpdate.GetTitle() != "Renamed" || afterUpdate.GetWid() != 0 || afterUpdate.GetName() != created.GetName() {
		t.Fatalf("GetChart after UpdateChart = (%q, title=%q, wid=%d, err=%v), want (%q, \"Renamed\", 0, nil)", afterUpdate.GetName(), afterUpdate.GetTitle(), afterUpdate.GetWid(), err, created.GetName())
	}
}

// VerifyListChartsParentExperiment verifies that experiments/{xid} returns only that experiment's
// experiment-level charts across pages, isolated from work unit charts and other experiments.
func VerifyListChartsParentExperiment(ctx context.Context, t *testing.T, repo storage.DashboardStore) {
	t.Helper()
	e1, e2 := ExperimentChartProto("study-a", 100), ExperimentChartProto("study-b", 100)
	other := ExperimentChartProto("study-c", 200)
	wu := &dpb.Chart{Name: validator.ConstructWorkUnitChartName(100, 1, "wu-chart"), Title: "WU", Xid: 100, Wid: 1, Plots: []*dpb.PlotSpec{{PlotId: "p1", Type: dpb.PlotType_SCATTER}}}
	for _, c := range []*dpb.Chart{e1, e2, other, wu} {
		if err := repo.CreateChart(ctx, c); err != nil {
			t.Fatalf("CreateChart %s failed: %v", c.GetName(), err)
		}
	}
	ignoreTimestamps := protocmp.IgnoreFields(&dpb.Chart{}, "create_time", "update_time")
	got := CollectChartPageProtos(ctx, t, repo, "experiments/100", 1, "", "")
	if diff := gocmp.Diff([]*dpb.Chart{e1, e2}, got, protocmp.Transform(), ignoreTimestamps); diff != "" {
		t.Errorf("ListCharts experiment parent mismatch (-want +got):\n%s", diff)
	}
	wuCharts, wuNext, err := repo.ListCharts(ctx, "experiments/100/workUnits/1", 10, "", "", "", 0)
	if err != nil || wuNext != "" {
		t.Fatalf("ListCharts work unit parent err=%v next=%q, want nil and empty token", err, wuNext)
	}
	if diff := gocmp.Diff([]*dpb.Chart{wu}, wuCharts, protocmp.Transform(), ignoreTimestamps); diff != "" {
		t.Errorf("ListCharts work unit parent mismatch (-want +got):\n%s", diff)
	}
}

// VerifyListChartsWidNullOrderAndPagination checks wid NULL ordering and pageSize=1 pagination.
func VerifyListChartsWidNullOrderAndPagination(ctx context.Context, t *testing.T, repo storage.DashboardStore) {
	t.Helper()
	e1, e2 := ExperimentChartProto("wid-ord-a", 100), ExperimentChartProto("wid-ord-b", 100)
	wu := func(id string) *dpb.Chart {
		return &dpb.Chart{Name: validator.ConstructWorkUnitChartName(100, 1, id), Title: id, Xid: 100, Wid: 1, Plots: []*dpb.PlotSpec{{PlotId: "p1", Type: dpb.PlotType_SCATTER}}}
	}
	w1, w2 := wu("wid-ord-c"), wu("wid-ord-d")
	for _, c := range []*dpb.Chart{e1, e2, w1, w2} {
		if err := repo.CreateChart(ctx, c); err != nil {
			t.Fatalf("CreateChart %s failed: %v", c.GetName(), err)
		}
	}
	if err := repo.CreateDashboard(ctx, &dpb.Dashboard{Name: "dashboards/wid-ord-dash", Title: "Dash", Owner: "u", ChartIds: []string{"wid-ord-a", "wid-ord-b", "wid-ord-c", "wid-ord-d"}}, nil); err != nil {
		t.Fatalf("CreateDashboard failed: %v", err)
	}
	for _, tc := range []struct {
		name, parent, filter, orderBy string
		want                          []string
	}{
		{"root/wid asc", "", "xid = 100", "wid asc", []string{e1.GetName(), e2.GetName(), w1.GetName(), w2.GetName()}},
		{"root/wid desc", "", "xid = 100", "wid desc", []string{w1.GetName(), w2.GetName(), e1.GetName(), e2.GetName()}},
		{"dashboard_parent/wid asc", "dashboards/wid-ord-dash", "", "wid asc", []string{e1.GetName(), e2.GetName(), w1.GetName(), w2.GetName()}},
		{"dashboard_parent/wid desc", "dashboards/wid-ord-dash", "", "wid desc", []string{w1.GetName(), w2.GetName(), e1.GetName(), e2.GetName()}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if diff := gocmp.Diff(tc.want, CollectChartPages(ctx, t, repo, tc.parent, 1, tc.filter, tc.orderBy)); diff != "" {
				t.Errorf("ListCharts(%q) mismatch (-want +got):\n%s", tc.orderBy, diff)
			}
		})
	}
}
