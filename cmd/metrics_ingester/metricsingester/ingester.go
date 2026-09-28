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

package metricsingester

import (
	"context"
	"time"

	"cloud.google.com/go/pubsub"
	"google.golang.org/protobuf/proto"
	pb "xmanager_cloud/api/protos/metrics"
	"xmanager_cloud/logging/logging"
)

// MessageSource delivers Pub/Sub messages to f until ctx is done. *pubsub.Subscription satisfies it.
type MessageSource interface {
	Receive(ctx context.Context, f func(context.Context, *pubsub.Message)) error
}

// MetricWriter persists a batch of decoded metrics, all or nothing.
type MetricWriter interface {
	Write(ctx context.Context, metrics []*pb.Metric) error
}

// Ingester pulls metric messages from a MessageSource, batches them, and writes each batch.
//
// Delivery is at-least-once: messages are acked only after their batch is written, and nacked for
// redelivery when the write fails. A crash between a successful write and the ack therefore
// re-inserts that batch, the same guarantee ClickHouse's Kafka engine gives the Redpanda path.
type Ingester struct {
	source        MessageSource
	writer        MetricWriter
	batchSize     int
	batchInterval time.Duration
}

// NewIngester returns an Ingester that batches according to cfg.
func NewIngester(source MessageSource, writer MetricWriter, cfg *Config) *Ingester {
	return &Ingester{
		source:        source,
		writer:        writer,
		batchSize:     cfg.BatchSize,
		batchInterval: cfg.BatchInterval,
	}
}

// Run receives and writes messages until ctx is done or the source fails permanently, returning the
// source's error. Every message it received is acked or nacked before it returns.
func (in *Ingester) Run(ctx context.Context) error {
	msgCh := make(chan *pubsub.Message, in.batchSize)
	loopDone := make(chan struct{})
	go func() {
		defer close(loopDone)
		in.batchLoop(ctx, msgCh)
	}()

	defer func() {
		close(msgCh)
		<-loopDone
	}()

	return in.source.Receive(ctx, func(cbCtx context.Context, msg *pubsub.Message) {
		select {
		case msgCh <- msg:
		case <-cbCtx.Done():
			msg.Nack()
		}
	})
}

// batchLoop accumulates messages and flushes when a batch fills or batchInterval passes since the
// last flush. Resetting the ticker after a size-triggered flush avoids a tiny follow-up insert.
//
// Receive does not return on cancellation until every outstanding message is acked or nacked, so
// on ctx.Done this loop must settle its pending batch itself rather than wait for msgCh to close. It
// nacks them, so Pub/Sub redelivers them to the next ingester.
func (in *Ingester) batchLoop(ctx context.Context, msgCh <-chan *pubsub.Message) {
	ticker := time.NewTicker(in.batchInterval)
	defer ticker.Stop()

	var pending []*pubsub.Message
	for {
		select {
		case msg, ok := <-msgCh:
			if !ok {
				in.flush(ctx, pending)
				return
			}
			pending = append(pending, msg)
			if len(pending) >= in.batchSize {
				in.flush(ctx, pending)
				pending = nil
				ticker.Reset(in.batchInterval)
			}
		case <-ticker.C:
			in.flush(ctx, pending)
			pending = nil
		case <-ctx.Done():
			nackAll(pending)
			nackUntilClosed(msgCh)
			return
		}
	}
}

// flush writes msgs as one batch, acking them on success and nacking them for redelivery on failure.
func (in *Ingester) flush(ctx context.Context, msgs []*pubsub.Message) {
	metrics, decoded := decodeBatch(ctx, msgs)
	if len(decoded) == 0 {
		return
	}
	if err := in.writer.Write(ctx, metrics); err != nil {
		logging.Error(ctx, "Failed to write metrics batch, nacking for redelivery", "messages", len(decoded), "error", err)
		nackAll(decoded)
		return
	}
	for _, msg := range decoded {
		msg.Ack()
	}
}

// decodeBatch unmarshals msgs, returning the metrics alongside the messages they came from.
//
// A payload that fails to unmarshal will fail on every redelivery, so it is acked and dropped here
// instead of being nacked into an endless retry loop. This mirrors kafka_skip_broken_messages on the
// Redpanda path. Semantic validation (zero IDs, bad timestamps) is left to xmc_metrics_pubsub_mv.
func decodeBatch(ctx context.Context, msgs []*pubsub.Message) ([]*pb.Metric, []*pubsub.Message) {
	metrics := make([]*pb.Metric, 0, len(msgs))
	decoded := make([]*pubsub.Message, 0, len(msgs))
	for _, msg := range msgs {
		m := &pb.Metric{}
		if err := proto.Unmarshal(msg.Data, m); err != nil {
			logging.Warn(ctx, "Dropping malformed metric message", "message_id", msg.ID, "error", err)
			msg.Ack()
			continue
		}
		metrics = append(metrics, m)
		decoded = append(decoded, msg)
	}
	return metrics, decoded
}

// nackAll nacks msgs for redelivery.
func nackAll(msgs []*pubsub.Message) {
	for _, msg := range msgs {
		msg.Nack()
	}
}

// nackUntilClosed nacks messages buffered in or still reaching msgCh after shutdown began, until
// Run closes it.
func nackUntilClosed(msgCh <-chan *pubsub.Message) {
	for msg := range msgCh {
		msg.Nack()
	}
}
