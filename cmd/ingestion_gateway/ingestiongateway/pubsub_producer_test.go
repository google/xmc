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
	"strings"
	"sync"
	"testing"
	"time"

	"cloud.google.com/go/pubsub"
	"cloud.google.com/go/pubsub/apiv1/pubsubpb"
	"cloud.google.com/go/pubsub/pstest"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

const (
	testProjectID = "test-project"
	testTopicID   = "xmc-metrics"
)

// newFakePubSubProducer starts an in-process Pub/Sub fake, creates topicID on it, and returns a
// producer wired to it.
//
// Assertions read srv.Messages() directly rather than pulling from a subscription: the producer
// side never needs StreamingPull, so the fake's documented cancellation quirks do not apply here.
func newFakePubSubProducer(ctx context.Context, t *testing.T, topicID string, gMetrics *GatewayMetrics) (*PubSubProducer, *pstest.Server) {
	t.Helper()

	srv := pstest.NewServer()
	t.Cleanup(func() { srv.Close() })

	conn, err := grpc.NewClient(srv.Addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("failed to dial the pub/sub fake: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	cfg := &Config{
		MessageBus: MessageBusPubSub,
		ProjectID:  testProjectID,
		TopicName:  testTopicID,
	}
	producer, err := NewPubSubProducer(ctx, cfg, gMetrics, option.WithGRPCConn(conn))
	if err != nil {
		t.Fatalf("failed to create the Pub/Sub producer: %v", err)
	}
	t.Cleanup(producer.Close)

	// Created after the producer so callers can omit it and exercise the missing-topic path.
	if topicID != "" {
		if _, err := producer.client.CreateTopic(ctx, topicID); err != nil {
			t.Fatalf("failed to create topic %q on the fake: %v", topicID, err)
		}
	}

	return producer, srv
}

// produceAndWait publishes rec and returns the error the promise reported.
func produceAndWait(t *testing.T, producer *PubSubProducer, rec *MetricRecord) error {
	t.Helper()
	done := make(chan error, 1)
	producer.Produce(context.Background(), rec, func(gotRec *MetricRecord, err error) {
		if gotRec != rec {
			t.Errorf("expected the promise to receive the original record")
		}
		done <- err
	})
	select {
	case err := <-done:
		return err
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for the publish promise")
		return nil
	}
}

// TestNewPubSubProducer_EnablesMessageOrdering guards the trap that publishing with an ordering key
// on a topic that has not enabled ordering is rejected outright.
func TestNewPubSubProducer_EnablesMessageOrdering(t *testing.T) {
	producer, _ := newFakePubSubProducer(context.Background(), t, testTopicID, nil)

	if !producer.topic.EnableMessageOrdering {
		t.Errorf("expected EnableMessageOrdering to be set before any publish")
	}
}

func TestPubSubProducer_ProducePublishesPayloadWithOrderingKey(t *testing.T) {
	producer, srv := newFakePubSubProducer(context.Background(), t, testTopicID, nil)

	rec := &MetricRecord{
		Topic: testTopicID,
		Key:   FormatCompositeKey(101, 1),
		Value: []byte("serialized-metric"),
	}
	if err := produceAndWait(t, producer, rec); err != nil {
		t.Fatalf("unexpected publish error: %v", err)
	}

	msgs := srv.Messages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 published message, got %d", len(msgs))
	}
	if got := string(msgs[0].Data); got != "serialized-metric" {
		t.Errorf("published data = %q, want %q", got, "serialized-metric")
	}
	if got := msgs[0].OrderingKey; got != "101:1" {
		t.Errorf("ordering key = %q, want %q", got, "101:1")
	}
}

// TestPubSubProducer_ProducePreservesPerKeyOrder publishes without awaiting each result, the way
// the ingestion handler does, so the fake observes whatever order the ordering key produced.
func TestPubSubProducer_ProducePreservesPerKeyOrder(t *testing.T) {
	producer, srv := newFakePubSubProducer(context.Background(), t, testTopicID, nil)

	payloads := []string{"step-1", "step-2", "step-3"}
	var wg sync.WaitGroup
	for _, payload := range payloads {
		rec := &MetricRecord{Topic: testTopicID, Key: FormatCompositeKey(101, 1), Value: []byte(payload)}
		wg.Add(1)
		producer.Produce(context.Background(), rec, func(_ *MetricRecord, err error) {
			defer wg.Done()
			if err != nil {
				t.Errorf("unexpected publish error for %q: %v", payload, err)
			}
		})
	}
	wg.Wait()

	msgs := srv.Messages()
	if len(msgs) != len(payloads) {
		t.Fatalf("expected %d published messages, got %d", len(payloads), len(msgs))
	}
	for i, want := range payloads {
		if got := string(msgs[i].Data); got != want {
			t.Errorf("message %d = %q, want %q", i, got, want)
		}
	}
}

// TestPubSubProducer_ProduceResumesPublishAfterFailure is the regression test for the ordering-key
// wedge: Pub/Sub suspends an ordering key after any publish failure, so without ResumePublish every
// later record for that work unit would fail for the lifetime of the process.
func TestPubSubProducer_ProduceResumesPublishAfterFailure(t *testing.T) {
	producer, srv := newFakePubSubProducer(context.Background(), t, testTopicID, nil)

	// InvalidArgument is outside the publisher's retry set, so exactly one response is consumed.
	srv.SetAutoPublishResponse(false)
	srv.AddPublishResponse(nil, status.Error(codes.InvalidArgument, "injected publish failure"))

	key := FormatCompositeKey(101, 1)
	err := produceAndWait(t, producer, &MetricRecord{Topic: testTopicID, Key: key, Value: []byte("first")})
	if err == nil {
		t.Fatalf("expected the injected publish failure to be reported")
	}
	var pubErr *PublishError
	if !errors.As(err, &pubErr) {
		t.Fatalf("expected a *PublishError, got %T: %v", err, err)
	}
	if pubErr.Kind != PublishErrorClient {
		t.Errorf("kind = %v, want %v for InvalidArgument", pubErr.Kind, PublishErrorClient)
	}

	srv.SetAutoPublishResponse(true)
	if err := produceAndWait(t, producer, &MetricRecord{Topic: testTopicID, Key: key, Value: []byte("second")}); err != nil {
		t.Fatalf("publishing to a previously failed ordering key must succeed after ResumePublish, got: %v", err)
	}
}

// TestPubSubProducer_ProduceResumesPublishAfterCallerCancels covers the harder half of the
// ordering-key wedge. A client that disconnects mid-request cancels the context the promise waits
// on, so the producer learns of a cancellation while the publish is still in flight. Resuming on
// that alone is a no-op, and the failure that arrives afterwards would suspend the key for good.
func TestPubSubProducer_ProduceResumesPublishAfterCallerCancels(t *testing.T) {
	producer, srv := newFakePubSubProducer(context.Background(), t, testTopicID, nil)

	// Every response is queued explicitly. Leaving the fake on auto-response would let it answer
	// the in-flight publish successfully before the injected failure is queued, and the ordering
	// key would never be suspended at all.
	srv.SetAutoPublishResponse(false)

	ctx, cancel := context.WithCancel(context.Background())
	key := FormatCompositeKey(101, 1)
	done := make(chan error, 1)
	producer.Produce(ctx, &MetricRecord{Topic: testTopicID, Key: key, Value: []byte("first")}, func(_ *MetricRecord, err error) {
		done <- err
	})
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected the promise to report the caller's cancellation, got: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for the publish promise")
	}

	// Only now does the in-flight publish fail, suspending the ordering key. InvalidArgument is
	// outside the publisher's retry set, so it consumes exactly this one response and the success
	// queued behind it is left for the first probe that finds the key resumed.
	srv.AddPublishResponse(nil, status.Error(codes.InvalidArgument, "injected publish failure"))
	srv.AddPublishResponse(&pubsubpb.PublishResponse{MessageIds: []string{"resumed"}}, nil)

	waitUntilOrderingKeyPublishable(t, producer, key)
}

// waitUntilOrderingKeyPublishable polls until key accepts a publish again. The resume runs on a
// goroutine detached from the cancelled request, so it is not ordered against the caller.
//
// The probe deliberately goes through topic.Publish rather than PubSubProducer.Produce: Produce
// resumes the key itself on any error, which would heal the wedge the poll is trying to observe.
func waitUntilOrderingKeyPublishable(t *testing.T, producer *PubSubProducer, key []byte) {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		result := producer.topic.Publish(context.Background(), &pubsub.Message{
			Data:        []byte("second"),
			OrderingKey: string(key),
		})
		if _, lastErr = result.Get(context.Background()); lastErr == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("ordering key %q was never resumed after the detached publish failure, last error: %v", key, lastErr)
}

func TestPubSubProducer_ProduceRecordsTelemetry(t *testing.T) {
	gMetrics := NewGatewayMetrics(nil)
	producer, _ := newFakePubSubProducer(context.Background(), t, testTopicID, gMetrics)

	rec := &MetricRecord{Topic: testTopicID, Key: FormatCompositeKey(7, 2), Value: []byte("payload")}
	if err := produceAndWait(t, producer, rec); err != nil {
		t.Fatalf("unexpected publish error: %v", err)
	}

	if got := testutil.ToFloat64(gMetrics.ProduceRecordsTotal); got != 1 {
		t.Errorf("ProduceRecordsTotal = %v, want 1", got)
	}
	if got := testutil.ToFloat64(gMetrics.ProduceErrorsTotal); got != 0 {
		t.Errorf("ProduceErrorsTotal = %v, want 0", got)
	}
}

func TestPubSubProducer_ValidateTopic(t *testing.T) {
	tests := []struct {
		name          string
		createdTopic  string
		wantErr       bool
		wantErrSubstr string
	}{
		{name: "Existing topic passes validation", createdTopic: testTopicID, wantErr: false},
		{name: "Missing topic fails validation", createdTopic: "", wantErr: true, wantErrSubstr: "does not exist"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			producer, _ := newFakePubSubProducer(ctx, t, tt.createdTopic, nil)

			err := producer.ValidateTopic(ctx)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateTopic() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErrSubstr != "" && !errContains(err, tt.wantErrSubstr) {
				t.Errorf("ValidateTopic() error = %v, want it to mention %q", err, tt.wantErrSubstr)
			}
		})
	}
}

func TestClassifyPubSubError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantKind PublishErrorKind
		wantNil  bool
	}{
		{name: "nil error stays nil", err: nil, wantNil: true},
		{name: "ResourceExhausted is congestion", err: status.Error(codes.ResourceExhausted, "quota"), wantKind: PublishErrorCongestion},
		{name: "Unavailable is congestion", err: status.Error(codes.Unavailable, "transient"), wantKind: PublishErrorCongestion},
		{name: "DeadlineExceeded is congestion", err: status.Error(codes.DeadlineExceeded, "slow"), wantKind: PublishErrorCongestion},
		{name: "Aborted is congestion", err: status.Error(codes.Aborted, "contention"), wantKind: PublishErrorCongestion},
		{name: "topic stopped is congestion", err: pubsub.ErrTopicStopped, wantKind: PublishErrorCongestion},
		{name: "InvalidArgument is a client fault", err: status.Error(codes.InvalidArgument, "bad message"), wantKind: PublishErrorClient},
		{name: "FailedPrecondition is a client fault", err: status.Error(codes.FailedPrecondition, "precondition"), wantKind: PublishErrorClient},
		{name: "OutOfRange is a client fault", err: status.Error(codes.OutOfRange, "range"), wantKind: PublishErrorClient},
		{name: "oversized message is a client fault", err: pubsub.ErrOversizedMessage, wantKind: PublishErrorClient},
		{name: "NotFound is an operator fault", err: status.Error(codes.NotFound, "topic gone"), wantKind: PublishErrorUnknown},
		{name: "PermissionDenied is an operator fault", err: status.Error(codes.PermissionDenied, "no iam"), wantKind: PublishErrorUnknown},
		{name: "Unauthenticated is an operator fault", err: status.Error(codes.Unauthenticated, "no creds"), wantKind: PublishErrorUnknown},
		{name: "Internal is unknown", err: status.Error(codes.Internal, "boom"), wantKind: PublishErrorUnknown},
		{name: "plain error is unknown", err: errors.New("something else"), wantKind: PublishErrorUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyPubSubError(tt.err)
			if tt.wantNil {
				if got != nil {
					t.Fatalf("classifyPubSubError(nil) = %v, want nil", got)
				}
				return
			}
			var pubErr *PublishError
			if !errors.As(got, &pubErr) {
				t.Fatalf("expected a *PublishError, got %T", got)
			}
			if pubErr.Kind != tt.wantKind {
				t.Errorf("classifyPubSubError(%v) kind = %v, want %v", tt.err, pubErr.Kind, tt.wantKind)
			}
		})
	}
}

func errContains(err error, substr string) bool {
	return err != nil && strings.Contains(err.Error(), substr)
}
