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
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"cloud.google.com/go/pubsub"
	"cloud.google.com/go/pubsub/pstest"
	"github.com/google/go-cmp/cmp"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
	pb "xmanager_cloud/api/protos/metrics"
)

const (
	testProjectID      = "test-project"
	testTopicID        = "xmc-metrics"
	testSubscriptionID = "xmc-metrics-ingester"
	testTopicName      = "projects/" + testProjectID + "/topics/" + testTopicID
	waitTimeout        = 10 * time.Second
	neverTick          = time.Hour
)

// malformedPayload is not valid protobuf wire format: a field tag whose varint is truncated.
var malformedPayload = []byte{0xff}

// fakeWriter records each written batch and fails the first failures calls. When set, beforeWrite
// runs at the start of every call, outside the lock.
type fakeWriter struct {
	mu          sync.Mutex
	batches     [][]*pb.Metric
	failures    int
	written     chan struct{}
	beforeWrite func()
	calls       atomic.Int32
}

func newFakeWriter(failures int) *fakeWriter {
	return &fakeWriter{failures: failures, written: make(chan struct{}, 100)}
}

func (w *fakeWriter) Write(_ context.Context, metrics []*pb.Metric) error {
	w.calls.Add(1)
	if w.beforeWrite != nil {
		w.beforeWrite()
	}
	w.mu.Lock()
	defer func() {
		w.mu.Unlock()
		w.written <- struct{}{}
	}()
	w.batches = append(w.batches, metrics)
	if len(w.batches) <= w.failures {
		return errors.New("clickhouse unavailable")
	}
	return nil
}

func (w *fakeWriter) snapshot() [][]*pb.Metric {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([][]*pb.Metric(nil), w.batches...)
}

// awaitCalls blocks until Write has been entered n times, even if those calls have not returned.
func (w *fakeWriter) awaitCalls(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(waitTimeout)
	for w.calls.Load() < int32(n) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d write calls, got %d", n, w.calls.Load())
		}
		time.Sleep(time.Millisecond)
	}
}

// awaitWrites blocks until the writer has been called n times in total.
func (w *fakeWriter) awaitWrites(t *testing.T, n int) {
	t.Helper()
	for len(w.snapshot()) < n {
		select {
		case <-w.written:
		case <-time.After(waitTimeout):
			t.Fatalf("timed out waiting for %d writes, got %d", n, len(w.snapshot()))
		}
	}
}

// signallingSource wraps a MessageSource and signals after each callback hands a message over.
type signallingSource struct {
	MessageSource
	handedOver chan struct{}
}

func (s *signallingSource) Receive(ctx context.Context, f func(context.Context, *pubsub.Message)) error {
	return s.MessageSource.Receive(ctx, func(cbCtx context.Context, msg *pubsub.Message) {
		f(cbCtx, msg)
		s.handedOver <- struct{}{}
	})
}

// newFakeSubscription starts a pstest server with the metrics topic and an ingester subscription.
func newFakeSubscription(ctx context.Context, t *testing.T) (*pstest.Server, *pubsub.Client, *pubsub.Subscription) {
	t.Helper()
	srv := pstest.NewServer()
	t.Cleanup(func() { srv.Close() })

	conn, err := grpc.NewClient(srv.Addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("failed to dial the pub/sub fake: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	client, err := pubsub.NewClient(ctx, testProjectID, option.WithGRPCConn(conn))
	if err != nil {
		t.Fatalf("failed to create pub/sub client: %v", err)
	}
	t.Cleanup(func() { client.Close() })

	topic, err := client.CreateTopic(ctx, testTopicID)
	if err != nil {
		t.Fatalf("failed to create topic: %v", err)
	}
	sub, err := client.CreateSubscription(ctx, testSubscriptionID, pubsub.SubscriptionConfig{Topic: topic, AckDeadline: 10 * time.Second})
	if err != nil {
		t.Fatalf("failed to create subscription: %v", err)
	}
	return srv, client, sub
}

func publishMetric(t *testing.T, srv *pstest.Server, m *pb.Metric) string {
	t.Helper()
	data, err := proto.Marshal(m)
	if err != nil {
		t.Fatalf("proto.Marshal() error = %v", err)
	}
	return srv.Publish(testTopicName, data, nil)
}

// startIngester runs an Ingester in the background and returns a function that stops it and
// returns Run's error.
func startIngester(t *testing.T, source MessageSource, writer MetricWriter, batchSize int, interval time.Duration) (*Ingester, func() error) {
	t.Helper()
	in := NewIngester(source, writer, &Config{BatchSize: batchSize, BatchInterval: interval})
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- in.Run(ctx) }()

	var once sync.Once
	var runErr error
	stop := func() error {
		once.Do(func() {
			cancel()
			select {
			case runErr = <-errCh:
			case <-time.After(waitTimeout):
				t.Fatal("timed out waiting for Run to return")
			}
		})
		return runErr
	}
	t.Cleanup(func() { _ = stop() })
	return in, stop
}

// awaitAcks blocks until the pstest message with id has been acked at least once.
func awaitAcks(t *testing.T, srv *pstest.Server, ids ...string) {
	t.Helper()
	deadline := time.Now().Add(waitTimeout)
	for _, id := range ids {
		for srv.Message(id).Acks == 0 {
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for message %s to be acked", id)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
}

// awaitModack blocks until the server has received at least one modack for the message with id.
func awaitModack(t *testing.T, srv *pstest.Server, id string) {
	t.Helper()
	deadline := time.Now().Add(waitTimeout)
	for len(srv.Message(id).Modacks) == 0 {
		if time.Now().After(deadline) {
			t.Errorf("timed out waiting for a modack on message %s", id)
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func metricWithStep(step int64) *pb.Metric {
	m := testMetric()
	m.Step = step
	return m
}

func TestIngesterWritesFullBatchAndAcks(t *testing.T) {
	ctx := context.Background()
	srv, _, sub := newFakeSubscription(ctx, t)
	writer := newFakeWriter(0)
	_, stop := startIngester(t, sub, writer, 3, neverTick)

	ids := []string{publishMetric(t, srv, metricWithStep(1)), publishMetric(t, srv, metricWithStep(2)), publishMetric(t, srv, metricWithStep(3))}
	writer.awaitWrites(t, 1)
	awaitAcks(t, srv, ids...)

	got := writer.snapshot()
	if len(got) != 1 || len(got[0]) != 3 {
		t.Fatalf("writes = %v, want one batch of 3", got)
	}
	if err := stop(); err != nil {
		t.Errorf("Run() error = %v, want nil", err)
	}
}

func TestIngesterFlushesPartialBatchOnInterval(t *testing.T) {
	ctx := context.Background()
	srv, _, sub := newFakeSubscription(ctx, t)
	writer := newFakeWriter(0)
	startIngester(t, sub, writer, 100, 20*time.Millisecond)

	id := publishMetric(t, srv, testMetric())
	writer.awaitWrites(t, 1)
	awaitAcks(t, srv, id)

	if diff := cmp.Diff([][]*pb.Metric{{testMetric()}}, writer.snapshot(), protocmp.Transform()); diff != "" {
		t.Errorf("writes mismatch (-want +got):\n%s", diff)
	}
}

func TestIngesterAcksAndSkipsMalformedMessages(t *testing.T) {
	ctx := context.Background()
	srv, _, sub := newFakeSubscription(ctx, t)
	writer := newFakeWriter(0)
	startIngester(t, sub, writer, 2, neverTick)

	badID := srv.Publish(testTopicName, malformedPayload, nil)
	goodID := publishMetric(t, srv, testMetric())
	writer.awaitWrites(t, 1)
	awaitAcks(t, srv, badID, goodID)

	if diff := cmp.Diff([][]*pb.Metric{{testMetric()}}, writer.snapshot(), protocmp.Transform()); diff != "" {
		t.Errorf("writes mismatch (-want +got):\n%s", diff)
	}
}

func TestIngesterNacksFailedWriteForRedelivery(t *testing.T) {
	ctx := context.Background()
	srv, _, sub := newFakeSubscription(ctx, t)
	id := publishMetric(t, srv, testMetric())
	writer := newFakeWriter(1)
	// The client library modacks each message on receipt, asynchronously. If the nack reaches the
	// server first, that receipt modack re-leases the message and redelivery waits out the whole ack
	// deadline. Holding the failing write until the receipt modack lands keeps the test fast.
	writer.beforeWrite = func() { awaitModack(t, srv, id) }
	startIngester(t, sub, writer, 1, neverTick)

	writer.awaitWrites(t, 2)
	awaitAcks(t, srv, id)

	want := [][]*pb.Metric{{testMetric()}, {testMetric()}}
	if diff := cmp.Diff(want, writer.snapshot(), protocmp.Transform()); diff != "" {
		t.Errorf("writes mismatch (-want +got):\n%s", diff)
	}
	if got := srv.Message(id).Deliveries; got < 2 {
		t.Errorf("Deliveries = %d, want at least 2 after a nack", got)
	}
}

// failingSource invokes f once per message and then fails permanently, as Receive does on a
// non-retryable service error.
type failingSource struct {
	msgs []*pubsub.Message
	err  error
}

func (s *failingSource) Receive(ctx context.Context, f func(context.Context, *pubsub.Message)) error {
	for _, msg := range s.msgs {
		f(ctx, msg)
	}
	return s.err
}

func TestIngesterReturnsSourceErrorAfterFlushing(t *testing.T) {
	data, err := proto.Marshal(testMetric())
	if err != nil {
		t.Fatalf("proto.Marshal() error = %v", err)
	}
	permissionDenied := errors.New("permission denied")
	source := &failingSource{msgs: []*pubsub.Message{{ID: "1", Data: data}}, err: permissionDenied}
	writer := newFakeWriter(0)
	in := NewIngester(source, writer, &Config{BatchSize: 100, BatchInterval: neverTick})

	if err := in.Run(context.Background()); !errors.Is(err, permissionDenied) {
		t.Fatalf("Run() error = %v, want %v", err, permissionDenied)
	}
	if diff := cmp.Diff([][]*pb.Metric{{testMetric()}}, writer.snapshot(), protocmp.Transform()); diff != "" {
		t.Errorf("writes mismatch (-want +got):\n%s", diff)
	}
}

// scriptedSource runs script as its Receive implementation, for driving exact shutdown orderings.
type scriptedSource struct {
	script func(ctx context.Context, f func(context.Context, *pubsub.Message)) error
}

func (s *scriptedSource) Receive(ctx context.Context, f func(context.Context, *pubsub.Message)) error {
	return s.script(ctx, f)
}

func metricMessage(t *testing.T, id string, step int64) *pubsub.Message {
	t.Helper()
	data, err := proto.Marshal(metricWithStep(step))
	if err != nil {
		t.Fatalf("proto.Marshal() error = %v", err)
	}
	return &pubsub.Message{ID: id, Data: data}
}

func TestIngesterNacksInsteadOfBlockingWhenCallbackContextIsDone(t *testing.T) {
	gate := make(chan struct{})
	writer := newFakeWriter(0)
	writer.beforeWrite = func() { <-gate }
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	source := &scriptedSource{script: func(ctx context.Context, f func(context.Context, *pubsub.Message)) error {
		f(ctx, metricMessage(t, "1", 1)) // Taken by the loop, whose write then blocks on gate.
		writer.awaitCalls(t, 1)
		f(ctx, metricMessage(t, "2", 2))       // Fills the one-slot buffer.
		f(cancelled, metricMessage(t, "3", 3)) // Buffer full and callback context done: nacked.
		close(gate)
		return nil
	}}

	if err := NewIngester(source, writer, &Config{BatchSize: 1, BatchInterval: neverTick}).Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	want := [][]*pb.Metric{{metricWithStep(1)}, {metricWithStep(2)}}
	if diff := cmp.Diff(want, writer.snapshot(), protocmp.Transform()); diff != "" {
		t.Errorf("writes mismatch (-want +got):\n%s", diff)
	}
}

func TestIngesterNacksPendingBatchOnShutdown(t *testing.T) {
	ctx := context.Background()
	srv, _, sub := newFakeSubscription(ctx, t)
	writer := newFakeWriter(0)
	source := &signallingSource{MessageSource: sub, handedOver: make(chan struct{}, 10)}
	_, stop := startIngester(t, source, writer, 100, neverTick)

	id := publishMetric(t, srv, testMetric())
	select {
	case <-source.handedOver:
	case <-time.After(waitTimeout):
		t.Fatal("timed out waiting for the message to be received")
	}
	if err := stop(); err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}

	if got := writer.snapshot(); len(got) != 0 {
		t.Errorf("writes = %v, want none: pending messages are nacked on shutdown", got)
	}
	if acks := srv.Message(id).Acks; acks != 0 {
		t.Errorf("Acks = %d, want 0 for a message nacked on shutdown", acks)
	}
}

func TestNackUntilClosedDrainsChannel(t *testing.T) {
	msgCh := make(chan *pubsub.Message, 2)
	msgCh <- &pubsub.Message{ID: "1"}
	msgCh <- &pubsub.Message{ID: "2"}
	close(msgCh)

	nackUntilClosed(msgCh)
	if n := len(msgCh); n != 0 {
		t.Errorf("len(msgCh) = %d after nackUntilClosed, want 0", n)
	}
}
