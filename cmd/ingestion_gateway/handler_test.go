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
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
)

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

	MetricsIngestionHandler(w, req)

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

	MetricsIngestionHandler(w, req)

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

	MetricsIngestionHandler(w, req)

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

	MetricsIngestionHandler(w, req)

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

	MetricsIngestionHandler(w, req)

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

	MetricsIngestionHandler(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405 Method Not Allowed, got %d", resp.StatusCode)
	}
}

func TestMetricsIngestionHandler_ConstantMemoryStreaming(t *testing.T) {
	runtime.GC()
	var mBefore runtime.MemStats
	runtime.ReadMemStats(&mBefore)

	var buf bytes.Buffer
	buf.WriteString("[")
	for i := 0; i < 10000; i++ {
		if i > 0 {
			buf.WriteString(",")
		}
		fmt.Fprintf(&buf, `{"xid": 1, "work_unit_id": 1, "execution_id": 1, "replica_id": "rep-0", "metric_name": "metric_%d", "metric_value": %f, "step": %d, "timestamp": 1700000000}`, i, float64(i)*0.1, i)
	}
	buf.WriteString("]")

	req := httptest.NewRequest(http.MethodPost, "/api/v1/metrics", &buf)
	w := httptest.NewRecorder()

	MetricsIngestionHandler(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected status 202, got %d", resp.StatusCode)
	}

	runtime.GC()
	var mAfter runtime.MemStats
	runtime.ReadMemStats(&mAfter)

	t.Logf("HeapAlloc before: %d, after: %d", mBefore.HeapAlloc, mAfter.HeapAlloc)

	const maxAllowedHeapDelta = 5 * 1024 * 1024 // 5MB limit
	heapDelta := int64(mAfter.HeapAlloc) - int64(mBefore.HeapAlloc)
	if heapDelta > maxAllowedHeapDelta {
		t.Errorf("heap memory allocation increased by %d bytes, exceeding maximum allowed threshold of %d bytes", heapDelta, maxAllowedHeapDelta)
	}
}
