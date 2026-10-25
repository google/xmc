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
	"time"

	pb "xmanager_cloud/api/protos/metrics"
)

const (
	// MinValidTimestampMicros is 2020-01-01T00:00:00Z in Unix epoch microseconds,
	// matching xmc_metrics_pubsub_mv in deploy/clickhouse/schema.sql.
	MinValidTimestampMicros int64 = 1_577_836_800_000_000
	// MaxValidTimestampMicros is 2050-01-01T00:00:00Z in Unix epoch microseconds,
	// matching xmc_metrics_pubsub_mv in deploy/clickhouse/schema.sql.
	MaxValidTimestampMicros int64 = 2_524_608_000_000_000

	defaultReplicaID = "0"
)

// NormalizedMetric holds a validated, MV-normalized metric row ready for storage in xmc_metrics.
type NormalizedMetric struct {
	Xid             int64
	WorkUnitID      int64
	ExecutionID     int64
	JobName         string
	ReplicaID       string
	MetricName      string
	MetricValue     float64
	Step            int64
	TimestampMicros int64
	CreatedAtMicros int64
}

// NormalizeMetrics applies the exact filtering, replica defaulting, and timestamp clamping of
// ClickHouse's xmc_metrics_pubsub_mv so BigQuery stores identical rows.
func NormalizeMetrics(metrics []*pb.Metric, now time.Time) []NormalizedMetric {
	if len(metrics) == 0 {
		return nil
	}
	nowMicros := now.UnixMicro()
	out := make([]NormalizedMetric, 0, len(metrics))
	for _, m := range metrics {
		row, ok := normalizeSingle(m, nowMicros)
		if !ok {
			continue
		}
		out = append(out, row)
	}
	return out
}

func normalizeSingle(m *pb.Metric, nowMicros int64) (NormalizedMetric, bool) {
	if m.GetXid() == 0 || m.GetMetricName() == "" || m.GetExecutionId() == 0 {
		return NormalizedMetric{}, false
	}
	replicaID := m.GetReplicaId()
	if replicaID == "" {
		replicaID = defaultReplicaID
	}
	ts := m.GetTimestamp()
	if ts < MinValidTimestampMicros || ts > MaxValidTimestampMicros {
		ts = nowMicros
	}
	return NormalizedMetric{
		Xid:             m.GetXid(),
		WorkUnitID:      m.GetWorkUnitId(),
		ExecutionID:     m.GetExecutionId(),
		JobName:         m.GetJobName(),
		ReplicaID:       replicaID,
		MetricName:      m.GetMetricName(),
		MetricValue:     m.GetMetricValue(),
		Step:            m.GetStep(),
		TimestampMicros: ts,
		CreatedAtMicros: nowMicros,
	}, true
}
