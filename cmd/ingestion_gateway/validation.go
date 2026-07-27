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
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	pb "xmanager_cloud/api/protos/metrics"
	"xmanager_cloud/pkg/apis/metrics"
)

// FailedRecord represents a single record validation failure diagnostic.
type FailedRecord struct {
	Index      int    `json:"index"`
	MetricName string `json:"metric_name"`
	Reason     string `json:"reason"`
}

// ValidationErrorPayload represents the structured HTTP 422 JSON response body.
type ValidationErrorPayload struct {
	Error         string         `json:"error"`
	FailedRecords []FailedRecord `json:"failed_records"`
}

// BadRequestErrorPayload represents the HTTP 400 JSON error response.
type BadRequestErrorPayload struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

// MetricConsumer processes each valid metric inline during streaming to avoid accumulating
// metric slices and preserve an O(1) memory footprint.
type MetricConsumer func(*metrics.Metric) error

// StreamDecodeResult contains the outcome of stream decoding and validation.
type StreamDecodeResult struct {
	ValidCount int
	Validation *ValidationErrorPayload
	Err        error
}

// DecodeAndValidateMetrics performs pure, side-effect-free streaming token decoding and metric field validation.
// Operational logging for metrics ingestion lifecycle events is handled at the HTTP entrypoint in handler.go.
func DecodeAndValidateMetrics(r io.Reader, consumer MetricConsumer) StreamDecodeResult {
	bufReader := bufio.NewReader(r)
	if err := skipWhitespace(bufReader); err != nil {
		return StreamDecodeResult{Err: fmt.Errorf("bad request: %w", err)}
	}

	// Peek the first non-whitespace byte to distinguish JSON arrays ([...])
	// from NDJSON object sequences without consuming tokenizer state.
	firstByte, err := bufReader.Peek(1)
	if err != nil {
		return StreamDecodeResult{Err: fmt.Errorf("bad request: %w", err)}
	}

	dec := json.NewDecoder(bufReader)
	if firstByte[0] == '[' {
		return decodeJSONArray(dec, consumer)
	}
	return decodeJSONSequence(dec, consumer)
}

func skipWhitespace(r *bufio.Reader) error {
	for {
		b, err := r.Peek(1)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return errors.New("empty payload")
			}
			return err
		}
		if b[0] == ' ' || b[0] == '\t' || b[0] == '\r' || b[0] == '\n' {
			r.ReadByte()
			continue
		}
		return nil
	}
}

type jsonMetricInput struct {
	Xid         json.Number `json:"xid"`
	WorkUnitId  json.Number `json:"work_unit_id"`
	ExecutionId json.Number `json:"execution_id"`
	ReplicaId   string      `json:"replica_id"`
	MetricName  string      `json:"metric_name"`
	MetricValue float64     `json:"metric_value"`
	Step        int64       `json:"step"`
	Timestamp   int64       `json:"timestamp"`
}

func (input *jsonMetricInput) toProto(pbMetric *pb.Metric) error {
	if string(input.Xid) != "" {
		val, err := input.Xid.Int64()
		if err != nil {
			return fmt.Errorf("invalid xid value %q: %w", input.Xid, err)
		}
		pbMetric.Xid = val
	}

	if string(input.WorkUnitId) != "" {
		val, err := input.WorkUnitId.Int64()
		if err != nil {
			return fmt.Errorf("invalid work_unit_id value %q: %w", input.WorkUnitId, err)
		}
		pbMetric.WorkUnitId = val
	}

	if string(input.ExecutionId) != "" {
		val, err := input.ExecutionId.Int64()
		if err != nil {
			return fmt.Errorf("invalid execution_id value %q: %w", input.ExecutionId, err)
		}
		pbMetric.ExecutionId = val
	}

	pbMetric.ReplicaId = input.ReplicaId
	pbMetric.MetricName = input.MetricName
	pbMetric.MetricValue = input.MetricValue
	pbMetric.Step = input.Step
	pbMetric.Timestamp = input.Timestamp
	return nil
}

func processNextRecord(dec *json.Decoder, idx int, consumer MetricConsumer) (bool, *FailedRecord, error) {
	var input jsonMetricInput
	if err := dec.Decode(&input); err != nil {
		return false, nil, err
	}

	var pbMetric pb.Metric
	if err := input.toProto(&pbMetric); err != nil {
		return false, nil, err
	}

	m := &metrics.Metric{Metric: &pbMetric}
	if valErr := m.IsValid(); valErr != nil {
		return false, &FailedRecord{
			Index:      idx,
			MetricName: m.MetricName,
			Reason:     valErr.Error(),
		}, nil
	}

	if consumer != nil {
		if err := consumer(m); err != nil {
			return false, nil, fmt.Errorf("failed to process metric at index %d: %w", idx, err)
		}
	}
	return true, nil, nil
}

func buildStreamDecodeResult(validCount int, failed []FailedRecord) StreamDecodeResult {
	if len(failed) > 0 {
		return StreamDecodeResult{
			ValidCount: validCount,
			Validation: &ValidationErrorPayload{
				Error:         "unprocessable_entity",
				FailedRecords: failed,
			},
		}
	}
	return StreamDecodeResult{ValidCount: validCount}
}

func decodeJSONArray(dec *json.Decoder, consumer MetricConsumer) StreamDecodeResult {
	t, err := dec.Token()
	if err != nil || t != json.Delim('[') {
		return StreamDecodeResult{Err: fmt.Errorf("malformed JSON: expected array opening '['")}
	}

	validCount := 0
	var failed []FailedRecord
	idx := 0

	for dec.More() {
		valid, failedRec, err := processNextRecord(dec, idx, consumer)
		if err != nil {
			return StreamDecodeResult{Err: fmt.Errorf("malformed JSON at record index %d: %w", idx, err)}
		}
		if failedRec != nil {
			failed = append(failed, *failedRec)
		} else if valid {
			validCount++
		}
		idx++
	}

	tClose, err := dec.Token()
	if err != nil || tClose != json.Delim(']') {
		return StreamDecodeResult{Err: fmt.Errorf("malformed JSON: expected array closing ']'")}
	}

	return buildStreamDecodeResult(validCount, failed)
}

// decodeJSONSequence loops dec.Decode() until io.EOF because dec.More() only checks
// array/object element boundaries, not top-level NDJSON stream boundaries.
func decodeJSONSequence(dec *json.Decoder, consumer MetricConsumer) StreamDecodeResult {
	validCount := 0
	var failed []FailedRecord
	idx := 0

	for {
		valid, failedRec, err := processNextRecord(dec, idx, consumer)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return StreamDecodeResult{Err: fmt.Errorf("malformed JSON at record index %d: %w", idx, err)}
		}
		if failedRec != nil {
			failed = append(failed, *failedRec)
		} else if valid {
			validCount++
		}
		idx++
	}

	return buildStreamDecodeResult(validCount, failed)
}
