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

package metricsquery

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

func TestBuildWhereClause(t *testing.T) {
	testTime1 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	testTime2 := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
	step100, step500, step900 := int64(100), int64(500), int64(900)

	tests := []struct {
		name       string
		filter     MetricFilter
		wantClause string
		wantArgs   []any
	}{
		{
			name:       "xid only",
			filter:     MetricFilter{XID: 1001},
			wantClause: "xid = ?",
			wantArgs:   []any{int64(1001)},
		},
		{
			name:       "negative work unit selects all work units",
			filter:     MetricFilter{XID: 1001, WorkUnitID: -1},
			wantClause: "xid = ?",
			wantArgs:   []any{int64(1001)},
		},
		{
			name:       "xid and wid",
			filter:     MetricFilter{XID: 1001, WorkUnitID: 42, MetricNames: []string{"train/loss"}},
			wantClause: "xid = ? AND work_unit_id = ? AND metric_name = ?",
			wantArgs:   []any{int64(1001), int64(42), "train/loss"},
		},
		{
			name:       "multiple metrics",
			filter:     MetricFilter{XID: 1001, MetricNames: []string{"train/loss", "eval/loss"}},
			wantClause: "xid = ? AND metric_name IN (?, ?)",
			wantArgs:   []any{int64(1001), "train/loss", "eval/loss"},
		},
		{
			name: "with step ranges",
			filter: MetricFilter{
				XID: 1001, MetricNames: []string{"accuracy"},
				Ranges: []Range{{StartStep: &step100, EndStep: &step500}},
			},
			wantClause: "xid = ? AND metric_name = ? AND step >= ? AND step <= ?",
			wantArgs:   []any{int64(1001), "accuracy", int64(100), int64(500)},
		},
		{
			name: "with timestamp ranges",
			filter: MetricFilter{
				XID: 1001, MetricNames: []string{"accuracy"},
				Ranges: []Range{{StartTime: &testTime1, EndTime: &testTime2}},
			},
			wantClause: "xid = ? AND metric_name = ? AND timestamp >= ? AND timestamp <= ?",
			wantArgs:   []any{int64(1001), "accuracy", testTime1, testTime2},
		},
		{
			name: "multiple ranges keep order and open bounds",
			filter: MetricFilter{
				XID:    1001,
				Ranges: []Range{{}, {EndStep: &step900}, {StartTime: &testTime1}},
			},
			wantClause: "xid = ? AND step <= ? AND timestamp >= ?",
			wantArgs:   []any{int64(1001), int64(900), testTime1},
		},
		{
			name: "step and time bounds in one range",
			filter: MetricFilter{
				XID:    1001,
				Ranges: []Range{{StartStep: &step100, StartTime: &testTime1, EndStep: &step500, EndTime: &testTime2}},
			},
			wantClause: "xid = ? AND step >= ? AND timestamp >= ? AND step <= ? AND timestamp <= ?",
			wantArgs:   []any{int64(1001), int64(100), testTime1, int64(500), testTime2},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildWhereClause(tt.filter)
			if got.whereClause != tt.wantClause {
				t.Errorf("buildWhereClause().whereClause = %q, want %q", got.whereClause, tt.wantClause)
			}
			if diff := cmp.Diff(tt.wantArgs, got.args); diff != "" {
				t.Errorf("buildWhereClause().args mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
