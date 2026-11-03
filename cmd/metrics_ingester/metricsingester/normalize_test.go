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

package metricsingester

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	pb "xmanager_cloud/api/protos/metrics"
	"xmanager_cloud/internal/prototest"
)

var fixedIngestTime = time.Date(2026, 10, 23, 12, 0, 0, 123_000, time.UTC)

func TestTimestampBoundsConstantsMatchCalendarUTC(t *testing.T) {
	wantMin := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC).UnixMicro()
	wantMax := time.Date(2050, 1, 1, 0, 0, 0, 0, time.UTC).UnixMicro()
	if MinValidTimestampMicros != wantMin {
		t.Errorf("MinValidTimestampMicros = %d, want %d", MinValidTimestampMicros, wantMin)
	}
	if MaxValidTimestampMicros != wantMax {
		t.Errorf("MaxValidTimestampMicros = %d, want %d", MaxValidTimestampMicros, wantMax)
	}
}

func TestNormalizeMetrics(t *testing.T) {
	baseMetric := testMetric()
	nowMicros := fixedIngestTime.UnixMicro()
	baseRow := NormalizedMetric{
		Xid:             1,
		WorkUnitID:      2,
		ExecutionID:     3,
		JobName:         "trainer",
		ReplicaID:       "0",
		MetricName:      "loss",
		MetricValue:     0.5,
		Step:            10,
		TimestampMicros: 1_780_000_000_000_000,
		CreatedAtMicros: nowMicros,
	}

	tests := []struct {
		name  string
		input []*pb.Metric
		want  []NormalizedMetric
	}{
		{
			name:  "empty slice returns nil",
			input: nil,
			want:  nil,
		},
		{
			name:  "valid metric preserved verbatim",
			input: []*pb.Metric{prototest.Modify(baseMetric, func(m *pb.Metric) { m.ReplicaId = "worker-3" })},
			want: []NormalizedMetric{
				func() NormalizedMetric { r := baseRow; r.ReplicaID = "worker-3"; return r }(),
			},
		},
		{
			name: "filters xid=0, execution_id=0, empty metric_name, and nil entries",
			input: []*pb.Metric{
				nil,
				prototest.Modify(baseMetric, func(m *pb.Metric) { m.Xid = 0 }),
				prototest.Modify(baseMetric, func(m *pb.Metric) { m.ExecutionId = 0 }),
				prototest.Modify(baseMetric, func(m *pb.Metric) { m.MetricName = "" }),
			},
			want: []NormalizedMetric{},
		},
		{
			name: "retains zero work_unit_id, empty job_name, zero step, zero value, and defaults empty replica_id",
			input: []*pb.Metric{
				prototest.Modify(baseMetric, func(m *pb.Metric) {
					m.WorkUnitId = 0
					m.JobName = ""
					m.ReplicaId = ""
					m.MetricValue = 0
					m.Step = 0
				}),
			},
			want: []NormalizedMetric{
				{
					Xid:             1,
					WorkUnitID:      0,
					ExecutionID:     3,
					JobName:         "",
					ReplicaID:       "0",
					MetricName:      "loss",
					MetricValue:     0,
					Step:            0,
					TimestampMicros: 1_780_000_000_000_000,
					CreatedAtMicros: nowMicros,
				},
			},
		},
		{
			name: "timestamp boundary inclusivity and out-of-range clamping",
			input: []*pb.Metric{
				prototest.Modify(baseMetric, func(m *pb.Metric) { m.Step = 1; m.Timestamp = MinValidTimestampMicros }),
				prototest.Modify(baseMetric, func(m *pb.Metric) { m.Step = 2; m.Timestamp = MinValidTimestampMicros - 1 }),
				prototest.Modify(baseMetric, func(m *pb.Metric) { m.Step = 3; m.Timestamp = MaxValidTimestampMicros }),
				prototest.Modify(baseMetric, func(m *pb.Metric) { m.Step = 4; m.Timestamp = MaxValidTimestampMicros + 1 }),
				prototest.Modify(baseMetric, func(m *pb.Metric) { m.Step = 5; m.Timestamp = -42 }),
			},
			want: []NormalizedMetric{
				func() NormalizedMetric {
					r := baseRow
					r.Step = 1
					r.TimestampMicros = MinValidTimestampMicros
					return r
				}(),
				func() NormalizedMetric { r := baseRow; r.Step = 2; r.TimestampMicros = nowMicros; return r }(),
				func() NormalizedMetric {
					r := baseRow
					r.Step = 3
					r.TimestampMicros = MaxValidTimestampMicros
					return r
				}(),
				func() NormalizedMetric { r := baseRow; r.Step = 4; r.TimestampMicros = nowMicros; return r }(),
				func() NormalizedMetric { r := baseRow; r.Step = 5; r.TimestampMicros = nowMicros; return r }(),
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := NormalizeMetrics(tc.input, fixedIngestTime)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("NormalizeMetrics() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
