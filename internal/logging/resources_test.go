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
package logging

import (
	"context"
	"log/slog"
	"testing"

	"golang/cmp/cmp"
	"xmanager_cloud/kubernetes/common/constants"
)

func TestWithJobSetName(t *testing.T) {
	tests := []struct {
		name       string
		jobSetName string
		labels     map[string]string
		want       labelsMap
	}{
		{
			name:       "with_labels",
			jobSetName: "xm-job-123-1-456",
			labels: map[string]string{
				constants.ExperimentIDLabel: "123",
				constants.WorkUnitIDLabel:   "1",
				constants.ExecutionIDLabel:  "456",
			},
			want: labelsMap{
				keyXID:    "123",
				keyWID:    "1",
				keyEID:    "456",
				keyJobSet: "xm-job-123-1-456",
			},
		},
		{
			name:       "with_partial_labels",
			jobSetName: "xm-job-123-1",
			labels: map[string]string{
				constants.ExperimentIDLabel: "123",
				constants.WorkUnitIDLabel:   "1",
			},
			want: labelsMap{
				keyXID:    "123",
				keyWID:    "1",
				keyJobSet: "xm-job-123-1",
			},
		},
		{
			name:       "no_labels_valid_name",
			jobSetName: "xm-job-123-1",
			labels:     nil,
			want: labelsMap{
				keyXID:    "123",
				keyWID:    "1",
				keyJobSet: "xm-job-123-1",
			},
		},
		{
			name:       "no_labels_invalid_name",
			jobSetName: "invalid-name",
			labels:     nil,
			want: labelsMap{
				keyJobSet: "invalid-name",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			// WithLogger is needed to avoid panic in Warn call for invalid name case.
			ctx = WithLogger(ctx, slog.Default())

			ctx = WithJobSetName(ctx, tc.jobSetName, tc.labels)
			got := getLabelsFromContext(ctx)

			if diff := cmp.Diff(got, tc.want); diff != "" {
				t.Errorf("WithJobSetName() resulted in wrong labels in context (-got +want):\n%s", diff)
			}
		})
	}
}
