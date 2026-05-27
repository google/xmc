// Copyright 2025 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package labels

import (
	"strings"
	"testing"

	"xmanager_cloud/kubernetes/common/constants"
)

func TestParseXMCLabels(t *testing.T) {
	labels := map[string]string{
		constants.ExperimentIDLabel: "123",
		constants.WorkUnitIDLabel:   "456",
		constants.ExecutionIDLabel:  "789",
	}
	wantXID := int64(123)
	wantWID := int64(456)
	wantEID := int64(789)

	gotXID, gotWID, gotEID, err := ParseXMCLabels(labels)
	if err != nil {
		t.Fatalf("ParseXMCLabels() failed with %v", err)
	}

	if gotXID != wantXID || gotWID != wantWID || gotEID != wantEID {
		t.Errorf("ParseXMCLabels() = (%d, %d, %d), want (%d, %d, %d)", gotXID, gotWID, gotEID, wantXID, wantWID, wantEID)
	}
}

func TestParseXMCLabelsErrors(t *testing.T) {
	testCases := []struct {
		name    string
		labels  map[string]string
		wantErr string
	}{
		{
			name:    "Nil labels",
			labels:  nil,
			wantErr: "object is missing required labels",
		},
		{
			name:    "Empty labels",
			labels:  map[string]string{},
			wantErr: "object is missing required labels",
		},
		{
			name: "Missing experiment-id",
			labels: map[string]string{
				constants.WorkUnitIDLabel:  "456",
				constants.ExecutionIDLabel: "789",
			},
			wantErr: "object is missing required labels",
		},
		{
			name: "Missing work-unit-id",
			labels: map[string]string{
				constants.ExperimentIDLabel: "123",
				constants.ExecutionIDLabel:  "789",
			},
			wantErr: "object is missing required labels",
		},
		{
			name: "Missing execution-id",
			labels: map[string]string{
				constants.ExperimentIDLabel: "123",
				constants.WorkUnitIDLabel:   "456",
			},
			wantErr: "object is missing required labels",
		},
		{
			name: "Invalid experiment-id",
			labels: map[string]string{
				constants.ExperimentIDLabel: "invalid",
				constants.WorkUnitIDLabel:   "456",
				constants.ExecutionIDLabel:  "789",
			},
			wantErr: "invalid xmc.google.com/experiment-id label value",
		},
		{
			name: "Invalid work-unit-id",
			labels: map[string]string{
				constants.ExperimentIDLabel: "123",
				constants.WorkUnitIDLabel:   "invalid",
				constants.ExecutionIDLabel:  "789",
			},
			wantErr: "invalid xmc.google.com/work-unit-id label value",
		},
		{
			name: "Invalid execution-id",
			labels: map[string]string{
				constants.ExperimentIDLabel: "123",
				constants.WorkUnitIDLabel:   "456",
				constants.ExecutionIDLabel:  "invalid",
			},
			wantErr: "invalid xmc.google.com/execution-id label value",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			gotXID, gotWID, gotEID, err := ParseXMCLabels(tc.labels)
			if err == nil {
				t.Fatalf("ParseXMCLabels() got nil error, want error containing %v", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("ParseXMCLabels() error = %v, want error containing %v", err, tc.wantErr)
			}

			if gotXID != 0 {
				t.Errorf("ParseXMCLabels() got xid %d, want 0 when error is %v", gotXID, tc.wantErr)
			}

			if gotWID != 0 {
				t.Errorf("ParseXMCLabels() got wid %d, want 0 when error is %v", gotWID, tc.wantErr)
			}

			if gotEID != 0 {
				t.Errorf("ParseXMCLabels() got eid %d, want 0 when error is %v", gotEID, tc.wantErr)
			}
		})
	}
}
