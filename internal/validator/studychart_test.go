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
	"strings"
	"testing"

	spb "xmanager_cloud/api/protos/dashboard"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/prototest"
)

func TestValidateStudyChartID(t *testing.T) {
	if got, want := StudyChartID(123), "study-123-parallel-coordinates"; got != want {
		t.Fatalf("StudyChartID(123) = %q, want %q", got, want)
	}
	for _, tc := range []struct {
		name, chartID string
		xid, wid      int64
		wantErr       error
	}{
		{name: "own_study_chart_id", chartID: "study-123-parallel-coordinates", xid: 123},
		{name: "unreserved_id", chartID: "my-chart", xid: 123},
		{name: "unreserved_id_on_work_unit", chartID: "my-chart", xid: 123, wid: 4},
		{name: "empty_id", chartID: "", xid: 123},
		{name: "prefix_only_is_not_reserved", chartID: "study-123-parallel-coordinates-v2", xid: 456},
		{name: "suffix_only_is_not_reserved", chartID: "v2-study-123-parallel-coordinates", xid: 456},
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
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateStudyChartID(tc.chartID, tc.xid, tc.wid); !errors.Is(err, tc.wantErr) {
				t.Errorf("validateStudyChartID(%q, %d, %d) error = %v, want %v", tc.chartID, tc.xid, tc.wid, err, tc.wantErr)
			}
		})
	}
}

func TestValidateCreateChartRequest_StudyChartID(t *testing.T) {
	baseReq := &spb.CreateChartRequest{
		Parent: "experiments/123", ChartId: "study-123-parallel-coordinates",
		Chart: &spb.Chart{Title: "Parallel Coordinates", Xid: 123, Plots: []*spb.PlotSpec{{PlotId: "parallel_coordinates", Type: spb.PlotType_PARALLEL_COORDINATES}}},
	}
	withID := func(id string) *spb.CreateChartRequest {
		return prototest.Modify(baseReq, func(r *spb.CreateChartRequest) { r.ChartId = id })
	}
	withExp := func(parent string, xid int64, id string) *spb.CreateChartRequest {
		return prototest.Modify(baseReq, func(r *spb.CreateChartRequest) { r.Parent, r.Chart.Xid, r.ChartId = parent, xid, id })
	}
	wuReq := func(chartID string) *spb.CreateChartRequest {
		return &spb.CreateChartRequest{Parent: "experiments/123/workUnits/1", ChartId: chartID, Chart: &spb.Chart{Title: "Loss", Xid: 123, Wid: 1}}
	}
	for _, tc := range []struct {
		name         string
		req          *spb.CreateChartRequest
		wantReserved bool
	}{
		{name: "valid_matching_experiment_parent", req: baseReq},
		{name: "unrelated_chart_id_on_experiment", req: withID("custom-parallel-coords")},
		{name: "unrelated_chart_id_on_work_unit", req: wuReq("loss-chart")},
		{name: "malformed_non_digit_study_id_unaffected", req: withExp("experiments/456", 456, "study-12a-parallel-coordinates")},
		{name: "multibyte_digits_unaffected", req: withExp("experiments/456", 456, "study-１２３-parallel-coordinates")},
		{name: "wrong_experiment_parent", req: withExp("experiments/456", 456, "study-123-parallel-coordinates"), wantReserved: true},
		{name: "work_unit_parent", req: wuReq("study-123-parallel-coordinates"), wantReserved: true},
		{name: "leading_zero_in_chart_id", req: withExp("experiments/1", 1, "study-01-parallel-coordinates"), wantReserved: true},
		{name: "zero_in_chart_id", req: withID("study-0-parallel-coordinates"), wantReserved: true},
		{name: "overflowing_number_in_chart_id", req: withID("study-99999999999999999999999-parallel-coordinates"), wantReserved: true},
		{name: "whitespace_padded_study_chart_id", req: withID(" study-123-parallel-coordinates "), wantReserved: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateCreateChartRequest(tc.req)
			if !tc.wantReserved {
				if err != nil {
					t.Errorf("ValidateCreateChartRequest() error = %v, want nil", err)
				}
				return
			}
			// The rejection must come from the reservation itself, not from an earlier check.
			if !errors.Is(err, grpcerr.ErrInvalidArgument) || !strings.Contains(err.Error(), "is reserved") {
				t.Errorf("ValidateCreateChartRequest() error = %v, want the reserved chart ID error", err)
			}
		})
	}
}
