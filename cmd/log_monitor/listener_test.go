// Copyright 2025 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package main

import (
	"context"
	"testing"
	"time"

	"golang/cloud_google_com/go/pubsub/v/v1/pstest/pstest"
	"golang/cloud_google_com/go/pubsub/v/v1/pubsub"
	"golang/google_api/option/option"
	"golang/grpc/credentials/insecure/insecure"
	"golang/grpc/grpc"
	"xmanager_cloud/storage/storage"
)

type MockLogProcessor struct {
	BatchCh chan int // Sends the size of the received batch
}

func (m *MockLogProcessor) ProcessBatch(ctx context.Context, batch []PubSubMessage) map[int64][]*storage.WorkUnitExecutionUpdate {
	// Non-blocking send to avoid hanging if the test isn't listening
	select {
	case m.BatchCh <- len(batch):
	default:
	}
	return nil
}

func TestListener_Start(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client, topic := setup(ctx, t)

	// Config
	batchSize := 2
	cfg := &Config{
		ProjectID:      "test-project",
		SubscriptionID: "test-sub",
		BatchSize:      batchSize,
		BatchInterval:  500 * time.Millisecond,
	}

	// Initialize with Channel-based Mock
	// Buffer channel so we don't block the listener
	lp := &MockLogProcessor{BatchCh: make(chan int, 10)}
	l := NewListener(cfg, lp, client)

	// Start Listener in background
	go l.Start(ctx)

	// --- Test Case: 3 messages -> Batch(2) then Batch(1) ---
	// Publish 3 messages.
	// First 2 should fill a batch immediately.
	// The 3rd should trigger after the interval.
	publishMsg(ctx, t, topic, "msg1")
	publishMsg(ctx, t, topic, "msg2")
	publishMsg(ctx, t, topic, "msg3")

	// Assertion 1: First batch should be size 2 (immediate trigger by size)
	select {
	case size := <-lp.BatchCh:
		if size != 2 {
			t.Errorf("First batch size = %d, want 2", size)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for first batch")
	}

	// Assertion 2: Second batch should be size 1 (trigger by interval)
	select {
	case size := <-lp.BatchCh:
		if size != 1 {
			t.Errorf("Second batch size = %d, want 1", size)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for second batch")
	}
}

func TestListener_IsHealthy(t *testing.T) {
	// Add timeout to prevent the test from hanging indefinitely.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client, _ := setup(ctx, t)

	cfg := &Config{
		ProjectID:      "test-project",
		SubscriptionID: "test-sub",
		BatchSize:      2,
		BatchInterval:  500 * time.Millisecond,
	}

	lp := &MockLogProcessor{BatchCh: make(chan int, 10)}
	l := NewListener(cfg, lp, client)

	if got := l.IsHealthy(); got {
		t.Errorf("IsHealthy() = %t, want false", got)
	}

	startCtx, startCancel := context.WithCancel(ctx)

	done := make(chan struct{})
	go func() {
		l.Start(startCtx)
		close(done)
	}()

	// Wait for listener to become healthy.
	timeout := time.After(2 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	healthy := false
WaitHealthy:
	for {
		select {
		case <-ticker.C:
			if l.IsHealthy() {
				healthy = true
				break WaitHealthy
			}
		case <-timeout:
			break WaitHealthy
		}
	}

	if !healthy {
		t.Fatalf("IsHealthy() = %t, want true (timed out waiting for healthy)", healthy)
	}

	startCancel()

	// The TestListener_IsHealthy test case verifies that the listener starts and transitions to a
	// healthy state. However, it does not fully test the graceful shutdown and transition back to
	// an unhealthy state upon context cancellation. This is because the pstest fake Pub/Sub server's
	// StreamingPull implementation does not reliably return when the client-side context provided to
	// subscription.Receive() is cancelled. Attempts to force stream closure within the test were
	// unsuccessful. As a result, the l.Start() goroutine does not exit cleanly in the test,
	// preventing verification of the final IsHealthy() state. A timeout is used in the test to
	// prevent it from hanging indefinitely.
	select {
	case <-done:
		// Listener successfully shut down.
	case <-time.After(500 * time.Millisecond):
		// Expected timeout due to pstest issue.
	}
}

func setup(ctx context.Context, t *testing.T) (*pubsub.Client, *pubsub.Topic) {
	srv := pstest.NewServer()
	t.Cleanup(func() { srv.Close() })

	conn, err := grpc.NewClient(srv.Addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("Failed to dial fake server: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	client, err := pubsub.NewClient(ctx, "test-project", option.WithGRPCConn(conn))
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	t.Cleanup(func() { client.Close() })

	topic, err := client.CreateTopic(ctx, "test-topic")
	if err != nil {
		t.Fatalf("CreateTopic failed: %v", err)
	}
	_, err = client.CreateSubscription(ctx, "test-sub", pubsub.SubscriptionConfig{Topic: topic})
	if err != nil {
		t.Fatalf("CreateSubscription failed: %v", err)
	}

	return client, topic
}

func publishMsg(ctx context.Context, t *testing.T, topic *pubsub.Topic, data string) {
	res := topic.Publish(ctx, &pubsub.Message{Data: []byte(data)})
	if _, err := res.Get(ctx); err != nil {
		t.Fatalf("Failed to publish: %v", err)
	}
}
