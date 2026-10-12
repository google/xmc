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
	"fmt"
	"slices"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"
	fieldmaskpb "google.golang.org/protobuf/types/known/fieldmaskpb"
	dpb "xmanager_cloud/dashboard_service/proto/api_go_proto"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/validator/validator"
)

// DashboardUpdatePlan holds the merged dashboard proto and diff flags for an UpdateDashboard operation.
type DashboardUpdatePlan struct {
	Merged         *dpb.Dashboard
	Changed        bool
	UpdateChartIDs bool
}

// DashboardChartLink represents a validated, deduplicated chart reference and its serialized grid position.
type DashboardChartLink struct {
	ChartID      string
	GridPosition []byte
}

// SortChartIDs returns a new cloned and sorted slice of chart IDs, removing duplicates.
func SortChartIDs(chartIDs []string) []string {
	if len(chartIDs) == 0 {
		return nil
	}
	sorted := slices.Clone(chartIDs)
	slices.Sort(sorted)
	return slices.Compact(sorted)
}

// ChartIDsEqual reports whether two sorted chart ID slices are equal.
func ChartIDsEqual(a, b []string) bool {
	return slices.Equal(a, b)
}

var (
	defaultDashboardMaskPaths = []string{"title", "description", "chart_layout", "chart_ids"}
	defaultChartMaskPaths     = []string{"title", "description", "plots", "plot_layout"}
)

type protoPointer[T any] interface {
	*T
	proto.Message
}

func mergeProtoWithMask[T any, P protoPointer[T]](
	resource string,
	current, updated P,
	updateMask *fieldmaskpb.FieldMask,
	defaultPaths []string,
	applyPath func(dst, src P, path string) error,
) (P, error) {
	if current == nil {
		return nil, fmt.Errorf("current %s is nil: %w", resource, grpcerr.ErrInvalidArgument)
	}
	if updated == nil {
		return nil, fmt.Errorf("updated %s is nil: %w", resource, grpcerr.ErrInvalidArgument)
	}

	merged := proto.Clone(current).(P)
	paths := updateMask.GetPaths()
	if len(paths) == 0 {
		paths = defaultPaths
	}
	for _, path := range paths {
		if err := applyPath(merged, updated, path); err != nil {
			return nil, err
		}
	}
	return merged, nil
}

// MergeDashboards merges an updated Dashboard proto message into a current Dashboard proto message based on an update mask.
func MergeDashboards(current, updated *dpb.Dashboard, updateMask *fieldmaskpb.FieldMask) (*dpb.Dashboard, error) {
	return mergeProtoWithMask("dashboard", current, updated, updateMask, defaultDashboardMaskPaths, applyDashboardMaskPath)
}

func applyDashboardMaskPath(merged, updated *dpb.Dashboard, path string) error {
	switch path {
	case "title":
		merged.Title = updated.GetTitle()
	case "description":
		merged.Description = updated.GetDescription()
	case "chart_layout", "chartLayout":
		merged.ChartLayout = updated.GetChartLayout()
	case "chart_ids", "chartIds":
		merged.ChartIds = updated.GetChartIds()
	default:
		return fmt.Errorf("field %q is not updateable: %w", path, grpcerr.ErrInvalidArgument)
	}
	return nil
}

// PrepareDashboardUpdate merges an updated dashboard with the existing one, canonically sorts chart IDs,
// and determines whether the dashboard or its chart links changed.
func PrepareDashboardUpdate(existing, updated *dpb.Dashboard, updateMask *fieldmaskpb.FieldMask) (DashboardUpdatePlan, error) {
	merged, err := MergeDashboards(existing, updated, updateMask)
	if err != nil {
		return DashboardUpdatePlan{}, err
	}
	merged.ChartIds = SortChartIDs(merged.GetChartIds())
	if proto.Equal(existing, merged) {
		return DashboardUpdatePlan{Merged: merged}, nil
	}
	return DashboardUpdatePlan{
		Merged:         merged,
		Changed:        true,
		UpdateChartIDs: !ChartIDsEqual(existing.GetChartIds(), merged.GetChartIds()),
	}, nil
}

// MergeCharts merges an updated Chart proto message into a current Chart proto message based on an update mask.
// Note: xid and wid are immutable chart ownership fields and cannot be updated.
func MergeCharts(current, updated *dpb.Chart, updateMask *fieldmaskpb.FieldMask) (*dpb.Chart, error) {
	return mergeProtoWithMask("chart", current, updated, updateMask, defaultChartMaskPaths, applyChartMaskPath)
}

func applyChartMaskPath(merged, updated *dpb.Chart, path string) error {
	switch path {
	case "title":
		merged.Title = updated.GetTitle()
	case "description":
		merged.Description = updated.GetDescription()
	case "plots":
		merged.Plots = updated.GetPlots()
	case "plot_layout", "plotLayout":
		merged.PlotLayout = updated.GetPlotLayout()
	default:
		return fmt.Errorf("field %q is not updateable: %w", path, grpcerr.ErrInvalidArgument)
	}
	return nil
}

// BuildPositionsMap encodes grid positions from a layout specification keyed by ItemId.
func BuildPositionsMap(layout *dpb.LayoutSpec) (map[string][]byte, error) {
	if layout == nil {
		return nil, nil
	}
	positionsMap := make(map[string][]byte, len(layout.GetPositions()))
	for _, pos := range layout.GetPositions() {
		if pos == nil || pos.GetItemId() == "" {
			continue
		}
		posBytes, err := proto.Marshal(pos)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal grid position for %q: %w", pos.GetItemId(), err)
		}
		positionsMap[pos.GetItemId()] = posBytes
	}
	return positionsMap, nil
}

// DeduplicateAndValidateChartIDs validates each chart ID and removes duplicates while preserving order.
func DeduplicateAndValidateChartIDs(rawIDs []string) ([]string, error) {
	if len(rawIDs) == 0 {
		return nil, nil
	}
	seen := make(map[string]bool, len(rawIDs))
	chartIDs := make([]string, 0, len(rawIDs))
	for _, rawID := range rawIDs {
		chartID, err := validator.ValidateChartID(rawID)
		if err != nil {
			return nil, fmt.Errorf("invalid chart ID %q: %w", rawID, err)
		}
		if seen[chartID] {
			continue
		}
		seen[chartID] = true
		chartIDs = append(chartIDs, chartID)
	}
	return chartIDs, nil
}

// BuildDashboardChartLinks extracts validated, deduplicated chart links and serialized grid positions from a dashboard.
func BuildDashboardChartLinks(dashboard *dpb.Dashboard) ([]DashboardChartLink, error) {
	if dashboard == nil {
		return nil, fmt.Errorf("dashboard proto must not be nil: %w", grpcerr.ErrInvalidArgument)
	}
	positionsMap, err := BuildPositionsMap(dashboard.GetChartLayout())
	if err != nil {
		return nil, err
	}
	chartIDs, err := DeduplicateAndValidateChartIDs(dashboard.GetChartIds())
	if err != nil {
		return nil, err
	}
	links := make([]DashboardChartLink, 0, len(chartIDs))
	for _, chartID := range chartIDs {
		links = append(links, DashboardChartLink{
			ChartID:      chartID,
			GridPosition: positionsMap[chartID],
		})
	}
	return links, nil
}

// UnmarshalPlotSpec decodes a stored PlotSpec, naming the chart it belongs to when it cannot.
func UnmarshalPlotSpec(chartID string, plotSpecBytes []byte) (*dpb.PlotSpec, error) {
	var plot dpb.PlotSpec
	if err := proto.Unmarshal(plotSpecBytes, &plot); err != nil {
		return nil, fmt.Errorf("failed to unmarshal plot spec for chart %q: %w", chartID, err)
	}
	return &plot, nil
}

// CombineFilters joins a parent filter expression and a user filter expression with AND when both are non-empty.
func CombineFilters(parentFilter, filter string) string {
	if parentFilter == "" {
		return filter
	}
	if filter == "" {
		return parentFilter
	}
	return fmt.Sprintf("(%s) AND (%s)", parentFilter, filter)
}

// BuildChartParentFilter translates a chart collection parent into a filter expression.
// The only two parents ListCharts serves are "dashboards/{dashboard_id}" and "experiments/{xid}/workUnits/{wid}".
func BuildChartParentFilter(parent string) (string, error) {
	if parent == "" {
		return "", nil
	}
	if strings.HasPrefix(parent, "dashboards/") || (!strings.HasPrefix(parent, "experiments/") && !strings.Contains(parent, "/")) {
		trimmed := strings.TrimPrefix(parent, "dashboards/")
		withPrefix := "dashboards/" + trimmed
		return fmt.Sprintf("dashboard_id = %q OR dashboard_id = %q", trimmed, withPrefix), nil
	}
	if strings.HasPrefix(parent, "experiments/") {
		xid, wid, err := validator.ValidateWorkUnitName(parent)
		if err != nil {
			return "", fmt.Errorf("failed to validate work unit parent name: %w", err)
		}
		return fmt.Sprintf("xid = %d AND wid = %d", xid, wid), nil
	}
	return "", fmt.Errorf("unsupported parent format: %q", parent)
}

// BuildListChartsFilter combines a chart collection parent filter with an optional user filter.
func BuildListChartsFilter(parent, filter string) (string, error) {
	parentFilter, err := BuildChartParentFilter(parent)
	if err != nil {
		return "", err
	}
	return CombineFilters(parentFilter, filter), nil
}

// ExtractChartCursor extracts pagination cursor values from a Chart proto.
func ExtractChartCursor(chart *dpb.Chart) (map[string]any, error) {
	if chart == nil {
		return nil, fmt.Errorf("chart cannot be nil")
	}
	_, _, chartID, err := validator.ValidateChartName(chart.GetName())
	if err != nil {
		return nil, fmt.Errorf("failed to extract chart ID from %q: %w", chart.GetName(), err)
	}
	return map[string]any{
		"chart_id": chartID,
	}, nil
}

// ExtractDashboardCursor extracts pagination cursor values from a Dashboard proto.
func ExtractDashboardCursor(dash *dpb.Dashboard) (map[string]any, error) {
	if dash == nil {
		return nil, fmt.Errorf("dashboard must not be nil: %w", grpcerr.ErrInvalidArgument)
	}

	dashID, err := validator.ValidateDashboardName(dash.GetName())
	if err != nil {
		return nil, fmt.Errorf("invalid dashboard name in cursor: %w", err)
	}

	cursor := map[string]any{
		"dashboard_id": dashID,
		"created":      time.Time{},
		"updated":      time.Time{},
	}
	if dash.GetCreateTime() != nil {
		cursor["created"] = dash.GetCreateTime().AsTime()
	}
	if dash.GetUpdateTime() != nil {
		cursor["updated"] = dash.GetUpdateTime().AsTime()
	}
	return cursor, nil
}

// IndexDashboardsByID validates each non-nil dashboard's resource name and returns a map keyed by
// dashboard ID along with the ordered slice of dashboard IDs.
func IndexDashboardsByID(dashboards []*dpb.Dashboard) (map[string]*dpb.Dashboard, []string, error) {
	if len(dashboards) == 0 {
		return nil, nil, nil
	}
	byID := make(map[string]*dpb.Dashboard, len(dashboards))
	dashIDs := make([]string, 0, len(dashboards))
	for _, dash := range dashboards {
		if dash == nil {
			continue
		}
		dashID, err := validator.ValidateDashboardName(dash.GetName())
		if err != nil {
			return nil, nil, fmt.Errorf("invalid dashboard name %q: %w", dash.GetName(), err)
		}
		byID[dashID] = dash
		dashIDs = append(dashIDs, dashID)
	}
	return byID, dashIDs, nil
}

// AssignDashboardChartIDs assigns canonically sorted chart IDs from linked onto the indexed dashboards.
func AssignDashboardChartIDs(byID map[string]*dpb.Dashboard, linked map[string][]string) {
	for dashID, chartIDs := range linked {
		if dash, ok := byID[dashID]; ok {
			dash.ChartIds = SortChartIDs(chartIDs)
		}
	}
}

// ExtractChartIDs validates the resource name of each chart and returns the extracted chart IDs in order.
func ExtractChartIDs(charts []*dpb.Chart) ([]string, error) {
	if len(charts) == 0 {
		return nil, nil
	}
	chartIDs := make([]string, 0, len(charts))
	for _, chart := range charts {
		_, _, chartID, err := validator.ValidateChartName(chart.GetName())
		if err != nil {
			return nil, fmt.Errorf("failed to extract chart ID from %q: %w", chart.GetName(), err)
		}
		chartIDs = append(chartIDs, chartID)
	}
	return chartIDs, nil
}

// AssignChartPlots populates each chart's Plots slice from plotsByChartID using the corresponding chartIDs index.
func AssignChartPlots(charts []*dpb.Chart, chartIDs []string, plotsByChartID map[string][]*dpb.PlotSpec) {
	for i, chart := range charts {
		if i < len(chartIDs) {
			chart.Plots = plotsByChartID[chartIDs[i]]
		}
	}
}

// BuildChartPlotsFilter constructs a disjunction filter expression matching any of the given chart IDs.
func BuildChartPlotsFilter(chartIDs []string) string {
	if len(chartIDs) == 0 {
		return ""
	}
	clauses := make([]string, 0, len(chartIDs))
	for _, chartID := range chartIDs {
		clauses = append(clauses, fmt.Sprintf("chart_id = %q", chartID))
	}
	return strings.Join(clauses, " OR ")
}
