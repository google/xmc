// Copyright 2025 Google LLC
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

	"task/go/status"
)

func TestValidateXidName(t *testing.T) {
	tests := []struct {
		name    string
		xidName string
		want    int64
		wantErr error
	}{
		{
			name:    "valid_xid_name",
			xidName: "xids/123",
			want:    123,
			wantErr: nil,
		},
		{
			name:    "invalid_xid_name_format",
			xidName: "invalid/123",
			want:    0,
			wantErr: status.ErrInvalidArgument,
		},
		{
			name:    "invalid_xid_name_parts",
			xidName: "xids/123/456",
			want:    0,
			wantErr: status.ErrInvalidArgument,
		},
		{
			name:    "invalid_xid_name_xid",
			xidName: "xids/abc",
			want:    0,
			wantErr: status.ErrInvalidArgument,
		},
		{
			name:    "invalid_xid_name_negative_xid",
			xidName: "xids/-123",
			want:    0,
			wantErr: status.ErrInvalidArgument,
		},
		{
			name:    "invalid_xid_name_zero_xid",
			xidName: "xids/0",
			want:    0,
			wantErr: status.ErrInvalidArgument,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ValidateXIDName(tc.xidName)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("ValidateXidName(%q) returned an unexpected error: %v, want %v", tc.xidName, err, tc.wantErr)
			}

			if got != tc.want {
				t.Errorf("ValidateXidName(%q) = %d, want: %d", tc.xidName, got, tc.want)
			}
		})
	}
}

func TestValidateExperimentName(t *testing.T) {
	tests := []struct {
		name    string
		expName string
		want    int64
		wantErr error
	}{
		{
			name:    "valid_experiment_name",
			expName: "experiments/123",
			want:    123,
			wantErr: nil,
		},
		{
			name:    "invalid_experiment_name_format",
			expName: "invalid/123",
			want:    0,
			wantErr: status.ErrInvalidArgument,
		},
		{
			name:    "invalid_experiment_name_parts",
			expName: "experiments/123/456",
			want:    0,
			wantErr: status.ErrInvalidArgument,
		},
		{
			name:    "invalid_experiment_name_xid",
			expName: "experiments/abc",
			want:    0,
			wantErr: status.ErrInvalidArgument,
		},
		{
			name:    "invalid_experiment_name_negative_xid",
			expName: "experiments/-123",
			want:    0,
			wantErr: status.ErrInvalidArgument,
		},
		{
			name:    "invalid_experiment_name_zero_xid",
			expName: "experiments/0",
			want:    0,
			wantErr: status.ErrInvalidArgument,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ValidateExperimentName(tc.expName)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("ValidateExperimentName(%q) returned an unexpected error: %v, want %v", tc.expName, err, tc.wantErr)
			}

			if got != tc.want {
				t.Errorf("ValidateExperimentName(%q) = %d, want: %d", tc.expName, got, tc.want)
			}
		})
	}
}

func TestValidateWorkUnitName(t *testing.T) {
	tests := []struct {
		name    string
		wuName  string
		wantXID int64
		wantWID int64
		wantErr error
	}{
		{
			name:    "valid_work_unit_name",
			wuName:  "experiments/123/workUnits/456",
			wantXID: 123,
			wantWID: 456,
			wantErr: nil,
		},
		{
			name:    "invalid_work_unit_name_format",
			wuName:  "experiments/123/invalid/456",
			wantXID: 0,
			wantWID: 0,
			wantErr: status.ErrInvalidArgument,
		},
		{
			name:    "invalid_work_unit_name_parts",
			wuName:  "experiments/123/workUnits/456/789",
			wantXID: 0,
			wantWID: 0,
			wantErr: status.ErrInvalidArgument,
		},
		{
			name:    "invalid_work_unit_name_xid",
			wuName:  "experiments/abc/workUnits/456",
			wantXID: 0,
			wantWID: 0,
			wantErr: status.ErrInvalidArgument,
		},
		{
			name:    "invalid_work_unit_name_wid",
			wuName:  "experiments/123/workUnits/abc",
			wantXID: 0,
			wantWID: 0,
			wantErr: status.ErrInvalidArgument,
		},
		{
			name:    "invalid_work_unit_name_negative_xid",
			wuName:  "experiments/-123/workUnits/456",
			wantXID: 0,
			wantWID: 0,
			wantErr: status.ErrInvalidArgument,
		},
		{
			name:    "invalid_work_unit_name_zero_xid",
			wuName:  "experiments/0/workUnits/456",
			wantXID: 0,
			wantWID: 0,
			wantErr: status.ErrInvalidArgument,
		},
		{
			name:    "invalid_work_unit_name_negative_wid",
			wuName:  "experiments/123/workUnits/-456",
			wantXID: 0,
			wantWID: 0,
			wantErr: status.ErrInvalidArgument,
		},
		{
			name:    "invalid_work_unit_name_zero_wid",
			wuName:  "experiments/123/workUnits/0",
			wantXID: 0,
			wantWID: 0,
			wantErr: status.ErrInvalidArgument,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotXID, gotWID, err := ValidateWorkUnitName(tc.wuName)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("ValidateWorkUnitName(%q) returned an unexpected error: %v, want %v", tc.wuName, err, tc.wantErr)
			}

			if gotXID != tc.wantXID || gotWID != tc.wantWID {
				t.Errorf("ValidateWorkUnitName(%q) = (%d, %d), want: (%d, %d)", tc.wuName, gotXID, gotWID, tc.wantXID, tc.wantWID)
			}
		})
	}
}

func TestValidateWorkUnitNameAllowZeroWID(t *testing.T) {
	tests := []struct {
		name    string
		wuName  string
		wantXID int64
		wantWID int64
		wantErr error
	}{
		{
			name:    "valid_work_unit_name",
			wuName:  "experiments/123/workUnits/456",
			wantXID: 123,
			wantWID: 456,
			wantErr: nil,
		},
		{
			name:    "valid_work_unit_name_zero_wid",
			wuName:  "experiments/123/workUnits/0",
			wantXID: 123,
			wantWID: 0,
			wantErr: nil,
		},
		{
			name:    "invalid_work_unit_name_format",
			wuName:  "experiments/123/invalid/456",
			wantXID: 0,
			wantWID: 0,
			wantErr: status.ErrInvalidArgument,
		},
		{
			name:    "invalid_work_unit_name_xid",
			wuName:  "experiments/abc/workUnits/456",
			wantXID: 0,
			wantWID: 0,
			wantErr: status.ErrInvalidArgument,
		},
		{
			name:    "invalid_work_unit_name_zero_xid",
			wuName:  "experiments/0/workUnits/456",
			wantXID: 0,
			wantWID: 0,
			wantErr: status.ErrInvalidArgument,
		},
		{
			name:    "invalid_work_unit_name_negative_wid",
			wuName:  "experiments/123/workUnits/-1",
			wantXID: 0,
			wantWID: 0,
			wantErr: status.ErrInvalidArgument,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotXID, gotWID, err := ValidateWorkUnitNameAllowZeroWID(tc.wuName)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("ValidateWorkUnitNameAllowZeroWID(%q) returned an unexpected error: %v, want %v", tc.wuName, err, tc.wantErr)
			}

			if gotXID != tc.wantXID || gotWID != tc.wantWID {
				t.Errorf("ValidateWorkUnitNameAllowZeroWID(%q) = (%d, %d), want: (%d, %d)", tc.wuName, gotXID, gotWID, tc.wantXID, tc.wantWID)
			}
		})
	}
}

func TestValidateWorkUnitExecutionName(t *testing.T) {
	tests := []struct {
		name    string
		wueName string
		wantXID int64
		wantWID int64
		wantEID int64
		wantErr error
	}{
		{
			name:    "valid_work_unit_execution_name",
			wueName: "experiments/123/workUnits/456/executions/789",
			wantXID: 123,
			wantWID: 456,
			wantEID: 789,
			wantErr: nil,
		},
		{
			name:    "invalid_work_unit_execution_name_format",
			wueName: "experiments/123/workUnits/456/invalid/789",
			wantXID: 0,
			wantWID: 0,
			wantEID: 0,
			wantErr: status.ErrInvalidArgument,
		},
		{
			name:    "invalid_work_unit_execution_name_parts",
			wueName: "experiments/123/workUnits/456/executions/789/10",
			wantXID: 0,
			wantWID: 0,
			wantEID: 0,
			wantErr: status.ErrInvalidArgument,
		},
		{
			name:    "invalid_work_unit_execution_name_xid",
			wueName: "experiments/abc/workUnits/456/executions/789",
			wantXID: 0,
			wantWID: 0,
			wantEID: 0,
			wantErr: status.ErrInvalidArgument,
		},
		{
			name:    "invalid_work_unit_execution_name_wid",
			wueName: "experiments/123/workUnits/abc/executions/789",
			wantXID: 0,
			wantWID: 0,
			wantEID: 0,
			wantErr: status.ErrInvalidArgument,
		},
		{
			name:    "invalid_work_unit_execution_name_eid",
			wueName: "experiments/123/workUnits/456/executions/abc",
			wantXID: 0,
			wantWID: 0,
			wantEID: 0,
			wantErr: status.ErrInvalidArgument,
		},
		{
			name:    "invalid_work_unit_execution_name_negative_xid",
			wueName: "experiments/-123/workUnits/456/executions/789",
			wantXID: 0,
			wantWID: 0,
			wantEID: 0,
			wantErr: status.ErrInvalidArgument,
		},
		{
			name:    "invalid_work_unit_execution_name_zero_xid",
			wueName: "experiments/0/workUnits/456/executions/789",
			wantXID: 0,
			wantWID: 0,
			wantEID: 0,
			wantErr: status.ErrInvalidArgument,
		},
		{
			name:    "invalid_work_unit_execution_name_negative_wid",
			wueName: "experiments/123/workUnits/-456/executions/789",
			wantXID: 0,
			wantWID: 0,
			wantEID: 0,
			wantErr: status.ErrInvalidArgument,
		},
		{
			name:    "invalid_work_unit_execution_name_zero_wid",
			wueName: "experiments/123/workUnits/0/executions/789",
			wantXID: 0,
			wantWID: 0,
			wantEID: 0,
			wantErr: status.ErrInvalidArgument,
		},
		{
			name:    "invalid_work_unit_execution_name_negative_eid",
			wueName: "experiments/123/workUnits/456/executions/-789",
			wantXID: 0,
			wantWID: 0,
			wantEID: 0,
			wantErr: status.ErrInvalidArgument,
		},
		{
			name:    "invalid_work_unit_execution_name_zero_eid",
			wueName: "experiments/123/workUnits/456/executions/0",
			wantXID: 0,
			wantWID: 0,
			wantEID: 0,
			wantErr: status.ErrInvalidArgument,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotXID, gotWID, gotEID, err := ValidateWorkUnitExecutionName(tc.wueName)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("ValidateWorkUnitExecutionName(%q) returned an unexpected error: %v, want %v", tc.wueName, err, tc.wantErr)
			}

			if gotXID != tc.wantXID || gotWID != tc.wantWID || gotEID != tc.wantEID {
				t.Errorf("ValidateWorkUnitExecutionName(%q) = (%d, %d, %d), want: (%d, %d, %d)", tc.wueName, gotXID, gotWID, gotEID, tc.wantXID, tc.wantWID, tc.wantEID)
			}
		})
	}
}

func TestValidateStatusMessageName(t *testing.T) {
	tests := []struct {
		name          string
		statusMsgName string
		wantXID       int64
		wantWID       int64
		wantMessageID int64
		wantErr       error
	}{
		{
			name:          "valid_experiment_status_message_name",
			statusMsgName: "experiments/123/statusMessages/789",
			wantXID:       123,
			wantWID:       0,
			wantMessageID: 789,
			wantErr:       nil,
		},
		{
			name:          "valid_work_unit_status_message_name",
			statusMsgName: "experiments/123/workUnits/456/statusMessages/789",
			wantXID:       123,
			wantWID:       456,
			wantMessageID: 789,
			wantErr:       nil,
		},
		{
			name:          "invalid_status_message_name_format",
			statusMsgName: "experiments/123/invalid/789",
			wantXID:       0,
			wantWID:       0,
			wantMessageID: 0,
			wantErr:       status.ErrInvalidArgument,
		},
		{
			name:          "invalid_status_message_name_parts",
			statusMsgName: "experiments/123/statusMessages/789/10",
			wantXID:       0,
			wantWID:       0,
			wantMessageID: 0,
			wantErr:       status.ErrInvalidArgument,
		},
		{
			name:          "invalid_status_message_name_xid",
			statusMsgName: "experiments/abc/statusMessages/789",
			wantXID:       0,
			wantWID:       0,
			wantMessageID: 0,
			wantErr:       status.ErrInvalidArgument,
		},
		{
			name:          "invalid_status_message_name_wid",
			statusMsgName: "experiments/123/workUnits/abc/statusMessages/789",
			wantXID:       0,
			wantWID:       0,
			wantMessageID: 0,
			wantErr:       status.ErrInvalidArgument,
		},
		{
			name:          "invalid_status_message_name_message_id",
			statusMsgName: "experiments/123/statusMessages/abc",
			wantXID:       0,
			wantWID:       0,
			wantMessageID: 0,
			wantErr:       status.ErrInvalidArgument,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotXID, gotWID, gotMessageID, err := ValidateStatusMessageName(tc.statusMsgName)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("ValidateStatusMessageName(%q) returned an unexpected error: %v, want %v", tc.statusMsgName, err, tc.wantErr)
			}

			if gotXID != tc.wantXID || gotWID != tc.wantWID || gotMessageID != tc.wantMessageID {
				t.Errorf("ValidateStatusMessageName(%q) = (%d, %d, %d), want: (%d, %d, %d)", tc.statusMsgName, gotXID, gotWID, gotMessageID, tc.wantXID, tc.wantWID, tc.wantMessageID)
			}
		})
	}
}

func TestConstructXidName(t *testing.T) {
	xid := int64(123)
	want := "xids/123"
	got := ConstructXIDName(xid)
	if got != want {
		t.Errorf("ConstructXidName(%d) = %q, want: %q", xid, got, want)
	}
}

func TestConstructExperimentName(t *testing.T) {
	xid := int64(123)
	want := "experiments/123"
	got := ConstructExperimentName(xid)
	if got != want {
		t.Errorf("ConstructExperimentName(%d) = %q, want: %q", xid, got, want)
	}
}

func TestConstructWorkUnitName(t *testing.T) {
	xid := int64(123)
	wid := int64(456)
	want := "experiments/123/workUnits/456"
	got := ConstructWorkUnitName(xid, wid)
	if got != want {
		t.Errorf("ConstructWorkUnitName(%d, %d) = %q, want: %q", xid, wid, got, want)
	}
}

func TestConstructWorkUnitExecutionName(t *testing.T) {
	xid := int64(123)
	wid := int64(456)
	eid := int64(789)
	want := "experiments/123/workUnits/456/executions/789"
	got := ConstructWorkUnitExecutionName(xid, wid, eid)
	if got != want {
		t.Errorf("ConstructWorkUnitExecutionName(%d, %d, %d) = %q, want: %q", xid, wid, eid, got, want)
	}
}

func TestValidateOperationName(t *testing.T) {
	tests := []struct {
		name     string
		opName   string
		wantXID  int64
		wantWID  int64
		wantUUID string
		wantErr  error
	}{
		{
			name:     "valid_operation_name",
			opName:   "experiments/123/workUnits/456/operations/uuid-123",
			wantXID:  123,
			wantWID:  456,
			wantUUID: "uuid-123",
			wantErr:  nil,
		},
		{
			name:     "invalid_operation_name_format",
			opName:   "experiments/123/workUnits/456/invalid/uuid-123",
			wantXID:  0,
			wantWID:  0,
			wantUUID: "",
			wantErr:  status.ErrInvalidArgument,
		},
		{
			name:     "invalid_operation_name_no_uuid",
			opName:   "experiments/123/workUnits/456/operations/",
			wantXID:  0,
			wantWID:  0,
			wantUUID: "",
			wantErr:  status.ErrInvalidArgument,
		},
		{
			name:     "invalid_operation_name_bad_work_unit",
			opName:   "experiments/123/workUnits/abc/operations/uuid-123",
			wantXID:  0,
			wantWID:  0,
			wantUUID: "",
			wantErr:  status.ErrInvalidArgument,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotXID, gotWID, gotUUID, err := ValidateOperationName(tc.opName)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("ValidateOperationName(%q) returned an unexpected error: %v, want %v", tc.opName, err, tc.wantErr)
			}

			if gotXID != tc.wantXID || gotWID != tc.wantWID || gotUUID != tc.wantUUID {
				t.Errorf("ValidateOperationName(%q) = (%d, %d, %q), want: (%d, %d, %q)", tc.opName, gotXID, gotWID, gotUUID, tc.wantXID, tc.wantWID, tc.wantUUID)
			}
		})
	}
}

func TestValidateArtifactName(t *testing.T) {
	tests := []struct {
		name    string
		artName string
		want    int64
		wantErr error
	}{
		{
			name:    "valid_artifact_name",
			artName: "artifacts/123",
			want:    123,
			wantErr: nil,
		},
		{
			name:    "invalid_artifact_name_format",
			artName: "invalid/123",
			want:    0,
			wantErr: status.ErrInvalidArgument,
		},
		{
			name:    "invalid_artifact_name_parts",
			artName: "artifacts/123/456",
			want:    0,
			wantErr: status.ErrInvalidArgument,
		},
		{
			name:    "invalid_artifact_name_aid",
			artName: "artifacts/abc",
			want:    0,
			wantErr: status.ErrInvalidArgument,
		},
		{
			name:    "invalid_artifact_name_negative_aid",
			artName: "artifacts/-123",
			want:    0,
			wantErr: status.ErrInvalidArgument,
		},
		{
			name:    "invalid_artifact_name_zero_aid",
			artName: "artifacts/0",
			want:    0,
			wantErr: status.ErrInvalidArgument,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ValidateArtifactName(tc.artName)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("ValidateArtifactName(%q) returned an unexpected error: %v, want %v", tc.artName, err, tc.wantErr)
			}

			if got != tc.want {
				t.Errorf("ValidateArtifactName(%q) = %d, want: %d", tc.artName, got, tc.want)
			}
		})
	}
}

func TestValidateArtifactParent(t *testing.T) {
	tests := []struct {
		name       string
		parentName string
		wantXID    int64
		wantWID    int64
		wantErr    error
	}{
		{
			name:       "valid_work_unit_parent",
			parentName: "experiments/123/workUnits/456",
			wantXID:    123,
			wantWID:    456,
			wantErr:    nil,
		},
		{
			name:       "valid_experiment_parent",
			parentName: "experiments/123",
			wantXID:    123,
			wantWID:    0,
			wantErr:    nil,
		},
		{
			name:       "invalid_parent_format",
			parentName: "invalid/123",
			wantXID:    0,
			wantWID:    0,
			wantErr:    status.ErrInvalidArgument,
		},
		{
			name:       "invalid_work_unit_parent_wid",
			parentName: "experiments/123/workUnits/abc",
			wantXID:    0,
			wantWID:    0,
			wantErr:    status.ErrInvalidArgument,
		},
		{
			name:       "invalid_experiment_parent_xid",
			parentName: "experiments/abc",
			wantXID:    0,
			wantWID:    0,
			wantErr:    status.ErrInvalidArgument,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotXID, gotWID, err := ValidateArtifactParent(tc.parentName)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("ValidateArtifactParent(%q) returned an unexpected error: %v, want %v", tc.parentName, err, tc.wantErr)
			}

			if gotXID != tc.wantXID || gotWID != tc.wantWID {
				t.Errorf("ValidateArtifactParent(%q) = (%d, %d), want: (%d, %d)", tc.parentName, gotXID, gotWID, tc.wantXID, tc.wantWID)
			}
		})
	}
}

func TestValidateDashboardName(t *testing.T) {
	tests := []struct {
		name     string
		dashName string
		wantID   string
		wantErr  error
	}{
		{
			name:     "valid_dashboard_name",
			dashName: "dashboards/123",
			wantID:   "123",
			wantErr:  nil,
		},
		{
			name:     "valid_dashboard_name_with_uuid",
			dashName: "dashboards/550e8400-e29b-41d4-a716-446655440000",
			wantID:   "550e8400-e29b-41d4-a716-446655440000",
			wantErr:  nil,
		},
		{
			name:     "invalid_dashboard_name_format",
			dashName: "invalid/123",
			wantID:   "",
			wantErr:  status.ErrInvalidArgument,
		},
		{
			name:     "invalid_dashboard_name_parts",
			dashName: "dashboards/123/456",
			wantID:   "",
			wantErr:  status.ErrInvalidArgument,
		},
		{
			name:     "invalid_dashboard_name_empty_id",
			dashName: "dashboards/",
			wantID:   "",
			wantErr:  status.ErrInvalidArgument,
		},
		{
			name:     "invalid_dashboard_name_empty_string",
			dashName: "",
			wantID:   "",
			wantErr:  status.ErrInvalidArgument,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ValidateDashboardName(tc.dashName)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("ValidateDashboardName(%q) returned an unexpected error: %v, want %v", tc.dashName, err, tc.wantErr)
			}

			if got != tc.wantID {
				t.Errorf("ValidateDashboardName(%q) = %q, want: %q", tc.dashName, got, tc.wantID)
			}
		})
	}
}

func TestConstructDashboardName(t *testing.T) {
	dashID := "123"
	want := "dashboards/123"
	got := ConstructDashboardName(dashID)
	if got != want {
		t.Errorf("ConstructDashboardName(%q) = %q, want: %q", dashID, got, want)
	}
}

func TestGetResourceType(t *testing.T) {
	tests := []struct {
		name     string
		resource string
		want     ResourceType
	}{
		{
			name:     "valid_xid",
			resource: "xids/1",
			want:     XIDResource,
		},
		{
			name:     "valid_experiment",
			resource: "experiments/1",
			want:     ExperimentResource,
		},
		{
			name:     "valid_work_unit",
			resource: "experiments/1/workUnits/1",
			want:     WorkUnitResource,
		},
		{
			name:     "valid_work_unit_execution",
			resource: "experiments/1/workUnits/1/executions/1",
			want:     WorkUnitExecutionResource,
		},
		{
			name:     "valid_artifact",
			resource: "artifacts/1",
			want:     ArtifactResource,
		},
		{
			name:     "valid_operation",
			resource: "experiments/1/workUnits/1/operations/uuid",
			want:     OperationResource,
		},
		{
			name:     "valid_dashboard",
			resource: "dashboards/123",
			want:     DashboardResource,
		},
		{
			name:     "valid_chart_dashboard_pattern",
			resource: "dashboards/123/charts/456",
			want:     ChartResource,
		},
		{
			name:     "valid_chart_root_pattern",
			resource: "charts/456",
			want:     ChartResource,
		},
		{
			name:     "valid_chart_work_unit_pattern",
			resource: "experiments/1001/workUnits/123/charts/456",
			want:     ChartResource,
		},
		{
			name:     "invalid_chart_work_unit_zero_wid",
			resource: "experiments/1001/workUnits/0/charts/456",
			want:     UnknownResource,
		},
		{
			name:     "unknown_resource",
			resource: "invalid/resource",
			want:     UnknownResource,
		},
		{
			name:     "ambiguous_prefix_experiment_workunit",
			resource: "experiments/1/workUnits",
			want:     UnknownResource,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := GetResourceType(tc.resource)
			if got != tc.want {
				t.Errorf("GetResourceType(%q) = %v, want %v", tc.resource, got, tc.want)
			}
		})
	}
}

func TestValidateChartName(t *testing.T) {
	tests := []struct {
		name        string
		chartName   string
		wantXID     int64
		wantWID     int64
		wantChartID string
		wantErr     error
	}{
		{
			name:        "valid_dashboard_chart_name",
			chartName:   "dashboards/dash-123/charts/chart-456",
			wantXID:     0,
			wantWID:     0,
			wantChartID: "chart-456",
			wantErr:     nil,
		},
		{
			name:        "valid_dashboard_chart_name_uuid",
			chartName:   "dashboards/550e8400-e29b-41d4-a716-446655440000/charts/chart-uuid-1",
			wantXID:     0,
			wantWID:     0,
			wantChartID: "chart-uuid-1",
			wantErr:     nil,
		},
		{
			name:        "valid_work_unit_chart_name",
			chartName:   "experiments/1001/workUnits/123/charts/chart-loss",
			wantXID:     1001,
			wantWID:     123,
			wantChartID: "chart-loss",
			wantErr:     nil,
		},
		{
			name:        "invalid_work_unit_chart_name_zero_wid",
			chartName:   "experiments/1001/workUnits/0/charts/chart-loss",
			wantXID:     0,
			wantWID:     0,
			wantChartID: "",
			wantErr:     status.ErrInvalidArgument,
		},
		{
			name:        "valid_root_chart_name",
			chartName:   "charts/chart-loss",
			wantXID:     0,
			wantWID:     0,
			wantChartID: "chart-loss",
			wantErr:     nil,
		},
		{
			name:        "invalid_chart_name_format",
			chartName:   "invalid/123/charts/456",
			wantXID:     0,
			wantWID:     0,
			wantChartID: "",
			wantErr:     status.ErrInvalidArgument,
		},
		{
			name:        "invalid_chart_name_missing_charts_literal",
			chartName:   "dashboards/123/panels/456",
			wantXID:     0,
			wantWID:     0,
			wantChartID: "",
			wantErr:     status.ErrInvalidArgument,
		},
		{
			name:        "invalid_chart_name_empty_dashboard_id",
			chartName:   "dashboards//charts/456",
			wantXID:     0,
			wantWID:     0,
			wantChartID: "",
			wantErr:     status.ErrInvalidArgument,
		},
		{
			name:        "invalid_chart_name_whitespace_dashboard_id",
			chartName:   "dashboards/   /charts/456",
			wantXID:     0,
			wantWID:     0,
			wantChartID: "",
			wantErr:     status.ErrInvalidArgument,
		},
		{
			name:        "invalid_chart_name_empty_chart_id",
			chartName:   "dashboards/123/charts/",
			wantXID:     0,
			wantWID:     0,
			wantChartID: "",
			wantErr:     status.ErrInvalidArgument,
		},
		{
			name:        "invalid_chart_name_whitespace_root_chart_id",
			chartName:   "charts/   ",
			wantXID:     0,
			wantWID:     0,
			wantChartID: "",
			wantErr:     status.ErrInvalidArgument,
		},
		{
			name:        "invalid_chart_name_extra_parts",
			chartName:   "dashboards/123/charts/456/extra",
			wantXID:     0,
			wantWID:     0,
			wantChartID: "",
			wantErr:     status.ErrInvalidArgument,
		},
		{
			name:        "invalid_chart_name_empty_string",
			chartName:   "",
			wantXID:     0,
			wantWID:     0,
			wantChartID: "",
			wantErr:     status.ErrInvalidArgument,
		},
		{
			name:        "invalid_work_unit_chart_name_negative_wid",
			chartName:   "experiments/1001/workUnits/-1/charts/chart-1",
			wantXID:     0,
			wantWID:     0,
			wantChartID: "",
			wantErr:     status.ErrInvalidArgument,
		},
		{
			name:        "invalid_work_unit_chart_name_zero_xid",
			chartName:   "experiments/0/workUnits/1/charts/chart-1",
			wantXID:     0,
			wantWID:     0,
			wantChartID: "",
			wantErr:     status.ErrInvalidArgument,
		},
		{
			name:        "invalid_work_unit_chart_name_zero_xid_and_zero_wid",
			chartName:   "experiments/0/workUnits/0/charts/chart-1",
			wantXID:     0,
			wantWID:     0,
			wantChartID: "",
			wantErr:     status.ErrInvalidArgument,
		},
		{
			name:        "invalid_work_unit_chart_name_whitespace_chart_id",
			chartName:   "experiments/1001/workUnits/1/charts/   ",
			wantXID:     0,
			wantWID:     0,
			wantChartID: "",
			wantErr:     status.ErrInvalidArgument,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotXID, gotWID, gotChartID, err := ValidateChartName(tc.chartName)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("ValidateChartName(%q) returned unexpected error: %v, want %v", tc.chartName, err, tc.wantErr)
			}
			if gotXID != tc.wantXID || gotWID != tc.wantWID || gotChartID != tc.wantChartID {
				t.Errorf("ValidateChartName(%q) = (%d, %d, %q), want (%d, %d, %q)", tc.chartName, gotXID, gotWID, gotChartID, tc.wantXID, tc.wantWID, tc.wantChartID)
			}
		})
	}
}

func TestParseChartName(t *testing.T) {
	tests := []struct {
		name      string
		chartName string
		want      ParsedChartName
		wantErr   error
	}{
		{
			name:      "experiment_chart_without_wid",
			chartName: "experiments/1001/charts/chart-1",
			wantErr:   status.ErrInvalidArgument,
		},
		{
			name:      "work_unit_chart",
			chartName: "experiments/1001/workUnits/2002/charts/chart-1",
			want: ParsedChartName{
				XID:     1001,
				WID:     2002,
				ChartID: "chart-1",
				Parent:  "experiments/1001/workUnits/2002",
			},
		},
		{
			name:      "invalid_zero_wid",
			chartName: "experiments/1001/workUnits/0/charts/chart-1",
			wantErr:   status.ErrInvalidArgument,
		},
		{
			name:      "root_chart",
			chartName: "charts/chart-1",
			want: ParsedChartName{
				ChartID: "chart-1",
			},
		},
		{
			name:      "dashboard_chart",
			chartName: "dashboards/dash-1/charts/chart-1",
			want: ParsedChartName{
				DashboardID: "dash-1",
				ChartID:     "chart-1",
				Parent:      "dashboards/dash-1",
			},
		},
		{
			name:      "invalid_name",
			chartName: "invalid/name",
			wantErr:   status.ErrInvalidArgument,
		},
		{
			name:      "invalid_whitespace_root",
			chartName: "charts/   ",
			wantErr:   status.ErrInvalidArgument,
		},
		{
			name:      "invalid_negative_wid",
			chartName: "experiments/1001/workUnits/-1/charts/chart-1",
			wantErr:   status.ErrInvalidArgument,
		},
		{
			name:      "invalid_zero_xid_and_zero_wid",
			chartName: "experiments/0/workUnits/0/charts/chart-1",
			wantErr:   status.ErrInvalidArgument,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseChartName(tc.chartName)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("ParseChartName(%q) error = %v, wantErr %v", tc.chartName, err, tc.wantErr)
			}
			if tc.wantErr == nil {
				if got != tc.want {
					t.Errorf("ParseChartName(%q) = %+v, want %+v", tc.chartName, got, tc.want)
				}
			}
		})
	}
}

func TestValidateRootChartName(t *testing.T) {
	tests := []struct {
		name        string
		chartName   string
		wantChartID string
		wantErr     error
	}{
		{
			name:        "valid_root_chart",
			chartName:   "charts/chart-1",
			wantChartID: "chart-1",
			wantErr:     nil,
		},
		{
			name:        "valid_root_chart_alphanumeric_and_dashes",
			chartName:   "charts/loss-metric-123",
			wantChartID: "loss-metric-123",
			wantErr:     nil,
		},
		{
			name:        "invalid_empty_chart_id",
			chartName:   "charts/",
			wantChartID: "",
			wantErr:     status.ErrInvalidArgument,
		},
		{
			name:        "invalid_whitespace_chart_id",
			chartName:   "charts/   ",
			wantChartID: "",
			wantErr:     status.ErrInvalidArgument,
		},
		{
			name:        "invalid_wrong_prefix",
			chartName:   "dashboards/chart-1",
			wantChartID: "",
			wantErr:     status.ErrInvalidArgument,
		},
		{
			name:        "invalid_work_unit_hierarchical_path",
			chartName:   "experiments/100/workUnits/1/charts/c1",
			wantChartID: "",
			wantErr:     status.ErrInvalidArgument,
		},
		{
			name:        "invalid_dashboard_hierarchical_path",
			chartName:   "dashboards/d1/charts/c1",
			wantChartID: "",
			wantErr:     status.ErrInvalidArgument,
		},
		{
			name:        "invalid_extra_parts",
			chartName:   "charts/c1/extra",
			wantChartID: "",
			wantErr:     status.ErrInvalidArgument,
		},
		{
			name:        "invalid_missing_prefix",
			chartName:   "c1",
			wantChartID: "",
			wantErr:     status.ErrInvalidArgument,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			chartID, err := ValidateRootChartName(tc.chartName)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("ValidateRootChartName(%q) error = %v, wantErr %v", tc.chartName, err, tc.wantErr)
			}
			if tc.wantErr == nil && chartID != tc.wantChartID {
				t.Errorf("ValidateRootChartName(%q) chartID = %q, want %q", tc.chartName, chartID, tc.wantChartID)
			}
		})
	}
}

func TestConstructChartNames(t *testing.T) {
	t.Run("ConstructDashboardChartName", func(t *testing.T) {
		dashID := "dash-123"
		chartID := "chart-456"
		want := "dashboards/dash-123/charts/chart-456"
		got := ConstructDashboardChartName(dashID, chartID)
		if got != want {
			t.Errorf("ConstructDashboardChartName(%q, %q) = %q, want: %q", dashID, chartID, got, want)
		}
	})

	t.Run("ConstructWorkUnitChartName", func(t *testing.T) {
		want := "experiments/1001/workUnits/2002/charts/chart-456"
		got := ConstructWorkUnitChartName(1001, 2002, "chart-456")
		if got != want {
			t.Errorf("ConstructWorkUnitChartName() = %q, want %q", got, want)
		}
	})

	t.Run("ConstructRootChartName", func(t *testing.T) {
		want := "charts/chart-456"
		got := ConstructRootChartName("chart-456")
		if got != want {
			t.Errorf("ConstructRootChartName() = %q, want %q", got, want)
		}
	})
}
