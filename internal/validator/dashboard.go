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

// Package validator provides request validation for the XManager Cloud services.
package validator

import (
	"fmt"
	"strings"

	spb "xmanager_cloud/api/protos/dashboard"
	"xmanager_cloud/internal/grpcerr"
)

var allowedUpdateDashboardPaths = map[string]bool{
	"title":        true,
	"description":  true,
	"chart_layout": true,
	"chartLayout":  true,
	"chart_ids":    true,
	"chartIds":     true,
}

// ValidateChartID validates that a chart ID string is non-empty and contains no slashes.
func ValidateChartID(s string) (string, error) {
	if strings.TrimSpace(s) == "" {
		return "", fmt.Errorf("chart ID cannot be empty or whitespace: %w", grpcerr.ErrInvalidArgument)
	}
	if strings.Contains(s, "/") {
		return "", fmt.Errorf("chart ID %q cannot contain slashes: %w", s, grpcerr.ErrInvalidArgument)
	}
	return s, nil
}

// ValidateChartIDs validates a slice of chart IDs, ensuring valid format and no duplicates.
func ValidateChartIDs(chartIDs []string) error {
	if len(chartIDs) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(chartIDs))
	for _, id := range chartIDs {
		if _, err := ValidateChartID(id); err != nil {
			return err
		}
		if seen[id] {
			return fmt.Errorf("duplicate chart ID %q: %w", id, grpcerr.ErrInvalidArgument)
		}
		seen[id] = true
	}
	return nil
}

var allowedUpdateChartPaths = map[string]bool{
	"title":       true,
	"description": true,
	"plots":       true,
	"plot_layout": true,
	"plotLayout":  true,
}

// validateDashboardID checks if a dashboard ID string is non-empty and contains no slashes.
func validateDashboardID(id string) error {
	if id == "" {
		return fmt.Errorf("dashboard_id cannot be empty: %w", grpcerr.ErrInvalidArgument)
	}
	if strings.Contains(id, "/") {
		return fmt.Errorf("dashboard_id %q cannot contain slashes: %w", id, grpcerr.ErrInvalidArgument)
	}
	return nil
}

// ValidateCreateDashboardRequest validates the given CreateDashboardRequest.
func ValidateCreateDashboardRequest(in *spb.CreateDashboardRequest) error {
	if in == nil {
		return fmt.Errorf("request cannot be nil: %w", grpcerr.ErrInvalidArgument)
	}
	if in.GetDashboard() == nil {
		return fmt.Errorf("dashboard cannot be nil: %w", grpcerr.ErrInvalidArgument)
	}
	dash := in.GetDashboard()
	if strings.TrimSpace(dash.GetTitle()) == "" {
		return fmt.Errorf("dashboard.title must be set and not only contain whitespace: %w", grpcerr.ErrInvalidArgument)
	}
	if in.GetDashboardId() != "" {
		if err := validateDashboardID(in.GetDashboardId()); err != nil {
			return err
		}
	}
	if dash.GetName() != "" {
		if _, err := ValidateDashboardName(dash.GetName()); err != nil {
			return fmt.Errorf("invalid dashboard.name: %w", err)
		}
	}
	if len(dash.GetChartIds()) > 0 {
		if err := ValidateChartIDs(dash.GetChartIds()); err != nil {
			return err
		}
	}
	return validateInlineCharts(in.GetCharts())
}

// validateInlineCharts validates the charts created alongside a dashboard. Their names and IDs are
// assigned by the server, so only the caller-supplied fields are checked: a title, and the work unit
// coordinates that decide which experiment's ACL governs the write.
func validateInlineCharts(charts []*spb.Chart) error {
	for i, chart := range charts {
		if chart == nil {
			return fmt.Errorf("charts[%d] cannot be nil: %w", i, grpcerr.ErrInvalidArgument)
		}
		if strings.TrimSpace(chart.GetTitle()) == "" {
			return fmt.Errorf("charts[%d].title must be set and not only contain whitespace: %w", i, grpcerr.ErrInvalidArgument)
		}
		if chart.GetXid() <= 0 {
			return fmt.Errorf("charts[%d].xid %d must be a positive experiment ID: %w", i, chart.GetXid(), grpcerr.ErrInvalidArgument)
		}
		if chart.GetWid() <= 0 {
			return fmt.Errorf("charts[%d].wid %d must be a positive work unit ID: %w", i, chart.GetWid(), grpcerr.ErrInvalidArgument)
		}
		if err := ValidateChartPlotTypes(chart.GetPlots(), false); err != nil {
			return fmt.Errorf("charts[%d]: %w", i, err)
		}
	}
	return nil
}

// ValidateGetDashboardRequest validates the given GetDashboardRequest.
func ValidateGetDashboardRequest(in *spb.GetDashboardRequest) error {
	if in == nil {
		return fmt.Errorf("request cannot be nil: %w", grpcerr.ErrInvalidArgument)
	}
	if in.GetName() == "" {
		return fmt.Errorf("dashboard name cannot be empty: %w", grpcerr.ErrInvalidArgument)
	}
	if _, err := ValidateDashboardName(in.GetName()); err != nil {
		return fmt.Errorf("invalid dashboard name: %w", err)
	}
	return nil
}

// ValidateListDashboardsRequest validates the given ListDashboardsRequest.
func ValidateListDashboardsRequest(in *spb.ListDashboardsRequest) error {
	if in == nil {
		return fmt.Errorf("request cannot be nil: %w", grpcerr.ErrInvalidArgument)
	}
	if in.GetPageSize() < 0 {
		return fmt.Errorf("page_size cannot be negative: %w", grpcerr.ErrInvalidArgument)
	}
	return nil
}

// ValidateUpdateDashboardRequest validates updates in UpdateDashboardRequest.
func ValidateUpdateDashboardRequest(in *spb.UpdateDashboardRequest) error {
	if in == nil {
		return fmt.Errorf("request cannot be nil: %w", grpcerr.ErrInvalidArgument)
	}
	if in.GetDashboard() == nil {
		return fmt.Errorf("dashboard cannot be nil: %w", grpcerr.ErrInvalidArgument)
	}
	dash := in.GetDashboard()
	if dash.GetName() == "" {
		return fmt.Errorf("dashboard.name cannot be empty: %w", grpcerr.ErrInvalidArgument)
	}
	if _, err := ValidateDashboardName(dash.GetName()); err != nil {
		return fmt.Errorf("invalid dashboard.name: %w", err)
	}
	if in.GetUpdateMask() != nil && len(in.GetUpdateMask().GetPaths()) > 0 {
		return validateDashboardUpdateMask(in)
	}
	if strings.TrimSpace(dash.GetTitle()) == "" {
		return fmt.Errorf("dashboard title cannot be empty or contain only whitespaces: %w", grpcerr.ErrInvalidArgument)
	}
	if len(dash.GetChartIds()) > 0 {
		if err := ValidateChartIDs(dash.GetChartIds()); err != nil {
			return err
		}
	}
	return nil
}

// validateDashboardUpdateMask verifies update mask paths and associated field constraints.
func validateDashboardUpdateMask(in *spb.UpdateDashboardRequest) error {
	dash := in.GetDashboard()
	for _, path := range in.GetUpdateMask().GetPaths() {
		if !allowedUpdateDashboardPaths[path] {
			return fmt.Errorf("unsupported update_mask path %q, allowed paths are: title, description, chart_layout (chartLayout), chart_ids (chartIds): %w", path, grpcerr.ErrInvalidArgument)
		}
		if path == "title" && strings.TrimSpace(dash.GetTitle()) == "" {
			return fmt.Errorf("dashboard title cannot be empty or contain only whitespaces: %w", grpcerr.ErrInvalidArgument)
		}
		if (path == "chart_ids" || path == "chartIds") && len(dash.GetChartIds()) > 0 {
			if err := ValidateChartIDs(dash.GetChartIds()); err != nil {
				return err
			}
		}
	}
	return nil
}

// ValidateDeleteDashboardRequest validates the given DeleteDashboardRequest.
func ValidateDeleteDashboardRequest(in *spb.DeleteDashboardRequest) error {
	if in == nil {
		return fmt.Errorf("request cannot be nil: %w", grpcerr.ErrInvalidArgument)
	}
	if in.GetName() == "" {
		return fmt.Errorf("dashboard name cannot be empty: %w", grpcerr.ErrInvalidArgument)
	}
	if _, err := ValidateDashboardName(in.GetName()); err != nil {
		return fmt.Errorf("invalid dashboard name: %w", err)
	}
	return nil
}

// ValidateGetChartDataRequest validates the given GetChartDataRequest.
func ValidateGetChartDataRequest(in *spb.GetChartDataRequest) error {
	if in == nil {
		return fmt.Errorf("request cannot be nil: %w", grpcerr.ErrInvalidArgument)
	}
	if in.GetName() == "" {
		return fmt.Errorf("chart name cannot be empty: %w", grpcerr.ErrInvalidArgument)
	}
	if _, _, _, err := ValidateChartName(in.GetName()); err != nil {
		return fmt.Errorf("invalid chart name: %w", err)
	}
	if in.GetChartWidthPixels() < 0 {
		return fmt.Errorf("chart_width_pixels cannot be negative: %w", grpcerr.ErrInvalidArgument)
	}
	if in.GetParentExperiment() != "" {
		if _, err := ValidateExperimentName(in.GetParentExperiment()); err != nil {
			return fmt.Errorf("invalid parent_experiment: %w", err)
		}
	}
	if in.GetParentWorkunit() != "" {
		if _, _, err := ValidateWorkUnitName(in.GetParentWorkunit()); err != nil {
			return fmt.Errorf("invalid parent_workunit: %w", err)
		}
	}
	return nil
}

// ValidateGetChartRequest validates the given GetChartRequest.
func ValidateGetChartRequest(in *spb.GetChartRequest) error {
	if in == nil {
		return fmt.Errorf("request cannot be nil: %w", grpcerr.ErrInvalidArgument)
	}
	if in.GetName() == "" {
		return fmt.Errorf("chart name cannot be empty: %w", grpcerr.ErrInvalidArgument)
	}
	if _, err := ValidateRootChartName(in.GetName()); err != nil {
		return fmt.Errorf("invalid chart name: %w", err)
	}
	return nil
}

// ValidateCreateChartParent extracts and validates the experiment XID and work unit WID of a
// CreateChartRequest parent. The WID is 0 for an experiment-level parent "experiments/{xid}".
func ValidateCreateChartParent(in *spb.CreateChartRequest) (xid int64, wid int64, err error) {
	if in == nil {
		return 0, 0, fmt.Errorf("request cannot be nil: %w", grpcerr.ErrInvalidArgument)
	}
	if in.GetParent() == "" {
		return 0, 0, fmt.Errorf("parent cannot be empty: %w", grpcerr.ErrInvalidArgument)
	}
	expXid, wuWid, err := validateExperimentOrWorkUnitParent(in.GetParent())
	if err != nil {
		return 0, 0, fmt.Errorf("invalid chart parent: %w", err)
	}
	return expXid, wuWid, nil
}

// ValidateCreateChartRequest validates the given CreateChartRequest.
func ValidateCreateChartRequest(in *spb.CreateChartRequest) error {
	if in == nil {
		return fmt.Errorf("request cannot be nil: %w", grpcerr.ErrInvalidArgument)
	}
	if in.GetChart() == nil {
		return fmt.Errorf("chart cannot be nil: %w", grpcerr.ErrInvalidArgument)
	}
	if strings.TrimSpace(in.GetChart().GetTitle()) == "" {
		return fmt.Errorf("chart.title must be set and not only contain whitespace: %w", grpcerr.ErrInvalidArgument)
	}
	if in.GetChartId() != "" {
		if _, err := ValidateChartID(in.GetChartId()); err != nil {
			return err
		}
	}
	expXid, wuWid, err := ValidateCreateChartParent(in)
	if err != nil {
		return err
	}
	if in.GetChart().GetXid() != expXid {
		return fmt.Errorf("chart.xid %d must match parent experiment xid %d: %w", in.GetChart().GetXid(), expXid, grpcerr.ErrInvalidArgument)
	}
	// An experiment-level parent has wid 0, which no work unit has, so its charts must leave wid 0.
	if in.GetChart().GetWid() != wuWid {
		return fmt.Errorf("chart.wid %d must match parent work unit wid %d (0 for an experiment-level chart): %w", in.GetChart().GetWid(), wuWid, grpcerr.ErrInvalidArgument)
	}
	if err := validateChartPlotIDs(in.GetChart().GetPlots()); err != nil {
		return err
	}
	return ValidateChartPlotTypes(in.GetChart().GetPlots(), wuWid == 0)
}

// validateChartPlotIDs checks that every non-nil plot carries a non-whitespace, unique plot_id.
func validateChartPlotIDs(plots []*spb.PlotSpec) error {
	seen := make(map[string]bool, len(plots))
	for i, plot := range plots {
		if plot == nil {
			continue
		}
		id := plot.GetPlotId()
		if strings.TrimSpace(id) == "" {
			return fmt.Errorf("plots[%d].plot_id cannot be empty or whitespace: %w", i, grpcerr.ErrInvalidArgument)
		}
		if seen[id] {
			return fmt.Errorf("duplicate plots[%d].plot_id %q: %w", i, id, grpcerr.ErrInvalidArgument)
		}
		seen[id] = true
	}
	return nil
}

// ValidateChartPlotTypes checks that the plot types fit the level of their chart. A
// PARALLEL_COORDINATES plot spans every work unit of a study, so it is only allowed on
// experiment-level charts, which in turn allow no other plot type: the other plot types read the
// metrics of a single work unit.
func ValidateChartPlotTypes(plots []*spb.PlotSpec, experimentLevel bool) error {
	for i, plot := range plots {
		if plot == nil {
			return fmt.Errorf("plots[%d] cannot be nil: %w", i, grpcerr.ErrInvalidArgument)
		}
		isParallelCoordinates := plot.GetType() == spb.PlotType_PARALLEL_COORDINATES
		if experimentLevel && !isParallelCoordinates {
			return fmt.Errorf("plots[%d].type %s is not allowed on an experiment-level chart, which only allows PARALLEL_COORDINATES: %w", i, plot.GetType(), grpcerr.ErrInvalidArgument)
		}
		if !experimentLevel && isParallelCoordinates {
			return fmt.Errorf("plots[%d].type PARALLEL_COORDINATES is only allowed on experiment-level charts: %w", i, grpcerr.ErrInvalidArgument)
		}
	}
	return nil
}

// ValidateListChartsRequest validates the given ListChartsRequest.
func ValidateListChartsRequest(in *spb.ListChartsRequest) error {
	if in == nil {
		return fmt.Errorf("request cannot be nil: %w", grpcerr.ErrInvalidArgument)
	}
	if in.GetParent() == "" {
		return fmt.Errorf("parent cannot be empty: %w", grpcerr.ErrInvalidArgument)
	}
	if err := ValidateChartParentName(in.GetParent()); err != nil {
		return err
	}
	if in.GetPageSize() < 0 {
		return fmt.Errorf("page_size cannot be negative: %w", grpcerr.ErrInvalidArgument)
	}
	return nil
}

// ParsedChartParent describes a validated ListCharts collection parent. Either DashboardID is
// non-empty (for dashboards/{id}), or XID > 0 (with WID > 0 for work unit parents and WID == 0 for
// experiment parents).
type ParsedChartParent struct {
	DashboardID string
	XID         int64
	WID         int64
}

// ParseChartParent parses and validates a chart collection parent ("dashboards/{dashboard_id}",
// "experiments/{xid}/workUnits/{wid}", or "experiments/{xid}").
func ParseChartParent(parent string) (ParsedChartParent, error) {
	if dashID, err := ValidateDashboardName(parent); err == nil {
		return ParsedChartParent{DashboardID: dashID}, nil
	}
	if xid, wid, err := validateExperimentOrWorkUnitParent(parent); err == nil {
		return ParsedChartParent{XID: xid, WID: wid}, nil
	}
	return ParsedChartParent{}, fmt.Errorf("parent must be in the format 'dashboards/{dashboard_id}', 'experiments/{xid}/workUnits/{wid}', or 'experiments/{xid}': %w", grpcerr.ErrInvalidArgument)
}

// ValidateChartParentName validates a chart collection parent, which may be a dashboard, a work unit,
// or an experiment (for its experiment-level charts).
func ValidateChartParentName(parent string) error {
	_, err := ParseChartParent(parent)
	return err
}

// ValidateUpdateChartRequest validates the given UpdateChartRequest.
func ValidateUpdateChartRequest(in *spb.UpdateChartRequest) error {
	if in == nil {
		return fmt.Errorf("request cannot be nil: %w", grpcerr.ErrInvalidArgument)
	}
	chart := in.GetChart()
	if chart == nil {
		return fmt.Errorf("chart cannot be nil: %w", grpcerr.ErrInvalidArgument)
	}
	if chart.GetName() == "" {
		return fmt.Errorf("chart.name cannot be empty: %w", grpcerr.ErrInvalidArgument)
	}
	if _, err := ValidateRootChartName(chart.GetName()); err != nil {
		return fmt.Errorf("invalid chart.name: %w", err)
	}
	if in.GetUpdateMask() == nil || len(in.GetUpdateMask().GetPaths()) == 0 {
		if strings.TrimSpace(chart.GetTitle()) == "" {
			return fmt.Errorf("chart title cannot be empty or contain only whitespaces: %w", grpcerr.ErrInvalidArgument)
		}
		return nil
	}
	return validateChartUpdateMask(chart.GetTitle(), in.GetUpdateMask().GetPaths())
}

// validateChartUpdateMask verifies update mask paths and associated field constraints for a chart.
func validateChartUpdateMask(title string, paths []string) error {
	for _, path := range paths {
		if !allowedUpdateChartPaths[path] {
			return fmt.Errorf("unsupported update_mask path %q, allowed paths are: title, description, plots, plot_layout: %w", path, grpcerr.ErrInvalidArgument)
		}
		if path == "title" && strings.TrimSpace(title) == "" {
			return fmt.Errorf("chart title cannot be empty or contain only whitespaces: %w", grpcerr.ErrInvalidArgument)
		}
	}
	return nil
}

// ValidateDeleteChartRequest validates the given DeleteChartRequest.
func ValidateDeleteChartRequest(in *spb.DeleteChartRequest) error {
	if in == nil {
		return fmt.Errorf("request cannot be nil: %w", grpcerr.ErrInvalidArgument)
	}
	if in.GetName() == "" {
		return fmt.Errorf("chart name cannot be empty: %w", grpcerr.ErrInvalidArgument)
	}
	if _, err := ValidateRootChartName(in.GetName()); err != nil {
		return fmt.Errorf("invalid chart name: %w", err)
	}
	return nil
}
