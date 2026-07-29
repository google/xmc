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
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/protobuf/proto"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/pkg/apis/metrics"
)

const (
	// MaxDrainBytes is the maximum amount of remaining request body to drain on error.
	MaxDrainBytes = 5 * 1024 * 1024 // 5MB
	// DefaultTopicName is the fallback Kafka topic for metrics ingestion.
	DefaultTopicName = "xmc-metrics"
)

// ServiceUnavailableErrorPayload represents the HTTP 503 JSON error response.
type ServiceUnavailableErrorPayload struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

// NewMetricsIngestionHandler constructs an http.HandlerFunc that produces metrics asynchronously to Kafka/Redpanda.
func NewMetricsIngestionHandler(producer MetricProducer, topicName string, gMetrics *GatewayMetrics) http.HandlerFunc {
	if topicName == "" {
		topicName = DefaultTopicName
	}
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if r.Method != http.MethodPost {
			logging.Warn(ctx, "Method not allowed for metrics ingestion", "method", r.Method)
			if gMetrics != nil {
				gMetrics.RecordDroppedPayload("method_not_allowed")
			}
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}

		if producer == nil {
			logging.Error(ctx, "Metrics ingestion request failed due to missing producer")
			if gMetrics != nil {
				gMetrics.RecordDroppedPayload("missing_producer")
			}
			writeJSONResponse(w, r, http.StatusServiceUnavailable, ServiceUnavailableErrorPayload{
				Error:   "service_unavailable",
				Message: "producer is not initialized",
			})
			return
		}

		var (
			wg         sync.WaitGroup
			publishErr atomic.Pointer[error]
		)
		replicaIDHeader := r.Header.Get("X-Replica-ID")

		consumer := func(m *metrics.Metric) error {
			if err := ctx.Err(); err != nil {
				return err
			}

			// Early-abort optimization: stop decoding if a previous async write failed.
			if errPtr := publishErr.Load(); errPtr != nil {
				return *errPtr
			}

			if m == nil || m.Metric == nil {
				return fmt.Errorf("received nil metric record")
			}

			if m.ReplicaId == "" && replicaIDHeader != "" {
				m.ReplicaId = replicaIDHeader
			}

			valBytes, err := proto.Marshal(m.Metric)
			if err != nil {
				return fmt.Errorf("failed to marshal metric proto: %w", err)
			}

			record := &kgo.Record{
				Topic: topicName,
				Key:   FormatCompositeKey(m.Xid, m.WorkUnitId),
				Value: valBytes,
			}

			wg.Add(1)
			producer.Produce(ctx, record, func(r *kgo.Record, err error) {
				defer wg.Done()
				if err != nil {
					errCopy := fmt.Errorf("async produce error: %w", err)
					publishErr.CompareAndSwap(nil, &errCopy)
				}
			})

			return nil
		}

		res := DecodeAndValidateMetrics(r.Body, consumer)

		// Wait for all in-flight async writes to complete before responding.
		wg.Wait()

		// If request context was canceled or timed out, log and respond with an appropriate error status
		// to prevent returning a misleading 200 OK to clients if the connection is still open (e.g., during server shutdown or timeouts).
		if ctx.Err() != nil {
			logging.Info(ctx, "Metrics ingestion request canceled or timed out", "error", ctx.Err().Error())
			statusCode := http.StatusServiceUnavailable
			errType := "service_unavailable"
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				statusCode = http.StatusGatewayTimeout
				errType = "gateway_timeout"
			}
			if gMetrics != nil {
				gMetrics.RecordDroppedPayload(errType)
			}
			writeJSONResponse(w, r, statusCode, ServiceUnavailableErrorPayload{
				Error:   errType,
				Message: ctx.Err().Error(),
			})
			return
		}

		// If any async publish failed, evaluate error type before responding.
		if errPtr := publishErr.Load(); errPtr != nil {
			errVal := *errPtr
			if IsClientProduceError(errVal) {
				logging.Warn(ctx, "Metrics ingestion request failed due to invalid metric payload", "error", errVal.Error())
				if gMetrics != nil {
					gMetrics.RecordDroppedPayload("client_produce_error")
				}
				writeJSONResponse(w, r, http.StatusBadRequest, BadRequestErrorPayload{
					Error:   "bad_request",
					Message: errVal.Error(),
				})
				return
			}

			if IsCongestionError(errVal) {
				logging.Error(ctx, "Metrics ingestion request failed due to broker congestion or outage", "error", errVal.Error())
				if gMetrics != nil {
					gMetrics.RecordDroppedPayload("congestion_error")
				}
				writeJSONResponse(w, r, http.StatusServiceUnavailable, ServiceUnavailableErrorPayload{
					Error:   "service_unavailable",
					Message: errVal.Error(),
				})
				return
			}

			logging.Error(ctx, "Metrics ingestion request failed due to broker publish error", "error", errVal.Error())
			if gMetrics != nil {
				gMetrics.RecordDroppedPayload("broker_publish_error")
			}
			writeJSONResponse(w, r, http.StatusInternalServerError, ServiceUnavailableErrorPayload{
				Error:   "internal_server_error",
				Message: errVal.Error(),
			})
			return
		}

		if res.Err != nil {
			logging.Warn(ctx, "Metrics ingestion request failed with decoding error", "error", res.Err.Error())
			if gMetrics != nil {
				gMetrics.RecordDroppedPayload("decode_error")
			}
			writeJSONResponse(w, r, http.StatusBadRequest, BadRequestErrorPayload{
				Error:   "bad_request",
				Message: res.Err.Error(),
			})
			return
		}

		if res.Validation != nil {
			logging.Warn(ctx, "Metrics ingestion request contained record validation errors",
				"failed_count", len(res.Validation.FailedRecords),
				"valid_count", res.ValidCount,
			)
			if gMetrics != nil {
				gMetrics.RecordDroppedPayload("validation_error")
			}
			writeJSONResponse(w, r, http.StatusUnprocessableEntity, res.Validation)
			return
		}

		logging.Info(ctx, "Metrics batch successfully accepted", "valid_count", res.ValidCount)
		writeJSONResponse(w, r, http.StatusAccepted, map[string]any{
			"status": "accepted",
			"count":  res.ValidCount,
		})
	}
}

func writeJSONResponse(w http.ResponseWriter, r *http.Request, statusCode int, payload any) {
	if statusCode >= 400 && r != nil {
		drainRequestBody(r.Body, MaxDrainBytes)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(payload)
}

func drainRequestBody(r io.Reader, maxBytes int64) {
	if r == nil {
		return
	}
	io.Copy(io.Discard, io.LimitReader(r, maxBytes))
}
