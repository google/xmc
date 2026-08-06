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

package workunit

import (
	"context"
	"testing"
	"time"

	spb "xmanager_cloud/api/protos/rod"
)

func TestParseWorkUnitState(t *testing.T) {
	tests := []struct {
		name      string
		state     string
		wantState spb.WorkUnitState
	}{
		{
			name:      "Initializing",
			state:     "INITIALIZING",
			wantState: spb.WorkUnitState_INITIALIZING,
		},
		{
			name:      "Scheduling",
			state:     "SCHEDULING",
			wantState: spb.WorkUnitState_SCHEDULING,
		},
		{
			name:      "Running",
			state:     "RUNNING",
			wantState: spb.WorkUnitState_RUNNING,
		},
		{
			name:      "Unknown",
			state:     "UNKNOWN_STATE",
			wantState: spb.WorkUnitState_WORK_UNIT_STATE_UNSPECIFIED,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseWorkUnitState(tt.state)
			if got != tt.wantState {
				t.Errorf("ParseWorkUnitState(%q) = %v, want %v", tt.state, got, tt.wantState)
			}
		})
	}
}

func TestFormatTimeInQueue(t *testing.T) {
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		t    time.Time
		want string
	}{
		{
			name: "Just now",
			t:    now.Add(-30 * time.Second),
			want: "just now",
		},
		{
			name: "Minutes ago",
			t:    now.Add(-15 * time.Minute),
			want: "15m ago",
		},
		{
			name: "Hours ago",
			t:    now.Add(-3 * time.Hour),
			want: "3h ago",
		},
		{
			name: "Days ago",
			t:    now.Add(-48 * time.Hour),
			want: "2d ago",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatTimeInQueue(tt.t, now)
			if got != tt.want {
				t.Errorf("FormatTimeInQueue(%v, %v) = %q, want %q", tt.t, now, got, tt.want)
			}
		})
	}
}

func TestFetchActiveWorkUnitsByQueues_EmptyQueue(t *testing.T) {
	repo := &Repository{}
	jobs, _, err := repo.FetchActiveWorkUnitsByQueues(context.Background(), nil, 10, "")
	if err != nil {
		t.Errorf("FetchActiveWorkUnitsByQueues(nil) expected nil error, got %v", err)
	}
	if len(jobs) != 0 {
		t.Errorf("FetchActiveWorkUnitsByQueues(nil) expected empty jobs slice, got %d", len(jobs))
	}
}
