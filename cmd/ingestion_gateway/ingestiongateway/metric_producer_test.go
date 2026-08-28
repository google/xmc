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

package ingestiongateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// MockProducer implements MetricProducer for unit testing.
type MockProducer struct {
	mu           sync.Mutex
	records      []*MetricRecord
	produceDelay time.Duration
	failIndex    int
	failErr      error
	produceCount int
	closed       bool
}

func NewMockProducer() *MockProducer {
	return &MockProducer{
		failIndex: -1,
	}
}

func (m *MockProducer) Produce(ctx context.Context, record *MetricRecord, promise func(*MetricRecord, error)) {
	m.mu.Lock()
	idx := m.produceCount
	m.produceCount++
	m.records = append(m.records, record)
	failIdx := m.failIndex
	failErr := m.failErr
	delay := m.produceDelay
	m.mu.Unlock()

	exec := func() {
		if failIdx >= 0 && idx >= failIdx {
			if failErr == nil {
				failErr = newPublishError(PublishErrorUnknown, errors.New("simulated produce error"))
			}
			promise(record, failErr)
			return
		}
		promise(record, nil)
	}

	if delay > 0 {
		go func() {
			time.Sleep(delay)
			exec()
		}()
	} else {
		exec()
	}
}

func (m *MockProducer) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
}

func (m *MockProducer) ProducedRecords() []*MetricRecord {
	m.mu.Lock()
	defer m.mu.Unlock()
	copied := make([]*MetricRecord, len(m.records))
	copy(copied, m.records)
	return copied
}

func TestMockProducer_Close(t *testing.T) {
	mp := NewMockProducer()
	mp.Close()
	if !mp.closed {
		t.Errorf("expected mp.closed to be true after Close()")
	}
}

func TestFormatCompositeKey(t *testing.T) {
	tests := []struct {
		xid        int64
		workUnitID int64
		wantKey    string
	}{
		{xid: 101, workUnitID: 1, wantKey: "101:1"},
		{xid: 101, workUnitID: 2, wantKey: "101:2"},
		{xid: 202, workUnitID: 1, wantKey: "202:1"},
		{xid: 0, workUnitID: 0, wantKey: "0:0"},
		{xid: math.MaxInt64, workUnitID: math.MaxInt64, wantKey: "9223372036854775807:9223372036854775807"},
		{xid: math.MinInt64, workUnitID: math.MinInt64, wantKey: "-9223372036854775808:-9223372036854775808"},
		{xid: -100, workUnitID: -50, wantKey: "-100:-50"},
	}

	for _, tt := range tests {
		key := FormatCompositeKey(tt.xid, tt.workUnitID)
		if string(key) != tt.wantKey {
			t.Errorf("FormatCompositeKey(%d, %d) = %q, want %q", tt.xid, tt.workUnitID, string(key), tt.wantKey)
		}
	}
}

func TestPublishError(t *testing.T) {
	cause := errors.New("underlying failure")
	err := newPublishError(PublishErrorCongestion, cause)

	var pubErr *PublishError
	if !errors.As(err, &pubErr) {
		t.Fatalf("expected errors.As to match *PublishError, got %T", err)
	}
	if pubErr.Kind != PublishErrorCongestion {
		t.Errorf("Kind = %v, want %v", pubErr.Kind, PublishErrorCongestion)
	}
	if !errors.Is(err, cause) {
		t.Errorf("expected errors.Is to unwrap to the underlying cause")
	}
	if err.Error() != cause.Error() {
		t.Errorf("Error() = %q, want %q", err.Error(), cause.Error())
	}
}

func TestNewPublishError_NilErrorStaysNil(t *testing.T) {
	if got := newPublishError(PublishErrorClient, nil); got != nil {
		t.Errorf("newPublishError(_, nil) = %v, want nil", got)
	}
}

func TestPublishError_NilCause(t *testing.T) {
	err := &PublishError{Kind: PublishErrorUnknown}
	if err.Error() != "publish failed" {
		t.Errorf("Error() = %q, want %q", err.Error(), "publish failed")
	}
}

func TestNewMetricProducer_NilConfig(t *testing.T) {
	if _, err := NewMetricProducer(context.Background(), nil, nil); err == nil {
		t.Fatalf("expected an error for a nil config")
	}
}

func TestNewMetricProducer_UnsupportedMessageBus(t *testing.T) {
	cfg := &Config{
		MessageBus:      "rabbitmq",
		RedpandaBrokers: []string{"127.0.0.1:9092"},
		TopicName:       "xmc-metrics",
	}
	producer, err := NewMetricProducer(context.Background(), cfg, nil)
	if err == nil {
		producer.Close()
		t.Fatalf("expected an error for an unsupported message bus")
	}
	if !strings.Contains(err.Error(), "rabbitmq") {
		t.Errorf("expected the error to name the rejected backend, got %q", err.Error())
	}
}

// TestNewMetricProducer_RedpandaValidatesTopology asserts the Redpanda branch performs its startup
// topology check: with no broker listening the check must fail rather than yield a live producer.
func TestNewMetricProducer_RedpandaValidatesTopology(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	cfg := &Config{
		MessageBus:      MessageBusRedpanda,
		RedpandaBrokers: []string{"127.0.0.1:1"},
		TopicName:       "xmc-metrics",
		MinPartitions:   1,
		MinReplicas:     1,
	}
	producer, err := NewMetricProducer(ctx, cfg, nil)
	if err == nil {
		producer.Close()
		t.Fatalf("expected topology validation to fail against an unreachable broker")
	}
	if !strings.Contains(err.Error(), "topic topology validation failed") {
		t.Errorf("expected a topology validation error, got %q", err.Error())
	}
}

func TestAsyncPublishing_WaitGroupBlocking(t *testing.T) {
	mp := NewMockProducer()
	mp.produceDelay = 30 * time.Millisecond

	handler := NewMetricsIngestionHandler(mp, "test-topic", nil)

	payload := `[
		{"xid": 101, "work_unit_id": 1, "execution_id": 1, "replica_id": "rep-0", "metric_name": "loss", "metric_value": 0.5, "step": 1, "timestamp": 1700000000},
		{"xid": 101, "work_unit_id": 1, "execution_id": 1, "replica_id": "rep-0", "metric_name": "acc", "metric_value": 0.9, "step": 1, "timestamp": 1700000000}
	]`

	req := httptest.NewRequest(http.MethodPost, "/api/v1/metrics", strings.NewReader(payload))
	w := httptest.NewRecorder()

	start := time.Now()
	handler.ServeHTTP(w, req)
	elapsed := time.Since(start)

	resp := w.Result()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected status 202 Accepted, got %d", resp.StatusCode)
	}

	if elapsed < 20*time.Millisecond {
		t.Errorf("expected handler to block until async promises complete (>=20ms), elapsed: %v", elapsed)
	}

	recs := mp.ProducedRecords()
	if len(recs) != 2 {
		t.Fatalf("expected 2 records produced, got %d", len(recs))
	}

	expectedKey := "101:1"
	if string(recs[0].Key) != expectedKey {
		t.Errorf("expected key %q, got %q", expectedKey, string(recs[0].Key))
	}
	if recs[0].Topic != "test-topic" {
		t.Errorf("expected topic test-topic, got %q", recs[0].Topic)
	}
}

func TestEarlyAbortOptimization_BrokerFailure(t *testing.T) {
	mp := NewMockProducer()
	mp.failIndex = 1
	mp.failErr = newPublishError(PublishErrorCongestion, errors.New("kafka cluster unreachable"))
	mp.produceDelay = 0

	handler := NewMetricsIngestionHandler(mp, "test-topic", nil)

	var buf bytes.Buffer
	buf.WriteString("[")
	for i := 0; i < 10; i++ {
		if i > 0 {
			buf.WriteString(",")
		}
		buf.WriteString(fmt.Sprintf(`{"xid": 202, "work_unit_id": 2, "execution_id": 1, "replica_id": "rep-0", "metric_name": "m_%d", "metric_value": 1.0, "step": %d, "timestamp": 1700000000}`, i, i))
	}
	buf.WriteString("]")

	req := httptest.NewRequest(http.MethodPost, "/api/v1/metrics", &buf)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503 Service Unavailable on broker failure, got %d", resp.StatusCode)
	}

	var errPayload ServiceUnavailableErrorPayload
	if err := json.NewDecoder(resp.Body).Decode(&errPayload); err != nil {
		t.Fatalf("failed to decode 503 response JSON: %v", err)
	}
	if errPayload.Error != "service_unavailable" {
		t.Errorf("expected error service_unavailable, got %q", errPayload.Error)
	}

	recs := mp.ProducedRecords()
	if len(recs) >= 10 {
		t.Errorf("expected early-abort to halt processing before sending all 10 records, but %d records were produced", len(recs))
	}
}
