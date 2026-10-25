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
	"errors"
	"fmt"
	"math"
	"time"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
	pb "xmanager_cloud/api/protos/metrics"
)

// defaultMaxAppendBytes caps each AppendRows call well below BigQuery Storage Write API's 10 MB limit.
const defaultMaxAppendBytes = 8 << 20

// RowAppender appends serialized proto2 rows to a BigQuery write stream and waits for server ack.
type RowAppender interface {
	AppendRows(ctx context.Context, rows [][]byte) error
}

// BigQueryWriterOption configures optional BigQueryWriter behavior.
type BigQueryWriterOption func(*BigQueryWriter)

// WithClock overrides the clock used for timestamp clamping and created_at population.
func WithClock(now func() time.Time) BigQueryWriterOption {
	return func(w *BigQueryWriter) {
		if now != nil {
			w.now = now
		}
	}
}

// WithMaxAppendBytes overrides the byte budget per AppendRows call.
func WithMaxAppendBytes(limit int) BigQueryWriterOption {
	return func(w *BigQueryWriter) {
		if limit > 0 {
			w.maxAppendBytes = limit
		}
	}
}

// BigQueryWriter normalizes metric batches and appends them via the BigQuery Storage Write API.
type BigQueryWriter struct {
	stream         RowAppender
	now            func() time.Time
	maxAppendBytes int
}

var _ MetricWriter = (*BigQueryWriter)(nil)

// NewBigQueryWriter returns a MetricWriter backed by stream.
func NewBigQueryWriter(stream RowAppender, opts ...BigQueryWriterOption) *BigQueryWriter {
	w := &BigQueryWriter{
		stream:         stream,
		now:            time.Now,
		maxAppendBytes: defaultMaxAppendBytes,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(w)
		}
	}
	return w
}

// Write normalizes metrics to match ClickHouse's xmc_metrics_pubsub_mv and appends surviving rows.
// Batches whose rows are all filtered out succeed without calling AppendRows so messages are acked.
func (w *BigQueryWriter) Write(ctx context.Context, metrics []*pb.Metric) error {
	if w.stream == nil {
		return errors.New("bigquery row appender is nil")
	}
	normalized := NormalizeMetrics(metrics, w.now())
	if len(normalized) == 0 {
		return nil
	}
	rows := make([][]byte, len(normalized))
	for i, m := range normalized {
		rows[i] = marshalMetricRow(m)
	}
	return w.appendChunked(ctx, rows)
}

func (w *BigQueryWriter) appendChunked(ctx context.Context, rows [][]byte) error {
	for start := 0; start < len(rows); {
		end, chunkBytes := start, 0
		for end < len(rows) && (end == start || chunkBytes+len(rows[end]) <= w.maxAppendBytes) {
			chunkBytes += len(rows[end])
			end++
		}
		if err := w.stream.AppendRows(ctx, rows[start:end]); err != nil {
			return fmt.Errorf("failed to append metrics to bigquery: %w", err)
		}
		start = end
	}
	return nil
}

// MetricRowDescriptor returns the proto2 DescriptorProto matching deploy/bigquery/schema.json.
// All fields use LABEL_REQUIRED so zero values (step=0, metric_value=0, job_name="") stay non-NULL.
func MetricRowDescriptor() *descriptorpb.DescriptorProto {
	return &descriptorpb.DescriptorProto{
		Name: proto.String("MetricRow"),
		Field: []*descriptorpb.FieldDescriptorProto{
			requiredField(1, "xid", descriptorpb.FieldDescriptorProto_TYPE_INT64),
			requiredField(2, "work_unit_id", descriptorpb.FieldDescriptorProto_TYPE_INT64),
			requiredField(3, "execution_id", descriptorpb.FieldDescriptorProto_TYPE_INT64),
			requiredField(4, "job_name", descriptorpb.FieldDescriptorProto_TYPE_STRING),
			requiredField(5, "replica_id", descriptorpb.FieldDescriptorProto_TYPE_STRING),
			requiredField(6, "metric_name", descriptorpb.FieldDescriptorProto_TYPE_STRING),
			requiredField(7, "metric_value", descriptorpb.FieldDescriptorProto_TYPE_DOUBLE),
			requiredField(8, "step", descriptorpb.FieldDescriptorProto_TYPE_INT64),
			requiredField(9, "timestamp", descriptorpb.FieldDescriptorProto_TYPE_INT64),
			requiredField(10, "created_at", descriptorpb.FieldDescriptorProto_TYPE_INT64),
		},
	}
}

func requiredField(num int32, name string, typ descriptorpb.FieldDescriptorProto_Type) *descriptorpb.FieldDescriptorProto {
	return &descriptorpb.FieldDescriptorProto{
		Name:   proto.String(name),
		Number: proto.Int32(num),
		Label:  descriptorpb.FieldDescriptorProto_LABEL_REQUIRED.Enum(),
		Type:   typ.Enum(),
	}
}

// marshalMetricRow encodes all 10 proto2 fields unconditionally, including zero values.
func marshalMetricRow(r NormalizedMetric) []byte {
	b := make([]byte, 0, 128+len(r.JobName)+len(r.ReplicaID)+len(r.MetricName))
	b = appendInt64Field(b, 1, r.Xid)
	b = appendInt64Field(b, 2, r.WorkUnitID)
	b = appendInt64Field(b, 3, r.ExecutionID)
	b = appendStringField(b, 4, r.JobName)
	b = appendStringField(b, 5, r.ReplicaID)
	b = appendStringField(b, 6, r.MetricName)
	b = protowire.AppendTag(b, 7, protowire.Fixed64Type)
	b = protowire.AppendFixed64(b, math.Float64bits(r.MetricValue))
	b = appendInt64Field(b, 8, r.Step)
	b = appendInt64Field(b, 9, r.TimestampMicros)
	b = appendInt64Field(b, 10, r.CreatedAtMicros)
	return b
}

func appendInt64Field(b []byte, num protowire.Number, v int64) []byte {
	b = protowire.AppendTag(b, num, protowire.VarintType)
	return protowire.AppendVarint(b, uint64(v))
}

func appendStringField(b []byte, num protowire.Number, v string) []byte {
	b = protowire.AppendTag(b, num, protowire.BytesType)
	return protowire.AppendString(b, v)
}
