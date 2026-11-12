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
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/types/known/timestamppb"

	dpb "xmanager_cloud/api/protos/dashboard"
	"xmanager_cloud/pkg/metricsquery"
)

func TestMetricFilter(t *testing.T) {
	testTime1 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	testTime2 := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
	step100, step500 := int64(100), int64(500)

	tests := []struct {
		name     string
		xid      int64
		wid      int64
		dataSpec *dpb.DataSpec
		want     metricsquery.MetricFilter
	}{
		{
			name: "nil data spec",
			xid:  1001,
			want: metricsquery.MetricFilter{XID: 1001},
		},
		{
			name:     "xid, wid, and metrics",
			xid:      1001,
			wid:      42,
			dataSpec: &dpb.DataSpec{Metrics: []string{"train/loss", "eval/loss"}},
			want:     metricsquery.MetricFilter{XID: 1001, WorkUnitID: 42, MetricNames: []string{"train/loss", "eval/loss"}},
		},
		{
			name: "step ranges",
			xid:  1001,
			dataSpec: &dpb.DataSpec{Ranges: []*dpb.DataSpec_DataRange{{
				Start: &dpb.DataSpec_DataRange_StartStep{StartStep: 100},
				End:   &dpb.DataSpec_DataRange_EndStep{EndStep: 500},
			}}},
			want: metricsquery.MetricFilter{XID: 1001, Ranges: []metricsquery.Range{{StartStep: &step100, EndStep: &step500}}},
		},
		{
			name: "timestamp ranges",
			xid:  1001,
			dataSpec: &dpb.DataSpec{Ranges: []*dpb.DataSpec_DataRange{{
				Start: &dpb.DataSpec_DataRange_StartTime{StartTime: timestamppb.New(testTime1)},
				End:   &dpb.DataSpec_DataRange_EndTime{EndTime: timestamppb.New(testTime2)},
			}}},
			want: metricsquery.MetricFilter{XID: 1001, Ranges: []metricsquery.Range{{StartTime: &testTime1, EndTime: &testTime2}}},
		},
		{
			name: "nil ranges skipped and invalid timestamps dropped",
			xid:  1001,
			dataSpec: &dpb.DataSpec{Ranges: []*dpb.DataSpec_DataRange{
				nil,
				{
					Start: &dpb.DataSpec_DataRange_StartTime{StartTime: &timestamppb.Timestamp{Nanos: -1}},
					End:   &dpb.DataSpec_DataRange_EndTime{EndTime: &timestamppb.Timestamp{Nanos: -1}},
				},
				{},
			}},
			want: metricsquery.MetricFilter{XID: 1001, Ranges: []metricsquery.Range{{}, {}}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := metricFilter(tt.xid, tt.wid, tt.dataSpec)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("metricFilter() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
