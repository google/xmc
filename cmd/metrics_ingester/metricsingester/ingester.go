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
	"sync/atomic"
	"time"

	"cloud.google.com/go/pubsub"
	"google.golang.org/protobuf/proto"
	pb "xmanager_cloud/api/protos/metrics"
	"xmanager_cloud/internal/logging"
)

const (
	// writeTimeout bounds each ClickHouse batch insert so a stalled connection cannot block the
	// batch loop indefinitely, while detaching in-flight writes from SIGTERM cancellation.
	writeTimeout = 10 * time.Second
	// defaultRetryInitial and defaultRetryMax bound exponential backoff after a failed write so
	// an unreachable ClickHouse does not spin through Receive -> PrepareBatch -> Nack at line rate.
	defaultRetryInitial = 250 * time.Millisecond
	defaultRetryMax     = 30 * time.Second
)

// shutdownFlushTimeout bounds the final insert attempted when Run's context is cancelled, keeping
// shutdown well inside the default 30s Kubernetes termination grace period.
const shutdownFlushTimeout = 10 * time.Second

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
	source          MessageSource
	writer          MetricWriter
	batchSize       int
	batchInterval   time.Duration
	writeTimeout    time.Duration
	retryInitial    time.Duration
	retryMax        time.Duration
	droppedMessages atomic.Uint64
	running         atomic.Bool
}

// NewIngester returns an Ingester that batches according to cfg.
func NewIngester(source MessageSource, writer MetricWriter, cfg *Config) *Ingester {
	return &Ingester{
		source:        source,
		writer:        writer,
		batchSize:     cfg.BatchSize,
		batchInterval: cfg.BatchInterval,
		writeTimeout:  writeTimeout,
		retryInitial:  defaultRetryInitial,
		retryMax:      defaultRetryMax,
	}
}

// DroppedMessages returns the number of messages acked and dropped because their payload failed
// protobuf wire unmarshaling.
func (in *Ingester) DroppedMessages() uint64 {
	return in.droppedMessages.Load()
}

// IsRunning reports whether Run's receive loop is active.
func (in *Ingester) IsRunning() bool {
	return in.running.Load()
}

// Run receives and writes messages until ctx is done or the source fails permanently, returning the
// source's error. Every message it received is acked or nacked before it returns.
func (in *Ingester) Run(ctx context.Context) error {
	in.running.Store(true)
	defer in.running.Store(false)

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
// on ctx.Done this loop must settle its pending batch itself rather than wait for msgCh to close.
func (in *Ingester) batchLoop(ctx context.Context, msgCh <-chan *pubsub.Message) {
	ticker := time.NewTicker(in.batchInterval)
	defer ticker.Stop()

	var pending []*pubsub.Message
	var backoff time.Duration
	for {
		select {
		case msg, ok := <-msgCh:
			if !ok {
				in.flushBounded(ctx, pending)
				return
			}
			pending = append(pending, msg)
			if len(pending) >= in.batchSize {
				if !in.flushOrStop(ctx, msgCh, pending, &backoff) {
					return
				}
				pending = nil
				ticker.Reset(in.batchInterval)
			}
		case <-ticker.C:
			if !in.flushOrStop(ctx, msgCh, pending, &backoff) {
				return
			}
			pending = nil
		case <-ctx.Done():
			in.flushOnShutdown(ctx, drainBuffered(msgCh, pending))
			nackUntilClosed(msgCh)
			return
		}
	}
}

// flushOrStop flushes pending and backs off on write failure, returning false once shutdown has
// begun and msgCh is drained. Checking ctx.Err() first avoids flushing when select chose msgCh or
// ticker over an already-ready ctx.Done.
func (in *Ingester) flushOrStop(ctx context.Context, msgCh <-chan *pubsub.Message, pending []*pubsub.Message, backoff *time.Duration) bool {
	if ctx.Err() != nil {
		nackAll(pending)
		nackUntilClosed(msgCh)
		return false
	}
	if len(pending) == 0 {
		return true
	}
	if in.flushBounded(ctx, pending) {
		*backoff = 0
		return true
	}
	*backoff = nextBackoff(*backoff, in.retryInitial, in.retryMax)
	if !waitBackoff(ctx, *backoff) {
		nackUntilClosed(msgCh)
		return false
	}
	return true
}

// flushBounded runs flush on a context detached from Run's cancellation and bounded by writeTimeout,
// so SIGTERM does not abort an in-flight ClickHouse insert mid-Send.
func (in *Ingester) flushBounded(ctx context.Context, msgs []*pubsub.Message) bool {
	if len(msgs) == 0 {
		return true
	}
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), in.writeTimeout)
	defer cancel()
	return in.flush(writeCtx, msgs)
}

// flushOnShutdown writes the final batch with a fresh, bounded context, since ctx is already done.
func (in *Ingester) flushOnShutdown(ctx context.Context, pending []*pubsub.Message) {
	flushCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownFlushTimeout)
	defer cancel()
	in.flush(flushCtx, pending)
}

// flush writes msgs as one batch, acking them on success and nacking them for redelivery on failure.
// It reports whether the batch settled without a write error.
func (in *Ingester) flush(ctx context.Context, msgs []*pubsub.Message) bool {
	if len(msgs) == 0 {
		return true
	}
	metrics, decoded := in.decodeBatch(ctx, msgs)
	if len(decoded) == 0 {
		return true
	}
	if err := in.writer.Write(ctx, metrics); err != nil {
		logging.Error(ctx, "Failed to write metrics batch, nacking for redelivery", "messages", len(decoded), "error", err)
		nackAll(decoded)
		return false
	}
	for _, msg := range decoded {
		msg.Ack()
	}
	return true
}

// decodeBatch unmarshals msgs, returning the metrics alongside the messages they came from.
//
// A payload that fails wire unmarshaling will fail on every redelivery, so it is acked and dropped
// (unbounded, unlike kafka_skip_broken_messages, which caps skips at 100 per block). Note that
// proto3 unmarshaling is lenient, so corrupt bytes that still form valid wire tags decode into a
// near-empty Metric and are filtered out silently by xmc_metrics_pubsub_mv.
func (in *Ingester) decodeBatch(ctx context.Context, msgs []*pubsub.Message) ([]*pb.Metric, []*pubsub.Message) {
	metrics := make([]*pb.Metric, 0, len(msgs))
	decoded := make([]*pubsub.Message, 0, len(msgs))
	for _, msg := range msgs {
		m := &pb.Metric{}
		if err := proto.Unmarshal(msg.Data, m); err != nil {
			dropped := in.droppedMessages.Add(1)
			logging.Warn(ctx, "Dropping malformed metric message", "message_id", msg.ID, "dropped_total", dropped, "error", err)
			msg.Ack()
			continue
		}
		metrics = append(metrics, m)
		decoded = append(decoded, msg)
	}
	return metrics, decoded
}

func nextBackoff(cur, initial, maxDelay time.Duration) time.Duration {
	if cur <= 0 {
		return min(initial, maxDelay)
	}
	return min(cur*2, maxDelay)
}

func waitBackoff(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// nackAll nacks msgs for redelivery.
func nackAll(msgs []*pubsub.Message) {
	for _, msg := range msgs {
		msg.Nack()
	}
}

// drainBuffered appends the messages already buffered in msgCh to pending without blocking, so the
// final flush includes everything the receive callbacks handed over before shutdown began.
func drainBuffered(msgCh <-chan *pubsub.Message, pending []*pubsub.Message) []*pubsub.Message {
	for {
		select {
		case msg, ok := <-msgCh:
			if !ok {
				return pending
			}
			pending = append(pending, msg)
		default:
			return pending
		}
	}
}

// nackUntilClosed nacks messages that reach msgCh after shutdown began, until Run closes it.
func nackUntilClosed(msgCh <-chan *pubsub.Message) {
	for msg := range msgCh {
		msg.Nack()
	}
}
