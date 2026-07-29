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
	BrokersConnections         prometheus.Gauge
	ProduceBatchSizeBytes      prometheus.Histogram
	ProduceRecordsTotal        prometheus.Counter
	ProduceErrorsTotal         prometheus.Counter
	ProduceRetriesTotal        prometheus.Counter
	ProduceQueueLatencySeconds prometheus.Histogram
}

// NewGatewayMetrics initializes and registers all Prometheus metrics with the given registry.
func NewGatewayMetrics(reg *prometheus.Registry) *GatewayMetrics {
	if reg == nil {
		reg = prometheus.NewRegistry()
	}

	m := &GatewayMetrics{
		Registry: reg,

		HTTPRequestsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "xmc_ingestion_http_requests_total",
				Help: "Total number of HTTP requests processed by ingestion gateway.",
			},
			[]string{"status_code", "path", "method"},
		),

		HTTPRequestDurationSeconds: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "xmc_ingestion_http_request_duration_seconds",
				Help:    "HTTP request latency in seconds.",
				Buckets: httpDurationBuckets,
			},
			[]string{"status_code", "path", "method"},
		),

		ActiveConnections: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "xmc_ingestion_active_connections",
				Help: "Number of active HTTP connections.",
			},
		),

		DroppedPayloadsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "xmc_ingestion_dropped_payloads_total",
				Help: "Total number of dropped metric payloads.",
			},
			[]string{"reason"},
		),

		BrokersConnections: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "franz_go_brokers_connections",
				Help: "Number of active Franz-Go broker connections.",
			},
		),

		ProduceBatchSizeBytes: prometheus.NewHistogram(
			prometheus.HistogramOpts{
				Name:    "franz_go_produce_batch_size_bytes",
				Help:    "Size of produce batches in bytes.",
				Buckets: prometheus.ExponentialBuckets(1024, 2, 12),
			},
		),

		ProduceRecordsTotal: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "franz_go_produce_records_total",
				Help: "Total number of records produced to Kafka.",
			},
		),

		ProduceErrorsTotal: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "franz_go_produce_errors_total",
				Help: "Total number of produce errors.",
			},
		),

		ProduceRetriesTotal: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "franz_go_produce_retries_total",
				Help: "Total number of produce retries.",
			},
		),

		ProduceQueueLatencySeconds: prometheus.NewHistogram(
			prometheus.HistogramOpts{
				Name:    "franz_go_produce_queue_latency_seconds",
				Help:    "Latency of produce requests in queue in seconds.",
				Buckets: httpDurationBuckets,
			},
		),
	}

	reg.MustRegister(collectors.NewGoCollector())
	reg.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

	reg.MustRegister(m.HTTPRequestsTotal)
	reg.MustRegister(m.HTTPRequestDurationSeconds)
	reg.MustRegister(m.ActiveConnections)
	reg.MustRegister(m.DroppedPayloadsTotal)

	reg.MustRegister(m.BrokersConnections)
	reg.MustRegister(m.ProduceBatchSizeBytes)
	reg.MustRegister(m.ProduceRecordsTotal)
	reg.MustRegister(m.ProduceErrorsTotal)
	reg.MustRegister(m.ProduceRetriesTotal)
	reg.MustRegister(m.ProduceQueueLatencySeconds)

	return m
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

// HookBrokerConnect implements kgo.HookBrokerConnect.
func (h *FranzGoMetricsHook) HookBrokerConnect(meta kgo.BrokerMetadata) {
	if h != nil && h.metrics != nil {
		h.metrics.BrokersConnections.Inc()
	}
}

// HookBrokerDisconnect implements kgo.HookBrokerDisconnect.
func (h *FranzGoMetricsHook) HookBrokerDisconnect(meta kgo.BrokerMetadata) {
	if h != nil && h.metrics != nil {
		h.metrics.BrokersConnections.Dec()
	}
}

// HookProduceRecordUnbuffered implements kgo.HookProduceRecordUnbuffered.
func (h *FranzGoMetricsHook) HookProduceRecordUnbuffered(r *kgo.Record, err error) {
	if h == nil || h.metrics == nil {
		return
	}
	if r != nil && len(r.Value) > 0 {
		h.metrics.ProduceBatchSizeBytes.Observe(float64(len(r.Value)))
	}
	if err != nil {
		h.metrics.ProduceErrorsTotal.Inc()
	} else {
		h.metrics.ProduceRecordsTotal.Inc()
	}
}
