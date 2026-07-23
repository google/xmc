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
	"errors"
	"fmt"
	"strings"
	"testing"

	"xmanager_cloud/pkg/apis/metrics"
)

func TestDecodeAndValidateMetrics_EmptyPayload(t *testing.T) {
	res := DecodeAndValidateMetrics(strings.NewReader("   \n\t  "), nil)
	if res.Err == nil {
		t.Fatal("expected error for empty payload, got nil")
	}
	if !strings.Contains(res.Err.Error(), "empty payload") {
		t.Errorf("unexpected error message: %v", res.Err)
	}
}

func TestDecodeAndValidateMetrics_WhitespaceHandling(t *testing.T) {
	payload := "   \n\t  [{\"xid\": 1, \"work_unit_id\": 1, \"execution_id\": 1, \"replica_id\": \"rep-0\", \"metric_name\": \"loss\", \"metric_value\": 0.1, \"step\": 1, \"timestamp\": 100}]"
	res := DecodeAndValidateMetrics(strings.NewReader(payload), nil)
	if res.Err != nil {
		t.Fatalf("unexpected error: %v", res.Err)
	}
	if res.ValidCount != 1 {
		t.Errorf("expected ValidCount 1, got %d", res.ValidCount)
	}
}

func TestDecodeAndValidateMetrics_ConsumerCallback(t *testing.T) {
	payload := `[
		{"xid": 1, "work_unit_id": 1, "execution_id": 1, "replica_id": "rep-0", "metric_name": "m1", "metric_value": 1.0, "step": 1, "timestamp": 100},
		{"xid": 1, "work_unit_id": 1, "execution_id": 1, "replica_id": "rep-0", "metric_name": "m2", "metric_value": 2.0, "step": 2, "timestamp": 100}
	]`

	var consumedNames []string
	consumer := func(m *metrics.Metric) error {
		consumedNames = append(consumedNames, m.MetricName)
		return nil
	}

	res := DecodeAndValidateMetrics(strings.NewReader(payload), consumer)
	if res.Err != nil {
		t.Fatalf("unexpected error: %v", res.Err)
	}
	if res.ValidCount != 2 {
		t.Errorf("expected ValidCount 2, got %d", res.ValidCount)
	}
	if len(consumedNames) != 2 || consumedNames[0] != "m1" || consumedNames[1] != "m2" {
		t.Errorf("unexpected consumed metrics: %v", consumedNames)
	}
}

func TestDecodeAndValidateMetrics_ConsumerErrorAbort(t *testing.T) {
	payload := `[
		{"xid": 1, "work_unit_id": 1, "execution_id": 1, "replica_id": "rep-0", "metric_name": "m1", "metric_value": 1.0, "step": 1, "timestamp": 100},
		{"xid": 1, "work_unit_id": 1, "execution_id": 1, "replica_id": "rep-0", "metric_name": "m2", "metric_value": 2.0, "step": 2, "timestamp": 100}
	]`

	consumerErr := errors.New("kafka publish failed")
	consumer := func(m *metrics.Metric) error {
		if m.MetricName == "m2" {
			return consumerErr
		}
		return nil
	}

	res := DecodeAndValidateMetrics(strings.NewReader(payload), consumer)
	if res.Err == nil {
		t.Fatal("expected error when consumer fails, got nil")
	}
	if !strings.Contains(res.Err.Error(), "kafka publish failed") {
		t.Errorf("unexpected error message: %v", res.Err)
	}
}

func TestDecodeAndValidateMetrics_MalformedArrayClosing(t *testing.T) {
	payload := `[{"xid": 1, "work_unit_id": 1, "execution_id": 1, "replica_id": "rep-0", "metric_name": "m1", "metric_value": 1.0, "step": 1, "timestamp": 100}` // missing closing ]

	res := DecodeAndValidateMetrics(strings.NewReader(payload), nil)
	if res.Err == nil {
		t.Fatal("expected error for unclosed array, got nil")
	}
	if !strings.Contains(res.Err.Error(), "malformed JSON") {
		t.Errorf("unexpected error message: %v", res.Err)
	}
}

func TestDecodeAndValidateMetrics_DeterministicO1Streaming(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteString("[")
	totalMetrics := 10000
	for i := 0; i < totalMetrics; i++ {
		if i > 0 {
			buf.WriteString(",")
		}
		fmt.Fprintf(&buf, `{"xid": 1, "work_unit_id": 1, "execution_id": 1, "replica_id": "rep-0", "metric_name": "metric_%d", "metric_value": %f, "step": %d, "timestamp": 1700000000}`, i, float64(i)*0.1, i)
	}
	buf.WriteString("]")

	var activeCount int
	var maxActiveCount int

	consumer := func(m *metrics.Metric) error {
		activeCount++
		if activeCount > maxActiveCount {
			maxActiveCount = activeCount
		}
		activeCount--
		return nil
	}

	res := DecodeAndValidateMetrics(&buf, consumer)
	if res.Err != nil {
		t.Fatalf("unexpected error during streaming: %v", res.Err)
	}
	if res.ValidCount != totalMetrics {
		t.Errorf("expected ValidCount %d, got %d", totalMetrics, res.ValidCount)
	}
	if maxActiveCount != 1 {
		t.Errorf("expected deterministic O(1) streaming (max 1 active metric at a time), got max %d active metrics", maxActiveCount)
	}
}
