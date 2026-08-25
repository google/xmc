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
	"time"

	"cloud.google.com/go/pubsub"
	"google.golang.org/api/option"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// publishDelayThreshold mirrors kgo.ProducerLinger in BuildProducerOptions so both backends trade
// the same amount of latency for batching.
const publishDelayThreshold = 15 * time.Millisecond

// publishByteThreshold mirrors kgo.ProducerBatchMaxBytes in BuildProducerOptions.
const publishByteThreshold = 1 * 1024 * 1024

// PubSubProducer publishes metric records to a Google Cloud Pub/Sub topic.
//
// Message ordering is always enabled: each record is published under an ordering key equal to its
// MetricRecord.Key, giving the same per-(xid, work_unit_id) FIFO guarantee that the Kafka backend
// gets from sticky-key partitioning. End-to-end ordering additionally requires the subscription to
// enable message ordering, which is outside this service's control.
type PubSubProducer struct {
	client    *pubsub.Client
	topic     *pubsub.Topic
	projectID string
	topicID   string
	metrics   *GatewayMetrics
}

var _ MetricProducer = (*PubSubProducer)(nil)

// NewPubSubProducer constructs a Pub/Sub backed producer for cfg.TopicName in cfg.ProjectID.
//
// Credentials come from Application Default Credentials. The variadic opts exist so tests can
// inject a pstest connection via option.WithGRPCConn; production passes none. It does not contact
// the service; NewMetricProducer probes the topic separately.
func NewPubSubProducer(ctx context.Context, cfg *Config, gMetrics *GatewayMetrics, opts ...option.ClientOption) (*PubSubProducer, error) {
	client, err := pubsub.NewClient(ctx, cfg.ProjectID, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create pub/sub client for project %q: %w", cfg.ProjectID, err)
	}

	topic := client.Topic(cfg.TopicName)
	// Must be set before the first Publish: publishing with an ordering key on a topic that has not
	// enabled ordering is rejected outright.
	topic.EnableMessageOrdering = true
	topic.PublishSettings.DelayThreshold = publishDelayThreshold
	topic.PublishSettings.ByteThreshold = publishByteThreshold

	return &PubSubProducer{
		client:    client,
		topic:     topic,
		projectID: cfg.ProjectID,
		topicID:   cfg.TopicName,
		metrics:   gMetrics,
	}, nil
}

// newValidatedPubSubProducer builds a Pub/Sub producer and asserts the destination topic exists
// before it is handed any traffic.
func newValidatedPubSubProducer(ctx context.Context, cfg *Config, gMetrics *GatewayMetrics) (*PubSubProducer, error) {
	producer, err := NewPubSubProducer(ctx, cfg, gMetrics)
	if err != nil {
		return nil, err
	}
	if err := producer.ValidateTopic(ctx); err != nil {
		producer.Close()
		return nil, err
	}
	return producer, nil
}

// ValidateTopic asserts the configured topic exists and is visible to the caller.
//
// This is the Pub/Sub equivalent of ValidateTopicTopology: it turns a missing topic or a missing
// pubsub.topics.get permission into a startup failure instead of a per-request error. Note that
// roles/pubsub.publisher alone does not grant pubsub.topics.get.
func (p *PubSubProducer) ValidateTopic(ctx context.Context) error {
	exists, err := p.topic.Exists(ctx)
	if err != nil {
		return fmt.Errorf("failed to query pub/sub topic %q: %w", p.topicID, err)
	}
	if !exists {
		return fmt.Errorf("required pub/sub topic %q does not exist in project %q", p.topicID, p.projectID)
	}
	return nil
}

// Produce publishes rec asynchronously, invoking promise with a classified *PublishError on failure.
//
// One goroutine is spawned per record to await the publish result, which the franz-go backend does
// not need. The handler bounds this by waiting on all in-flight publishes before responding, so the
// goroutine count is bounded by concurrent requests times batch size.
func (p *PubSubProducer) Produce(ctx context.Context, rec *MetricRecord, promise func(*MetricRecord, error)) {
	orderingKey := string(rec.Key)
	result := p.topic.Publish(ctx, &pubsub.Message{
		Data:        rec.Value,
		OrderingKey: orderingKey,
	})

	go func() {
		_, err := result.Get(ctx)
		if err != nil {
			// Pub/Sub suspends an ordering key permanently after any publish failure. Without this
			// call a single transient error would black-hole one work unit's metrics for the
			// lifetime of the process, invisibly to aggregate error rates.
			p.topic.ResumePublish(orderingKey)
		}
		p.metrics.RecordProducedRecord(len(rec.Value), err)
		promise(rec, classifyPubSubError(err))
	}()
}

// Close flushes buffered publishes and releases the underlying client.
func (p *PubSubProducer) Close() {
	p.topic.Stop()
	p.client.Close()
}

// classifyPubSubError tags a Pub/Sub publish error with its backend-neutral kind, mapping onto the
// same HTTP contract the Kafka backend produces.
//
// NotFound, PermissionDenied, and Unauthenticated are deliberately left unknown (HTTP 500): the
// fault is a broken deployment, not the caller's payload, and startup validation already rejects
// both at boot.
func classifyPubSubError(err error) error {
	if err == nil {
		return nil
	}
	// Payload too large is the equivalent of kerr.MessageTooLarge, which IsClientProduceError treats
	// as a client fault.
	if errors.Is(err, pubsub.ErrOversizedMessage) {
		return newPublishError(PublishErrorClient, err)
	}
	// Shutdown in progress is the equivalent of kgo.ErrClientClosed, which IsCongestionError treats
	// as transient.
	if errors.Is(err, pubsub.ErrTopicStopped) {
		return newPublishError(PublishErrorCongestion, err)
	}

	switch status.Code(err) {
	case codes.ResourceExhausted, codes.Unavailable, codes.DeadlineExceeded, codes.Aborted:
		return newPublishError(PublishErrorCongestion, err)
	case codes.InvalidArgument, codes.FailedPrecondition, codes.OutOfRange:
		return newPublishError(PublishErrorClient, err)
	}
	return newPublishError(PublishErrorUnknown, err)
}
