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

func TestFakeClientPublishAndSubscribe(t *testing.T) {
	fake := NewFakeClient("test-broadcast-stream")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	received := make(chan string, 1)
	go fake.Subscribe(ctx, "$", func(msg string) {
		received <- msg
	})

	// Brief wait for subscriber registration
	time.Sleep(50 * time.Millisecond)

	if err := fake.Publish(ctx, "hello-world"); err != nil {
		t.Fatalf("Publish() unexpected error: %v", err)
	}

	select {
	case msg := <-received:
		if msg != "hello-world" {
			t.Errorf("received message = %q, want %q", msg, "hello-world")
		}
	case <-time.After(time.Second):
		t.Fatal("handler did not receive published message")
	}

	msgs := fake.Messages()
	if len(msgs) != 1 || msgs[0] != "hello-world" {
		t.Errorf("Messages() = %v, want [\"hello-world\"]", msgs)
	}
}

func TestFakeClientMultipleSubscribers(t *testing.T) {
	fake := NewFakeClient("fanout-stream")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	received1 := make(chan string, 1)
	received2 := make(chan string, 1)

	go fake.Subscribe(ctx, "$", func(msg string) { received1 <- msg })
	go fake.Subscribe(ctx, "$", func(msg string) { received2 <- msg })

	time.Sleep(50 * time.Millisecond)

	if err := fake.Publish(ctx, "broadcast-payload"); err != nil {
		t.Fatalf("Publish() unexpected error: %v", err)
	}

	for i, ch := range []chan string{received1, received2} {
		select {
		case msg := <-ch:
			if msg != "broadcast-payload" {
				t.Errorf("subscriber %d received %q, want %q", i+1, msg, "broadcast-payload")
			}
		case <-time.After(time.Second):
			t.Fatalf("subscriber %d did not receive message", i+1)
		}
	}
}

func TestFakeClientSubscribeCancelAndNilHandler(t *testing.T) {
	fake := NewFakeClient("cancel-stream")

	// Nil handler should return immediately
	fake.Subscribe(context.Background(), "$", nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		fake.Subscribe(ctx, "$", func(msg string) {})
		close(done)
	}()

	cancel()
	select {
	case <-done:
		// Succeeded
	case <-time.After(time.Second):
		t.Fatal("Subscribe() did not exit on context cancel")
	}
}

func TestFakeClientPublishCanceledContext(t *testing.T) {
	fake := NewFakeClient("ctx-cancel-stream")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := fake.Publish(ctx, "should fail"); !errors.Is(err, context.Canceled) {
		t.Errorf("Publish() error = %v, want context.Canceled", err)
	}
}
