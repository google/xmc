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

// fakeWriter records each written batch, checks ctx.Err(), and fails the first failures calls.
// When set, beforeWrite runs at the start of every call, outside the lock.
type fakeWriter struct {
	mu           sync.Mutex
	batches      [][]*pb.Metric
	ctxErrs      []error
	hasDeadlines []bool
	failures     int
	written      chan struct{}
	beforeWrite  func()
	calls        atomic.Int32
}

func newFakeWriter(failures int) *fakeWriter {
	return &fakeWriter{failures: failures, written: make(chan struct{}, 100)}
}

func (w *fakeWriter) Write(ctx context.Context, metrics []*pb.Metric) error {
	w.calls.Add(1)
	if w.beforeWrite != nil {
		w.beforeWrite()
	}
	w.mu.Lock()
	defer func() {
		w.mu.Unlock()
		w.written <- struct{}{}
	}()
	_, hasDeadline := ctx.Deadline()
	ctxErr := ctx.Err()
	w.batches = append(w.batches, metrics)
	w.ctxErrs = append(w.ctxErrs, ctxErr)
	w.hasDeadlines = append(w.hasDeadlines, hasDeadline)
	if ctxErr != nil {
		return ctxErr
	}
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

func (w *fakeWriter) deadlines() []bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]bool(nil), w.hasDeadlines...)
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

// startConfiguredIngester runs in in the background and returns a function that stops it and
// returns Run's error.
func startConfiguredIngester(t *testing.T, in *Ingester) func() error {
	t.Helper()
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
	return stop
}

// startIngester constructs and runs an Ingester in the background.
func startIngester(t *testing.T, source MessageSource, writer MetricWriter, batchSize int, interval time.Duration) (*Ingester, func() error) {
	t.Helper()
	in := NewIngester(source, writer, &Config{BatchSize: batchSize, BatchInterval: interval})
	in.retryInitial = 5 * time.Millisecond
	in.retryMax = 20 * time.Millisecond
	return in, startConfiguredIngester(t, in)
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

// awaitModacks blocks until the server has received at least count modacks for message id.
func awaitModacks(t *testing.T, srv *pstest.Server, id string, count int) {
	t.Helper()
	deadline := time.Now().Add(waitTimeout)
	for len(srv.Message(id).Modacks) < count {
		if time.Now().After(deadline) {
			t.Errorf("timed out waiting for %d modacks on message %s, got %d", count, id, len(srv.Message(id).Modacks))
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// awaitNack blocks until pstest records a zero-deadline modack (Nack) for message id.
func awaitNack(t *testing.T, srv *pstest.Server, id string) {
	t.Helper()
	deadline := time.Now().Add(waitTimeout)
	for {
		for _, m := range srv.Message(id).Modacks {
			if m.AckDeadline == 0 {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for nack (AckDeadline=0) on message %s; modacks=%+v", id, srv.Message(id).Modacks)
		}
		time.Sleep(5 * time.Millisecond)
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
	in, stop := startIngester(t, sub, writer, 3, neverTick)

	ids := []string{publishMetric(t, srv, metricWithStep(1)), publishMetric(t, srv, metricWithStep(2)), publishMetric(t, srv, metricWithStep(3))}
	writer.awaitWrites(t, 1)
	awaitAcks(t, srv, ids...)

	if !in.IsRunning() {
		t.Error("IsRunning() = false while Run is active")
	}
	got := writer.snapshot()
	if len(got) != 1 || len(got[0]) != 3 {
		t.Fatalf("writes = %v, want one batch of 3", got)
	}
	if diff := cmp.Diff([]bool{true}, writer.deadlines()); diff != "" {
		t.Errorf("write deadlines mismatch (-want +got):\n%s", diff)
	}
	if err := stop(); err != nil {
		t.Errorf("Run() error = %v, want nil", err)
	}
	if in.IsRunning() {
		t.Error("IsRunning() = true after Run returned")
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
	in, _ := startIngester(t, sub, writer, 2, neverTick)

	onlyBad1 := srv.Publish(testTopicName, malformedPayload, nil)
	onlyBad2 := srv.Publish(testTopicName, malformedPayload, nil)
	awaitAcks(t, srv, onlyBad1, onlyBad2)

	badID := srv.Publish(testTopicName, malformedPayload, nil)
	goodID := publishMetric(t, srv, testMetric())
	writer.awaitWrites(t, 1)
	awaitAcks(t, srv, badID, goodID)

	if diff := cmp.Diff([][]*pb.Metric{{testMetric()}}, writer.snapshot(), protocmp.Transform()); diff != "" {
		t.Errorf("writes mismatch (-want +got):\n%s", diff)
	}
	if got := in.DroppedMessages(); got != 3 {
		t.Errorf("DroppedMessages() = %d, want 3", got)
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
	writer.beforeWrite = func() { awaitModacks(t, srv, id, 1) }
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

func TestIngesterBacksOffAfterFailedWrites(t *testing.T) {
	ctx := context.Background()
	srv, _, sub := newFakeSubscription(ctx, t)
	id := publishMetric(t, srv, testMetric())
	writer := newFakeWriter(2)
	// Each failed attempt adds 2 modacks (1 receipt lease + 1 zero-deadline nack), so attempt k waits
	// for 2k-1 modacks to ensure attempt k's receipt lease landed before nacking.
	writer.beforeWrite = func() { awaitModacks(t, srv, id, int(2*writer.calls.Load()-1)) }

	in := NewIngester(sub, writer, &Config{BatchSize: 1, BatchInterval: neverTick})
	in.retryInitial = 25 * time.Millisecond
	in.retryMax = 50 * time.Millisecond
	start := time.Now()
	startConfiguredIngester(t, in)

	writer.awaitWrites(t, 3)
	awaitAcks(t, srv, id)
	if elapsed := time.Since(start); elapsed < 70*time.Millisecond {
		t.Errorf("3 attempts completed in %v, want at least ~75ms of retry backoff (25ms + 50ms)", elapsed)
	}
}

func TestIngesterCancelsRetryBackoffOnShutdown(t *testing.T) {
	ctx := context.Background()
	srv, _, sub := newFakeSubscription(ctx, t)
	id := publishMetric(t, srv, testMetric())
	writer := newFakeWriter(1)
	writer.beforeWrite = func() { awaitModacks(t, srv, id, 1) }

	in := NewIngester(sub, writer, &Config{BatchSize: 1, BatchInterval: neverTick})
	in.retryInitial = time.Hour
	in.retryMax = time.Hour
	stop := startConfiguredIngester(t, in)

	writer.awaitWrites(t, 1)
	if err := stop(); err != nil {
		t.Fatalf("Run() error = %v, want nil when cancelled during retry backoff", err)
	}
}

func TestIngesterInFlightWriteSurvivesCancellation(t *testing.T) {
	ctx := context.Background()
	srv, _, sub := newFakeSubscription(ctx, t)
	gate := make(chan struct{})
	closeGate := sync.OnceFunc(func() { close(gate) })
	t.Cleanup(closeGate)
	writer := newFakeWriter(0)
	writer.beforeWrite = func() { <-gate }
	_, stop := startIngester(t, sub, writer, 1, neverTick)

	id := publishMetric(t, srv, testMetric())
	writer.awaitCalls(t, 1)

	stopped := make(chan error, 1)
	go func() { stopped <- stop() }()
	closeGate()

	if err := <-stopped; err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
	awaitAcks(t, srv, id)
	if diff := cmp.Diff([][]*pb.Metric{{testMetric()}}, writer.snapshot(), protocmp.Transform()); diff != "" {
		t.Errorf("writes mismatch (-want +got):\n%s", diff)
	}
}

func TestNextBackoff(t *testing.T) {
	tests := []struct {
		name     string
		cur      time.Duration
		initial  time.Duration
		maxDelay time.Duration
		want     time.Duration
	}{
		{name: "first failure uses initial", cur: 0, initial: 250 * time.Millisecond, maxDelay: 30 * time.Second, want: 250 * time.Millisecond},
		{name: "initial capped by max", cur: 0, initial: time.Minute, maxDelay: 30 * time.Second, want: 30 * time.Second},
		{name: "doubles below max", cur: 500 * time.Millisecond, initial: 250 * time.Millisecond, maxDelay: 30 * time.Second, want: time.Second},
		{name: "caps at max", cur: 20 * time.Second, initial: 250 * time.Millisecond, maxDelay: 30 * time.Second, want: 30 * time.Second},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := nextBackoff(tc.cur, tc.initial, tc.maxDelay); got != tc.want {
				t.Errorf("nextBackoff(%v, %v, %v) = %v, want %v", tc.cur, tc.initial, tc.maxDelay, got, tc.want)
			}
		})
	}
}

func TestWaitBackoff(t *testing.T) {
	if !waitBackoff(context.Background(), 0) {
		t.Error("waitBackoff(live, 0) = false, want true")
	}
	if !waitBackoff(context.Background(), time.Millisecond) {
		t.Error("waitBackoff(live, 1ms) = false, want true")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if waitBackoff(cancelled, 0) {
		t.Error("waitBackoff(cancelled, 0) = true, want false")
	}
	if waitBackoff(cancelled, time.Hour) {
		t.Error("waitBackoff(cancelled, 1h) = true, want false")
	}
}

func TestIngesterFlushesPendingBatchOnShutdown(t *testing.T) {
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

	if diff := cmp.Diff([][]*pb.Metric{{testMetric()}}, writer.snapshot(), protocmp.Transform()); diff != "" {
		t.Errorf("writes mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]bool{true}, writer.deadlines()); diff != "" {
		t.Errorf("shutdown write deadlines mismatch (-want +got):\n%s", diff)
	}
	awaitAcks(t, srv, id)
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
	if in.IsRunning() {
		t.Error("IsRunning() = true after Run returned")
	}
}

func TestDrainBuffered(t *testing.T) {
	msgCh := make(chan *pubsub.Message, 3)
	first, second := &pubsub.Message{ID: "1"}, &pubsub.Message{ID: "2"}
	msgCh <- second

	got := drainBuffered(msgCh, []*pubsub.Message{first})
	if len(got) != 2 || got[0] != first || got[1] != second {
		t.Errorf("drainBuffered() = %v, want [first second]", got)
	}

	close(msgCh)
	if got := drainBuffered(msgCh, nil); len(got) != 0 {
		t.Errorf("drainBuffered(closed) = %v, want empty", got)
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
	closeGate := sync.OnceFunc(func() { close(gate) })
	t.Cleanup(closeGate)
	writer := newFakeWriter(0)
	writer.beforeWrite = func() { <-gate }
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	source := &scriptedSource{script: func(ctx context.Context, f func(context.Context, *pubsub.Message)) error {
		f(ctx, metricMessage(t, "1", 1)) // Taken by the loop, whose write then blocks on gate.
		writer.awaitCalls(t, 1)
		f(ctx, metricMessage(t, "2", 2))       // Fills the one-slot buffer.
		f(cancelled, metricMessage(t, "3", 3)) // Buffer full and callback context done: nacked.
		closeGate()
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

func TestFlushOrStopFlushesPendingOnShutdownWhenContextDone(t *testing.T) {
	writer := newFakeWriter(0)
	in := NewIngester(nil, writer, &Config{BatchSize: 10, BatchInterval: neverTick})
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	msgCh := make(chan *pubsub.Message, 1)
	msgCh <- metricMessage(t, "2", 2)
	close(msgCh)

	var backoff time.Duration
	if in.flushOrStop(cancelled, msgCh, []*pubsub.Message{metricMessage(t, "1", 1)}, &backoff) {
		t.Error("flushOrStop(cancelled) = true, want false")
	}
	want := [][]*pb.Metric{{metricWithStep(1), metricWithStep(2)}}
	if diff := cmp.Diff(want, writer.snapshot(), protocmp.Transform()); diff != "" {
		t.Errorf("shutdown writes mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]bool{true}, writer.deadlines()); diff != "" {
		t.Errorf("shutdown write deadlines mismatch (-want +got):\n%s", diff)
	}
}

func TestFlushOnShutdownChunksByBatchSize(t *testing.T) {
	writer := newFakeWriter(0)
	in := NewIngester(nil, writer, &Config{BatchSize: 2, BatchInterval: neverTick})
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	in.flushOnShutdown(cancelled, nil)
	if got := writer.snapshot(); len(got) != 0 {
		t.Fatalf("flushOnShutdown(nil) writes = %v, want none", got)
	}

	in.flushOnShutdown(cancelled, []*pubsub.Message{
		metricMessage(t, "1", 1),
		metricMessage(t, "2", 2),
		metricMessage(t, "3", 3),
	})
	want := [][]*pb.Metric{{metricWithStep(1), metricWithStep(2)}, {metricWithStep(3)}}
	if diff := cmp.Diff(want, writer.snapshot(), protocmp.Transform()); diff != "" {
		t.Errorf("chunked shutdown writes mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]bool{true, true}, writer.deadlines()); diff != "" {
		t.Errorf("chunked shutdown deadlines mismatch (-want +got):\n%s", diff)
	}
}

func TestFlushEmptyIsNoOp(t *testing.T) {
	writer := newFakeWriter(0)
	in := NewIngester(nil, writer, &Config{BatchSize: 10, BatchInterval: neverTick})
	var backoff time.Duration
	if !in.flushOrStop(context.Background(), nil, nil, &backoff) {
		t.Error("flushOrStop(empty) = false, want true")
	}
	if !in.flushBounded(context.Background(), nil) {
		t.Error("flushBounded(empty) = false, want true")
	}
	if !in.flush(context.Background(), nil) {
		t.Error("flush(empty) = false, want true")
	}
	if got := writer.snapshot(); len(got) != 0 {
		t.Errorf("writes = %v, want none", got)
	}
}

// receiveLeasedMessages opens a persistent subscription stream and forwards each delivered message
// onto the returned channel.
func receiveLeasedMessages(t *testing.T, sub *pubsub.Subscription) <-chan *pubsub.Message {
	t.Helper()
	subCtx, stopSub := context.WithCancel(context.Background())
	leased := make(chan *pubsub.Message, 10)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = sub.Receive(subCtx, func(_ context.Context, msg *pubsub.Message) {
			leased <- msg
		})
	}()
	t.Cleanup(func() {
		stopSub()
		<-done
	})
	return leased
}

// takeLeasedMessage waits for the next message from ch and ensures its receipt modack landed.
func takeLeasedMessage(t *testing.T, srv *pstest.Server, ch <-chan *pubsub.Message) *pubsub.Message {
	t.Helper()
	select {
	case msg := <-ch:
		awaitModacks(t, srv, msg.ID, 1)
		return msg
	case <-time.After(waitTimeout):
		t.Fatal("timed out waiting for leased message")
		return nil
	}
}

func TestIngesterNacksMessagesArrivingAfterShutdownFlush(t *testing.T) {
	srv, _, sub := newFakeSubscription(context.Background(), t)
	leased := receiveLeasedMessages(t, sub)
	id1 := publishMetric(t, srv, metricWithStep(1))
	first1 := takeLeasedMessage(t, srv, leased)
	id2 := publishMetric(t, srv, metricWithStep(2))
	first2 := takeLeasedMessage(t, srv, leased)

	writer := newFakeWriter(0)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	source := &scriptedSource{script: func(rCtx context.Context, f func(context.Context, *pubsub.Message)) error {
		f(rCtx, first1)
		cancel()
		writer.awaitWrites(t, 1) // Wait for the shutdown flush of id1 after buffer drain.
		f(context.Background(), first2)
		return nil
	}}

	if err := NewIngester(source, writer, &Config{BatchSize: 100, BatchInterval: neverTick}).Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if diff := cmp.Diff([][]*pb.Metric{{metricWithStep(1)}}, writer.snapshot(), protocmp.Transform()); diff != "" {
		t.Errorf("writes mismatch (-want +got):\n%s", diff)
	}
	awaitAcks(t, srv, id1)
	awaitNack(t, srv, id2)
	if got := srv.Message(id2).Acks; got != 0 {
		t.Errorf("message %s Acks = %d, want 0", id2, got)
	}

	takeLeasedMessage(t, srv, leased).Ack()
	awaitAcks(t, srv, id2)
	if got := srv.Message(id2).Deliveries; got < 2 {
		t.Errorf("message %s Deliveries = %d, want at least 2 after nack", id2, got)
	}
}
