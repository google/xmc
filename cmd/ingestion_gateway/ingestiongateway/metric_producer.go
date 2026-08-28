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
	"context"
	"errors"
	"fmt"
	"strconv"
)

// Supported message bus backends selectable via the -message_bus flag.
const (
	// MessageBusRedpanda publishes metrics over the Kafka protocol to Redpanda.
	MessageBusRedpanda = "redpanda"
	// MessageBusPubSub publishes metrics to a Google Cloud Pub/Sub topic.
	MessageBusPubSub = "pubsub"
)

// errUnsupportedMessageBus reports a backend that is neither supported message bus.
//
// Config validation and producer construction both reject one, and an operator reading either
// message should be told the same thing.
func errUnsupportedMessageBus(bus string) error {
	return fmt.Errorf("unsupported message_bus %q: must be one of %q, %q", bus, MessageBusRedpanda, MessageBusPubSub)
}

// MetricRecord is a backend-neutral metrics message.
//
// Key carries the composite (xid, work_unit_id) identifier that both backends use to preserve
// per-work-unit FIFO ordering: Redpanda partitions by it, Pub/Sub orders by it.
type MetricRecord struct {
	Topic string
	Key   []byte
	Value []byte
}

// MetricProducer publishes metric records asynchronously to the configured message bus.
//
// Produce must not block on broker acknowledgement. It reports the outcome of each record by
// invoking promise exactly once, passing a *PublishError so callers can map failures to an HTTP
// status without knowing which backend produced them.
type MetricProducer interface {
	Produce(ctx context.Context, rec *MetricRecord, promise func(*MetricRecord, error))
	Close()
}

// PublishErrorKind classifies a publish failure independently of the message bus backend.
type PublishErrorKind int

const (
	// PublishErrorUnknown is an unclassified failure and maps to HTTP 500.
	PublishErrorUnknown PublishErrorKind = iota
	// PublishErrorCongestion is a transient saturation, throttling, or availability failure and
	// maps to HTTP 503.
	PublishErrorCongestion
	// PublishErrorClient is caused by an invalid or oversized payload and maps to HTTP 400.
	PublishErrorClient
)

// PublishError wraps a backend publish failure with its backend-neutral classification.
type PublishError struct {
	Kind PublishErrorKind
	Err  error
}

func (e *PublishError) Error() string {
	if e.Err == nil {
		return "publish failed"
	}
	return e.Err.Error()
}

// Unwrap exposes the underlying backend error to errors.Is and errors.As.
func (e *PublishError) Unwrap() error {
	return e.Err
}

// newPublishError classifies err, returning nil when there is nothing to report.
func newPublishError(kind PublishErrorKind, err error) error {
	if err == nil {
		return nil
	}
	return &PublishError{Kind: kind, Err: err}
}

// publishErrorKind reports the classification a backend attached to err.
//
// An error that is not a *PublishError is treated as PublishErrorUnknown: a backend that failed to
// classify a failure should surface as a server-side fault, never as a caller error.
func publishErrorKind(err error) PublishErrorKind {
	var pubErr *PublishError
	if errors.As(err, &pubErr) {
		return pubErr.Kind
	}
	return PublishErrorUnknown
}

// FormatCompositeKey formats the composite partitioning key (xid, work_unit_id).
func FormatCompositeKey(xid, workUnitID int64) []byte {
	buf := make([]byte, 0, 32)
	buf = strconv.AppendInt(buf, xid, 10)
	buf = append(buf, ':')
	buf = strconv.AppendInt(buf, workUnitID, 10)
	return buf
}

// NewMetricProducer constructs the message bus backend selected by cfg.MessageBus and verifies
// its destination topic before returning.
//
// It returns an interface rather than a concrete struct because the backend is chosen at runtime;
// each backend constructor still returns its own type.
func NewMetricProducer(ctx context.Context, cfg *Config, gMetrics *GatewayMetrics) (MetricProducer, error) {
	if cfg == nil {
		return nil, errors.New("config cannot be nil")
	}
	if cfg.messageBusOrDefault() == MessageBusPubSub {
		producer, err := newValidatedPubSubProducer(ctx, cfg, gMetrics)
		if err != nil {
			return nil, err
		}
		return producer, nil
	}
	if cfg.messageBusOrDefault() == MessageBusRedpanda {
		producer, err := newValidatedKafkaProducer(ctx, cfg, gMetrics)
		if err != nil {
			return nil, err
		}
		return producer, nil
	}
	return nil, errUnsupportedMessageBus(cfg.MessageBus)
}
