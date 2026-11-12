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

// fakeRedisClient records the stream commands used by ExtendClaim, processMessage, StartWorker
// and RunCleanup. Other Client methods are not implemented and panic if called.
type fakeRedisClient struct {
	Client
	mu        sync.Mutex
	claimArgs []*redis.XClaimArgs
	claimErr  error
	ackedIDs  []string
	addedArgs []*redis.XAddArgs
	// settleCtxErrs records ctx.Err() of every XAck and XAdd call.
	settleCtxErrs []error
	// readErr and autoClaimErr are returned by XReadGroup and XAutoClaim.
	readErr      error
	autoClaimErr error
	// groupCreates counts XGroupCreateMkStream calls; onGroupCreate runs on each of them.
	groupCreates  int
	onGroupCreate func(n int)
}

func (f *fakeRedisClient) XClaim(_ context.Context, a *redis.XClaimArgs) *redis.XMessageSliceCmd {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.claimArgs = append(f.claimArgs, a)
	return redis.NewXMessageSliceCmdResult(nil, f.claimErr)
}

func (f *fakeRedisClient) XAck(ctx context.Context, _, _ string, ids ...string) *redis.IntCmd {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ackedIDs = append(f.ackedIDs, ids...)
	f.settleCtxErrs = append(f.settleCtxErrs, ctx.Err())
	return redis.NewIntResult(int64(len(ids)), nil)
}

func (f *fakeRedisClient) XAdd(ctx context.Context, a *redis.XAddArgs) *redis.StringCmd {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.addedArgs = append(f.addedArgs, a)
	f.settleCtxErrs = append(f.settleCtxErrs, ctx.Err())
	return redis.NewStringResult("1-0", nil)
}

func (f *fakeRedisClient) XReadGroup(context.Context, *redis.XReadGroupArgs) *redis.XStreamSliceCmd {
	return redis.NewXStreamSliceCmdResult(nil, f.readErr)
}

func (f *fakeRedisClient) XAutoClaim(context.Context, *redis.XAutoClaimArgs) *redis.XAutoClaimCmd {
	cmd := redis.NewXAutoClaimCmd(context.Background())
	cmd.SetErr(f.autoClaimErr)
	return cmd
}

func (f *fakeRedisClient) XGroupCreateMkStream(context.Context, string, string, string) *redis.StatusCmd {
	f.mu.Lock()
	f.groupCreates++
	n, hook := f.groupCreates, f.onGroupCreate
	f.mu.Unlock()
	if hook != nil {
		hook(n)
	}
	return redis.NewStatusResult("OK", nil)
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

func TestProcessMessage_InvalidDataGoesToDLQ(t *testing.T) {
	tests := []struct {
		name   string
		values map[string]any
	}{
		{name: "missing data", values: map[string]any{"failedAttempts": "0"}},
		{name: "non-string data", values: map[string]any{"data": 42, "failedAttempts": "0"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client, fake := newTestConsumer(500 * time.Millisecond)
			var handlerCalled bool
			client.SetTaskHandler(func(ctx context.Context, workerID, data string) TaskResult {
				handlerCalled = true
				return TaskResult{WasRunSuccessful: true}
			})

			client.processMessage(context.Background(), redis.XMessage{ID: "msg-poison", Values: tc.values}, "worker-1")

			if handlerCalled {
				t.Error("task handler was called for a message without string data, want it skipped")
			}
			if len(fake.addedArgs) != 1 || fake.addedArgs[0].Stream != "test-dlq" || fake.addedArgs[0].Values.(map[string]any)["reason"] != "missing_data" {
				t.Errorf("addedArgs = %+v, want 1 DLQ entry on test-dlq with reason missing_data", fake.addedArgs)
			}
			if !slices.Equal(fake.ackedIDs, []string{"msg-poison"}) {
				t.Errorf("ackedIDs = %v, want [msg-poison]", fake.ackedIDs)
			}
		})
	}
}

// errNoGroup is the error Redis returns when the stream or consumer group does not exist.
var errNoGroup = errors.New("NOGROUP No such key 'test-stream' or consumer group 'test-group'")

func TestStartWorker_RecreatesMissingGroup(t *testing.T) {
	client, fake := newTestConsumer(time.Second)
	fake.readErr = errNoGroup
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// The first create is the worker's start-up; the second is the re-creation after NOGROUP.
	fake.onGroupCreate = func(n int) {
		if n == 2 {
			cancel()
		}
	}

	done := make(chan struct{})
	go func() {
		client.StartWorker(ctx, "worker-1")
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("StartWorker did not re-create the group after NOGROUP")
	}
	if fake.groupCreates != 2 {
		t.Errorf("XGroupCreateMkStream calls = %d, want 2 (start-up and after NOGROUP)", fake.groupCreates)
	}
}

func TestStartWorker_OtherReadErrorsKeepGroup(t *testing.T) {
	client, fake := newTestConsumer(time.Second)
	fake.readErr = errors.New("connection refused")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	client.StartWorker(ctx, "worker-1")

	if elapsed := time.Since(start); elapsed > 900*time.Millisecond {
		t.Errorf("StartWorker took %v after its context ended, want the error back-off to stop early", elapsed)
	}
	if fake.groupCreates != 1 {
		t.Errorf("XGroupCreateMkStream calls = %d, want 1 (start-up only)", fake.groupCreates)
	}
}

func TestRunCleanup_RecreatesMissingGroup(t *testing.T) {
	client, fake := newTestConsumer(time.Second)
	fake.autoClaimErr = errNoGroup
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fake.onGroupCreate = func(int) { cancel() }

	done := make(chan struct{})
	go func() {
		client.RunCleanup(ctx, "cleanup-1")
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("RunCleanup did not re-create the group after NOGROUP")
	}
	if fake.groupCreates != 1 {
		t.Errorf("XGroupCreateMkStream calls = %d, want 1", fake.groupCreates)
	}
}

func TestProcessMessage_SettlesAfterShutdown(t *testing.T) {
	tests := []struct {
		name      string
		result    TaskResult
		wantAdds  int
		wantAcked []string
	}{
		{name: "success is acknowledged", result: TaskResult{WasRunSuccessful: true}, wantAcked: []string{"msg-1"}},
		{name: "non-retryable failure is dead-lettered", result: TaskResult{Error: errors.New("fatal")}, wantAdds: 1, wantAcked: []string{"msg-1"}},
		{name: "retryable failure is re-added", result: TaskResult{ShouldBeRetried: true, Error: errors.New("temporary")}, wantAdds: 1, wantAcked: []string{"msg-1"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client, fake := newTestConsumer(time.Second)
			ctx, cancel := context.WithCancel(context.Background())
			// Shutdown cancels the worker's context while the handler runs.
			client.SetTaskHandler(func(context.Context, string, string) TaskResult {
				cancel()
				return tc.result
			})

			client.processMessage(ctx, redis.XMessage{ID: "msg-1", Values: map[string]any{"data": "d"}}, "worker-1")

			if len(fake.addedArgs) != tc.wantAdds || !slices.Equal(fake.ackedIDs, tc.wantAcked) {
				t.Errorf("after shutdown: %d XAdds, acked %v; want %d and %v", len(fake.addedArgs), fake.ackedIDs, tc.wantAdds, tc.wantAcked)
			}
			for i, err := range fake.settleCtxErrs {
				if err != nil {
					t.Errorf("settle write %d ran on a done context (%v), want one that survives shutdown", i, err)
				}
			}
		})
	}
}

func TestSleepUnlessDone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	tests := []struct {
		name    string
		ctx     context.Context
		d       time.Duration
		wantMin time.Duration
		wantMax time.Duration
	}{
		{name: "sleeps for d", ctx: context.Background(), d: 20 * time.Millisecond, wantMin: 20 * time.Millisecond, wantMax: time.Second},
		{name: "returns when ctx is done", ctx: ctx, d: time.Hour, wantMax: time.Second},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			start := time.Now()
			sleepUnlessDone(tc.ctx, tc.d)
			if elapsed := time.Since(start); elapsed < tc.wantMin || elapsed > tc.wantMax {
				t.Errorf("sleepUnlessDone() took %v, want between %v and %v", elapsed, tc.wantMin, tc.wantMax)
			}
		})
	}
}

func TestProcessMessage_TooManyAttemptsGoesToDLQ(t *testing.T) {
	client, fake := newTestConsumer(time.Second)
	var handlerCalled bool
	client.SetTaskHandler(func(context.Context, string, string) TaskResult {
		handlerCalled = true
		return TaskResult{WasRunSuccessful: true}
	})

	client.processMessage(context.Background(), redis.XMessage{ID: "msg-old", Values: map[string]any{"data": "d", "failedAttempts": "4"}}, "worker-1")

	if handlerCalled {
		t.Error("task handler was called for a message past MaxRetries, want it skipped")
	}
	if len(fake.addedArgs) != 1 || fake.addedArgs[0].Stream != "test-dlq" || !slices.Equal(fake.ackedIDs, []string{"msg-old"}) {
		t.Errorf("addedArgs = %+v, acked %v; want 1 DLQ entry on test-dlq and [msg-old] acked", fake.addedArgs, fake.ackedIDs)
	}
}
