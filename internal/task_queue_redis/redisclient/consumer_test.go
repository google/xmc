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

package redisclient

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// fakeRedisClient records the stream commands used by ExtendClaim and processMessage.
// Other Client methods are not implemented and panic if called.
type fakeRedisClient struct {
	Client
	mu        sync.Mutex
	claimArgs []*redis.XClaimArgs
	claimErr  error
	ackedIDs  []string
	addedArgs []*redis.XAddArgs
}

func (f *fakeRedisClient) XClaim(_ context.Context, a *redis.XClaimArgs) *redis.XMessageSliceCmd {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.claimArgs = append(f.claimArgs, a)
	return redis.NewXMessageSliceCmdResult(nil, f.claimErr)
}

func (f *fakeRedisClient) XAck(_ context.Context, _, _ string, ids ...string) *redis.IntCmd {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ackedIDs = append(f.ackedIDs, ids...)
	return redis.NewIntResult(int64(len(ids)), nil)
}

func (f *fakeRedisClient) XAdd(_ context.Context, a *redis.XAddArgs) *redis.StringCmd {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.addedArgs = append(f.addedArgs, a)
	return redis.NewStringResult("1-0", nil)
}

func newTestConsumer(processTimeout time.Duration) (*ConsumerClient, *fakeRedisClient) {
	fake := &fakeRedisClient{}
	client := &ConsumerClient{
		baseManager:    baseManager{Client: fake, StreamName: "test-stream"},
		GroupName:      "test-group",
		DLQStreamName:  "test-dlq",
		MaxRetries:     3,
		ProcessTimeout: processTimeout,
		TaskHandler:    defaultTaskHandler,
	}
	return client, fake
}

func TestExtendClaim_NoClaimInContext(t *testing.T) {
	client, _ := newTestConsumer(time.Second)

	err := client.ExtendClaim(context.Background())
	if err == nil || !strings.Contains(err.Error(), "no message claim found in context") {
		t.Errorf("ExtendClaim() error = %v, want 'no message claim found in context'", err)
	}
}

func TestExtendClaim_Success(t *testing.T) {
	client, fake := newTestConsumer(time.Second)

	extendChan := make(chan struct{}, 1)
	ctx := withClaim(context.Background(), claim{msgID: "msg-42", workerID: "worker-99", extendChan: extendChan})

	if err := client.ExtendClaim(ctx); err != nil {
		t.Fatalf("ExtendClaim() unexpected error: %v", err)
	}

	select {
	case <-extendChan:
	default:
		t.Fatal("extendChan was not signaled by ExtendClaim()")
	}

	if len(fake.claimArgs) != 1 {
		t.Fatalf("len(claimArgs) = %d, want 1", len(fake.claimArgs))
	}
	got := fake.claimArgs[0]
	if got.Stream != "test-stream" || got.Group != "test-group" || got.Consumer != "worker-99" || got.MinIdle != 0 || !slices.Equal(got.Messages, []string{"msg-42"}) {
		t.Errorf("XClaimArgs = %+v, want Stream=test-stream Group=test-group Consumer=worker-99 MinIdle=0 Messages=[msg-42]", got)
	}

	// Non-blocking when extendChan is already full.
	extendChan <- struct{}{}
	if err := client.ExtendClaim(ctx); err != nil {
		t.Fatalf("ExtendClaim() with full channel unexpected error: %v", err)
	}
}

func TestExtendClaim_RedisError(t *testing.T) {
	client, fake := newTestConsumer(time.Second)
	fake.claimErr = errors.New("simulated claim error")

	extendChan := make(chan struct{}, 1)
	ctx := withClaim(context.Background(), claim{msgID: "msg-1", workerID: "worker-1", extendChan: extendChan})

	err := client.ExtendClaim(ctx)
	if !errors.Is(err, fake.claimErr) {
		t.Fatalf("ExtendClaim() error = %v, want %v", err, fake.claimErr)
	}
	if !strings.Contains(err.Error(), "failed to extend claim for message msg-1") {
		t.Errorf("ExtendClaim() error = %q, want containing 'failed to extend claim for message msg-1'", err.Error())
	}

	select {
	case <-extendChan:
		t.Fatal("extendChan should not be signaled when redis claim fails")
	default:
	}
}

func TestProcessMessage_Success(t *testing.T) {
	client, fake := newTestConsumer(500 * time.Millisecond)

	var handlerCalled bool
	var received claim
	client.SetTaskHandler(func(ctx context.Context, workerID, data string) TaskResult {
		handlerCalled = true
		received, _ = claimFromContext(ctx)
		return TaskResult{WasRunSuccessful: true}
	})

	msg := redis.XMessage{
		ID:     "msg-101",
		Values: map[string]any{"data": "payload-data"},
	}

	client.processMessage(context.Background(), msg, "worker-1")

	if !handlerCalled {
		t.Fatal("task handler was not called")
	}
	if received.msgID != "msg-101" || received.workerID != "worker-1" || received.extendChan == nil {
		t.Errorf("claim in handler context = %+v, want msgID 'msg-101', workerID 'worker-1', non-nil extendChan", received)
	}
	if !slices.Equal(fake.ackedIDs, []string{"msg-101"}) {
		t.Errorf("ackedIDs = %v, want [msg-101]", fake.ackedIDs)
	}
}

func TestProcessMessage_ExtendClaim_PreventsTimeout(t *testing.T) {
	// Short timeout: 100ms. Handler runs for 120ms, extending claim at 60ms.
	client, fake := newTestConsumer(100 * time.Millisecond)

	var extended bool
	client.SetTaskHandler(func(ctx context.Context, workerID, data string) TaskResult {
		time.Sleep(60 * time.Millisecond)
		if err := client.ExtendClaim(ctx); err != nil {
			return TaskResult{Error: err}
		}
		extended = true
		time.Sleep(60 * time.Millisecond)
		return TaskResult{WasRunSuccessful: true}
	})

	msg := redis.XMessage{
		ID:     "msg-long-running",
		Values: map[string]any{"data": "batch-replay"},
	}

	start := time.Now()
	client.processMessage(context.Background(), msg, "worker-1")
	elapsed := time.Since(start)

	if !extended {
		t.Fatal("ExtendClaim was not called in handler")
	}
	if elapsed < 110*time.Millisecond {
		t.Errorf("elapsed = %v, want >= 110ms", elapsed)
	}
	if len(fake.claimArgs) != 1 {
		t.Errorf("len(claimArgs) = %d, want 1", len(fake.claimArgs))
	}
	if !slices.Equal(fake.ackedIDs, []string{"msg-long-running"}) {
		t.Errorf("ackedIDs = %v, want [msg-long-running]", fake.ackedIDs)
	}
	if len(fake.addedArgs) != 0 {
		t.Errorf("len(addedArgs) = %d, want 0 (no timeout retry triggered)", len(fake.addedArgs))
	}
}

func TestProcessMessage_Timeout(t *testing.T) {
	client, fake := newTestConsumer(40 * time.Millisecond)

	unblock := make(chan struct{})
	defer close(unblock)

	client.SetTaskHandler(func(ctx context.Context, workerID, data string) TaskResult {
		<-unblock
		return TaskResult{WasRunSuccessful: true}
	})

	msg := redis.XMessage{
		ID:     "msg-timed-out",
		Values: map[string]any{"data": "slow-task"},
	}

	start := time.Now()
	client.processMessage(context.Background(), msg, "worker-1")
	elapsed := time.Since(start)

	if elapsed > 100*time.Millisecond {
		t.Errorf("processMessage elapsed = %v, want < 100ms for timeout", elapsed)
	}
	if len(fake.claimArgs) != 0 {
		t.Errorf("len(claimArgs) = %d, want 0", len(fake.claimArgs))
	}
	if len(fake.addedArgs) != 1 || fake.addedArgs[0].Stream != "test-stream" {
		t.Errorf("addedArgs = %+v, want 1 retry on test-stream", fake.addedArgs)
	}
	if !slices.Equal(fake.ackedIDs, []string{"msg-timed-out"}) {
		t.Errorf("ackedIDs = %v, want [msg-timed-out]", fake.ackedIDs)
	}
}

func TestProcessMessage_RetryableError(t *testing.T) {
	client, fake := newTestConsumer(500 * time.Millisecond)

	client.SetTaskHandler(func(ctx context.Context, workerID, data string) TaskResult {
		return TaskResult{WasRunSuccessful: false, ShouldBeRetried: true, Error: errors.New("temporary error")}
	})

	msg := redis.XMessage{
		ID:     "msg-retryable",
		Values: map[string]any{"data": "payload", "failedAttempts": "1"},
	}

	client.processMessage(context.Background(), msg, "worker-1")

	if len(fake.addedArgs) != 1 || fake.addedArgs[0].Stream != "test-stream" {
		t.Errorf("addedArgs = %+v, want 1 retry on test-stream", fake.addedArgs)
	}
	if !slices.Equal(fake.ackedIDs, []string{"msg-retryable"}) {
		t.Errorf("ackedIDs = %v, want [msg-retryable]", fake.ackedIDs)
	}
}

func TestProcessMessage_NonRetryableError(t *testing.T) {
	client, fake := newTestConsumer(500 * time.Millisecond)

	client.SetTaskHandler(func(ctx context.Context, workerID, data string) TaskResult {
		return TaskResult{WasRunSuccessful: false, ShouldBeRetried: false, Error: errors.New("fatal error")}
	})

	msg := redis.XMessage{
		ID:     "msg-fatal",
		Values: map[string]any{"data": "payload", "failedAttempts": "0"},
	}

	client.processMessage(context.Background(), msg, "worker-1")

	if len(fake.addedArgs) != 1 || fake.addedArgs[0].Stream != "test-dlq" {
		t.Errorf("addedArgs = %+v, want 1 DLQ entry on test-dlq", fake.addedArgs)
	}
	if !slices.Equal(fake.ackedIDs, []string{"msg-fatal"}) {
		t.Errorf("ackedIDs = %v, want [msg-fatal]", fake.ackedIDs)
	}
}
