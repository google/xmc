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

package main

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

	"github.com/twmb/franz-go/pkg/kgo"
)

// MockProducer implements MetricProducer for unit testing.
type MockProducer struct {
	mu           sync.Mutex
	records      []*kgo.Record
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

func (m *MockProducer) Produce(ctx context.Context, record *kgo.Record, promise func(*kgo.Record, error)) {
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
				failErr = errors.New("simulated broker produce error")
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

func (m *MockProducer) ProducedRecords() []*kgo.Record {
	m.mu.Lock()
	defer m.mu.Unlock()
	copied := make([]*kgo.Record, len(m.records))
	copy(copied, m.records)
	return copied
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

func TestBuildProducerOptions(t *testing.T) {
	cfg := &Config{
		RedpandaBrokers: []string{"localhost:9092", "localhost:9093"},
		TopicName:       "xmc-metrics",
		SASLEnabled:     true,
		SASLMechanism:   "SCRAM-SHA-256",
		SASLUser:        "user",
		SASLPassword:    "pass",
	}

	opts := BuildProducerOptions(cfg)
	if len(opts) == 0 {
		t.Fatalf("expected non-empty kgo options")
	}

	client, err := kgo.NewClient(opts...)
	if err != nil {
		t.Fatalf("failed to construct kgo.Client with options: %v", err)
	}
	defer client.Close()
}

func TestAsyncPublishing_WaitGroupBlocking(t *testing.T) {
	mp := NewMockProducer()
	mp.produceDelay = 30 * time.Millisecond

	handler := NewMetricsIngestionHandler(mp, "test-topic")

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
	mp.failErr = errors.New("kafka cluster unreachable")
	mp.produceDelay = 0

	handler := NewMetricsIngestionHandler(mp, "test-topic")

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

func TestNewKafkaProducer(t *testing.T) {
	cfg := &Config{
		RedpandaBrokers: []string{"127.0.0.1:9092"},
		TopicName:       "xmc-metrics",
	}

	client, err := NewKafkaProducer(cfg)
	if err != nil {
		t.Fatalf("failed to construct Kafka producer: %v", err)
	}
	if client == nil {
		t.Fatalf("expected non-nil kgo.Client")
	}
	client.Close()
}

func TestBuildProducerOptions_AllSASLMechanisms(t *testing.T) {
	tests := []struct {
		name        string
		saslEnabled bool
		mechanism   string
	}{
		{name: "Disabled SASL", saslEnabled: false, mechanism: ""},
		{name: "SCRAM-SHA-256", saslEnabled: true, mechanism: "SCRAM-SHA-256"},
		{name: "SCRAM-SHA-512", saslEnabled: true, mechanism: "SCRAM-SHA-512"},
		{name: "PLAIN", saslEnabled: true, mechanism: "PLAIN"},
		{name: "Unknown Mechanism", saslEnabled: true, mechanism: "UNSUPPORTED"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				RedpandaBrokers: []string{"localhost:9092"},
				SASLEnabled:     tt.saslEnabled,
				SASLMechanism:   tt.mechanism,
				SASLUser:        "testuser",
				SASLPassword:    "testpass",
			}
			opts := BuildProducerOptions(cfg)
			if len(opts) == 0 {
				t.Fatalf("expected options for %s", tt.name)
			}
			client, err := kgo.NewClient(opts...)
			if err != nil {
				t.Fatalf("failed to construct kgo.Client for %s: %v", tt.name, err)
			}
			client.Close()
		})
	}
}

func TestNewKafkaProducer_EmptyBrokers(t *testing.T) {
	cfg := &Config{
		RedpandaBrokers: nil,
	}
	client, err := NewKafkaProducer(cfg)
	if err != nil {
		t.Logf("NewKafkaProducer returned error for empty brokers as expected: %v", err)
	} else if client != nil {
		client.Close()
	}
}

func TestMockProducer_Close(t *testing.T) {
	mp := NewMockProducer()
	mp.Close()
	if !mp.closed {
		t.Errorf("expected mp.closed to be true after Close()")
	}
}
