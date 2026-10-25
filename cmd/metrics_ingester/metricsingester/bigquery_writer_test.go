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
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/bigquery"
	"cloud.google.com/go/bigquery/storage/managedwriter/adapt"
	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/testing/protocmp"
	"google.golang.org/protobuf/types/dynamicpb"
	pb "xmanager_cloud/api/protos/metrics"
	"xmanager_cloud/internal/prototest"
)

type fakeRowAppender struct {
	calls [][][]byte
	err   error
}

func (f *fakeRowAppender) AppendRows(_ context.Context, rows [][]byte) error {
	if f.err != nil {
		return f.err
	}
	copied := make([][]byte, len(rows))
	for i, r := range rows {
		copied[i] = append([]byte(nil), r...)
	}
	f.calls = append(f.calls, copied)
	return nil
}

func loadCanonicalBQDescriptor(t *testing.T) protoreflect.MessageDescriptor {
	t.Helper()
	raw := readBigQuerySchemaJSON(t)
	bqSchema, err := bigquery.SchemaFromJSON(raw)
	if err != nil {
		t.Fatalf("bigquery.SchemaFromJSON() error = %v", err)
	}
	storageSchema, err := adapt.BQSchemaToStorageTableSchema(bqSchema)
	if err != nil {
		t.Fatalf("BQSchemaToStorageTableSchema() error = %v", err)
	}
	desc, err := adapt.StorageSchemaToProto2Descriptor(storageSchema, "MetricRow")
	if err != nil {
		t.Fatalf("StorageSchemaToProto2Descriptor() error = %v", err)
	}
	msgDesc, ok := desc.(protoreflect.MessageDescriptor)
	if !ok {
		t.Fatalf("descriptor %T is not a MessageDescriptor", desc)
	}
	return msgDesc
}

func readBigQuerySchemaJSON(t *testing.T) []byte {
	t.Helper()
	var candidates []string
	if srcDir := os.Getenv("TEST_SRCDIR"); srcDir != "" {
		ws := os.Getenv("TEST_WORKSPACE")
		if ws == "" {
			ws = "_main"
		}
		candidates = append(candidates,
			filepath.Join(srcDir, ws, "deploy", "bigquery", "schema.json"),
			filepath.Join(srcDir, "_main", "deploy", "bigquery", "schema.json"),
			filepath.Join(srcDir, "xmanager_cloud", "deploy", "bigquery", "schema.json"),
		)
	}
	candidates = append(candidates, filepath.Join("..", "..", "..", "deploy", "bigquery", "schema.json"))
	for _, p := range candidates {
		if data, err := os.ReadFile(p); err == nil {
			return data
		}
	}
	t.Fatalf("deploy/bigquery/schema.json not found in %v", candidates)
	return nil
}

func TestMetricRowDescriptorAndWireFormatMatchSchemaJSON(t *testing.T) {
	msgDesc := loadCanonicalBQDescriptor(t)
	wantDP := protodesc.ToDescriptorProto(msgDesc)
	if diff := cmp.Diff(wantDP, MetricRowDescriptor(), protocmp.Transform()); diff != "" {
		t.Fatalf("MetricRowDescriptor() mismatch against deploy/bigquery/schema.json (-want +got):\n%s", diff)
	}

	rows := []NormalizedMetric{
		{
			Xid:             42,
			WorkUnitID:      0,
			ExecutionID:     1,
			JobName:         "",
			ReplicaID:       "0",
			MetricName:      "grad_norm",
			MetricValue:     0.0,
			Step:            0,
			TimestampMicros: MinValidTimestampMicros,
			CreatedAtMicros: fixedIngestTime.UnixMicro(),
		},
		{
			Xid:             99,
			WorkUnitID:      7,
			ExecutionID:     2,
			JobName:         "eval",
			ReplicaID:       "worker-1",
			MetricName:      "accuracy",
			MetricValue:     0.9875,
			Step:            -1,
			TimestampMicros: 1_780_000_000_000_000,
			CreatedAtMicros: fixedIngestTime.UnixMicro(),
		},
	}

	for _, r := range rows {
		dyn := dynamicpb.NewMessage(msgDesc)
		if err := proto.Unmarshal(marshalMetricRow(r), dyn); err != nil {
			t.Fatalf("proto.Unmarshal() error = %v", err)
		}
		for i := 0; i < msgDesc.Fields().Len(); i++ {
			fd := msgDesc.Fields().Get(i)
			if !dyn.Has(fd) {
				t.Errorf("field %s missing explicit proto2 presence for row %+v", fd.Name(), r)
			}
		}
		if got := dyn.Get(msgDesc.Fields().ByName("xid")).Int(); got != r.Xid {
			t.Errorf("xid = %d, want %d", got, r.Xid)
		}
		if got := dyn.Get(msgDesc.Fields().ByName("job_name")).String(); got != r.JobName {
			t.Errorf("job_name = %q, want %q", got, r.JobName)
		}
		if got := dyn.Get(msgDesc.Fields().ByName("metric_value")).Float(); got != r.MetricValue {
			t.Errorf("metric_value = %v, want %v", got, r.MetricValue)
		}
		if got := dyn.Get(msgDesc.Fields().ByName("step")).Int(); got != r.Step {
			t.Errorf("step = %d, want %d", got, r.Step)
		}
		if got := dyn.Get(msgDesc.Fields().ByName("timestamp")).Int(); got != r.TimestampMicros {
			t.Errorf("timestamp = %d, want %d", got, r.TimestampMicros)
		}
		if got := dyn.Get(msgDesc.Fields().ByName("created_at")).Int(); got != r.CreatedAtMicros {
			t.Errorf("created_at = %d, want %d", got, r.CreatedAtMicros)
		}
	}
}

func TestBigQueryWriterWriteNormalizesChunksAndSkipsFilteredBatches(t *testing.T) {
	msgDesc := loadCanonicalBQDescriptor(t)
	appender := &fakeRowAppender{}
	writer := NewBigQueryWriter(
		appender,
		WithClock(func() time.Time { return fixedIngestTime }),
		WithMaxAppendBytes(1), // force 1 row per AppendRows chunk
	)

	if err := writer.Write(context.Background(), nil); err != nil {
		t.Fatalf("Write(nil) error = %v", err)
	}
	filteredOnly := []*pb.Metric{prototest.Modify(testMetric(), func(m *pb.Metric) { m.Xid = 0 })}
	if err := writer.Write(context.Background(), filteredOnly); err != nil {
		t.Fatalf("Write(filteredOnly) error = %v", err)
	}
	if len(appender.calls) != 0 {
		t.Fatalf("AppendRows calls after empty/filtered batches = %d, want 0", len(appender.calls))
	}

	batch := []*pb.Metric{
		prototest.Modify(testMetric(), func(m *pb.Metric) { m.ReplicaId = ""; m.Timestamp = 5 }),
		prototest.Modify(testMetric(), func(m *pb.Metric) { m.ExecutionId = 0 }), // filtered
		prototest.Modify(testMetric(), func(m *pb.Metric) { m.Step = 11 }),
	}
	if err := writer.Write(context.Background(), batch); err != nil {
		t.Fatalf("Write(batch) error = %v", err)
	}
	if len(appender.calls) != 2 {
		t.Fatalf("AppendRows calls = %d, want 2 chunks", len(appender.calls))
	}

	first := dynamicpb.NewMessage(msgDesc)
	if err := proto.Unmarshal(appender.calls[0][0], first); err != nil {
		t.Fatalf("Unmarshal chunk 0 error = %v", err)
	}
	if got := first.Get(msgDesc.Fields().ByName("replica_id")).String(); got != "0" {
		t.Errorf("chunk 0 replica_id = %q, want \"0\"", got)
	}
	if got := first.Get(msgDesc.Fields().ByName("timestamp")).Int(); got != fixedIngestTime.UnixMicro() {
		t.Errorf("chunk 0 clamped timestamp = %d, want %d", got, fixedIngestTime.UnixMicro())
	}
}

func TestBigQueryWriterWriteWrapsAppendError(t *testing.T) {
	if err := NewBigQueryWriter(nil).Write(context.Background(), nil); err == nil {
		t.Fatal("NewBigQueryWriter(nil).Write() = nil, want non-nil error")
	}

	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	canceledAppender := &fakeRowAppender{}
	if err := NewBigQueryWriter(canceledAppender).Write(canceledCtx, []*pb.Metric{testMetric()}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Write(canceledCtx) error = %v, want wrapping context.Canceled", err)
	}
	if len(canceledAppender.calls) != 0 {
		t.Fatalf("AppendRows calls on canceled ctx = %d, want 0", len(canceledAppender.calls))
	}

	boom := errors.New("stream broken")
	writer := NewBigQueryWriter(&fakeRowAppender{err: boom}, WithClock(nil), WithMaxAppendBytes(0))
	err := writer.Write(context.Background(), []*pb.Metric{testMetric()})
	if !errors.Is(err, boom) || !strings.Contains(err.Error(), "failed to append metrics to bigquery") {
		t.Fatalf("Write() error = %v, want wrapping %v", err, boom)
	}
}
