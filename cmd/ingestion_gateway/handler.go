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
	"encoding/json"
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
func NewMetricsIngestionHandler(producer MetricProducer, topicName string) http.HandlerFunc {
	if topicName == "" {
		topicName = DefaultTopicName
	}
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if r.Method != http.MethodPost {
			logging.Warn(ctx, "Method not allowed for metrics ingestion", "method", r.Method)
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}

		if producer == nil {
			logging.Error(ctx, "Metrics ingestion request failed due to missing producer")
			drainRequestBody(r.Body, MaxDrainBytes)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(ServiceUnavailableErrorPayload{
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

		// If any async publish failed, respond with 503 Service Unavailable.
		if errPtr := publishErr.Load(); errPtr != nil {
			logging.Error(ctx, "Metrics ingestion request failed due to broker publish error", "error", (*errPtr).Error())
			drainRequestBody(r.Body, MaxDrainBytes)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(ServiceUnavailableErrorPayload{
				Error:   "service_unavailable",
				Message: (*errPtr).Error(),
			})
			return
		}

		if res.Err != nil {
			logging.Warn(ctx, "Metrics ingestion request failed with decoding error", "error", res.Err.Error())
			drainRequestBody(r.Body, MaxDrainBytes)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(BadRequestErrorPayload{
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
			drainRequestBody(r.Body, MaxDrainBytes)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnprocessableEntity)
			json.NewEncoder(w).Encode(res.Validation)
			return
		}

		logging.Info(ctx, "Metrics batch successfully accepted", "valid_count", res.ValidCount)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(map[string]any{
			"status": "accepted",
			"count":  res.ValidCount,
		})
	}
}

func drainRequestBody(r io.Reader, maxBytes int64) {
	if r == nil {
		return
	}
	io.Copy(io.Discard, io.LimitReader(r, maxBytes))
}
