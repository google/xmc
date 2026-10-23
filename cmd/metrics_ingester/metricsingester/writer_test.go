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
	"strings"
	"testing"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/google/go-cmp/cmp"
	pb "xmanager_cloud/api/protos/metrics"
)

// fakeBatch records appended rows. Embedding driver.Batch satisfies the rest of the interface;
// calling any method it does not override panics, which flags unexpected use.
type fakeBatch struct {
	driver.Batch
	rows      [][]any
	appendErr error
	sendErr   error
	sent      bool
	aborted   bool
}

func (b *fakeBatch) Append(v ...any) error {
	if b.appendErr != nil {
		return b.appendErr
	}
	b.rows = append(b.rows, v)
	return nil
}

func (b *fakeBatch) Send() error {
	b.sent = true
	return b.sendErr
}

func (b *fakeBatch) Abort() error {
	b.aborted = true
	return nil
}

type fakePreparer struct {
	batch      *fakeBatch
	prepareErr error
	queries    []string
}

func (p *fakePreparer) PrepareBatch(_ context.Context, query string, _ ...driver.PrepareBatchOption) (driver.Batch, error) {
	p.queries = append(p.queries, query)
	if p.prepareErr != nil {
		return nil, p.prepareErr
	}
	return p.batch, nil
}

func testMetric() *pb.Metric {
	return &pb.Metric{
		Xid:         1,
		WorkUnitId:  2,
		ExecutionId: 3,
		JobName:     "trainer",
		ReplicaId:   "0",
		MetricName:  "loss",
		MetricValue: 0.5,
		Step:        10,
		Timestamp:   1_780_000_000_000_000,
	}
}

func TestClickHouseWriterWriteAppendsRowsInColumnOrder(t *testing.T) {
	p := &fakePreparer{batch: &fakeBatch{}}
	second := testMetric()
	second.Step = 11

	if err := NewClickHouseWriter(p).Write(context.Background(), []*pb.Metric{testMetric(), second}); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	if diff := cmp.Diff([]string{insertQueuedMetrics}, p.queries); diff != "" {
		t.Errorf("PrepareBatch queries mismatch (-want +got):\n%s", diff)
	}
	want := [][]any{
		{int64(1), int64(2), int64(3), "trainer", "0", "loss", 0.5, int64(10), int64(1_780_000_000_000_000)},
		{int64(1), int64(2), int64(3), "trainer", "0", "loss", 0.5, int64(11), int64(1_780_000_000_000_000)},
	}
	if diff := cmp.Diff(want, p.batch.rows); diff != "" {
		t.Errorf("appended rows mismatch (-want +got):\n%s", diff)
	}
	if !p.batch.sent {
		t.Error("Write() did not send the batch")
	}
}

func TestClickHouseWriterWriteEmptyIsNoOp(t *testing.T) {
	p := &fakePreparer{batch: &fakeBatch{}}

	if err := NewClickHouseWriter(p).Write(context.Background(), nil); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if len(p.queries) != 0 {
		t.Errorf("Write(nil) prepared %d batches, want 0", len(p.queries))
	}
}

func TestClickHouseWriterWriteErrors(t *testing.T) {
	boom := errors.New("boom")
	tests := []struct {
		name        string
		preparer    *fakePreparer
		wantErr     string
		wantAborted bool
	}{
		{name: "prepare fails", preparer: &fakePreparer{prepareErr: boom}, wantErr: "failed to prepare clickhouse batch"},
		{name: "append fails", preparer: &fakePreparer{batch: &fakeBatch{appendErr: boom}}, wantErr: "failed to append metric", wantAborted: true},
		{name: "send fails", preparer: &fakePreparer{batch: &fakeBatch{sendErr: boom}}, wantErr: "failed to send clickhouse batch"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := NewClickHouseWriter(tc.preparer).Write(context.Background(), []*pb.Metric{testMetric()})
			if !errors.Is(err, boom) || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Write() error = %v, want wrapping %v and containing %q", err, boom, tc.wantErr)
			}
			if tc.preparer.batch != nil && tc.preparer.batch.aborted != tc.wantAborted {
				t.Errorf("batch aborted = %v, want %v", tc.preparer.batch.aborted, tc.wantAborted)
			}
		})
	}
}
