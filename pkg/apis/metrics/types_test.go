// Copyright 2026 Google LLC
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

package metrics

import (
	"math"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	pb "xmanager_cloud/api/protos/metrics"
)

func TestMetric_IsValid(t *testing.T) {
	validMetric := Metric{
		Metric: &pb.Metric{
			Xid:         12345,
			WorkUnitId:  101,
			ExecutionId: 202,
			ReplicaId:   "1",
			MetricName:  "accuracy/epoch_1",
			MetricValue: 0.985,
			Step:        100,
			Timestamp:   1700000000000000,
		},
	}

	tests := []struct {
		name    string
		modify  func(m *Metric)
		wantErr bool
	}{
		{
			name:    "valid metric",
			modify:  func(m *Metric) {},
			wantErr: false,
		},
		{
			name: "valid metric name with allowed chars",
			modify: func(m *Metric) {
				m.MetricName = "train/loss-v1_2.0"
			},
			wantErr: false,
		},
		{
			name: "empty metric name",
			modify: func(m *Metric) {
				m.MetricName = ""
			},
			wantErr: true,
		},
		{
			name: "metric name exceeds 255 chars",
			modify: func(m *Metric) {
				m.MetricName = strings.Repeat("a", 256)
			},
			wantErr: true,
		},
		{
			name: "metric name contains invalid character space",
			modify: func(m *Metric) {
				m.MetricName = "invalid metric name"
			},
			wantErr: true,
		},
		{
			name: "metric name contains invalid character colon",
			modify: func(m *Metric) {
				m.MetricName = "metric:name"
			},
			wantErr: true,
		},
		{
			name: "metric value is NaN",
			modify: func(m *Metric) {
				m.MetricValue = math.NaN()
			},
			wantErr: true,
		},
		{
			name: "metric value is +Inf",
			modify: func(m *Metric) {
				m.MetricValue = math.Inf(1)
			},
			wantErr: true,
		},
		{
			name: "metric value is -Inf",
			modify: func(m *Metric) {
				m.MetricValue = math.Inf(-1)
			},
			wantErr: true,
		},
		{
			name: "negative step",
			modify: func(m *Metric) {
				m.Step = -1
			},
			wantErr: true,
		},
		{
			name: "zero step",
			modify: func(m *Metric) {
				m.Step = 0
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cloned, ok := proto.Clone(validMetric.Metric).(*pb.Metric)
			if !ok {
				t.Fatalf("proto.Clone(validMetric.Metric) returned unexpected type %T", validMetric.Metric)
			}
			m := Metric{Metric: cloned}
			tt.modify(&m)
			err := m.IsValid()
			if (err != nil) != tt.wantErr {
				t.Errorf("IsValid() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestNilMetric_IsValid(t *testing.T) {
	var m *Metric
	if err := m.IsValid(); err == nil {
		t.Errorf("expected error for nil Metric wrapper, got nil")
	}

	m2 := Metric{Metric: nil}
	if err := m2.IsValid(); err == nil {
		t.Errorf("expected error for Metric with nil inner proto, got nil")
	}
}
