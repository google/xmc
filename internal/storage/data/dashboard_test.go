// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package data

import (
	"errors"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
	fieldmaskpb "google.golang.org/protobuf/types/known/fieldmaskpb"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	dpb "xmanager_cloud/api/protos/dashboard"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/prototest"
)

func TestSortChartIDs(t *testing.T) {
	tests := []struct {
		name  string
		input []string
		want  []string
	}{
		{name: "empty slice", input: nil, want: nil},
		{name: "sorted unique", input: []string{"a", "b", "c"}, want: []string{"a", "b", "c"}},
		{name: "unsorted with duplicates", input: []string{"c", "a", "b", "a"}, want: []string{"a", "b", "c"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := SortChartIDs(tc.input)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("SortChartIDs(%v) diff (-want +got):\n%s", tc.input, diff)
			}
		})
	}

	input := []string{"c", "a"}
	_ = SortChartIDs(input)
	if input[0] != "c" || input[1] != "a" {
		t.Errorf("SortChartIDs() mutated input slice: got %v", input)
	}
}

func TestChartIDsEqual(t *testing.T) {
	tests := []struct {
		name string
		a    []string
		b    []string
		want bool
	}{
		{name: "both nil", a: nil, b: nil, want: true},
		{name: "one nil one empty", a: nil, b: []string{}, want: true},
		{name: "both empty", a: []string{}, b: []string{}, want: true},
		{name: "equal single", a: []string{"a"}, b: []string{"a"}, want: true},
		{name: "equal multiple", a: []string{"a", "b"}, b: []string{"a", "b"}, want: true},
		{name: "different lengths", a: []string{"a"}, b: []string{"a", "b"}, want: false},
		{name: "different elements", a: []string{"a", "b"}, b: []string{"a", "c"}, want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ChartIDsEqual(tc.a, tc.b); got != tc.want {
				t.Errorf("ChartIDsEqual(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

func TestMergeDashboards(t *testing.T) {
	current := &dpb.Dashboard{
		Name:        "dashboards/d1",
		Title:       "Old Title",
		Description: "Old Desc",
		Owner:       "owner@example.com",
		ChartIds:    []string{"c1"},
		ChartLayout: &dpb.LayoutSpec{Columns: 2},
	}
	updated := &dpb.Dashboard{
		Title:       "New Title",
		Description: "New Desc",
		ChartIds:    []string{"c2", "c3"},
		ChartLayout: &dpb.LayoutSpec{Columns: 4},
	}

	tests := []struct {
		name       string
		current    *dpb.Dashboard
		updated    *dpb.Dashboard
		updateMask *fieldmaskpb.FieldMask
		want       *dpb.Dashboard
		wantErr    bool
	}{
		{
			name:       "nil mask updates all mutable fields",
			current:    current,
			updated:    updated,
			updateMask: nil,
			want: prototest.Modify(current, func(d *dpb.Dashboard) {
				d.Title = "New Title"
				d.Description = "New Desc"
				d.ChartIds = []string{"c2", "c3"}
				d.ChartLayout = &dpb.LayoutSpec{Columns: 4}
			}),
		},
		{
			name:       "partial mask updates only specified fields",
			current:    current,
			updated:    updated,
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{"title", "chartIds", "description", "chartLayout"}},
			want: prototest.Modify(current, func(d *dpb.Dashboard) {
				d.Title = "New Title"
				d.Description = "New Desc"
				d.ChartIds = []string{"c2", "c3"}
				d.ChartLayout = &dpb.LayoutSpec{Columns: 4}
			}),
		},
		{
			name:       "snake_case mask paths",
			current:    current,
			updated:    updated,
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{"chart_layout", "chart_ids"}},
			want: prototest.Modify(current, func(d *dpb.Dashboard) {
				d.ChartIds = []string{"c2", "c3"}
				d.ChartLayout = &dpb.LayoutSpec{Columns: 4}
			}),
		},
		{
			name:       "nil current returns error",
			current:    nil,
			updated:    updated,
			updateMask: nil,
			wantErr:    true,
		},
		{
			name:       "nil updated returns error",
			current:    current,
			updated:    nil,
			updateMask: nil,
			wantErr:    true,
		},
		{
			name:       "unrecognized field returns error",
			current:    current,
			updated:    updated,
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{"owner"}},
			wantErr:    true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := MergeDashboards(tc.current, tc.updated, tc.updateMask)
			if (err != nil) != tc.wantErr {
				t.Fatalf("MergeDashboards() error = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if diff := cmp.Diff(tc.want, got, protocmp.Transform()); diff != "" {
				t.Errorf("MergeDashboards() diff (-want +got):\n%s", diff)
			}
		})
	}
}

func TestPrepareDashboardUpdate(t *testing.T) {
	existing := &dpb.Dashboard{
		Name:        "dashboards/d1",
		Title:       "Title",
		Description: "Desc",
		Owner:       "owner@example.com",
		ChartIds:    []string{"c1", "c2"},
	}

	t.Run("no-op when fields and sorted chart IDs match", func(t *testing.T) {
		updated := prototest.Modify(existing, func(d *dpb.Dashboard) {
			d.ChartIds = []string{"c2", "c1", "c1"}
		})
		plan, err := PrepareDashboardUpdate(existing, updated, nil)
		if err != nil {
			t.Fatalf("PrepareDashboardUpdate() unexpected error: %v", err)
		}
		if plan.Changed {
			t.Errorf("plan.Changed = true, want false")
		}
		if plan.UpdateChartIDs {
			t.Errorf("plan.UpdateChartIDs = true, want false")
		}
		if diff := cmp.Diff([]string{"c1", "c2"}, plan.Merged.GetChartIds()); diff != "" {
			t.Errorf("plan.Merged.ChartIds diff (-want +got):\n%s", diff)
		}
	})

	t.Run("metadata change without chart ID change", func(t *testing.T) {
		updated := &dpb.Dashboard{Title: "Updated Title"}
		mask := &fieldmaskpb.FieldMask{Paths: []string{"title"}}
		plan, err := PrepareDashboardUpdate(existing, updated, mask)
		if err != nil {
			t.Fatalf("PrepareDashboardUpdate() unexpected error: %v", err)
		}
		if !plan.Changed {
			t.Errorf("plan.Changed = false, want true")
		}
		if plan.UpdateChartIDs {
			t.Errorf("plan.UpdateChartIDs = true, want false")
		}
		if plan.Merged.GetTitle() != "Updated Title" {
			t.Errorf("plan.Merged.Title = %q, want %q", plan.Merged.GetTitle(), "Updated Title")
		}
	})

	t.Run("chart IDs change sets UpdateChartIDs", func(t *testing.T) {
		updated := &dpb.Dashboard{ChartIds: []string{"c3", "c1"}}
		mask := &fieldmaskpb.FieldMask{Paths: []string{"chart_ids"}}
		plan, err := PrepareDashboardUpdate(existing, updated, mask)
		if err != nil {
			t.Fatalf("PrepareDashboardUpdate() unexpected error: %v", err)
		}
		if !plan.Changed {
			t.Errorf("plan.Changed = false, want true")
		}
		if !plan.UpdateChartIDs {
			t.Errorf("plan.UpdateChartIDs = false, want true")
		}
		if diff := cmp.Diff([]string{"c1", "c3"}, plan.Merged.GetChartIds()); diff != "" {
			t.Errorf("plan.Merged.ChartIds diff (-want +got):\n%s", diff)
		}
	})

	t.Run("merge error propagates", func(t *testing.T) {
		_, err := PrepareDashboardUpdate(nil, existing, nil)
		if !errors.Is(err, grpcerr.ErrInvalidArgument) {
			t.Errorf("PrepareDashboardUpdate(nil) error = %v, want ErrInvalidArgument", err)
		}
	})
}

func TestMergeCharts(t *testing.T) {
	current := &dpb.Chart{
		Name:        "experiments/100/workUnits/1/charts/c1",
		Title:       "Old Chart",
		Description: "Old Desc",
		Xid:         100,
		Wid:         1,
		Plots:       []*dpb.PlotSpec{{PlotId: "p1", Title: "Plot 1"}},
		PlotLayout:  &dpb.LayoutSpec{Columns: 1},
	}
	updated := &dpb.Chart{
		Title:       "New Chart",
		Description: "New Desc",
		Xid:         999,
		Wid:         999,
		Plots:       []*dpb.PlotSpec{{PlotId: "p2", Title: "Plot 2"}},
		PlotLayout:  &dpb.LayoutSpec{Columns: 3},
	}

	tests := []struct {
		name       string
		current    *dpb.Chart
		updated    *dpb.Chart
		updateMask *fieldmaskpb.FieldMask
		want       *dpb.Chart
		wantErr    bool
	}{
		{
			name:       "nil mask updates all mutable fields while preserving xid and wid",
			current:    current,
			updated:    updated,
			updateMask: nil,
			want: prototest.Modify(current, func(c *dpb.Chart) {
				c.Title = "New Chart"
				c.Description = "New Desc"
				c.Plots = []*dpb.PlotSpec{{PlotId: "p2", Title: "Plot 2"}}
				c.PlotLayout = &dpb.LayoutSpec{Columns: 3}
			}),
		},
		{
			name:       "partial mask updates only specified fields",
			current:    current,
			updated:    updated,
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{"title", "plotLayout", "description", "plots", "plot_layout"}},
			want: prototest.Modify(current, func(c *dpb.Chart) {
				c.Title = "New Chart"
				c.Description = "New Desc"
				c.Plots = []*dpb.PlotSpec{{PlotId: "p2", Title: "Plot 2"}}
				c.PlotLayout = &dpb.LayoutSpec{Columns: 3}
			}),
		},
		{
			name:       "nil current returns error",
			current:    nil,
			updated:    updated,
			updateMask: nil,
			wantErr:    true,
		},
		{
			name:       "nil updated returns error",
			current:    current,
			updated:    nil,
			updateMask: nil,
			wantErr:    true,
		},
		{
			name:       "unrecognized field returns error",
			current:    current,
			updated:    updated,
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{"xid"}},
			wantErr:    true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := MergeCharts(tc.current, tc.updated, tc.updateMask)
			if (err != nil) != tc.wantErr {
				t.Fatalf("MergeCharts() error = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if diff := cmp.Diff(tc.want, got, protocmp.Transform()); diff != "" {
				t.Errorf("MergeCharts() diff (-want +got):\n%s", diff)
			}
		})
	}
}

func TestBuildDashboardChartLinks(t *testing.T) {
	posC1 := &dpb.GridPosition{ItemId: "c1", X: 1, Y: 2, Width: 6, Height: 4}
	posC1Bytes, err := proto.Marshal(posC1)
	if err != nil {
		t.Fatalf("proto.Marshal failed: %v", err)
	}

	t.Run("deduplicates chart IDs and attaches grid positions", func(t *testing.T) {
		dash := &dpb.Dashboard{
			Name:     "dashboards/d1",
			ChartIds: []string{"c1", "c2", "c1"},
			ChartLayout: &dpb.LayoutSpec{
				Positions: []*dpb.GridPosition{
					nil,
					{ItemId: ""},
					posC1,
				},
			},
		}
		links, err := BuildDashboardChartLinks(dash)
		if err != nil {
			t.Fatalf("BuildDashboardChartLinks() unexpected error: %v", err)
		}
		want := []DashboardChartLink{
			{ChartID: "c1", GridPosition: posC1Bytes},
			{ChartID: "c2", GridPosition: nil},
		}
		if diff := cmp.Diff(want, links); diff != "" {
			t.Errorf("BuildDashboardChartLinks() diff (-want +got):\n%s", diff)
		}
	})

	t.Run("nil dashboard returns error", func(t *testing.T) {
		if _, err := BuildDashboardChartLinks(nil); !errors.Is(err, grpcerr.ErrInvalidArgument) {
			t.Errorf("BuildDashboardChartLinks(nil) error = %v, want ErrInvalidArgument", err)
		}
	})

	t.Run("invalid chart ID returns error", func(t *testing.T) {
		dash := &dpb.Dashboard{ChartIds: []string{"bad/id"}}
		if _, err := BuildDashboardChartLinks(dash); !errors.Is(err, grpcerr.ErrInvalidArgument) {
			t.Errorf("BuildDashboardChartLinks(bad/id) error = %v, want ErrInvalidArgument", err)
		}
	})
}

func TestUnmarshalPlotSpec(t *testing.T) {
	plot1 := &dpb.PlotSpec{PlotId: "p1", Title: "Loss"}
	plotBytes, err := proto.Marshal(plot1)
	if err != nil {
		t.Fatalf("proto.Marshal() unexpected error: %v", err)
	}

	unmarshaled, err := UnmarshalPlotSpec("c1", plotBytes)
	if err != nil {
		t.Fatalf("UnmarshalPlotSpec() unexpected error: %v", err)
	}
	if diff := cmp.Diff(plot1, unmarshaled, protocmp.Transform()); diff != "" {
		t.Errorf("UnmarshalPlotSpec() diff (-want +got):\n%s", diff)
	}

	if _, err := UnmarshalPlotSpec("c1", []byte("invalid-proto-wire-bytes-\xff")); err == nil {
		t.Errorf("UnmarshalPlotSpec(invalid) succeeded, want error")
	}
}

func TestBuildListChartsFilter(t *testing.T) {
	tests := []struct {
		name    string
		parent  string
		filter  string
		want    string
		wantErr bool
	}{
		{
			name:   "empty parent and filter",
			parent: "",
			filter: "",
			want:   "",
		},
		{
			name:   "dashboard parent with prefix and empty filter",
			parent: "dashboards/d1",
			filter: "",
			want:   `dashboard_id = "d1" OR dashboard_id = "dashboards/d1"`,
		},
		{
			name:   "bare dashboard ID parent with filter",
			parent: "d1",
			filter: `title = "Loss"`,
			want:   `(dashboard_id = "d1" OR dashboard_id = "dashboards/d1") AND (title = "Loss")`,
		},
		{
			name:   "work unit parent without filter",
			parent: "experiments/100/workUnits/2",
			filter: "",
			want:   "xid = 100 AND wid = 2",
		},
		{
			name:   "empty parent with filter",
			parent: "",
			filter: "xid = 100",
			want:   "xid = 100",
		},
		{
			name:    "experiment-only parent rejected",
			parent:  "experiments/100",
			wantErr: true,
		},
		{
			name:    "invalid dashboard parent rejected",
			parent:  "dashboards/",
			wantErr: true,
		},
		{
			name:    "unsupported parent format rejected",
			parent:  "unknown/parent",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := BuildListChartsFilter(tc.parent, tc.filter)
			if (err != nil) != tc.wantErr {
				t.Fatalf("BuildListChartsFilter(%q, %q) error = %v, wantErr %v", tc.parent, tc.filter, err, tc.wantErr)
			}
			if !tc.wantErr && got != tc.want {
				t.Errorf("BuildListChartsFilter(%q, %q) = %q, want %q", tc.parent, tc.filter, got, tc.want)
			}
		})
	}
}

func TestExtractChartCursor(t *testing.T) {
	tests := []struct {
		name    string
		chart   *dpb.Chart
		want    map[string]any
		wantErr error
	}{
		{
			name:    "nil chart returns ErrInvalidArgument",
			chart:   nil,
			wantErr: grpcerr.ErrInvalidArgument,
		},
		{
			name:    "invalid chart name returns ErrInvalidArgument",
			chart:   &dpb.Chart{Name: "invalid"},
			wantErr: grpcerr.ErrInvalidArgument,
		},
		{
			name:  "valid chart returns chart_id cursor",
			chart: &dpb.Chart{Name: "experiments/100/workUnits/1/charts/c1"},
			want: map[string]any{
				"chart_id": "c1",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ExtractChartCursor(tc.chart)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("ExtractChartCursor() error = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ExtractChartCursor() unexpected error: %v", err)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("ExtractChartCursor() diff (-want +got):\n%s", diff)
			}
		})
	}
}

func TestExtractDashboardCursor(t *testing.T) {
	now := time.Now().Truncate(time.Microsecond).UTC()
	tests := []struct {
		name    string
		dash    *dpb.Dashboard
		want    map[string]any
		wantErr error
	}{
		{
			name:    "nil dashboard returns ErrInvalidArgument",
			dash:    nil,
			wantErr: grpcerr.ErrInvalidArgument,
		},
		{
			name:    "invalid dashboard name returns ErrInvalidArgument",
			dash:    &dpb.Dashboard{Name: "invalid"},
			wantErr: grpcerr.ErrInvalidArgument,
		},
		{
			name: "dashboard with zero timestamps",
			dash: &dpb.Dashboard{Name: "dashboards/d0"},
			want: map[string]any{
				"dashboard_id": "d0",
				"created":      time.Time{},
				"updated":      time.Time{},
			},
		},
		{
			name: "dashboard with timestamps excludes title and owner",
			dash: &dpb.Dashboard{
				Name:       "dashboards/d1",
				Title:      "My Dashboard",
				Owner:      "user@example.com",
				CreateTime: timestamppb.New(now),
				UpdateTime: timestamppb.New(now),
			},
			want: map[string]any{
				"dashboard_id": "d1",
				"created":      now,
				"updated":      now,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ExtractDashboardCursor(tc.dash)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("ExtractDashboardCursor() error = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ExtractDashboardCursor() unexpected error: %v", err)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("ExtractDashboardCursor() diff (-want +got):\n%s", diff)
			}
		})
	}
}

func TestDashboardAndChartAssemblyHelpers(t *testing.T) {
	t.Run("IndexDashboardsByID and AssignDashboardChartIDs", func(t *testing.T) {
		byID, dashIDs, err := IndexDashboardsByID(nil)
		if err != nil || byID != nil || dashIDs != nil {
			t.Fatalf("IndexDashboardsByID(nil) = (%v, %v, %v), want nil", byID, dashIDs, err)
		}

		d1 := &dpb.Dashboard{Name: "dashboards/d1"}
		d2 := &dpb.Dashboard{Name: "dashboards/d2"}
		byID, dashIDs, err = IndexDashboardsByID([]*dpb.Dashboard{nil, d1, d2})
		if err != nil {
			t.Fatalf("IndexDashboardsByID() unexpected error: %v", err)
		}
		if diff := cmp.Diff([]string{"d1", "d2"}, dashIDs); diff != "" {
			t.Errorf("dashIDs diff (-want +got):\n%s", diff)
		}

		AssignDashboardChartIDs(byID, map[string][]string{
			"d1": {"c2", "c1", "c2"},
		})
		if diff := cmp.Diff([]string{"c1", "c2"}, d1.GetChartIds()); diff != "" {
			t.Errorf("d1.ChartIds diff (-want +got):\n%s", diff)
		}
		if len(d2.GetChartIds()) != 0 {
			t.Errorf("d2.ChartIds = %v, want empty", d2.GetChartIds())
		}

		if _, _, err := IndexDashboardsByID([]*dpb.Dashboard{{Name: "bad"}}); err == nil {
			t.Errorf("IndexDashboardsByID(bad) succeeded, want error")
		}
	})

	t.Run("ExtractChartIDs, AssignChartPlots, and BuildChartPlotsFilter", func(t *testing.T) {
		ids, err := ExtractChartIDs(nil)
		if err != nil || ids != nil {
			t.Fatalf("ExtractChartIDs(nil) = (%v, %v), want nil", ids, err)
		}
		if _, err := ExtractChartIDs([]*dpb.Chart{nil}); !errors.Is(err, grpcerr.ErrInvalidArgument) {
			t.Errorf("ExtractChartIDs([nil]) error = %v, want ErrInvalidArgument", err)
		}
		if got := BuildChartPlotsFilter(nil); got != "" {
			t.Errorf("BuildChartPlotsFilter(nil) = %q, want empty", got)
		}

		c1 := &dpb.Chart{Name: "experiments/100/workUnits/1/charts/c1"}
		c2 := &dpb.Chart{Name: "experiments/100/workUnits/1/charts/c2"}
		charts := []*dpb.Chart{c1, c2}
		chartIDs, err := ExtractChartIDs(charts)
		if err != nil {
			t.Fatalf("ExtractChartIDs() unexpected error: %v", err)
		}
		if diff := cmp.Diff([]string{"c1", "c2"}, chartIDs); diff != "" {
			t.Errorf("chartIDs diff (-want +got):\n%s", diff)
		}

		wantFilter := `chart_id = "c1" OR chart_id = "c2"`
		if got := BuildChartPlotsFilter(chartIDs); got != wantFilter {
			t.Errorf("BuildChartPlotsFilter() = %q, want %q", got, wantFilter)
		}

		wantPlots := []*dpb.PlotSpec{{PlotId: "p1"}}
		AssignChartPlots([]*dpb.Chart{c1, nil, c2}, []string{"c1", "ignored", "c2"}, map[string][]*dpb.PlotSpec{
			"c1": wantPlots,
		})
		if diff := cmp.Diff(wantPlots, c1.GetPlots(), protocmp.Transform()); diff != "" {
			t.Errorf("c1.Plots diff (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff([]*dpb.PlotSpec(nil), c2.GetPlots(), protocmp.Transform()); diff != "" {
			t.Errorf("c2.Plots diff (-want +got):\n%s", diff)
		}

		if _, err := ExtractChartIDs([]*dpb.Chart{{Name: "bad"}}); err == nil {
			t.Errorf("ExtractChartIDs(bad) succeeded, want error")
		}
	})
}
