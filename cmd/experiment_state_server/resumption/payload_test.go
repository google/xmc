// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package resumption

import (
	"testing"

	"golang/cmp/cmp"
)

func TestParseTriggerPayload(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		data    string
		want    Request
		wantErr bool
	}{
		{
			name: "standard valid payload",
			data: `{"resumption_run_id":"run-123","batch_size":50,"max_backlog_threshold":250}`,
			want: Request{
				ResumptionRunID:     "run-123",
				BatchSize:           50,
				MaxBacklogThreshold: 250,
			},
			wantErr: false,
		},
		{
			name: "zero values fallback to defaults",
			data: `{"resumption_run_id":"run-zero","batch_size":0,"max_backlog_threshold":0}`,
			want: Request{
				ResumptionRunID:     "run-zero",
				BatchSize:           defaultBatchSize,
				MaxBacklogThreshold: defaultMaxBacklog,
			},
			wantErr: false,
		},
		{
			name: "negative values fallback to defaults",
			data: `{"resumption_run_id":"run-neg","batch_size":-10,"max_backlog_threshold":-20}`,
			want: Request{
				ResumptionRunID:     "run-neg",
				BatchSize:           defaultBatchSize,
				MaxBacklogThreshold: defaultMaxBacklog,
			},
			wantErr: false,
		},
		{
			name: "omitted batch and backlog fallback to defaults",
			data: `{"resumption_run_id":"run-defaults"}`,
			want: Request{
				ResumptionRunID:     "run-defaults",
				BatchSize:           defaultBatchSize,
				MaxBacklogThreshold: defaultMaxBacklog,
			},
			wantErr: false,
		},
		{
			name:    "empty string fails",
			data:    "",
			wantErr: true,
		},
		{
			name:    "whitespace string fails",
			data:    "   \n\t  ",
			wantErr: true,
		},
		{
			name:    "empty json object fails due to missing run id",
			data:    "{}",
			wantErr: true,
		},
		{
			name:    "invalid json syntax fails",
			data:    `{"resumption_run_id": invalid`,
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := ParseTriggerPayload(tc.data)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseTriggerPayload(%q) expected error, got nil", tc.data)
				}
				return
			}

			if err != nil {
				t.Fatalf("ParseTriggerPayload(%q) unexpected error: %v", tc.data, err)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("ParseTriggerPayload(%q) mismatch (-want +got):\n%s", tc.data, diff)
			}
		})
	}
}
