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
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"

	"github.com/twmb/franz-go/pkg/kgo"
)

type noopProducer struct{}

func (np *noopProducer) Produce(ctx context.Context, rec *kgo.Record, promise func(*kgo.Record, error)) {
	promise(rec, nil)
}

func (np *noopProducer) Close() {}

func newTestHandler() http.HandlerFunc {
	return NewMetricsIngestionHandler(NewMockProducer(), "xmc-metrics")
}

type trackableReader struct {
	io.Reader
	bytesRead int64
}

func (tr *trackableReader) Read(p []byte) (int, error) {
	n, err := tr.Reader.Read(p)
	tr.bytesRead += int64(n)
	return n, err
}

func TestMetricsIngestionHandler_ValidArray(t *testing.T) {
	payload := `[
		{"xid": 101, "work_unit_id": 1, "execution_id": 1, "replica_id": "rep-0", "metric_name": "train_loss", "metric_value": 0.42, "step": 100, "timestamp": 1700000000},
		{"xid": 101, "work_unit_id": 1, "execution_id": 1, "replica_id": "rep-0", "metric_name": "accuracy", "metric_value": 0.95, "step": 100, "timestamp": 1700000000}
	]`

	req := httptest.NewRequest(http.MethodPost, "/api/v1/metrics", strings.NewReader(payload))
	w := httptest.NewRecorder()

	handler := newTestHandler()
	handler(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected status 202 Accepted, got %d", resp.StatusCode)
	}

	var respBody map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&respBody); err != nil {
		t.Fatalf("failed to decode response JSON: %v", err)
	}
	if respBody["status"] != "accepted" {
		t.Errorf("expected status 'accepted', got %v", respBody["status"])
	}
	if respBody["count"] != float64(2) {
		t.Errorf("expected count 2, got %v", respBody["count"])
	}
}

func TestMetricsIngestionHandler_JSONSequence(t *testing.T) {
	payload := `{"xid": 101, "work_unit_id": 1, "execution_id": 1, "replica_id": "rep-0", "metric_name": "train_loss", "metric_value": 0.42, "step": 100, "timestamp": 1700000000}
{"xid": 101, "work_unit_id": 1, "execution_id": 1, "replica_id": "rep-0", "metric_name": "accuracy", "metric_value": 0.95, "step": 101, "timestamp": 1700000001}`

	req := httptest.NewRequest(http.MethodPost, "/api/v1/metrics", strings.NewReader(payload))
	w := httptest.NewRecorder()

	handler := newTestHandler()
	handler(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected status 202 Accepted, got %d", resp.StatusCode)
	}

	var respBody map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&respBody); err != nil {
		t.Fatalf("failed to decode response JSON: %v", err)
	}
	if respBody["status"] != "accepted" {
		t.Errorf("expected status 'accepted', got %v", respBody["status"])
	}
	if respBody["count"] != float64(2) {
		t.Errorf("expected count 2, got %v", respBody["count"])
	}
}

func TestMetricsIngestionHandler_ValidationFailure_HTTP422(t *testing.T) {
	payload := `[
		{"xid": 101, "work_unit_id": 1, "execution_id": 1, "replica_id": "rep-0", "metric_name": "valid_metric", "metric_value": 1.0, "step": 0, "timestamp": 1700000000},
		{"xid": 101, "work_unit_id": 1, "execution_id": 1, "replica_id": "rep-0", "metric_name": "invalid metric name!!!", "metric_value": 1.0, "step": 0, "timestamp": 1700000000},
		{"xid": 101, "work_unit_id": 1, "execution_id": 1, "replica_id": "rep-0", "metric_name": "bad_step", "metric_value": 1.0, "step": -5, "timestamp": 1700000000}
	]`

	req := httptest.NewRequest(http.MethodPost, "/api/v1/metrics", strings.NewReader(payload))
	w := httptest.NewRecorder()

	handler := newTestHandler()
	handler(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("expected status 422 Unprocessable Entity, got %d", resp.StatusCode)
	}

	var valPayload ValidationErrorPayload
	if err := json.NewDecoder(resp.Body).Decode(&valPayload); err != nil {
		t.Fatalf("failed to decode 422 JSON payload: %v", err)
	}

	if valPayload.Error != "unprocessable_entity" {
		t.Errorf("expected error 'unprocessable_entity', got %q", valPayload.Error)
	}
	if len(valPayload.FailedRecords) != 2 {
		t.Fatalf("expected 2 failed records, got %d", len(valPayload.FailedRecords))
	}

	r1 := valPayload.FailedRecords[0]
	if r1.Index != 1 || r1.MetricName != "invalid metric name!!!" {
		t.Errorf("unexpected record 0 details: %+v", r1)
	}

	r2 := valPayload.FailedRecords[1]
	if r2.Index != 2 || r2.MetricName != "bad_step" || !strings.Contains(r2.Reason, "step") {
		t.Errorf("unexpected record 1 details: %+v", r2)
	}
}

func TestMetricsIngestionHandler_MalformedJSON_HTTP400(t *testing.T) {
	payload := `[{"metric_name": "test", "metric_value": `

	req := httptest.NewRequest(http.MethodPost, "/api/v1/metrics", strings.NewReader(payload))
	w := httptest.NewRecorder()

	handler := newTestHandler()
	handler(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected status 400 Bad Request, got %d", resp.StatusCode)
	}

	var errPayload BadRequestErrorPayload
	if err := json.NewDecoder(resp.Body).Decode(&errPayload); err != nil {
		t.Fatalf("failed to decode 400 JSON payload: %v", err)
	}

	if errPayload.Error != "bad_request" {
		t.Errorf("expected error 'bad_request', got %q", errPayload.Error)
	}
}

func TestMetricsIngestionHandler_BodyDrainageOnFailure(t *testing.T) {
	basePayload := `[{"xid": 1, "metric_name": "invalid name!!", "metric_value": 1.0}]`
	extraBytes := strings.Repeat("x", 10000)
	fullPayload := basePayload + extraBytes

	tr := &trackableReader{Reader: strings.NewReader(fullPayload)}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/metrics", tr)
	w := httptest.NewRecorder()

	handler := newTestHandler()
	handler(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("expected status 422, got %d", resp.StatusCode)
	}

	if tr.bytesRead < int64(len(basePayload)) {
		t.Errorf("expected body to be drained beyond %d bytes, got %d bytes read", len(basePayload), tr.bytesRead)
	}
}

func TestMetricsIngestionHandler_MethodNotAllowed(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/metrics", nil)
	w := httptest.NewRecorder()

	handler := newTestHandler()
	handler(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405 Method Not Allowed, got %d", resp.StatusCode)
	}
}

type metricStreamReader struct {
	totalMetrics  int
	currentIndex  int
	currentBuffer []byte
	bufferOffset  int
	closed        bool
}

func newMetricStreamReader(count int) *metricStreamReader {
	return &metricStreamReader{
		totalMetrics:  count,
		currentBuffer: []byte("["),
	}
}

func (r *metricStreamReader) Read(p []byte) (int, error) {
	if r.bufferOffset < len(r.currentBuffer) {
		n := copy(p, r.currentBuffer[r.bufferOffset:])
		r.bufferOffset += n
		return n, nil
	}

	if r.currentIndex >= r.totalMetrics {
		if !r.closed {
			r.currentBuffer = []byte("]")
			r.bufferOffset = 0
			r.closed = true
			n := copy(p, r.currentBuffer)
			r.bufferOffset += n
			return n, nil
		}
		return 0, io.EOF
	}

	prefix := ""
	if r.currentIndex > 0 {
		prefix = ","
	}
	r.currentBuffer = []byte(fmt.Sprintf(`%s{"xid": 1, "work_unit_id": 1, "execution_id": 1, "replica_id": "rep-0", "metric_name": "metric_%d", "metric_value": %f, "step": %d, "timestamp": 1700000000}`, prefix, r.currentIndex, float64(r.currentIndex)*0.1, r.currentIndex))
	r.bufferOffset = 0
	r.currentIndex++

	n := copy(p, r.currentBuffer)
	r.bufferOffset += n
	return n, nil
}

func TestMetricsIngestionHandler_ConstantMemoryStreaming(t *testing.T) {
	handler := NewMetricsIngestionHandler(&noopProducer{}, "xmc-metrics")
	runtime.GC()
	var mBefore runtime.MemStats
	runtime.ReadMemStats(&mBefore)

	// Stream 50,000 metrics on demand via io.Reader (~7.6MB raw JSON) without pre-allocating buffers in RAM.
	stream := newMetricStreamReader(50000)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/metrics", stream)
	w := httptest.NewRecorder()

	handler(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected status 202, got %d", resp.StatusCode)
	}

	runtime.GC()
	var mAfter runtime.MemStats
	runtime.ReadMemStats(&mAfter)

	t.Logf("HeapAlloc before: %d, after: %d", mBefore.HeapAlloc, mAfter.HeapAlloc)

	// In O(1) streaming, net heap allocation delta remains near zero (<1MB).
	// In O(N) slice accumulation, 50,000 retained metrics would consume ~14MB.
	const maxAllowedHeapDelta = 5 * 1024 * 1024 // 5MB limit
	heapDelta := int64(mAfter.HeapAlloc) - int64(mBefore.HeapAlloc)
	if heapDelta > maxAllowedHeapDelta {
		t.Errorf("heap memory allocation increased by %d bytes, exceeding maximum allowed threshold of %d bytes", heapDelta, maxAllowedHeapDelta)
	}
}

func TestNewMetricsIngestionHandler_NilProducer(t *testing.T) {
	handler := NewMetricsIngestionHandler(nil, "test-topic")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/metrics", strings.NewReader(`[{"xid": 1, "work_unit_id": 1, "execution_id": 1, "replica_id": "rep-0", "metric_name": "loss", "metric_value": 0.5, "step": 1, "timestamp": 1700000000}]`))
	w := httptest.NewRecorder()

	handler(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503 Service Unavailable when producer is nil, got %d", resp.StatusCode)
	}

	var errPayload ServiceUnavailableErrorPayload
	if err := json.NewDecoder(resp.Body).Decode(&errPayload); err != nil {
		t.Fatalf("failed to decode 503 response JSON: %v", err)
	}
	if errPayload.Error != "service_unavailable" {
		t.Errorf("expected error service_unavailable, got %q", errPayload.Error)
	}
}

func TestMetricsIngestionHandler_ContextCancelled(t *testing.T) {
	mp := NewMockProducer()
	handler := NewMetricsIngestionHandler(mp, "test-topic")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/metrics", strings.NewReader(`[{"xid": 1, "work_unit_id": 1, "execution_id": 1, "replica_id": "rep-0", "metric_name": "loss", "metric_value": 0.5, "step": 1, "timestamp": 1700000000}]`)).WithContext(ctx)
	w := httptest.NewRecorder()

	handler(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected status 400 Bad Request on cancelled context, got %d", resp.StatusCode)
	}
}

func TestDrainRequestBody(t *testing.T) {
	t.Run("nil reader does not panic", func(t *testing.T) {
		drainRequestBody(nil, MaxDrainBytes)
	})

	t.Run("drains up to maxBytes", func(t *testing.T) {
		largePayload := strings.Repeat("a", 10*1024*1024)
		tr := &trackableReader{Reader: strings.NewReader(largePayload)}
		drainRequestBody(tr, MaxDrainBytes)
		if tr.bytesRead != MaxDrainBytes {
			t.Errorf("expected %d bytes drained, got %d bytes read", MaxDrainBytes, tr.bytesRead)
		}
	})
}

func TestMetricsIngestionHandler_NilAndWhitespaceBody(t *testing.T) {
	t.Run("nil body", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/metrics", nil)
		w := httptest.NewRecorder()
		handler := newTestHandler()
		handler(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("expected 400 Bad Request for nil body, got %d", w.Code)
		}
	})

	t.Run("whitespace body", func(t *testing.T) {
		payload := "   	  "
		req := httptest.NewRequest(http.MethodPost, "/api/v1/metrics", strings.NewReader(payload))
		w := httptest.NewRecorder()
		handler := newTestHandler()
		handler(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("expected 400 Bad Request for whitespace body, got %d", w.Code)
		}
	})
}

func TestMetricsIngestionHandler_XReplicaIDHeader(t *testing.T) {
	mp := NewMockProducer()
	handler := NewMetricsIngestionHandler(mp, "test-topic")

	// Payload omits "replica_id" key
	payload := `[{"xid": 101, "work_unit_id": 1, "execution_id": 1, "metric_name": "loss", "metric_value": 0.5, "step": 1, "timestamp": 1700000000}]`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/metrics", strings.NewReader(payload))
	req.Header.Set("X-Replica-ID", "header-rep-7")
	w := httptest.NewRecorder()

	handler(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected status 202 Accepted, got %d", resp.StatusCode)
	}

	recs := mp.ProducedRecords()
	if len(recs) != 1 {
		t.Fatalf("expected 1 record produced, got %d", len(recs))
	}
}
