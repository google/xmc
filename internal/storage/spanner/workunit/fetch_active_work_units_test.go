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
)

func TestFormatPriority(t *testing.T) {
	tests := []struct {
		name     string
		prio     int64
		wantText string
	}{
		{
			name:     "High Priority",
			prio:     250,
			wantText: "High",
		},
		{
			name:     "Normal Priority",
			prio:     100,
			wantText: "Normal",
		},
		{
			name:     "Low Priority",
			prio:     10,
			wantText: "Low",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatPriority(tt.prio)
			if got != tt.wantText {
				t.Errorf("FormatPriority(%d) = %q, want %q", tt.prio, got, tt.wantText)
			}
		})
	}
}

func TestFormatTimeInQueue(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name     string
		t        time.Time
		wantText string
	}{
		{
			name:     "Just Now",
			t:        now.Add(-10 * time.Second),
			wantText: "just now",
		},
		{
			name:     "Minutes Ago",
			t:        now.Add(-15 * time.Minute),
			wantText: "15m ago",
		},
		{
			name:     "Hours Ago",
			t:        now.Add(-3 * time.Hour),
			wantText: "3h ago",
		},
		{
			name:     "Days Ago",
			t:        now.Add(-48 * time.Hour),
			wantText: "2d ago",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatTimeInQueue(tt.t)
			if got != tt.wantText {
				t.Errorf("FormatTimeInQueue(%v) = %q, want %q", tt.t, got, tt.wantText)
			}
		})
	}
}

func TestFetchActiveWorkUnitsByQueue_EmptyQueue(t *testing.T) {
	repo := &Repository{}
	_, _, err := repo.FetchActiveWorkUnitsByQueue(context.Background(), "", 10, "")
	if err == nil {
		t.Errorf("FetchActiveWorkUnitsByQueue(\"\") wanted error, got nil")
	}
}
