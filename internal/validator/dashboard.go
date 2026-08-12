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
	"charts":       true,
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
	return nil
}

// validateDashboardUpdateMask verifies update mask paths and associated field constraints.
func validateDashboardUpdateMask(in *spb.UpdateDashboardRequest) error {
	dash := in.GetDashboard()
	for _, path := range in.GetUpdateMask().GetPaths() {
		if !allowedUpdateDashboardPaths[path] {
			return fmt.Errorf("unsupported update_mask path %q, allowed paths are: title, description, chart_layout, charts: %w", path, status.ErrInvalidArgument)
		}
		if path == "title" && strings.TrimSpace(dash.GetTitle()) == "" {
			return fmt.Errorf("dashboard title cannot be empty or contain only whitespaces: %w", status.ErrInvalidArgument)
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
