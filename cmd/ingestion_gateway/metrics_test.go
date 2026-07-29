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
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/twmb/franz-go/pkg/kgo"
)

func TestNewGatewayMetrics(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewGatewayMetrics(reg)

	if m == nil {
		t.Fatal("expected NewGatewayMetrics to return non-nil GatewayMetrics")
	}
	if m.Registry != reg {
		t.Errorf("expected Registry to be set to custom registry")
	}
}

func TestRecordHTTPRequest(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewGatewayMetrics(reg)

	m.RecordHTTPRequest(http.StatusOK, "/api/v1/metrics", http.MethodPost, 100*time.Millisecond)

	count := testutil.ToFloat64(m.HTTPRequestsTotal.WithLabelValues("200", "/api/v1/metrics", "POST"))
	if count != 1.0 {
		t.Errorf("expected HTTPRequestsTotal count 1.0, got %f", count)
	}
}

func TestRecordDroppedPayload(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewGatewayMetrics(reg)

	m.RecordDroppedPayload("decode_error")
	m.RecordDroppedPayload("decode_error")
	m.RecordDroppedPayload("rate_limited")

	countDecode := testutil.ToFloat64(m.DroppedPayloadsTotal.WithLabelValues("decode_error"))
	if countDecode != 2.0 {
		t.Errorf("expected DroppedPayloadsTotal for decode_error to be 2.0, got %f", countDecode)
	}

	countRateLimit := testutil.ToFloat64(m.DroppedPayloadsTotal.WithLabelValues("rate_limited"))
	if countRateLimit != 1.0 {
		t.Errorf("expected DroppedPayloadsTotal for rate_limited to be 1.0, got %f", countRateLimit)
	}
}

func TestHTTPMiddleware(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewGatewayMetrics(reg)

	handler := m.HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		active := testutil.ToFloat64(m.ActiveConnections)
		if active != 1.0 {
			t.Errorf("expected ActiveConnections to be 1.0 during request, got %f", active)
		}
		w.WriteHeader(http.StatusAccepted)
		w.Write([]byte("Accepted"))
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/metrics", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Errorf("expected status code %d, got %d", http.StatusAccepted, rec.Code)
	}

	activeAfter := testutil.ToFloat64(m.ActiveConnections)
	if activeAfter != 0.0 {
		t.Errorf("expected ActiveConnections to be 0.0 after request, got %f", activeAfter)
	}

	count := testutil.ToFloat64(m.HTTPRequestsTotal.WithLabelValues("202", "/api/v1/metrics", "POST"))
	if count != 1.0 {
		t.Errorf("expected HTTPRequestsTotal count 1.0, got %f", count)
	}
}

func TestMetricsEndpoint(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewGatewayMetrics(reg)

	m.RecordHTTPRequest(http.StatusOK, "/api/v1/metrics", http.MethodPost, 50*time.Millisecond)
	m.RecordDroppedPayload("validation_error")

	server := httptest.NewServer(m.MetricsHandler())
	defer server.Close()

	resp, err := http.Get(server.URL)
	if err != nil {
		t.Fatalf("failed to GET /metrics: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status code 200 from /metrics, got %d", resp.StatusCode)
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read response body: %v", err)
	}
	body := string(bodyBytes)

	expectedMetrics := []string{
		"xmc_ingestion_http_requests_total",
		"xmc_ingestion_http_request_duration_seconds",
		"xmc_ingestion_active_connections",
		"xmc_ingestion_dropped_payloads_total",
		"franz_go_brokers_connections",
		"franz_go_produce_batch_size_bytes",
		"franz_go_produce_records_total",
		"franz_go_produce_errors_total",
		"franz_go_produce_retries_total",
		"franz_go_produce_queue_latency_seconds",
		"go_goroutines",
		"process_open_fds",
	}

	for _, metricName := range expectedMetrics {
		if !strings.Contains(body, metricName) {
			t.Errorf("expected /metrics output to contain metric %q, body:\n%s", metricName, body)
		}
	}
}

func TestFranzGoMetricsHook(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewGatewayMetrics(reg)
	hook := NewFranzGoMetricsHook(m)
	_ = kgo.WithHooks(hook)
	meta := kgo.BrokerMetadata{NodeID: 1, Host: "localhost", Port: 9092}

	hook.HookBrokerConnect(meta)
	if count := testutil.ToFloat64(m.BrokersConnections); count != 1.0 {
		t.Errorf("expected BrokersConnections to be 1.0, got %f", count)
	}

	hook.HookBrokerDisconnect(meta)
	if count := testutil.ToFloat64(m.BrokersConnections); count != 0.0 {
		t.Errorf("expected BrokersConnections to be 0.0, got %f", count)
	}

	hook.HookProduceRecordUnbuffered(&kgo.Record{}, nil)
	if records := testutil.ToFloat64(m.ProduceRecordsTotal); records != 1.0 {
		t.Errorf("expected ProduceRecordsTotal to be 1.0, got %f", records)
	}

	hook.HookProduceRecordUnbuffered(&kgo.Record{}, errors.New("write error"))
	if errorsCount := testutil.ToFloat64(m.ProduceErrorsTotal); errorsCount != 1.0 {
		t.Errorf("expected ProduceErrorsTotal to be 1.0, got %f", errorsCount)
	}
}
