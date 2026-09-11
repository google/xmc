// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package broadcast

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPublisherStub(t *testing.T) {
	pub := NewPublisher(Config{Addr: "localhost:6379"}, "test-broadcast-stream")

	ctx := context.Background()
	if err := pub.Publish(ctx, "test message"); err != nil {
		t.Fatalf("Publish() unexpected error: %v", err)
	}

	msgs := pub.Messages()
	if len(msgs) != 1 || msgs[0] != "test message" {
		t.Errorf("Messages() = %v, want [\"test message\"]", msgs)
	}

	canceledCtx, cancel := context.WithCancel(ctx)
	cancel()
	if err := pub.Publish(canceledCtx, "test message"); !errors.Is(err, context.Canceled) {
		t.Errorf("Publish() error = %v, want context.Canceled", err)
	}
}

func TestConsumerStub(t *testing.T) {
	consumer := NewConsumer(Config{Addr: "localhost:6379"}, "test-broadcast-stream")

	// Verify nil handler does not block or panic
	consumer.Subscribe(context.Background(), "$", nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		consumer.Subscribe(ctx, "$", func(msg string) {})
		close(done)
	}()

	cancel()
	select {
	case <-done:
		// Subscribed consumer exited gracefully upon context cancellation
	case <-time.After(time.Second):
		t.Fatal("Subscribe() did not exit upon context cancellation")
	}
}
