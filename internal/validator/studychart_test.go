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

	spb "xmanager_cloud/api/protos/dashboard"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/prototest"
)

func TestStudyChartID(t *testing.T) {
	if got, want := StudyChartID(123), "study-123-parallel-coordinates"; got != want {
		t.Errorf("StudyChartID(123) = %q, want %q", got, want)
	}
}

func TestValidateStudyChartID(t *testing.T) {
	tests := []struct {
		name    string
		chartID string
		xid     int64
		wid     int64
		wantErr error
	}{
		{name: "own_study_chart_id", chartID: "study-123-parallel-coordinates", xid: 123},
		{name: "unreserved_id", chartID: "my-chart", xid: 123},
		{name: "unreserved_id_on_work_unit", chartID: "my-chart", xid: 123, wid: 4},
		{name: "empty_id", chartID: "", xid: 123},
		{name: "prefix_only_is_not_reserved", chartID: "study-123-parallel-coordinates-v2", xid: 456},
		{name: "missing_digits_is_not_reserved", chartID: "study--parallel-coordinates", xid: 456},
		{name: "negative_number_is_not_reserved", chartID: "study--123-parallel-coordinates", xid: 456},
		{name: "alphanumeric_segment_is_not_reserved", chartID: "study-12a-parallel-coordinates", xid: 456},
		{name: "fullwidth_digits_are_not_reserved", chartID: "study-１２３-parallel-coordinates", xid: 456},
		{name: "multibyte_emoji_is_not_reserved", chartID: "study-📈-parallel-coordinates", xid: 456},
		{name: "foreign_study_chart_id", chartID: "study-123-parallel-coordinates", xid: 456, wantErr: grpcerr.ErrInvalidArgument},
		{name: "own_id_under_work_unit", chartID: "study-123-parallel-coordinates", xid: 123, wid: 4, wantErr: grpcerr.ErrInvalidArgument},
		{name: "leading_zero_single_digit", chartID: "study-01-parallel-coordinates", xid: 1, wantErr: grpcerr.ErrInvalidArgument},
		{name: "leading_zero", chartID: "study-0123-parallel-coordinates", xid: 123, wantErr: grpcerr.ErrInvalidArgument},
		{name: "zero_xid_with_positive_parent", chartID: "study-0-parallel-coordinates", xid: 123, wantErr: grpcerr.ErrInvalidArgument},
		{name: "zero_xid_with_zero_parent", chartID: "study-0-parallel-coordinates", xid: 0, wantErr: grpcerr.ErrInvalidArgument},
		{name: "negative_parent_xid", chartID: "study-123-parallel-coordinates", xid: -123, wantErr: grpcerr.ErrInvalidArgument},
		{name: "overflowing_xid", chartID: "study-99999999999999999999999-parallel-coordinates", xid: 123, wantErr: grpcerr.ErrInvalidArgument},
		{name: "whitespace_padded_own_study_chart_id", chartID: " study-123-parallel-coordinates ", xid: 123, wantErr: grpcerr.ErrInvalidArgument},
		{name: "whitespace_padded_foreign_study_chart_id", chartID: "\tstudy-123-parallel-coordinates\n", xid: 456, wantErr: grpcerr.ErrInvalidArgument},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateStudyChartID(tc.chartID, tc.xid, tc.wid)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("validateStudyChartID(%q, %d, %d) error = %v, want %v", tc.chartID, tc.xid, tc.wid, err, tc.wantErr)
			}
		})
	}
}

func TestValidateCreateChartRequest_StudyChartID(t *testing.T) {
	baseReq := &spb.CreateChartRequest{
		Parent:  "experiments/123",
		ChartId: "study-123-parallel-coordinates",
		Chart: &spb.Chart{
			Title: "Parallel Coordinates",
			Xid:   123,
			Plots: []*spb.PlotSpec{{PlotId: "parallel_coordinates", Type: spb.PlotType_PARALLEL_COORDINATES}},
		},
	}
	modify := func(fn func(*spb.CreateChartRequest)) *spb.CreateChartRequest {
		return prototest.Modify(baseReq, fn)
	}
	wuReq := func(chartID string) *spb.CreateChartRequest {
		return &spb.CreateChartRequest{
			Parent: "experiments/123/workUnits/1", ChartId: chartID,
			Chart: &spb.Chart{Title: "Loss", Xid: 123, Wid: 1},
		}
	}
	tests := []struct {
		name    string
		req     *spb.CreateChartRequest
		wantErr error
	}{
		{name: "valid_matching_experiment_parent", req: baseReq},
		{name: "unrelated_chart_id_on_experiment", req: modify(func(r *spb.CreateChartRequest) { r.ChartId = "custom-parallel-coords" })},
		{name: "unrelated_chart_id_on_work_unit", req: wuReq("loss-chart")},
		{name: "malformed_non_digit_study_id_unaffected", req: modify(func(r *spb.CreateChartRequest) {
			r.Parent, r.Chart.Xid, r.ChartId = "experiments/456", 456, "study-12a-parallel-coordinates"
		})},
		{name: "multibyte_digits_unaffected", req: modify(func(r *spb.CreateChartRequest) {
			r.Parent, r.Chart.Xid, r.ChartId = "experiments/456", 456, "study-１２３-parallel-coordinates"
		})},
		{name: "wrong_experiment_parent", wantErr: grpcerr.ErrInvalidArgument, req: modify(func(r *spb.CreateChartRequest) {
			r.Parent, r.Chart.Xid = "experiments/456", 456
		})},
		{name: "work_unit_parent", req: wuReq("study-123-parallel-coordinates"), wantErr: grpcerr.ErrInvalidArgument},
		{name: "dashboard_parent", wantErr: grpcerr.ErrInvalidArgument, req: modify(func(r *spb.CreateChartRequest) { r.Parent = "dashboards/dash-1" })},
		{name: "empty_parent", wantErr: grpcerr.ErrInvalidArgument, req: modify(func(r *spb.CreateChartRequest) { r.Parent = "" })},
		{name: "leading_zero_in_chart_id", wantErr: grpcerr.ErrInvalidArgument, req: modify(func(r *spb.CreateChartRequest) {
			r.Parent, r.Chart.Xid, r.ChartId = "experiments/1", 1, "study-01-parallel-coordinates"
		})},
		{name: "zero_in_chart_id", wantErr: grpcerr.ErrInvalidArgument, req: modify(func(r *spb.CreateChartRequest) { r.ChartId = "study-0-parallel-coordinates" })},
		{name: "overflowing_number_in_chart_id", wantErr: grpcerr.ErrInvalidArgument, req: modify(func(r *spb.CreateChartRequest) {
			r.ChartId = "study-99999999999999999999999-parallel-coordinates"
		})},
		{name: "whitespace_padded_study_chart_id", wantErr: grpcerr.ErrInvalidArgument, req: modify(func(r *spb.CreateChartRequest) {
			r.ChartId = " study-123-parallel-coordinates "
		})},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateCreateChartRequest(tc.req); !errors.Is(err, tc.wantErr) {
				t.Errorf("ValidateCreateChartRequest() error = %v, want %v", err, tc.wantErr)
			}
		})
	}
}
