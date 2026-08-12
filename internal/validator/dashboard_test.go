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
			name: "valid_partial_update_all_allowed_fields",
			req: &spb.UpdateDashboardRequest{
				Dashboard: &dpb.Dashboard{
					Name:        "dashboards/dash-123",
					Title:       "Updated Title",
					Description: "Updated Description",
				},
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"title", "description", "chart_layout", "charts"},
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
