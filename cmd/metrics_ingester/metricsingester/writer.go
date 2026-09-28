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
	"context"
	"fmt"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	pb "xmanager_cloud/api/protos/metrics"
)

// insertQueuedMetrics targets the Null-engine landing table rather than xmc_metrics, so the
// filtering and timestamp sanitization in xmc_metrics_pubsub_mv apply exactly as they do to the
// Redpanda path. The raw microsecond timestamp is inserted unconverted for the same reason.
const insertQueuedMetrics = "INSERT INTO xmc_metrics_pubsub_queue (xid, work_unit_id, execution_id, job_name, replica_id, metric_name, metric_value, step, timestamp)"

// BatchPreparer is the subset of driver.Conn that ClickHouseWriter needs.
type BatchPreparer interface {
	PrepareBatch(ctx context.Context, query string, opts ...driver.PrepareBatchOption) (driver.Batch, error)
}

// ClickHouseWriter inserts metric records into ClickHouse in a single batch per call.
type ClickHouseWriter struct {
	conn BatchPreparer
}

// NewClickHouseWriter returns a writer that inserts through conn.
func NewClickHouseWriter(conn BatchPreparer) *ClickHouseWriter {
	return &ClickHouseWriter{conn: conn}
}

// Write inserts metrics as one ClickHouse batch. It either sends every record or none of them, so a
// failed call can be retried in full without partially duplicating the batch.
func (w *ClickHouseWriter) Write(ctx context.Context, metrics []*pb.Metric) error {
	if len(metrics) == 0 {
		return nil
	}

	batch, err := w.conn.PrepareBatch(ctx, insertQueuedMetrics)
	if err != nil {
		return fmt.Errorf("failed to prepare clickhouse batch: %w", err)
	}
	for _, m := range metrics {
		if err := appendMetric(batch, m); err != nil {
			_ = batch.Abort()
			return fmt.Errorf("failed to append metric to clickhouse batch: %w", err)
		}
	}
	if err := batch.Send(); err != nil {
		return fmt.Errorf("failed to send clickhouse batch: %w", err)
	}
	return nil
}

// appendMetric appends m as one row, in the column order of insertQueuedMetrics.
func appendMetric(batch driver.Batch, m *pb.Metric) error {
	return batch.Append(
		m.GetXid(),
		m.GetWorkUnitId(),
		m.GetExecutionId(),
		m.GetJobName(),
		m.GetReplicaId(),
		m.GetMetricName(),
		m.GetMetricValue(),
		m.GetStep(),
		m.GetTimestamp(),
	)
}
