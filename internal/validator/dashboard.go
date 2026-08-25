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

	"task/go/status"
	spb "xmanager_cloud/dashboard_service/proto/service_go_proto"
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
		return "", fmt.Errorf("chart ID cannot be empty or whitespace: %w", status.ErrInvalidArgument)
	}
	if strings.Contains(s, "/") {
		return "", fmt.Errorf("chart ID %q cannot contain slashes: %w", s, status.ErrInvalidArgument)
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
			return fmt.Errorf("duplicate chart ID %q: %w", id, status.ErrInvalidArgument)
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
	"wid":         true,
}

// validateDashboardID checks if a dashboard ID string is non-empty and contains no slashes.
func validateDashboardID(id string) error {
	if id == "" {
		return fmt.Errorf("dashboard_id cannot be empty: %w", status.ErrInvalidArgument)
	}
	if strings.Contains(id, "/") {
		return fmt.Errorf("dashboard_id %q cannot contain slashes: %w", id, status.ErrInvalidArgument)
	}
	return nil
}

// ValidateCreateDashboardRequest validates the given CreateDashboardRequest.
func ValidateCreateDashboardRequest(in *spb.CreateDashboardRequest) error {
	if in == nil {
		return fmt.Errorf("request cannot be nil: %w", status.ErrInvalidArgument)
	}
	if in.GetDashboard() == nil {
		return fmt.Errorf("dashboard cannot be nil: %w", status.ErrInvalidArgument)
	}
	dash := in.GetDashboard()
	if strings.TrimSpace(dash.GetTitle()) == "" {
		return fmt.Errorf("dashboard.title must be set and not only contain whitespace: %w", status.ErrInvalidArgument)
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
	return nil
}

// ValidateGetDashboardRequest validates the given GetDashboardRequest.
func ValidateGetDashboardRequest(in *spb.GetDashboardRequest) error {
	if in == nil {
		return fmt.Errorf("request cannot be nil: %w", status.ErrInvalidArgument)
	}
	if in.GetName() == "" {
		return fmt.Errorf("dashboard name cannot be empty: %w", status.ErrInvalidArgument)
	}
	if _, err := ValidateDashboardName(in.GetName()); err != nil {
		return fmt.Errorf("invalid dashboard name: %w", err)
	}
	return nil
}

// ValidateListDashboardsRequest validates the given ListDashboardsRequest.
func ValidateListDashboardsRequest(in *spb.ListDashboardsRequest) error {
	if in == nil {
		return fmt.Errorf("request cannot be nil: %w", status.ErrInvalidArgument)
	}
	if in.GetPageSize() < 0 {
		return fmt.Errorf("page_size cannot be negative: %w", status.ErrInvalidArgument)
	}
	return nil
}

// ValidateUpdateDashboardRequest validates updates in UpdateDashboardRequest.
func ValidateUpdateDashboardRequest(in *spb.UpdateDashboardRequest) error {
	if in == nil {
		return fmt.Errorf("request cannot be nil: %w", status.ErrInvalidArgument)
	}
	if in.GetDashboard() == nil {
		return fmt.Errorf("dashboard cannot be nil: %w", status.ErrInvalidArgument)
	}
	dash := in.GetDashboard()
	if dash.GetName() == "" {
		return fmt.Errorf("dashboard.name cannot be empty: %w", status.ErrInvalidArgument)
	}
	if _, err := ValidateDashboardName(dash.GetName()); err != nil {
		return fmt.Errorf("invalid dashboard.name: %w", err)
	}
	if in.GetUpdateMask() != nil && len(in.GetUpdateMask().GetPaths()) > 0 {
		return validateDashboardUpdateMask(in)
	}
	if strings.TrimSpace(dash.GetTitle()) == "" {
		return fmt.Errorf("dashboard title cannot be empty or contain only whitespaces: %w", status.ErrInvalidArgument)
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
			return fmt.Errorf("unsupported update_mask path %q, allowed paths are: title, description, chart_layout (chartLayout), chart_ids (chartIds): %w", path, status.ErrInvalidArgument)
		}
		if path == "title" && strings.TrimSpace(dash.GetTitle()) == "" {
			return fmt.Errorf("dashboard title cannot be empty or contain only whitespaces: %w", status.ErrInvalidArgument)
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
		return fmt.Errorf("request cannot be nil: %w", status.ErrInvalidArgument)
	}
	if in.GetName() == "" {
		return fmt.Errorf("dashboard name cannot be empty: %w", status.ErrInvalidArgument)
	}
	if _, err := ValidateDashboardName(in.GetName()); err != nil {
		return fmt.Errorf("invalid dashboard name: %w", err)
	}
	return nil
}

// ValidateGetChartDataRequest validates the given GetChartDataRequest.
func ValidateGetChartDataRequest(in *spb.GetChartDataRequest) error {
	if in == nil {
		return fmt.Errorf("request cannot be nil: %w", status.ErrInvalidArgument)
	}
	if in.GetName() == "" {
		return fmt.Errorf("chart name cannot be empty: %w", status.ErrInvalidArgument)
	}
	if _, _, _, err := ValidateChartName(in.GetName()); err != nil {
		return fmt.Errorf("invalid chart name: %w", err)
	}
	if in.GetChartWidthPixels() < 0 {
		return fmt.Errorf("chart_width_pixels cannot be negative: %w", status.ErrInvalidArgument)
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
		return fmt.Errorf("request cannot be nil: %w", status.ErrInvalidArgument)
	}
	if in.GetName() == "" {
		return fmt.Errorf("chart name cannot be empty: %w", status.ErrInvalidArgument)
	}
	if _, err := ValidateRootChartName(in.GetName()); err != nil {
		return fmt.Errorf("invalid chart name: %w", err)
	}
	return nil
}

// ValidateCreateChartParent extracts and validates experiment XID and work unit WID for a CreateChartRequest.
func ValidateCreateChartParent(in *spb.CreateChartRequest) (xid int64, wid int64, err error) {
	if in == nil {
		return 0, 0, fmt.Errorf("request cannot be nil: %w", status.ErrInvalidArgument)
	}
	if in.GetParent() == "" {
		return 0, 0, fmt.Errorf("parent cannot be empty: %w", status.ErrInvalidArgument)
	}
	expXid, wuWid, err := ValidateWorkUnitName(in.GetParent())
	if err != nil {
		return 0, 0, fmt.Errorf("invalid parent work unit: %w", err)
	}
	return expXid, wuWid, nil
}

// ValidateCreateChartRequest validates the given CreateChartRequest.
func ValidateCreateChartRequest(in *spb.CreateChartRequest) error {
	if in == nil {
		return fmt.Errorf("request cannot be nil: %w", status.ErrInvalidArgument)
	}
	if in.GetChart() == nil {
		return fmt.Errorf("chart cannot be nil: %w", status.ErrInvalidArgument)
	}
	if strings.TrimSpace(in.GetChart().GetTitle()) == "" {
		return fmt.Errorf("chart.title must be set and not only contain whitespace: %w", status.ErrInvalidArgument)
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
		return fmt.Errorf("chart.xid %d must match parent experiment xid %d: %w", in.GetChart().GetXid(), expXid, status.ErrInvalidArgument)
	}
	if in.GetChart().GetWid() != wuWid {
		return fmt.Errorf("chart.wid %d must match parent work unit wid %d: %w", in.GetChart().GetWid(), wuWid, status.ErrInvalidArgument)
	}
	return nil
}

// ValidateListChartsRequest validates the given ListChartsRequest.
func ValidateListChartsRequest(in *spb.ListChartsRequest) error {
	if in == nil {
		return fmt.Errorf("request cannot be nil: %w", status.ErrInvalidArgument)
	}
	if in.GetParent() == "" {
		return fmt.Errorf("parent cannot be empty: %w", status.ErrInvalidArgument)
	}
	if _, err := ValidateDashboardName(in.GetParent()); err != nil {
		return fmt.Errorf("invalid parent dashboard name: %w", err)
	}
	if in.GetPageSize() < 0 {
		return fmt.Errorf("page_size cannot be negative: %w", status.ErrInvalidArgument)
	}
	return nil
}

// ValidateUpdateChartRequest validates the given UpdateChartRequest.
func ValidateUpdateChartRequest(in *spb.UpdateChartRequest) error {
	if in == nil {
		return fmt.Errorf("request cannot be nil: %w", status.ErrInvalidArgument)
	}
	chart := in.GetChart()
	if chart == nil {
		return fmt.Errorf("chart cannot be nil: %w", status.ErrInvalidArgument)
	}
	if chart.GetName() == "" {
		return fmt.Errorf("chart.name cannot be empty: %w", status.ErrInvalidArgument)
	}
	if _, err := ValidateRootChartName(chart.GetName()); err != nil {
		return fmt.Errorf("invalid chart.name: %w", err)
	}
	if in.GetUpdateMask() == nil || len(in.GetUpdateMask().GetPaths()) == 0 {
		if strings.TrimSpace(chart.GetTitle()) == "" {
			return fmt.Errorf("chart title cannot be empty or contain only whitespaces: %w", status.ErrInvalidArgument)
		}
		return nil
	}
	return validateChartUpdateMask(chart.GetTitle(), in.GetUpdateMask().GetPaths())
}

// validateChartUpdateMask verifies update mask paths and associated field constraints for a chart.
func validateChartUpdateMask(title string, paths []string) error {
	for _, path := range paths {
		if !allowedUpdateChartPaths[path] {
			return fmt.Errorf("unsupported update_mask path %q, allowed paths are: title, description, plots, plot_layout, wid: %w", path, status.ErrInvalidArgument)
		}
		if path == "title" && strings.TrimSpace(title) == "" {
			return fmt.Errorf("chart title cannot be empty or contain only whitespaces: %w", status.ErrInvalidArgument)
		}
	}
	return nil
}

// ValidateDeleteChartRequest validates the given DeleteChartRequest.
func ValidateDeleteChartRequest(in *spb.DeleteChartRequest) error {
	if in == nil {
		return fmt.Errorf("request cannot be nil: %w", status.ErrInvalidArgument)
	}
	if in.GetName() == "" {
		return fmt.Errorf("chart name cannot be empty: %w", status.ErrInvalidArgument)
	}
	if _, err := ValidateRootChartName(in.GetName()); err != nil {
		return fmt.Errorf("invalid chart name: %w", err)
	}
	return nil
}
