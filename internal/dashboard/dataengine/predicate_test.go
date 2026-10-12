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

package dataengine

import (
	"reflect"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	dpb "github.com/google/xmc/api/protos/dashboard"
)

func TestBuildWhereClause(t *testing.T) {
	testTime1 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	testTime2 := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name       string
		xid        int64
		wid        int64
		dataSpec   *dpb.DataSpec
		ranges     []*dpb.DataSpec_DataRange
		wantClause string
		wantArgs   []any
	}{
		{
			name:       "xid only",
			xid:        1001,
			wid:        0,
			dataSpec:   nil,
			ranges:     nil,
			wantClause: "xid = ?",
			wantArgs:   []any{int64(1001)},
		},
		{
			name: "xid and wid",
			xid:  1001,
			wid:  42,
			dataSpec: &dpb.DataSpec{
				Metrics: []string{"train/loss"},
			},
			ranges:     nil,
			wantClause: "xid = ? AND work_unit_id = ? AND metric_name = ?",
			wantArgs:   []any{int64(1001), int64(42), "train/loss"},
		},
		{
			name: "multiple metrics",
			xid:  1001,
			wid:  0,
			dataSpec: &dpb.DataSpec{
				Metrics: []string{"train/loss", "eval/loss"},
			},
			ranges:     nil,
			wantClause: "xid = ? AND metric_name IN (?, ?)",
			wantArgs:   []any{int64(1001), "train/loss", "eval/loss"},
		},
		{
			name: "with step ranges",
			xid:  1001,
			wid:  0,
			dataSpec: &dpb.DataSpec{
				Metrics: []string{"accuracy"},
			},
			ranges: []*dpb.DataSpec_DataRange{
				{
					Start: &dpb.DataSpec_DataRange_StartStep{StartStep: 100},
					End:   &dpb.DataSpec_DataRange_EndStep{EndStep: 500},
				},
			},
			wantClause: "xid = ? AND metric_name = ? AND step >= ? AND step <= ?",
			wantArgs:   []any{int64(1001), "accuracy", int64(100), int64(500)},
		},
		{
			name: "with timestamp ranges",
			xid:  1001,
			wid:  0,
			dataSpec: &dpb.DataSpec{
				Metrics: []string{"accuracy"},
			},
			ranges: []*dpb.DataSpec_DataRange{
				{
					Start: &dpb.DataSpec_DataRange_StartTime{StartTime: timestamppb.New(testTime1)},
					End:   &dpb.DataSpec_DataRange_EndTime{EndTime: timestamppb.New(testTime2)},
				},
			},
			wantClause: "xid = ? AND metric_name = ? AND timestamp >= ? AND timestamp <= ?",
			wantArgs:   []any{int64(1001), "accuracy", testTime1, testTime2},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildWhereClause(tt.xid, tt.wid, tt.dataSpec, tt.ranges)
			if got.whereClause != tt.wantClause {
				t.Errorf("buildWhereClause().whereClause = %q, want %q", got.whereClause, tt.wantClause)
			}
			if !reflect.DeepEqual(got.args, tt.wantArgs) {
				t.Errorf("buildWhereClause().args = %#v, want %#v", got.args, tt.wantArgs)
			}
		})
	}
}
