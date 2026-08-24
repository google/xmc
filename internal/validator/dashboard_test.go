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

package validator

import (
	"errors"
	"testing"

	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"task/go/status"

	dpb "xmanager_cloud/dashboard_service/proto/api_go_proto"
	spb "xmanager_cloud/dashboard_service/proto/service_go_proto"
)

func TestValidateChartID(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantID  string
		wantErr error
	}{
		{
			name:    "valid_raw_uuid",
			input:   "f47ac10b-58cc-4372-a567-0e02b2c3d479",
			wantID:  "f47ac10b-58cc-4372-a567-0e02b2c3d479",
			wantErr: nil,
		},
		{
			name:    "valid_raw_uuid_uppercase_normalized",
			input:   "F47AC10B-58CC-4372-A567-0E02B2C3D479",
			wantID:  "f47ac10b-58cc-4372-a567-0e02b2c3d479",
			wantErr: nil,
		},
		{
			name:    "rejected_root_chart_name",
			input:   "charts/f47ac10b-58cc-4372-a567-0e02b2c3d479",
			wantErr: status.ErrInvalidArgument,
		},
		{
			name:    "rejected_workunit_chart_name",
			input:   "experiments/100/workUnits/2/charts/f47ac10b-58cc-4372-a567-0e02b2c3d479",
			wantErr: status.ErrInvalidArgument,
		},
		{
			name:    "rejected_dashboard_chart_name",
			input:   "dashboards/dash-1/charts/f47ac10b-58cc-4372-a567-0e02b2c3d479",
			wantErr: status.ErrInvalidArgument,
		},
		{
			name:    "empty_string",
			input:   "",
			wantErr: status.ErrInvalidArgument,
		},
		{
			name:    "whitespace_only",
			input:   "   \t\n  ",
			wantErr: status.ErrInvalidArgument,
		},
		{
			name:    "invalid_non_uuid_string",
			input:   "not-a-valid-uuid",
			wantErr: status.ErrInvalidArgument,
		},
		{
			name:    "invalid_uuid_length",
			input:   "f47ac10b-58cc-4372-a567",
			wantErr: status.ErrInvalidArgument,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotID, err := ValidateChartID(tc.input)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("ValidateChartID(%q) error = %v, wantErr %v", tc.input, err, tc.wantErr)
			}
			if tc.wantErr == nil && gotID != tc.wantID {
				t.Errorf("ValidateChartID(%q) = %q, want %q", tc.input, gotID, tc.wantID)
			}
		})
	}
}

func TestValidateChartIDs(t *testing.T) {
	tests := []struct {
		name     string
		chartIDs []string
		wantErr  error
	}{
		{
			name:     "nil_slice",
			chartIDs: nil,
			wantErr:  nil,
		},
		{
			name:     "empty_slice",
			chartIDs: []string{},
			wantErr:  nil,
		},
		{
			name:     "valid_chart_ids",
			chartIDs: []string{"f47ac10b-58cc-4372-a567-0e02b2c3d479", "c9a646d3-9c61-4cd7-bf10-8e96dc638210"},
			wantErr:  nil,
		},
		{
			name:     "rejected_resource_names_in_slice",
			chartIDs: []string{"charts/f47ac10b-58cc-4372-a567-0e02b2c3d479"},
			wantErr:  status.ErrInvalidArgument,
		},
		{
			name:     "rejected_mixed_names_and_ids",
			chartIDs: []string{"f47ac10b-58cc-4372-a567-0e02b2c3d479", "experiments/10/workUnits/1/charts/c9a646d3-9c61-4cd7-bf10-8e96dc638210"},
			wantErr:  status.ErrInvalidArgument,
		},
		{
			name:     "empty_item",
			chartIDs: []string{"f47ac10b-58cc-4372-a567-0e02b2c3d479", ""},
			wantErr:  status.ErrInvalidArgument,
		},
		{
			name:     "whitespace_item",
			chartIDs: []string{"f47ac10b-58cc-4372-a567-0e02b2c3d479", "   "},
			wantErr:  status.ErrInvalidArgument,
		},
		{
			name:     "exact_duplicate_ids",
			chartIDs: []string{"f47ac10b-58cc-4372-a567-0e02b2c3d479", "f47ac10b-58cc-4372-a567-0e02b2c3d479"},
			wantErr:  status.ErrInvalidArgument,
		},
		{
			name:     "case_insensitive_duplicate_ids",
			chartIDs: []string{"f47ac10b-58cc-4372-a567-0e02b2c3d479", "F47AC10B-58CC-4372-A567-0E02B2C3D479"},
			wantErr:  status.ErrInvalidArgument,
		},
		{
			name:     "malformed_item_in_slice",
			chartIDs: []string{"f47ac10b-58cc-4372-a567-0e02b2c3d479", "not-a-uuid"},
			wantErr:  status.ErrInvalidArgument,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateChartIDs(tc.chartIDs)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("ValidateChartIDs() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestValidateCreateDashboardRequest(t *testing.T) {
	tests := []struct {
		name    string
		req     *spb.CreateDashboardRequest
		wantErr error
	}{
		{
			name: "valid_request_minimal",
			req: &spb.CreateDashboardRequest{
				Dashboard: &dpb.Dashboard{
					Title: "Valid Dashboard",
				},
			},
			wantErr: nil,
		},
		{
			name: "valid_request_with_dashboard_id",
			req: &spb.CreateDashboardRequest{
				DashboardId: "my-dash-123",
				Dashboard: &dpb.Dashboard{
					Title: "Valid Dashboard",
				},
			},
			wantErr: nil,
		},
		{
			name: "valid_request_with_valid_name",
			req: &spb.CreateDashboardRequest{
				Dashboard: &dpb.Dashboard{
					Name:  "dashboards/my-dash-123",
					Title: "Valid Dashboard",
				},
			},
			wantErr: nil,
		},
		{
			name: "valid_request_with_chart_ids",
			req: &spb.CreateDashboardRequest{
				Dashboard: &dpb.Dashboard{
					Title:    "Valid Dashboard",
					ChartIds: []string{"f47ac10b-58cc-4372-a567-0e02b2c3d479", "c9a646d3-9c61-4cd7-bf10-8e96dc638210"},
				},
			},
			wantErr: nil,
		},
		{
			name: "invalid_request_duplicate_chart_ids",
			req: &spb.CreateDashboardRequest{
				Dashboard: &dpb.Dashboard{
					Title:    "Valid Dashboard",
					ChartIds: []string{"f47ac10b-58cc-4372-a567-0e02b2c3d479", "f47ac10b-58cc-4372-a567-0e02b2c3d479"},
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "invalid_request_resource_name_in_chart_ids",
			req: &spb.CreateDashboardRequest{
				Dashboard: &dpb.Dashboard{
					Title:    "Valid Dashboard",
					ChartIds: []string{"charts/f47ac10b-58cc-4372-a567-0e02b2c3d479"},
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name:    "nil_request",
			req:     nil,
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "nil_dashboard",
			req: &spb.CreateDashboardRequest{
				Dashboard: nil,
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "empty_title",
			req: &spb.CreateDashboardRequest{
				Dashboard: &dpb.Dashboard{
					Title: "",
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "whitespace_title",
			req: &spb.CreateDashboardRequest{
				Dashboard: &dpb.Dashboard{
					Title: "   \t\n  ",
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "invalid_dashboard_id_with_slashes",
			req: &spb.CreateDashboardRequest{
				DashboardId: "invalid/slash/id",
				Dashboard: &dpb.Dashboard{
					Title: "Valid Dashboard",
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "invalid_dashboard_name_format",
			req: &spb.CreateDashboardRequest{
				Dashboard: &dpb.Dashboard{
					Name:  "invalid-name-format",
					Title: "Valid Dashboard",
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateCreateDashboardRequest(tc.req)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("ValidateCreateDashboardRequest() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestValidateGetDashboardRequest(t *testing.T) {
	tests := []struct {
		name    string
		req     *spb.GetDashboardRequest
		wantErr error
	}{
		{
			name: "valid_request",
			req: &spb.GetDashboardRequest{
				Name: "dashboards/dash-123",
			},
			wantErr: nil,
		},
		{
			name:    "nil_request",
			req:     nil,
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "empty_name",
			req: &spb.GetDashboardRequest{
				Name: "",
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "invalid_name_format",
			req: &spb.GetDashboardRequest{
				Name: "invalid/format/dashboards/123",
			},
			wantErr: status.ErrInvalidArgument,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateGetDashboardRequest(tc.req)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("ValidateGetDashboardRequest() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestValidateListDashboardsRequest(t *testing.T) {
	tests := []struct {
		name    string
		req     *spb.ListDashboardsRequest
		wantErr error
	}{
		{
			name: "valid_request_with_params",
			req: &spb.ListDashboardsRequest{
				PageSize:  10,
				PageToken: "token-123",
				Filter:    "title = 'test'",
			},
			wantErr: nil,
		},
		{
			name:    "valid_empty_request",
			req:     &spb.ListDashboardsRequest{},
			wantErr: nil,
		},
		{
			name:    "nil_request",
			req:     nil,
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "negative_page_size",
			req: &spb.ListDashboardsRequest{
				PageSize: -5,
			},
			wantErr: status.ErrInvalidArgument,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateListDashboardsRequest(tc.req)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("ValidateListDashboardsRequest() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestValidateUpdateDashboardRequest(t *testing.T) {
	tests := []struct {
		name    string
		req     *spb.UpdateDashboardRequest
		wantErr error
	}{
		{
			name: "valid_full_update",
			req: &spb.UpdateDashboardRequest{
				Dashboard: &dpb.Dashboard{
					Name:        "dashboards/dash-123",
					Title:       "Updated Title",
					Description: "Updated Description",
				},
			},
			wantErr: nil,
		},
		{
			name: "valid_partial_update_all_allowed_fields_snake_case",
			req: &spb.UpdateDashboardRequest{
				Dashboard: &dpb.Dashboard{
					Name:        "dashboards/dash-123",
					Title:       "Updated Title",
					Description: "Updated Description",
					ChartIds:    []string{"f47ac10b-58cc-4372-a567-0e02b2c3d479"},
				},
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"title", "description", "chart_layout", "chart_ids"},
				},
			},
			wantErr: nil,
		},
		{
			name: "valid_partial_update_camel_case",
			req: &spb.UpdateDashboardRequest{
				Dashboard: &dpb.Dashboard{
					Name:        "dashboards/dash-123",
					Title:       "Updated Title",
					Description: "Updated Description",
					ChartIds:    []string{"f47ac10b-58cc-4372-a567-0e02b2c3d479"},
				},
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"chartLayout", "chartIds"},
				},
			},
			wantErr: nil,
		},
		{
			name: "valid_partial_update_clear_chart_ids",
			req: &spb.UpdateDashboardRequest{
				Dashboard: &dpb.Dashboard{
					Name:     "dashboards/dash-123",
					ChartIds: []string{},
				},
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"chart_ids"},
				},
			},
			wantErr: nil,
		},
		{
			name: "valid_partial_update_without_title",
			req: &spb.UpdateDashboardRequest{
				Dashboard: &dpb.Dashboard{
					Name:        "dashboards/dash-123",
					Description: "Only description updated",
				},
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"description"},
				},
			},
			wantErr: nil,
		},
		{
			name: "rejected_legacy_charts_field_mask",
			req: &spb.UpdateDashboardRequest{
				Dashboard: &dpb.Dashboard{
					Name:  "dashboards/dash-123",
					Title: "Title",
				},
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"charts"},
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "update_mask_chart_ids_with_duplicate",
			req: &spb.UpdateDashboardRequest{
				Dashboard: &dpb.Dashboard{
					Name:     "dashboards/dash-123",
					ChartIds: []string{"f47ac10b-58cc-4372-a567-0e02b2c3d479", "f47ac10b-58cc-4372-a567-0e02b2c3d479"},
				},
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"chart_ids"},
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "update_mask_chart_ids_with_malformed_id",
			req: &spb.UpdateDashboardRequest{
				Dashboard: &dpb.Dashboard{
					Name:     "dashboards/dash-123",
					ChartIds: []string{"not-a-uuid"},
				},
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"chart_ids"},
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "update_mask_chart_ids_with_resource_name",
			req: &spb.UpdateDashboardRequest{
				Dashboard: &dpb.Dashboard{
					Name:     "dashboards/dash-123",
					ChartIds: []string{"charts/f47ac10b-58cc-4372-a567-0e02b2c3d479"},
				},
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"chart_ids"},
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "full_update_with_duplicate_chart_ids",
			req: &spb.UpdateDashboardRequest{
				Dashboard: &dpb.Dashboard{
					Name:     "dashboards/dash-123",
					Title:    "Valid Title",
					ChartIds: []string{"f47ac10b-58cc-4372-a567-0e02b2c3d479", "f47ac10b-58cc-4372-a567-0e02b2c3d479"},
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "full_update_with_malformed_chart_id",
			req: &spb.UpdateDashboardRequest{
				Dashboard: &dpb.Dashboard{
					Name:     "dashboards/dash-123",
					Title:    "Valid Title",
					ChartIds: []string{"not-a-uuid"},
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "full_update_with_resource_name_chart_id",
			req: &spb.UpdateDashboardRequest{
				Dashboard: &dpb.Dashboard{
					Name:     "dashboards/dash-123",
					Title:    "Valid Title",
					ChartIds: []string{"charts/f47ac10b-58cc-4372-a567-0e02b2c3d479"},
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name:    "nil_request",
			req:     nil,
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "nil_dashboard",
			req: &spb.UpdateDashboardRequest{
				Dashboard: nil,
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "empty_dashboard_name",
			req: &spb.UpdateDashboardRequest{
				Dashboard: &dpb.Dashboard{
					Name:  "",
					Title: "Updated Title",
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "invalid_dashboard_name",
			req: &spb.UpdateDashboardRequest{
				Dashboard: &dpb.Dashboard{
					Name:  "invalid-name",
					Title: "Updated Title",
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "full_update_empty_title",
			req: &spb.UpdateDashboardRequest{
				Dashboard: &dpb.Dashboard{
					Name:  "dashboards/dash-123",
					Title: "",
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "unsupported_update_mask_path",
			req: &spb.UpdateDashboardRequest{
				Dashboard: &dpb.Dashboard{
					Name:  "dashboards/dash-123",
					Title: "Updated Title",
				},
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"owner"},
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "update_mask_title_with_empty_title",
			req: &spb.UpdateDashboardRequest{
				Dashboard: &dpb.Dashboard{
					Name:  "dashboards/dash-123",
					Title: "   ",
				},
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"title"},
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateUpdateDashboardRequest(tc.req)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("ValidateUpdateDashboardRequest() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestValidateDeleteDashboardRequest(t *testing.T) {
	tests := []struct {
		name    string
		req     *spb.DeleteDashboardRequest
		wantErr error
	}{
		{
			name: "valid_request",
			req: &spb.DeleteDashboardRequest{
				Name: "dashboards/dash-123",
			},
			wantErr: nil,
		},
		{
			name:    "nil_request",
			req:     nil,
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "empty_name",
			req: &spb.DeleteDashboardRequest{
				Name: "",
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "invalid_name",
			req: &spb.DeleteDashboardRequest{
				Name: "invalid-name",
			},
			wantErr: status.ErrInvalidArgument,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateDeleteDashboardRequest(tc.req)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("ValidateDeleteDashboardRequest() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestValidateGetChartDataRequest(t *testing.T) {
	tests := []struct {
		name    string
		req     *spb.GetChartDataRequest
		wantErr error
	}{
		{
			name: "valid_request_dashboard_chart",
			req: &spb.GetChartDataRequest{
				Name:             "dashboards/dash-1/charts/chart-1",
				ChartWidthPixels: 800,
			},
			wantErr: nil,
		},
		{
			name: "invalid_request_experiment_chart_without_wid",
			req: &spb.GetChartDataRequest{
				Name:             "experiments/1001/charts/chart-1",
				ChartWidthPixels: 800,
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "valid_request_work_unit_chart",
			req: &spb.GetChartDataRequest{
				Name:             "experiments/1001/workUnits/123/charts/chart-1",
				ChartWidthPixels: 800,
			},
			wantErr: nil,
		},
		{
			name: "valid_request_root_chart",
			req: &spb.GetChartDataRequest{
				Name:             "charts/chart-1",
				ChartWidthPixels: 800,
			},
			wantErr: nil,
		},
		{
			name: "valid_request_with_parents",
			req: &spb.GetChartDataRequest{
				Name:             "charts/chart-1",
				ParentExperiment: "experiments/1001",
				ParentWorkunit:   "experiments/1001/workUnits/123",
				ChartWidthPixels: 800,
			},
			wantErr: nil,
		},
		{
			name: "valid_request_zero_pixels",
			req: &spb.GetChartDataRequest{
				Name:             "dashboards/dash-1/charts/chart-1",
				ChartWidthPixels: 0,
			},
			wantErr: nil,
		},
		{
			name:    "nil_request",
			req:     nil,
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "empty_name",
			req: &spb.GetChartDataRequest{
				Name: "",
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "invalid_chart_name",
			req: &spb.GetChartDataRequest{
				Name: "invalid-name",
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "negative_chart_width_pixels",
			req: &spb.GetChartDataRequest{
				Name:             "dashboards/dash-1/charts/chart-1",
				ChartWidthPixels: -100,
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "invalid_parent_experiment",
			req: &spb.GetChartDataRequest{
				Name:             "charts/chart-1",
				ParentExperiment: "invalid-exp",
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "invalid_parent_workunit",
			req: &spb.GetChartDataRequest{
				Name:           "charts/chart-1",
				ParentWorkunit: "invalid-wu",
			},
			wantErr: status.ErrInvalidArgument,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateGetChartDataRequest(tc.req)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("ValidateGetChartDataRequest() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestValidateGetChartRequest(t *testing.T) {
	tests := []struct {
		name    string
		req     *spb.GetChartRequest
		wantErr error
	}{
		{
			name: "valid_root_chart",
			req: &spb.GetChartRequest{
				Name: "charts/chart-1",
			},
			wantErr: nil,
		},
		{
			name: "valid_root_chart_custom_id",
			req: &spb.GetChartRequest{
				Name: "charts/custom-chart-123",
			},
			wantErr: nil,
		},
		{
			name: "invalid_experiment_chart_request",
			req: &spb.GetChartRequest{
				Name: "experiments/1001/charts/chart-1",
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name:    "nil_request",
			req:     nil,
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "empty_name",
			req: &spb.GetChartRequest{
				Name: "",
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "invalid_dashboard_chart_name",
			req: &spb.GetChartRequest{
				Name: "dashboards/dash-1/charts/chart-1",
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "invalid_work_unit_chart_name",
			req: &spb.GetChartRequest{
				Name: "experiments/1001/workUnits/123/charts/chart-1",
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "invalid_name_dashboard_prefix_only",
			req: &spb.GetChartRequest{
				Name: "dashboards/dash-1",
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "invalid_whitespace_name",
			req: &spb.GetChartRequest{
				Name: "charts/   ",
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "invalid_empty_chart_id",
			req: &spb.GetChartRequest{
				Name: "charts/",
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "invalid_extra_parts",
			req: &spb.GetChartRequest{
				Name: "charts/chart-1/extra",
			},
			wantErr: status.ErrInvalidArgument,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateGetChartRequest(tc.req)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("ValidateGetChartRequest() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestValidateCreateChartParent(t *testing.T) {
	tests := []struct {
		name    string
		req     *spb.CreateChartRequest
		wantXid int64
		wantWid int64
		wantErr error
	}{
		{
			name: "valid_parent_workunit",
			req: &spb.CreateChartRequest{
				Parent: "experiments/100/workUnits/2",
			},
			wantXid: 100,
			wantWid: 2,
			wantErr: nil,
		},
		{
			name: "invalid_parent_zero_wid",
			req: &spb.CreateChartRequest{
				Parent: "experiments/100/workUnits/0",
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "empty_parent",
			req: &spb.CreateChartRequest{
				Parent: "",
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "invalid_parent_dashboard",
			req: &spb.CreateChartRequest{
				Parent: "dashboards/my-dashboard",
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "invalid_parent_non_numeric_wid",
			req: &spb.CreateChartRequest{
				Parent: "experiments/100/workUnits/abc",
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "invalid_parent_format",
			req: &spb.CreateChartRequest{
				Parent: "invalid-parent",
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name:    "nil_request",
			req:     nil,
			wantErr: status.ErrInvalidArgument,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			xid, wid, err := ValidateCreateChartParent(tc.req)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("ValidateCreateChartParent() error = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantErr == nil {
				if xid != tc.wantXid || wid != tc.wantWid {
					t.Errorf("ValidateCreateChartParent() got (xid=%d, wid=%d), want (xid=%d, wid=%d)",
						xid, wid, tc.wantXid, tc.wantWid)
				}
			}
		})
	}
}

func TestValidateCreateChartRequest(t *testing.T) {
	tests := []struct {
		name    string
		req     *spb.CreateChartRequest
		wantErr error
	}{
		{
			name: "valid_request_workunit_parent",
			req: &spb.CreateChartRequest{
				Parent:  "experiments/1/workUnits/2",
				Chart:   &dpb.Chart{Title: "My Chart", Xid: 1, Wid: 2},
				ChartId: "custom-chart-id",
			},
			wantErr: nil,
		},
		{
			name: "valid_request_with_chart_name_ignored",
			req: &spb.CreateChartRequest{
				Parent: "experiments/1/workUnits/2",
				Chart:  &dpb.Chart{Name: "experiments/1/workUnits/2/charts/c1", Title: "My Chart", Xid: 1, Wid: 2},
			},
			wantErr: nil,
		},
		{
			name: "zero_chart_xid",
			req: &spb.CreateChartRequest{
				Parent: "experiments/1/workUnits/2",
				Chart:  &dpb.Chart{Title: "My Chart", Xid: 0, Wid: 2},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "zero_chart_wid",
			req: &spb.CreateChartRequest{
				Parent: "experiments/1/workUnits/2",
				Chart:  &dpb.Chart{Title: "My Chart", Xid: 1, Wid: 0},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "mismatched_chart_xid",
			req: &spb.CreateChartRequest{
				Parent: "experiments/1/workUnits/2",
				Chart:  &dpb.Chart{Title: "My Chart", Xid: 999, Wid: 2},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "mismatched_chart_wid",
			req: &spb.CreateChartRequest{
				Parent: "experiments/1/workUnits/2",
				Chart:  &dpb.Chart{Title: "My Chart", Xid: 1, Wid: 999},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "invalid_chart_id_with_slash",
			req: &spb.CreateChartRequest{
				Parent:  "experiments/1/workUnits/2",
				Chart:   &dpb.Chart{Title: "My Chart", Xid: 1, Wid: 2},
				ChartId: "invalid/id",
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "empty_chart_title",
			req: &spb.CreateChartRequest{
				Parent: "experiments/1/workUnits/2",
				Chart:  &dpb.Chart{Title: "   ", Xid: 1, Wid: 2},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name:    "nil_request",
			req:     nil,
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "empty_parent",
			req: &spb.CreateChartRequest{
				Parent: "",
				Chart:  &dpb.Chart{Title: "My Chart", Xid: 1, Wid: 2},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "invalid_parent",
			req: &spb.CreateChartRequest{
				Parent: "invalid-parent",
				Chart:  &dpb.Chart{Title: "My Chart", Xid: 1, Wid: 2},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "dashboard_parent_rejected",
			req: &spb.CreateChartRequest{
				Parent: "dashboards/dash-1",
				Chart:  &dpb.Chart{Title: "My Chart", Xid: 1, Wid: 2},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "nil_chart",
			req: &spb.CreateChartRequest{
				Parent: "experiments/1/workUnits/2",
				Chart:  nil,
			},
			wantErr: status.ErrInvalidArgument,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateCreateChartRequest(tc.req)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("ValidateCreateChartRequest() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestValidateListChartsRequest(t *testing.T) {
	tests := []struct {
		name    string
		req     *spb.ListChartsRequest
		wantErr error
	}{
		{
			name: "valid_request",
			req: &spb.ListChartsRequest{
				Parent:   "dashboards/dash-1",
				PageSize: 10,
			},
			wantErr: nil,
		},
		{
			name:    "nil_request",
			req:     nil,
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "empty_parent",
			req: &spb.ListChartsRequest{
				Parent: "",
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "invalid_parent",
			req: &spb.ListChartsRequest{
				Parent: "invalid-dash",
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "negative_page_size",
			req: &spb.ListChartsRequest{
				Parent:   "dashboards/dash-1",
				PageSize: -1,
			},
			wantErr: status.ErrInvalidArgument,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateListChartsRequest(tc.req)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("ValidateListChartsRequest() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestValidateUpdateChartRequest(t *testing.T) {
	tests := []struct {
		name    string
		req     *spb.UpdateChartRequest
		wantErr error
	}{
		{
			name: "valid_request",
			req: &spb.UpdateChartRequest{
				Chart: &dpb.Chart{
					Name:  "dashboards/dash-1/charts/chart-1",
					Title: "Updated Chart",
				},
			},
			wantErr: nil,
		},
		{
			name:    "nil_request",
			req:     nil,
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "nil_chart",
			req: &spb.UpdateChartRequest{
				Chart: nil,
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "empty_chart_name",
			req: &spb.UpdateChartRequest{
				Chart: &dpb.Chart{
					Name: "",
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "invalid_chart_name",
			req: &spb.UpdateChartRequest{
				Chart: &dpb.Chart{
					Name: "invalid-name",
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateUpdateChartRequest(tc.req)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("ValidateUpdateChartRequest() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestValidateDeleteChartRequest(t *testing.T) {
	tests := []struct {
		name    string
		req     *spb.DeleteChartRequest
		wantErr error
	}{
		{
			name: "valid_request",
			req: &spb.DeleteChartRequest{
				Name: "dashboards/dash-1/charts/chart-1",
			},
			wantErr: nil,
		},
		{
			name:    "nil_request",
			req:     nil,
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "empty_name",
			req: &spb.DeleteChartRequest{
				Name: "",
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "invalid_name",
			req: &spb.DeleteChartRequest{
				Name: "invalid-name",
			},
			wantErr: status.ErrInvalidArgument,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateDeleteChartRequest(tc.req)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("ValidateDeleteChartRequest() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}
