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
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/twmb/franz-go/pkg/kgo"
)

// Specialized buckets for HTTP request duration: 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0
var httpDurationBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0}

// GatewayMetrics encapsulates Prometheus metrics for ingestion gateway and Redpanda client telemetry.
type GatewayMetrics struct {
	Registry *prometheus.Registry

	// HTTP Ingestion Metrics
	HTTPRequestsTotal          *prometheus.CounterVec
	HTTPRequestDurationSeconds *prometheus.HistogramVec
	ActiveConnections          prometheus.Gauge
	DroppedPayloadsTotal       *prometheus.CounterVec

	// Franz-Go Telemetry Metrics
	BrokersConnections     prometheus.Gauge
	ProduceRecordSizeBytes prometheus.Histogram
	ProduceRecordsTotal    prometheus.Counter
	ProduceErrorsTotal     prometheus.Counter
}

// NewGatewayMetrics initializes and registers all Prometheus metrics with the given registry.
func NewGatewayMetrics(reg *prometheus.Registry) *GatewayMetrics {
	if reg == nil {
		reg = prometheus.NewRegistry()
	}
	m := newGatewayMetricCollectors(reg)
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		m.HTTPRequestsTotal,
		m.HTTPRequestDurationSeconds,
		m.ActiveConnections,
		m.DroppedPayloadsTotal,
		m.BrokersConnections,
		m.ProduceRecordSizeBytes,
		m.ProduceRecordsTotal,
		m.ProduceErrorsTotal,
	)
	return m
}

func newGatewayMetricCollectors(reg *prometheus.Registry) *GatewayMetrics {
	return &GatewayMetrics{
		Registry: reg,
		HTTPRequestsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{Name: "xmc_ingestion_http_requests_total", Help: "Total number of HTTP requests processed by ingestion gateway."},
			[]string{"status_code", "path", "method"},
		),
		HTTPRequestDurationSeconds: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{Name: "xmc_ingestion_http_request_duration_seconds", Help: "HTTP request latency in seconds.", Buckets: httpDurationBuckets},
			[]string{"status_code", "path", "method"},
		),
		ActiveConnections: prometheus.NewGauge(prometheus.GaugeOpts{Name: "xmc_ingestion_active_connections", Help: "Number of active HTTP connections."}),
		DroppedPayloadsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{Name: "xmc_ingestion_dropped_payloads_total", Help: "Total number of dropped metric payloads."},
			[]string{"reason"},
		),
		BrokersConnections: prometheus.NewGauge(prometheus.GaugeOpts{Name: "franz_go_brokers_connections", Help: "Number of active Franz-Go broker connections."}),
		ProduceRecordSizeBytes: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name: "xmc_ingestion_produce_record_size_bytes",
			Help: "Size in bytes of each metric record accepted by the message bus.",
			// One serialized metric record, not a batch: a few hundred bytes typically, capped by
			// the 1 MiB producer batch limit. The old 1 KiB floor put nearly every observation in
			// the first bucket and told us nothing.
			Buckets: prometheus.ExponentialBuckets(64, 2, 15),
		}),
		ProduceRecordsTotal: prometheus.NewCounter(prometheus.CounterOpts{Name: "xmc_ingestion_produce_records_total", Help: "Total number of records produced to the message bus."}),
		ProduceErrorsTotal:  prometheus.NewCounter(prometheus.CounterOpts{Name: "xmc_ingestion_produce_errors_total", Help: "Total number of produce errors."}),
	}
}

// RecordHTTPRequest records status, duration, and active connection tracking for an HTTP request.
func (m *GatewayMetrics) RecordHTTPRequest(status int, path, method string, duration time.Duration) {
	statusStr := strconv.Itoa(status)
	m.HTTPRequestsTotal.WithLabelValues(statusStr, path, method).Inc()
	m.HTTPRequestDurationSeconds.WithLabelValues(statusStr, path, method).Observe(duration.Seconds())
}

// RecordDroppedPayload increments dropped payloads counter with a specific reason.
func (m *GatewayMetrics) RecordDroppedPayload(reason string) {
	m.DroppedPayloadsTotal.WithLabelValues(reason).Inc()
}

// RecordProducedRecord records the payload size and outcome of a single publish attempt.
//
// Both message bus backends report through here so the produce series stay comparable across them.
// A nil receiver is a no-op, since metrics are optional throughout the gateway.
//
// Size is observed only for accepted records, so the histogram describes the same population as
// ProduceRecordsTotal and a burst of rejections cannot skew it. Rejected payloads are therefore
// invisible here; ProduceErrorsTotal and the client_produce_error dropped-payload reason are where
// an oversized-record problem shows up.
func (m *GatewayMetrics) RecordProducedRecord(valueBytes int, err error) {
	if m == nil {
		return
	}
	if err != nil {
		m.ProduceErrorsTotal.Inc()
		return
	}
	if valueBytes > 0 {
		m.ProduceRecordSizeBytes.Observe(float64(valueBytes))
	}
	m.ProduceRecordsTotal.Inc()
}

// sanitizePath normalizes dynamic or unknown URL paths to prevent cardinality explosion in Prometheus.
func sanitizePath(p string) string {
	switch p {
	case "/api/v1/metrics", "/healthz", "/readyz", "/metrics":
		return p
	default:
		return "other"
	}
}

// HTTPMiddleware creates a middleware wrapping an http.Handler to instrument HTTP metrics.
func (m *GatewayMetrics) HTTPMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.ActiveConnections.Inc()
		defer m.ActiveConnections.Dec()

		start := time.Now()
		ww := &statusResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(ww, r)

		duration := time.Since(start)
		m.RecordHTTPRequest(ww.statusCode, sanitizePath(r.URL.Path), r.Method, duration)
	})
}

// statusResponseWriter wraps http.ResponseWriter to capture response status code accurately.
type statusResponseWriter struct {
	http.ResponseWriter
	statusCode  int
	wroteHeader bool
}

func (w *statusResponseWriter) WriteHeader(code int) {
	if !w.wroteHeader {
		w.statusCode = code
		w.wroteHeader = true
		w.ResponseWriter.WriteHeader(code)
	}
}

func (w *statusResponseWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.wroteHeader = true
	}
	return w.ResponseWriter.Write(b)
}

func (w *statusResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// MetricsHandler exposes the Prometheus metrics endpoint.
func (m *GatewayMetrics) MetricsHandler() http.Handler {
	return promhttp.HandlerFor(m.Registry, promhttp.HandlerOpts{
		EnableOpenMetrics: true,
	})
}

// FranzGoMetricsHook implements franz-go telemetry hooks for Prometheus metrics.
type FranzGoMetricsHook struct {
	metrics *GatewayMetrics
}

// NewFranzGoMetricsHook creates a new franz-go telemetry hook associated with GatewayMetrics.
func NewFranzGoMetricsHook(m *GatewayMetrics) *FranzGoMetricsHook {
	return &FranzGoMetricsHook{metrics: m}
}

// OnBrokerConnect implements kgo.HookBrokerConnect.
func (h *FranzGoMetricsHook) OnBrokerConnect(meta kgo.BrokerMetadata, dialDur time.Duration, conn net.Conn, err error) {
	if h != nil && h.metrics != nil && err == nil {
		h.metrics.BrokersConnections.Inc()
	}
}

// OnBrokerDisconnect implements kgo.HookBrokerDisconnect.
func (h *FranzGoMetricsHook) OnBrokerDisconnect(meta kgo.BrokerMetadata, conn net.Conn) {
	if h != nil && h.metrics != nil {
		h.metrics.BrokersConnections.Dec()
	}
}

// OnProduceRecordUnbuffered implements kgo.HookProduceRecordUnbuffered.
func (h *FranzGoMetricsHook) OnProduceRecordUnbuffered(r *kgo.Record, err error) {
	if h == nil {
		return
	}
	valueBytes := 0
	if r != nil {
		valueBytes = len(r.Value)
	}
	h.metrics.RecordProducedRecord(valueBytes, err)
}

var (
	_ kgo.HookBrokerConnect           = (*FranzGoMetricsHook)(nil)
	_ kgo.HookBrokerDisconnect        = (*FranzGoMetricsHook)(nil)
	_ kgo.HookProduceRecordUnbuffered = (*FranzGoMetricsHook)(nil)
)
